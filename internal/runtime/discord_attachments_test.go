package runtime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/nashory/agx/internal/config"
	"github.com/nashory/agx/internal/db"
	agxdiscord "github.com/nashory/agx/internal/discord"
)

func TestPrepareDiscordAttachmentsDownloadsAndPersists(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(tinyPNG)
	}))
	defer server.Close()
	store, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	project, err := store.EnsureProject(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTaskRuntimeModeInterface(db.NewTaskID(), project.ID, "discord task", nil, "claude", false, db.TaskInterfaceDiscord, db.StatusActive, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService("test")
	service.paths = Paths{ConfigDir: t.TempDir()}
	service.store = store
	service.attachments = newAttachmentDownloader(server.Client(), map[string]bool{testServerHost(t, server.URL): true}, true, 1024)
	prepared, skipped, err := service.prepareDiscordAttachments(context.Background(), task, agxdiscord.IncomingTaskMessage{
		DiscordMessageID: "msg-1",
		Attachments: []agxdiscord.IncomingAttachment{{
			DiscordAttachmentID: "att-1",
			Filename:            "../../screen shot.png",
			SizeBytes:           int64(len(tinyPNG)),
			URL:                 server.URL + "/screen.png",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(skipped) != 0 {
		t.Fatalf("skipped = %#v, want none", skipped)
	}
	if len(prepared) != 1 {
		t.Fatalf("prepared = %d, want 1", len(prepared))
	}
	if !strings.HasSuffix(prepared[0].LocalPath, "screen_shot.png") || prepared[0].ContentType != "image/png" {
		t.Fatalf("prepared attachment = %#v", prepared[0])
	}
	stored, err := store.GetTaskAttachmentByDiscord(task.ID, "msg-1", "att-1")
	if err != nil {
		t.Fatal(err)
	}
	if stored.ID != prepared[0].ID {
		t.Fatalf("stored ID = %q, want %q", stored.ID, prepared[0].ID)
	}
}

func TestTranscribeVoiceAttachmentsUsesConfiguredTranscriber(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("AGX_CONFIG_DIR", configDir)
	if err := config.SaveVoiceSTT(config.VoiceSTTConfig{Mode: config.VoiceSTTEnabled}); err != nil {
		t.Fatal(err)
	}
	service := NewService("test")
	service.voice = fakeVoiceTranscriber{text: "start the main task"}
	transcripts, warnings := service.transcribeVoiceAttachments(context.Background(), []db.TaskAttachment{{
		Filename:    "voice-message.ogg",
		ContentType: "audio/ogg",
		LocalPath:   filepath.Join(t.TempDir(), "voice-message.ogg"),
	}})
	if len(warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", warnings)
	}
	if len(transcripts) != 1 || transcripts[0].Transcript.Text != "start the main task" {
		t.Fatalf("transcripts = %#v, want fake transcript", transcripts)
	}
}

func TestTranscribeVoiceAttachmentsReportsUnavailable(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("AGX_CONFIG_DIR", configDir)
	if err := config.SaveVoiceSTT(config.VoiceSTTConfig{Mode: config.VoiceSTTDisabled}); err != nil {
		t.Fatal(err)
	}
	service := NewService("test")
	transcripts, warnings := service.transcribeVoiceAttachments(context.Background(), []db.TaskAttachment{{
		Filename:    "voice-message.ogg",
		ContentType: "audio/ogg",
		LocalPath:   filepath.Join(t.TempDir(), "voice-message.ogg"),
	}})
	if len(transcripts) != 0 {
		t.Fatalf("transcripts = %#v, want none", transcripts)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "local voice transcription is not available") {
		t.Fatalf("warnings = %#v, want unavailable warning", warnings)
	}
}

func TestPrepareDiscordAttachmentsSerializesTaskByteLimit(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		_, _ = w.Write(tinyPNG)
	}))
	defer server.Close()
	store, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	project, err := store.EnsureProject(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTaskRuntimeModeInterface(db.NewTaskID(), project.ID, "discord task", nil, "claude", false, db.TaskInterfaceDiscord, db.StatusActive, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService("test")
	service.paths = Paths{ConfigDir: t.TempDir()}
	service.store = store
	service.attachments = newAttachmentDownloader(server.Client(), map[string]bool{testServerHost(t, server.URL): true}, true, 1024)

	if _, err := store.CreateTaskAttachment(db.TaskAttachment{
		TaskID:              task.ID,
		DiscordMessageID:    "existing-msg",
		DiscordAttachmentID: "existing-att",
		Filename:            "existing.png",
		SizeBytes:           maxDiscordAttachmentsTask - int64(len(tinyPNG)) - 1,
		LocalPath:           filepath.Join(t.TempDir(), "existing.png"),
	}); err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	results := make(chan []string, 2)
	for _, messageID := range []string{"msg-1", "msg-2"} {
		messageID := messageID
		go func() {
			<-start
			_, skipped, err := service.prepareDiscordAttachments(context.Background(), task, agxdiscord.IncomingTaskMessage{
				DiscordMessageID: messageID,
				Attachments: []agxdiscord.IncomingAttachment{{
					DiscordAttachmentID: "att-" + messageID,
					Filename:            "screen.png",
					SizeBytes:           int64(len(tinyPNG)),
					URL:                 server.URL + "/screen.png",
				}},
			})
			if err != nil {
				results <- []string{"error: " + err.Error()}
				return
			}
			results <- skipped
		}()
	}
	close(start)
	var skippedCount int
	for i := 0; i < 2; i++ {
		skipped := <-results
		if len(skipped) > 0 {
			if strings.HasPrefix(skipped[0], "error: ") {
				t.Fatal(skipped[0])
			}
			skippedCount++
		}
	}
	if skippedCount != 1 {
		t.Fatalf("skipped messages = %d, want 1", skippedCount)
	}
	attachments, err := store.ListTaskAttachments(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(attachments) != 2 {
		t.Fatalf("attachments = %d, want existing plus one accepted attachment", len(attachments))
	}
	mu.Lock()
	gotRequests := requests
	mu.Unlock()
	if gotRequests != 1 {
		t.Fatalf("download requests = %d, want 1", gotRequests)
	}
}

func TestSendDiscordTaskMessageSkipsDuplicateDiscordMessage(t *testing.T) {
	store, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	project, err := store.EnsureProject(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTaskRuntimeModeInterface(db.NewTaskID(), project.ID, "discord task", nil, "missing-agent", false, db.TaskInterfaceDiscord, db.StatusActive, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if reserved, err := store.ReserveDiscordMessage(task.ID, "msg-duplicate"); err != nil || !reserved {
		t.Fatalf("ReserveDiscordMessage() = %v, %v; want true, nil", reserved, err)
	}
	service := NewService("test")
	service.store = store
	if _, err := service.sendDiscordTaskMessage(context.Background(), task.ID, agxdiscord.IncomingTaskMessage{
		Text:             "hello",
		DiscordMessageID: "msg-duplicate",
	}); err != nil {
		t.Fatalf("sendDiscordTaskMessage duplicate error = %v, want nil", err)
	}
}

func TestSendDiscordTaskMessageSkipsDeliveredDiscordMessage(t *testing.T) {
	store, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	project, err := store.EnsureProject(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTaskRuntimeModeInterface(db.NewTaskID(), project.ID, "discord task", nil, "missing-agent", false, db.TaskInterfaceDiscord, db.StatusActive, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if reserved, err := store.ReserveDiscordMessage(task.ID, "msg-delivered"); err != nil || !reserved {
		t.Fatalf("ReserveDiscordMessage() = %v, %v; want true, nil", reserved, err)
	}
	if err := store.MarkDiscordMessageDelivered(task.ID, "msg-delivered"); err != nil {
		t.Fatal(err)
	}
	service := NewService("test")
	service.store = store
	if _, err := service.sendDiscordTaskMessage(context.Background(), task.ID, agxdiscord.IncomingTaskMessage{
		Text:             "hello",
		DiscordMessageID: "msg-delivered",
	}); err != nil {
		t.Fatalf("sendDiscordTaskMessage duplicate delivered error = %v, want nil", err)
	}
	messages, err := store.ListTaskTranscriptMessages(task.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 0 {
		t.Fatalf("transcript messages = %d, want no duplicate append", len(messages))
	}
	status, err := store.DiscordMessageStatus(task.ID, "msg-delivered")
	if err != nil {
		t.Fatal(err)
	}
	if status != db.DiscordMessageDelivered {
		t.Fatalf("status = %q, want delivered", status)
	}
}

func TestSendDiscordTaskMessageReleasesReservationOnDeliveryError(t *testing.T) {
	store, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	project, err := store.EnsureProject(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTaskRuntimeModeInterface(db.NewTaskID(), project.ID, "discord task", nil, "missing-agent", false, db.TaskInterfaceDiscord, db.StatusActive, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService("test")
	service.store = store
	if _, err := service.sendDiscordTaskMessage(context.Background(), task.ID, agxdiscord.IncomingTaskMessage{
		Text:             "hello",
		DiscordMessageID: "msg-retry",
	}); err == nil {
		t.Fatal("sendDiscordTaskMessage error = nil, want delivery error")
	}
	if reserved, err := store.ReserveDiscordMessage(task.ID, "msg-retry"); err != nil || !reserved {
		t.Fatalf("ReserveDiscordMessage() after delivery error = %v, %v; want true, nil", reserved, err)
	}
}

func TestSendDiscordTaskMessageReleasesReservationOnEmptyMessage(t *testing.T) {
	store, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	project, err := store.EnsureProject(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTaskRuntimeModeInterface(db.NewTaskID(), project.ID, "discord task", nil, "missing-agent", false, db.TaskInterfaceDiscord, db.StatusActive, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService("test")
	service.store = store
	if _, err := service.sendDiscordTaskMessage(context.Background(), task.ID, agxdiscord.IncomingTaskMessage{
		Text:             " ",
		DiscordMessageID: "msg-empty",
	}); err == nil {
		t.Fatal("sendDiscordTaskMessage error = nil, want empty message error")
	}
	if reserved, err := store.ReserveDiscordMessage(task.ID, "msg-empty"); err != nil || !reserved {
		t.Fatalf("ReserveDiscordMessage() after empty message = %v, %v; want true, nil", reserved, err)
	}
}

func TestRecordDeliveredDiscordTaskMessageReturnsNoticeOnMetadataFailure(t *testing.T) {
	store, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.EnsureProject(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTaskRuntimeModeInterface(db.NewTaskID(), project.ID, "discord task", nil, "codex", false, db.TaskInterfaceDiscord, db.StatusActive, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	discordMessageID := "msg-delivered"
	if reserved, err := store.ReserveDiscordMessage(task.ID, discordMessageID); err != nil || !reserved {
		t.Fatalf("ReserveDiscordMessage() = %v, %v; want true, nil", reserved, err)
	}
	service := NewService("test")
	service.store = store
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	result := service.recordDeliveredDiscordTaskMessage(task.ID, "hello", &discordMessageID)
	if !strings.Contains(result.Notice, "Message sent") || !strings.Contains(result.Notice, "could not mark") {
		t.Fatalf("Notice = %q, want delivered-with-metadata-warning notice", result.Notice)
	}
}

func TestBuildDiscordAttachmentPromptSupportsAttachmentOnly(t *testing.T) {
	prompt := buildDiscordAttachmentPrompt("", []db.TaskAttachment{{
		Filename:    "screen.png",
		ContentType: "image/png",
		SizeBytes:   12,
		LocalPath:   "/tmp/screen.png",
		SourceURL:   "https://cdn.discordapp.com/attachments/1/2/screen.png?ex=secret#fragment",
	}}, nil, nil)
	for _, want := range []string{"User sent 1 attachment.", "Attachments:", "saved: /tmp/screen.png", "source: https://cdn.discordapp.com"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt = %q, want %q", prompt, want)
		}
	}
	if strings.Contains(prompt, "secret") || strings.Contains(prompt, "fragment") {
		t.Fatalf("prompt = %q, want source URL query and fragment stripped", prompt)
	}
}

func TestBuildDiscordAttachmentPromptIncludesVoiceTranscript(t *testing.T) {
	attachment := db.TaskAttachment{
		Filename:    "voice-message.ogg",
		ContentType: "audio/ogg",
		SizeBytes:   12,
		LocalPath:   "/tmp/voice-message.ogg",
	}
	prompt := buildDiscordAttachmentPrompt("", []db.TaskAttachment{attachment}, []voiceAttachmentTranscript{{
		Attachment: attachment,
		Transcript: voiceTranscript{Text: "create a task from main"},
	}}, nil)
	for _, want := range []string{"Voice transcript:", `"create a task from main"`, "Attachments:", "voice-message.ogg audio/ogg"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt = %q, want %q", prompt, want)
		}
	}
}

type fakeVoiceTranscriber struct {
	text string
	err  error
}

func (f fakeVoiceTranscriber) Transcribe(context.Context, string) (voiceTranscript, error) {
	if f.err != nil {
		return voiceTranscript{}, f.err
	}
	return voiceTranscript{Text: f.text, Engine: "fake"}, nil
}
