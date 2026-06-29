package runtime

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/nashory/agx/internal/db"
	agxdiscord "github.com/nashory/agx/internal/discord"
)

func (s *Service) sendDiscordTaskMessage(ctx context.Context, taskID string, message agxdiscord.IncomingTaskMessage) (agxdiscord.SendTaskMessageResult, error) {
	task, project, err := s.taskAndProject(taskID)
	if err != nil {
		return agxdiscord.SendTaskMessageResult{}, err
	}
	if task.Interface != db.TaskInterfaceDiscord {
		return agxdiscord.SendTaskMessageResult{}, fmt.Errorf("this AGX task is local-only and cannot be controlled from Discord")
	}
	if isPendingStructuredDiscordTask(task) {
		if task.Status == db.StatusOffline {
			return agxdiscord.SendTaskMessageResult{}, fmt.Errorf("this Discord task failed to start; check AGX logs and retry after fixing the startup error")
		}
		return agxdiscord.SendTaskMessageResult{}, fmt.Errorf("this Discord task is still starting; try again in a moment")
	}
	discordMessageID := cleanDiscordMessageID(message.DiscordMessageID)
	delivered := false
	if discordMessageID != nil {
		reserved, err := s.store.ReserveDiscordMessage(task.ID, *discordMessageID)
		if err != nil {
			return agxdiscord.SendTaskMessageResult{}, err
		}
		if !reserved {
			return agxdiscord.SendTaskMessageResult{}, nil
		}
		defer func() {
			if !delivered {
				_ = s.store.DeleteDiscordMessageReservation(task.ID, *discordMessageID)
			}
		}()
	}
	prepared, skipped, err := s.prepareDiscordAttachments(ctx, task, message)
	if err != nil {
		return agxdiscord.SendTaskMessageResult{}, err
	}
	voiceTranscripts, voiceWarnings := s.transcribeVoiceAttachments(ctx, prepared)
	prompt := buildDiscordAttachmentPrompt(message.Text, prepared, voiceTranscripts, voiceWarnings)
	if strings.TrimSpace(prompt) == "" {
		if len(skipped) > 0 {
			return agxdiscord.SendTaskMessageResult{}, fmt.Errorf("all attachments were skipped: %s", strings.Join(skipped, "; "))
		}
		return agxdiscord.SendTaskMessageResult{}, fmt.Errorf("message is empty")
	}
	if strings.TrimSpace(message.Text) == "" && nonVoiceAttachmentCount(prepared) == 0 && len(voiceTranscripts) == 0 {
		if len(voiceWarnings) > 0 {
			return agxdiscord.SendTaskMessageResult{}, fmt.Errorf("AGX received your voice message, but local voice transcription is not available. %s", strings.Join(voiceWarnings, "; "))
		}
		return agxdiscord.SendTaskMessageResult{}, fmt.Errorf("AGX received your voice message, but no voice transcript was available. Enable local STT in AGX Desktop settings, or send the message as text")
	}
	if isRuntimeStructuredDBTask(task) {
		s.syncDiscordTaskBestEffort(task.ID)
	}
	if err := s.deliverTaskMessage(ctx, task, project, prompt); err != nil {
		return agxdiscord.SendTaskMessageResult{}, err
	}
	delivered = true
	var result agxdiscord.SendTaskMessageResult
	if isAgentContextClearCommand(prompt) {
		result = s.recordDeliveredDiscordCommand(task.ID, discordMessageID)
	} else {
		result = s.recordDeliveredDiscordTaskMessage(task.ID, prompt, discordMessageID)
	}
	if len(skipped) > 0 {
		skippedNotice := "Message sent, but skipped attachments:\n- " + strings.Join(skipped, "\n- ")
		if result.Notice != "" {
			result.Notice += "\n\n" + skippedNotice
		} else {
			result.Notice = skippedNotice
		}
	}
	if len(voiceWarnings) > 0 {
		voiceNotice := "Message sent, but voice transcription had warnings:\n- " + strings.Join(voiceWarnings, "\n- ")
		if result.Notice != "" {
			result.Notice += "\n\n" + voiceNotice
		} else {
			result.Notice = voiceNotice
		}
	}
	return result, nil
}

func (s *Service) recordDeliveredDiscordCommand(taskID string, discordMessageID *string) agxdiscord.SendTaskMessageResult {
	if discordMessageID == nil {
		return agxdiscord.SendTaskMessageResult{}
	}
	if err := s.store.MarkDiscordMessageDelivered(taskID, *discordMessageID); err != nil {
		return agxdiscord.SendTaskMessageResult{
			Notice: "Command handled, but AGX could not mark the Discord message delivered: " + err.Error(),
		}
	}
	return agxdiscord.SendTaskMessageResult{}
}

func (s *Service) recordDeliveredDiscordTaskMessage(taskID, prompt string, discordMessageID *string) agxdiscord.SendTaskMessageResult {
	_ = s.store.AppendTaskTranscriptMessage(taskID, "user", prompt, nil, discordMessageID)
	_ = s.store.UpdateTaskLastUserPrompt(taskID, prompt)
	if discordMessageID == nil {
		return agxdiscord.SendTaskMessageResult{}
	}
	if err := s.store.MarkDiscordMessageDelivered(taskID, *discordMessageID); err != nil {
		return agxdiscord.SendTaskMessageResult{
			Notice: "Message sent, but AGX could not mark the Discord message delivered: " + err.Error(),
		}
	}
	return agxdiscord.SendTaskMessageResult{}
}

func (s *Service) prepareDiscordAttachments(ctx context.Context, task db.Task, message agxdiscord.IncomingTaskMessage) ([]db.TaskAttachment, []string, error) {
	if len(message.Attachments) == 0 {
		return nil, nil, nil
	}
	discordMessageID := strings.TrimSpace(message.DiscordMessageID)
	if discordMessageID == "" {
		return nil, nil, fmt.Errorf("discord message id is required for attachments")
	}
	lock := s.taskLock(task.ID)
	lock.Lock()
	defer lock.Unlock()

	root := attachmentRoot(s.paths)
	existingBytes, err := s.taskAttachmentBytes(task.ID)
	if err != nil {
		return nil, nil, err
	}
	downloader := s.attachments
	if downloader.client == nil {
		downloader = defaultAttachmentDownloader()
	}
	var prepared []db.TaskAttachment
	var skipped []string
	for i, incoming := range message.Attachments {
		if i >= maxDiscordAttachmentsMessage {
			skipped = append(skipped, fmt.Sprintf("%s exceeds the %d attachments per message limit", displayAttachmentName(incoming), maxDiscordAttachmentsMessage))
			continue
		}
		incoming.DiscordAttachmentID = strings.TrimSpace(incoming.DiscordAttachmentID)
		incoming.Filename = sanitizeAttachmentFilename(incoming.Filename)
		incoming.ContentType = strings.TrimSpace(incoming.ContentType)
		incoming.URL = strings.TrimSpace(incoming.URL)
		if incoming.DiscordAttachmentID == "" {
			skipped = append(skipped, fmt.Sprintf("%s is missing a Discord attachment ID", displayAttachmentName(incoming)))
			continue
		}
		if incoming.URL == "" {
			skipped = append(skipped, fmt.Sprintf("%s is missing a download URL", displayAttachmentName(incoming)))
			continue
		}
		if incoming.SizeBytes > maxDiscordAttachmentFileBytes {
			skipped = append(skipped, fmt.Sprintf("%s exceeds the %d byte file limit", displayAttachmentName(incoming), maxDiscordAttachmentFileBytes))
			continue
		}
		if existing, err := s.store.GetTaskAttachmentByDiscord(task.ID, discordMessageID, incoming.DiscordAttachmentID); err == nil {
			if _, statErr := os.Stat(existing.LocalPath); statErr == nil {
				prepared = append(prepared, existing)
				continue
			}
			skipped = append(skipped, fmt.Sprintf("%s is unavailable on disk", displayAttachmentName(incoming)))
			continue
		} else if !errors.Is(err, db.ErrTaskAttachmentNotFound) {
			return nil, nil, err
		}
		if existingBytes+incoming.SizeBytes > maxDiscordAttachmentsTask {
			skipped = append(skipped, fmt.Sprintf("%s exceeds the %d byte task attachment limit", displayAttachmentName(incoming), maxDiscordAttachmentsTask))
			continue
		}
		finalPath, err := uniqueAttachmentPath(root, task.ID, discordMessageID, incoming.Filename, incoming.DiscordAttachmentID)
		if err != nil {
			return nil, nil, err
		}
		downloaded, err := downloader.download(ctx, incoming.URL, finalPath)
		if err != nil {
			skipped = append(skipped, fmt.Sprintf("%s: %v", displayAttachmentName(incoming), err))
			continue
		}
		row, err := s.store.CreateTaskAttachment(db.TaskAttachment{
			TaskID:              task.ID,
			DiscordMessageID:    discordMessageID,
			DiscordAttachmentID: incoming.DiscordAttachmentID,
			Filename:            filepath.Base(finalPath),
			ContentType:         downloaded.ContentType,
			SizeBytes:           downloaded.SizeBytes,
			LocalPath:           finalPath,
			SourceURL:           safeAttachmentSourceURL(incoming.URL),
			SHA256:              downloaded.SHA256,
		})
		if err != nil {
			_ = os.Remove(finalPath)
			return nil, nil, err
		}
		if row.LocalPath != finalPath {
			_ = os.Remove(finalPath)
		}
		existingBytes += row.SizeBytes
		prepared = append(prepared, row)
	}
	return prepared, skipped, nil
}

func (s *Service) taskAttachmentBytes(taskID string) (int64, error) {
	attachments, err := s.store.ListTaskAttachments(taskID)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, attachment := range attachments {
		total += attachment.SizeBytes
	}
	return total, nil
}

func (s *Service) deliverTaskMessage(ctx context.Context, task db.Task, project db.Project, prompt string) error {
	if isRuntimeStructuredDBTask(task) {
		return s.agents.SendTaskMessage(ctx, task, project, prompt)
	}
	lock := s.taskLock(task.ID)
	lock.Lock()
	defer lock.Unlock()
	return s.managerForProject(project).SendMessage(task, prompt)
}

func buildDiscordAttachmentPrompt(text string, attachments []db.TaskAttachment, voiceTranscripts []voiceAttachmentTranscript, voiceWarnings []string) string {
	text = strings.TrimSpace(text)
	var b strings.Builder
	if text != "" {
		b.WriteString(text)
	}
	if len(voiceTranscripts) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		if len(voiceTranscripts) == 1 {
			b.WriteString("Voice transcript:")
			fmt.Fprintf(&b, "\n%q", voiceTranscripts[0].Transcript.Text)
		} else {
			b.WriteString("Voice transcripts:")
			for i, item := range voiceTranscripts {
				fmt.Fprintf(&b, "\n%d. %s", i+1, item.Attachment.Filename)
				fmt.Fprintf(&b, "\n   %q", item.Transcript.Text)
			}
		}
	}
	if len(voiceWarnings) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString("Voice transcription warnings:")
		for _, warning := range voiceWarnings {
			fmt.Fprintf(&b, "\n- %s", warning)
		}
	}
	if len(attachments) == 0 {
		return strings.TrimSpace(b.String())
	}
	if b.Len() > 0 {
		b.WriteString("\n\n")
	} else {
		fmt.Fprintf(&b, "User sent %d attachment", len(attachments))
		if len(attachments) != 1 {
			b.WriteByte('s')
		}
		b.WriteString(".\n\n")
	}
	b.WriteString("Attachments:")
	for _, attachment := range attachments {
		fmt.Fprintf(&b, "\n- %s %s %d bytes", attachment.Filename, attachment.ContentType, attachment.SizeBytes)
		fmt.Fprintf(&b, "\n  saved: %s", attachment.LocalPath)
		if sourceURL := safeAttachmentSourceURL(attachment.SourceURL); sourceURL != "" {
			fmt.Fprintf(&b, "\n  source: %s", sourceURL)
		}
	}
	return strings.TrimSpace(b.String())
}

func safeAttachmentSourceURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func uniqueAttachmentPath(root, taskID, discordMessageID, filename, discordAttachmentID string) (string, error) {
	path, err := attachmentPath(root, taskID, discordMessageID, filename)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return path, nil
	} else if err != nil {
		return "", err
	}
	ext := filepath.Ext(path)
	base := strings.TrimSuffix(path, ext)
	suffix := sanitizePathSegment(discordAttachmentID)
	if len(suffix) > 8 {
		suffix = suffix[:8]
	}
	if suffix == "" {
		suffix = "dup"
	}
	return base + "-" + suffix + ext, nil
}

func displayAttachmentName(attachment agxdiscord.IncomingAttachment) string {
	if strings.TrimSpace(attachment.Filename) != "" {
		return strings.TrimSpace(attachment.Filename)
	}
	if strings.TrimSpace(attachment.DiscordAttachmentID) != "" {
		return "attachment " + strings.TrimSpace(attachment.DiscordAttachmentID)
	}
	return "attachment"
}

func cleanDiscordMessageID(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}
