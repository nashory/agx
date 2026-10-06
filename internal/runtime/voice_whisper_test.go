package runtime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nashory/agx/internal/config"
)

type recordingVoiceRunner struct {
	calls []voiceCommandCall
}

type voiceCommandCall struct {
	name string
	args []string
}

func (r *recordingVoiceRunner) Run(_ context.Context, name string, args ...string) error {
	r.calls = append(r.calls, voiceCommandCall{name: name, args: append([]string(nil), args...)})
	if strings.Contains(name, "whisper") {
		prefix := ""
		for i, arg := range args {
			if arg == "-of" && i+1 < len(args) {
				prefix = args[i+1]
				break
			}
		}
		if prefix != "" {
			return os.WriteFile(prefix+".txt", []byte("transcribed voice"), 0o600)
		}
	}
	return nil
}

func TestLocalWhisperTranscriberRunsFFmpegAndWhisper(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("AGX_CONFIG_DIR", configDir)
	ffmpegPath := writeExecutable(t, "ffmpeg")
	whisperPath := writeExecutable(t, "whisper-cli")
	modelPath := filepath.Join(t.TempDir(), "ggml-base.bin")
	if err := os.WriteFile(modelPath, []byte("model"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := config.SaveVoiceSTT(config.VoiceSTTConfig{
		Mode:        config.VoiceSTTEnabled,
		Compute:     config.VoiceComputeCPU,
		FFmpegPath:  ffmpegPath,
		WhisperPath: whisperPath,
		ModelPath:   modelPath,
		Language:    "ko",
		Timeout:     "30s",
	}); err != nil {
		t.Fatal(err)
	}
	runner := &recordingVoiceRunner{}
	transcriber := localWhisperTranscriber{runner: runner}

	transcript, err := transcriber.Transcribe(context.Background(), filepath.Join(t.TempDir(), "voice-message.ogg"))
	if err != nil {
		t.Fatal(err)
	}
	if transcript.Text != "transcribed voice" || transcript.Engine != "whisper.cpp" || transcript.Model != "ggml-base.bin" || transcript.Language != "ko" {
		t.Fatalf("transcript = %#v, want parsed whisper output", transcript)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("calls = %#v, want ffmpeg and whisper", runner.calls)
	}
	if runner.calls[0].name != ffmpegPath {
		t.Fatalf("first call = %#v, want ffmpeg", runner.calls[0])
	}
	if runner.calls[1].name != whisperPath || !containsArgPair(runner.calls[1].args, "-m", modelPath) || !containsArg(runner.calls[1].args, "-otxt") || !containsArg(runner.calls[1].args, "--no-gpu") || !containsArgPair(runner.calls[1].args, "-l", "ko") {
		t.Fatalf("whisper call = %#v, want model, text output, and language", runner.calls[1])
	}
}

func TestLocalWhisperTranscriberPassesAutoLanguage(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("AGX_CONFIG_DIR", configDir)
	ffmpegPath := writeExecutable(t, "ffmpeg")
	whisperPath := writeExecutable(t, "whisper-cli")
	modelPath := filepath.Join(t.TempDir(), "ggml-base.bin")
	if err := os.WriteFile(modelPath, []byte("model"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := config.SaveVoiceSTT(config.VoiceSTTConfig{
		Mode:        config.VoiceSTTEnabled,
		FFmpegPath:  ffmpegPath,
		WhisperPath: whisperPath,
		ModelPath:   modelPath,
		Language:    "auto",
		Timeout:     "30s",
	}); err != nil {
		t.Fatal(err)
	}
	runner := &recordingVoiceRunner{}

	transcript, err := (localWhisperTranscriber{runner: runner}).Transcribe(context.Background(), filepath.Join(t.TempDir(), "voice-message.ogg"))
	if err != nil {
		t.Fatal(err)
	}
	if transcript.Language != "auto" {
		t.Fatalf("transcript language = %q, want auto", transcript.Language)
	}
	if len(runner.calls) != 2 || !containsArgPair(runner.calls[1].args, "-l", "auto") {
		t.Fatalf("whisper call = %#v, want explicit -l auto", runner.calls)
	}
}

func TestLocalWhisperTranscriberReportsMissingModel(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("AGX_CONFIG_DIR", configDir)
	if err := config.SaveVoiceSTT(config.VoiceSTTConfig{
		Mode:        config.VoiceSTTEnabled,
		FFmpegPath:  writeExecutable(t, "ffmpeg"),
		WhisperPath: writeExecutable(t, "whisper-cli"),
		ModelPath:   filepath.Join(t.TempDir(), "missing.bin"),
	}); err != nil {
		t.Fatal(err)
	}
	_, err := (localWhisperTranscriber{runner: &recordingVoiceRunner{}}).Transcribe(context.Background(), filepath.Join(t.TempDir(), "voice-message.ogg"))
	if err == nil || !strings.Contains(err.Error(), "Whisper model is unavailable") {
		t.Fatalf("Transcribe error = %v, want missing model", err)
	}
}

func TestNormalizeVoiceTranscriptText(t *testing.T) {
	got := normalizeVoiceTranscriptText(" Okay, then test file.\r\n Let's see   how it works.\n")
	if got != "Okay, then test file. Let's see how it works." {
		t.Fatalf("normalizeVoiceTranscriptText() = %q", got)
	}
}

func TestWhisperServerArgsSelectCompute(t *testing.T) {
	cfg := config.VoiceSTTConfig{ModelPath: "model.bin", Language: "ko", Compute: config.VoiceComputeCPU}
	cpuArgs := whisperServerArgs(cfg, 1234)
	if !containsArg(cpuArgs, "--no-gpu") {
		t.Fatalf("CPU args = %#v, want --no-gpu", cpuArgs)
	}
	cfg.Compute = config.VoiceComputeGPU
	gpuArgs := whisperServerArgs(cfg, 1234)
	if containsArg(gpuArgs, "--no-gpu") {
		t.Fatalf("GPU args = %#v, do not want --no-gpu", gpuArgs)
	}
	if !containsArgPair(gpuArgs, "--port", "1234") || !containsArgPair(gpuArgs, "-l", "ko") {
		t.Fatalf("GPU args = %#v, want port and language", gpuArgs)
	}
}

func TestWhisperServerInference(t *testing.T) {
	wavPath := filepath.Join(t.TempDir(), "input.wav")
	if err := os.WriteFile(wavPath, []byte("audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/inference" {
			t.Fatalf("request = %s %s, want POST /inference", r.Method, r.URL.Path)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		if got := r.FormValue("language"); got != "ko" {
			t.Fatalf("language = %q, want ko", got)
		}
		file, _, err := r.FormFile("file")
		if err != nil {
			t.Fatal(err)
		}
		_ = file.Close()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"transcribed voice"}`))
	}))
	defer server.Close()

	transcriber := &whisperServerTranscriber{client: server.Client()}
	text, err := transcriber.infer(context.Background(), server.URL, wavPath, "ko")
	if err != nil {
		t.Fatal(err)
	}
	if text != "transcribed voice" {
		t.Fatalf("text = %q, want transcribed voice", text)
	}
}

func writeExecutable(t *testing.T, name string) string {
	t.Helper()
	return writeStubCommand(t, t.TempDir(), name, stubExitZeroPosix, stubExitZeroBatch)
}

func containsArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}

func containsArgPair(args []string, key, value string) bool {
	for i, arg := range args {
		if arg == key && i+1 < len(args) && args[i+1] == value {
			return true
		}
	}
	return false
}
