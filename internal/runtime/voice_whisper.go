package runtime

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/nashory/agx/internal/config"
	"github.com/nashory/agx/internal/voicestt"
)

type voiceCommandRunner interface {
	Run(ctx context.Context, name string, args ...string) error
}

type osVoiceCommandRunner struct{}

func (osVoiceCommandRunner) Run(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	output, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	summary := strings.TrimSpace(string(output))
	if len(summary) > 4096 {
		summary = summary[:4096]
	}
	if summary == "" {
		return err
	}
	return fmt.Errorf("%w: %s", err, summary)
}

type localWhisperTranscriber struct {
	runner voiceCommandRunner
}

func defaultVoiceTranscriber() voiceTranscriber {
	return localWhisperTranscriber{runner: osVoiceCommandRunner{}}
}

func (t localWhisperTranscriber) Transcribe(ctx context.Context, inputPath string) (voiceTranscript, error) {
	cfg, warnings := config.LoadGlobal()
	if len(warnings) > 0 {
		return voiceTranscript{}, warnings[0]
	}
	voiceCfg := cfg.Discord.VoiceSTT
	resolved, err := voicestt.ResolveLocalWhisper(voiceCfg)
	if err != nil {
		return voiceTranscript{}, fmt.Errorf("%w: %v", errVoiceSTTUnavailable, err)
	}
	voiceCfg = resolved
	timeout, err := time.ParseDuration(voiceCfg.Timeout)
	if err != nil || timeout <= 0 {
		timeout = 60 * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	tmpRoot := filepath.Join(config.ConfigDir(), "tmp")
	if err := os.MkdirAll(tmpRoot, 0o700); err != nil {
		return voiceTranscript{}, fmt.Errorf("create voice transcription temp dir: %w", err)
	}
	tmpDir, err := os.MkdirTemp(tmpRoot, "voice-stt-*")
	if err != nil {
		return voiceTranscript{}, fmt.Errorf("create voice transcription temp dir: %w", err)
	}
	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()

	wavPath := filepath.Join(tmpDir, "input.wav")
	outPrefix := filepath.Join(tmpDir, "transcript")
	runner := t.runner
	if runner == nil {
		runner = osVoiceCommandRunner{}
	}
	if err := runner.Run(runCtx, voiceCfg.FFmpegPath, "-y", "-i", inputPath, "-ar", "16000", "-ac", "1", wavPath); err != nil {
		return voiceTranscript{}, fmt.Errorf("ffmpeg conversion failed: %w", err)
	}
	args := []string{"-m", voiceCfg.ModelPath, "-f", wavPath, "-otxt", "-of", outPrefix}
	language := strings.TrimSpace(voiceCfg.Language)
	if language == "" {
		language = "auto"
	}
	args = append(args, "-l", language)
	if err := runner.Run(runCtx, voiceCfg.WhisperPath, args...); err != nil {
		return voiceTranscript{}, fmt.Errorf("whisper command failed: %w", err)
	}
	text, err := os.ReadFile(outPrefix + ".txt")
	if err != nil {
		return voiceTranscript{}, fmt.Errorf("read whisper transcript: %w", err)
	}
	return voiceTranscript{
		Text:     normalizeVoiceTranscriptText(string(text)),
		Engine:   "whisper.cpp",
		Model:    filepath.Base(voiceCfg.ModelPath),
		Language: language,
	}, nil
}

func normalizeVoiceTranscriptText(value string) string {
	return strings.Join(strings.Fields(value), " ")
}
