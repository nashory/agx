package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/nashory/agx/internal/config"
	"github.com/nashory/agx/internal/db"
)

var errVoiceSTTUnavailable = errors.New("local voice transcription is not available")

type voiceTranscriber interface {
	Transcribe(ctx context.Context, inputPath string) (voiceTranscript, error)
}

type voiceTranscript struct {
	Text     string
	Engine   string
	Model    string
	Language string
}

type voiceAttachmentTranscript struct {
	Attachment db.TaskAttachment
	Transcript voiceTranscript
}

func (s *Service) transcribeVoiceAttachments(ctx context.Context, attachments []db.TaskAttachment) ([]voiceAttachmentTranscript, []string) {
	var transcripts []voiceAttachmentTranscript
	var warnings []string
	for _, attachment := range attachments {
		if !isVoiceAttachment(attachment) {
			continue
		}
		transcript, err := s.transcribeVoiceAttachment(ctx, attachment)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", attachment.Filename, err))
			continue
		}
		transcripts = append(transcripts, voiceAttachmentTranscript{Attachment: attachment, Transcript: transcript})
	}
	return transcripts, warnings
}

func (s *Service) transcribeVoiceAttachment(ctx context.Context, attachment db.TaskAttachment) (voiceTranscript, error) {
	cfg, warnings := config.LoadGlobal()
	if len(warnings) > 0 {
		return voiceTranscript{}, warnings[0]
	}
	voiceCfg := cfg.Discord.VoiceSTT
	if voiceCfg.Mode == config.VoiceSTTDisabled {
		return voiceTranscript{}, fmt.Errorf("%w; enable local STT in AGX Desktop settings or send text", errVoiceSTTUnavailable)
	}
	if s.voice == nil {
		return voiceTranscript{}, fmt.Errorf("%w; configure ffmpeg, Whisper, and a model in AGX Desktop settings", errVoiceSTTUnavailable)
	}
	transcript, err := s.voice.Transcribe(ctx, attachment.LocalPath)
	if err != nil {
		return voiceTranscript{}, err
	}
	transcript.Text = strings.TrimSpace(transcript.Text)
	if transcript.Text == "" {
		return voiceTranscript{}, fmt.Errorf("voice transcript was empty")
	}
	return transcript, nil
}

func isVoiceAttachment(attachment db.TaskAttachment) bool {
	contentType := strings.ToLower(strings.TrimSpace(attachment.ContentType))
	if strings.HasPrefix(contentType, "audio/ogg") {
		return true
	}
	filename := strings.ToLower(strings.TrimSpace(attachment.Filename))
	return strings.HasSuffix(filename, ".ogg") || strings.HasSuffix(filename, ".opus")
}

func nonVoiceAttachmentCount(attachments []db.TaskAttachment) int {
	count := 0
	for _, attachment := range attachments {
		if !isVoiceAttachment(attachment) {
			count++
		}
	}
	return count
}
