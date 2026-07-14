package voicestt

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nashory/agx/internal/config"
)

const (
	defaultModelName = "ggml-base.bin"
	defaultModelURL  = "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-base.bin"
	maxModelBytes    = 512 << 20
)

type LocalWhisperConfig struct {
	Mode        string `json:"mode"`
	FFmpegPath  string `json:"ffmpegPath"`
	WhisperPath string `json:"whisperPath"`
	ModelPath   string `json:"modelPath"`
	Language    string `json:"language"`
	Timeout     string `json:"timeout"`
}

type SetupResult struct {
	Config     LocalWhisperConfig `json:"config"`
	Downloaded bool               `json:"downloaded"`
	ModelURL   string             `json:"modelUrl"`
	Warnings   []string           `json:"warnings"`
}

func ResolveLocalWhisper(cfg config.VoiceSTTConfig) (config.VoiceSTTConfig, error) {
	var errs []string
	next := cfg
	if ffmpegPath, err := ResolveCommand(cfg.FFmpegPath, []string{"ffmpeg"}); err == nil {
		next.FFmpegPath = ffmpegPath
	} else {
		errs = append(errs, fmt.Sprintf("ffmpeg is unavailable: %v", err))
	}
	if whisperPath, err := ResolveCommand(cfg.WhisperPath, []string{"whisper-cli", "main"}); err == nil {
		next.WhisperPath = whisperPath
	} else {
		errs = append(errs, fmt.Sprintf("Whisper binary is unavailable: %v", err))
	}
	if modelPath, err := ResolveModel(cfg.ModelPath); err == nil {
		next.ModelPath = modelPath
	} else {
		errs = append(errs, fmt.Sprintf("Whisper model is unavailable: %v", err))
	}
	if len(errs) > 0 {
		return next, errors.New(strings.Join(errs, "; "))
	}
	return next, nil
}

func ResolveCommand(configured string, fallbacks []string) (string, error) {
	configured = strings.TrimSpace(configured)
	if configured != "" {
		return resolveCommandCandidate(configured)
	}
	var errs []string
	for _, fallback := range fallbacks {
		path, err := resolveCommandCandidate(fallback)
		if err == nil {
			return path, nil
		}
		errs = append(errs, err.Error())
	}
	if len(errs) == 0 {
		return "", fmt.Errorf("no command candidates configured")
	}
	return "", errors.New(strings.Join(errs, "; "))
}

func ResolveModel(configured string) (string, error) {
	configured = strings.TrimSpace(configured)
	if configured != "" {
		return existingFile(configured)
	}
	for _, candidate := range modelCandidates() {
		path, err := existingFile(candidate)
		if err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("model path is not configured and no default model was found")
}

func SetupLocalWhisper(ctx context.Context) (SetupResult, error) {
	cfg, warnings := config.LoadGlobal()
	if len(warnings) > 0 {
		return SetupResult{}, warnings[0]
	}
	result := SetupResult{ModelURL: defaultModelURL}
	next := cfg.Discord.VoiceSTT
	if next.Mode == config.VoiceSTTDisabled {
		next.Mode = config.VoiceSTTAuto
	}
	if ffmpegPath, err := ResolveCommand(next.FFmpegPath, []string{"ffmpeg"}); err == nil {
		next.FFmpegPath = ffmpegPath
	} else {
		result.Warnings = append(result.Warnings, fmt.Sprintf("ffmpeg was not found on PATH: %v", err))
	}
	if whisperPath, err := ResolveCommand(next.WhisperPath, []string{"whisper-cli", "main"}); err == nil {
		next.WhisperPath = whisperPath
	} else {
		result.Warnings = append(result.Warnings, fmt.Sprintf("whisper-cli was not found on PATH: %v", err))
	}
	modelPath, err := ResolveModel(next.ModelPath)
	if err != nil {
		modelPath = filepath.Join(DefaultModelDir(), defaultModelName)
		if err := downloadFile(ctx, defaultModelURL, modelPath); err != nil {
			return SetupResult{}, fmt.Errorf("download Whisper model: %w", err)
		}
		result.Downloaded = true
	}
	next.ModelPath = modelPath
	if err := config.SaveVoiceSTT(next); err != nil {
		return SetupResult{}, err
	}
	saved, warnings := config.LoadGlobal()
	if len(warnings) > 0 {
		return SetupResult{}, warnings[0]
	}
	result.Config = ConfigDTO(saved.Discord.VoiceSTT)
	return result, nil
}

func DefaultModelDir() string {
	return filepath.Join(config.ConfigDir(), "models", "whisper")
}

func ConfigDTO(cfg config.VoiceSTTConfig) LocalWhisperConfig {
	return LocalWhisperConfig{
		Mode:        cfg.Mode,
		FFmpegPath:  cfg.FFmpegPath,
		WhisperPath: cfg.WhisperPath,
		ModelPath:   cfg.ModelPath,
		Language:    cfg.Language,
		Timeout:     cfg.Timeout,
	}
}

func resolveCommandCandidate(candidate string) (string, error) {
	if looksLikePath(candidate) {
		return existingFile(candidate)
	}
	path, err := exec.LookPath(candidate)
	if err != nil {
		return "", err
	}
	return path, nil
}

func existingFile(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s is a directory", path)
	}
	return path, nil
}

func looksLikePath(value string) bool {
	return filepath.IsAbs(value) || strings.ContainsAny(value, `/\`)
}

func modelCandidates() []string {
	var candidates []string
	modelDir := DefaultModelDir()
	for _, name := range []string{
		defaultModelName,
		"ggml-small.bin",
		"ggml-tiny.bin",
		"ggml-base.en.bin",
		"ggml-small.en.bin",
		"ggml-tiny.en.bin",
	} {
		candidates = append(candidates, filepath.Join(modelDir, name))
	}
	matches, err := filepath.Glob(filepath.Join(modelDir, "ggml-*.bin"))
	if err == nil {
		sort.Strings(matches)
		candidates = append(candidates, matches...)
	}
	return dedupe(candidates)
}

func dedupe(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func downloadFile(ctx context.Context, url, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("unexpected HTTP status %s", resp.Status)
	}
	tmp := dst + ".download"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, io.LimitReader(resp.Body, maxModelBytes+1))
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	info, err := os.Stat(tmp)
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if info.Size() > maxModelBytes {
		_ = os.Remove(tmp)
		return fmt.Errorf("download exceeded %d bytes", maxModelBytes)
	}
	if info.Size() == 0 {
		_ = os.Remove(tmp)
		return fmt.Errorf("downloaded model is empty")
	}
	return os.Rename(tmp, dst)
}
