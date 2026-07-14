package voicestt

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nashory/agx/internal/config"
)

func TestResolveLocalWhisperUsesConfiguredPaths(t *testing.T) {
	t.Setenv("AGX_CONFIG_DIR", t.TempDir())
	ffmpegPath := writeFile(t, "ffmpeg")
	whisperPath := writeFile(t, "whisper-cli")
	modelPath := writeFile(t, "ggml-base.bin")

	resolved, err := ResolveLocalWhisper(config.VoiceSTTConfig{
		Mode:        config.VoiceSTTEnabled,
		FFmpegPath:  ffmpegPath,
		WhisperPath: whisperPath,
		ModelPath:   modelPath,
		Language:    "ko",
		Timeout:     "30s",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.FFmpegPath != ffmpegPath || resolved.WhisperPath != whisperPath || resolved.ModelPath != modelPath {
		t.Fatalf("resolved = %#v, want configured paths", resolved)
	}
}

func TestResolveModelFindsDefaultConfigModel(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("AGX_CONFIG_DIR", configDir)
	modelPath := filepath.Join(configDir, "models", "whisper", "ggml-base.bin")
	if err := os.MkdirAll(filepath.Dir(modelPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(modelPath, []byte("model"), 0o600); err != nil {
		t.Fatal(err)
	}

	resolved, err := ResolveModel("")
	if err != nil {
		t.Fatal(err)
	}
	if resolved != modelPath {
		t.Fatalf("ResolveModel = %q, want %q", resolved, modelPath)
	}
}

func TestSetupLocalWhisperReusesDefaultModelAndSavesConfig(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("AGX_CONFIG_DIR", configDir)
	t.Setenv("PATH", t.TempDir())
	modelPath := filepath.Join(configDir, "models", "whisper", "ggml-base.bin")
	if err := os.MkdirAll(filepath.Dir(modelPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(modelPath, []byte("model"), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := SetupLocalWhisper(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Downloaded {
		t.Fatal("SetupLocalWhisper downloaded unexpectedly")
	}
	if result.Config.ModelPath != modelPath || result.Config.Mode != config.VoiceSTTAuto {
		t.Fatalf("setup result = %#v, want saved auto config with default model", result)
	}
	if len(result.Warnings) == 0 || !strings.Contains(strings.Join(result.Warnings, " "), "ffmpeg") {
		t.Fatalf("warnings = %#v, want missing tool guidance in test PATH", result.Warnings)
	}
}

func writeFile(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("x"), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}
