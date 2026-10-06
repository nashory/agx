package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nashory/agx/internal/config"
	"github.com/nashory/agx/internal/voicestt"
)

const whisperServerStartupTimeout = 2 * time.Minute

type whisperServerTranscriber struct {
	mu       sync.Mutex
	server   *whisperServerInstance
	runner   voiceCommandRunner
	client   *http.Client
	fallback localWhisperTranscriber
}

type whisperServerInstance struct {
	cancel      context.CancelFunc
	done        chan struct{}
	baseURL     string
	fingerprint string
}

type whisperServerResponse struct {
	Text string `json:"text"`
}

func newWhisperServerTranscriber() *whisperServerTranscriber {
	runner := osVoiceCommandRunner{}
	return &whisperServerTranscriber{
		runner:   runner,
		client:   &http.Client{},
		fallback: localWhisperTranscriber{runner: runner},
	}
}

func (t *whisperServerTranscriber) Warm(ctx context.Context) error {
	cfg, warnings := config.LoadGlobal()
	if len(warnings) > 0 {
		return warnings[0]
	}
	if cfg.Discord.VoiceSTT.Mode == config.VoiceSTTDisabled {
		return t.Close()
	}
	resolved, err := voicestt.ResolveLocalWhisper(cfg.Discord.VoiceSTT)
	if err != nil {
		return fmt.Errorf("%w: %v", errVoiceSTTUnavailable, err)
	}
	_, err = t.ensureServer(ctx, resolved)
	return err
}

func (t *whisperServerTranscriber) Reload(ctx context.Context) error {
	if err := t.Close(); err != nil {
		return err
	}
	return t.Warm(ctx)
}

func (t *whisperServerTranscriber) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stopLocked()
	return nil
}

func (t *whisperServerTranscriber) Transcribe(ctx context.Context, inputPath string) (voiceTranscript, error) {
	cfg, warnings := config.LoadGlobal()
	if len(warnings) > 0 {
		return voiceTranscript{}, warnings[0]
	}
	voiceCfg, err := voicestt.ResolveLocalWhisper(cfg.Discord.VoiceSTT)
	if err != nil {
		return voiceTranscript{}, fmt.Errorf("%w: %v", errVoiceSTTUnavailable, err)
	}
	timeout, err := time.ParseDuration(voiceCfg.Timeout)
	if err != nil || timeout <= 0 {
		timeout = 60 * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	baseURL, err := t.ensureServer(runCtx, voiceCfg)
	if err != nil {
		// Keep installations that only ship whisper-cli working. The persistent
		// server is preferred whenever its sibling binary is available.
		return t.fallback.Transcribe(ctx, inputPath)
	}

	tmpRoot := filepath.Join(config.ConfigDir(), "tmp")
	if err := os.MkdirAll(tmpRoot, 0o700); err != nil {
		return voiceTranscript{}, fmt.Errorf("create voice transcription temp dir: %w", err)
	}
	tmpDir, err := os.MkdirTemp(tmpRoot, "voice-stt-*")
	if err != nil {
		return voiceTranscript{}, fmt.Errorf("create voice transcription temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	wavPath := filepath.Join(tmpDir, "input.wav")
	runner := t.runner
	if runner == nil {
		runner = osVoiceCommandRunner{}
	}
	if err := runner.Run(runCtx, voiceCfg.FFmpegPath, "-y", "-i", inputPath, "-ar", "16000", "-ac", "1", wavPath); err != nil {
		return voiceTranscript{}, fmt.Errorf("ffmpeg conversion failed: %w", err)
	}
	language := strings.TrimSpace(voiceCfg.Language)
	if language == "" {
		language = "auto"
	}
	text, err := t.infer(runCtx, baseURL, wavPath, language)
	if err != nil {
		return voiceTranscript{}, err
	}
	return voiceTranscript{
		Text:     normalizeVoiceTranscriptText(text),
		Engine:   "whisper.cpp-server",
		Model:    filepath.Base(voiceCfg.ModelPath),
		Language: language,
	}, nil
}

func (t *whisperServerTranscriber) ensureServer(ctx context.Context, cfg config.VoiceSTTConfig) (string, error) {
	serverPath, err := resolveWhisperServer(cfg.WhisperPath)
	if err != nil {
		return "", err
	}
	fingerprint := strings.Join([]string{serverPath, cfg.ModelPath, cfg.Language, cfg.Compute}, "\x00")

	t.mu.Lock()
	defer t.mu.Unlock()
	if t.server != nil && t.server.fingerprint == fingerprint {
		select {
		case <-t.server.done:
			t.server = nil
		default:
			return t.server.baseURL, nil
		}
	}
	t.stopLocked()

	port, err := availableLoopbackPort()
	if err != nil {
		return "", fmt.Errorf("reserve Whisper server port: %w", err)
	}
	serverCtx, serverCancel := context.WithCancel(context.Background())
	args := whisperServerArgs(cfg, port)
	cmd := exec.CommandContext(serverCtx, serverPath, args...)
	var stderr bytes.Buffer
	cmd.Stdout = io.Discard
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		serverCancel()
		return "", fmt.Errorf("start Whisper server: %w", err)
	}
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()

	baseURL := "http://127.0.0.1:" + strconv.Itoa(port)
	startupCtx, startupCancel := context.WithTimeout(ctx, whisperServerStartupTimeout)
	defer startupCancel()
	if err := waitForWhisperServer(startupCtx, port, done); err != nil {
		serverCancel()
		<-done
		summary := strings.TrimSpace(stderr.String())
		if len(summary) > 4096 {
			summary = summary[len(summary)-4096:]
		}
		if summary != "" {
			return "", fmt.Errorf("start Whisper server: %w: %s", err, summary)
		}
		return "", fmt.Errorf("start Whisper server: %w", err)
	}
	t.server = &whisperServerInstance{
		cancel:      serverCancel,
		done:        done,
		baseURL:     baseURL,
		fingerprint: fingerprint,
	}
	return baseURL, nil
}

func (t *whisperServerTranscriber) stopLocked() {
	if t.server == nil {
		return
	}
	t.server.cancel()
	select {
	case <-t.server.done:
	case <-time.After(5 * time.Second):
	}
	t.server = nil
}

func (t *whisperServerTranscriber) infer(ctx context.Context, baseURL, wavPath, language string) (string, error) {
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	file, err := os.Open(wavPath)
	if err != nil {
		return "", fmt.Errorf("open Whisper input: %w", err)
	}
	part, err := form.CreateFormFile("file", filepath.Base(wavPath))
	if err == nil {
		_, err = io.Copy(part, file)
	}
	_ = file.Close()
	if err != nil {
		return "", fmt.Errorf("build Whisper request: %w", err)
	}
	_ = form.WriteField("response_format", "json")
	_ = form.WriteField("language", language)
	if err := form.Close(); err != nil {
		return "", fmt.Errorf("build Whisper request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/inference", &body)
	if err != nil {
		return "", fmt.Errorf("build Whisper request: %w", err)
	}
	req.Header.Set("Content-Type", form.FormDataContentType())
	client := t.client
	if client == nil {
		client = &http.Client{}
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("Whisper server request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("Whisper server returned %s: %s", resp.Status, strings.TrimSpace(string(message)))
	}
	var result whisperServerResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode Whisper response: %w", err)
	}
	return result.Text, nil
}

func resolveWhisperServer(whisperPath string) (string, error) {
	resolved, err := voicestt.ResolveCommand(whisperPath, []string{"whisper-cli", "main"})
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(resolved)
	ext := filepath.Ext(resolved)
	if ext == "" && filepath.Separator == '\\' {
		ext = ".exe"
	}
	for _, name := range []string{"whisper-server" + ext, "server" + ext} {
		candidate := filepath.Join(dir, name)
		if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("whisper-server was not found next to %s", resolved)
}

func whisperServerArgs(cfg config.VoiceSTTConfig, port int) []string {
	language := strings.TrimSpace(cfg.Language)
	if language == "" {
		language = "auto"
	}
	args := []string{
		"-m", cfg.ModelPath,
		"--host", "127.0.0.1",
		"--port", strconv.Itoa(port),
		"-l", language,
	}
	if cfg.Compute == config.VoiceComputeCPU {
		args = append(args, "--no-gpu")
	}
	return args
}

func availableLoopbackPort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

func waitForWhisperServer(ctx context.Context, port int, done <-chan struct{}) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	for {
		conn, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-done:
			return fmt.Errorf("Whisper server exited before becoming ready")
		case <-ticker.C:
		}
	}
}
