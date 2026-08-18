package db

import (
	"testing"
	"time"
)

func TestTaskTranscriptMessages(t *testing.T) {
	store, err := OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	project, err := store.EnsureProject(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTaskRuntimeModeInterface(NewTaskID(), project.ID, "discord task", nil, "codex", false, TaskInterfaceDiscord, StatusActive, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	turnID := "turn-1"
	if err := store.AppendTaskTranscriptMessage(task.ID, "user", " hello ", &turnID, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendTaskTranscriptMessage(task.ID, "assistant", "world", &turnID, nil); err != nil {
		t.Fatal(err)
	}
	messages, err := store.ListTaskTranscriptMessages(task.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 {
		t.Fatalf("len(messages) = %d, want 2", len(messages))
	}
	if messages[0].Role != "user" || messages[0].Body != "hello" {
		t.Fatalf("first message = %#v, want trimmed user message", messages[0])
	}
	if messages[1].Role != "assistant" || messages[1].Body != "world" {
		t.Fatalf("second message = %#v, want assistant message", messages[1])
	}
}

func TestTaskTranscriptRejectsInvalidRole(t *testing.T) {
	store, err := OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if err := store.AppendTaskTranscriptMessage("task-1", "bad", "body", nil, nil); err == nil {
		t.Fatal("AppendTaskTranscriptMessage accepted invalid role")
	}
}

func TestTaskTranscriptEventMessagesAreDeduplicated(t *testing.T) {
	store, err := OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	project, err := store.EnsureProject(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTask(project.ID, "task", nil, "codex", StatusActive)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendTaskTranscriptEventMessage(task.ID, "assistant", "done", nil, "event-1"); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendTaskTranscriptEventMessage(task.ID, "assistant", "done", nil, "event-1"); err != nil {
		t.Fatal(err)
	}
	messages, err := store.ListTaskTranscriptMessages(task.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].EventKey == nil || *messages[0].EventKey != "event-1" {
		t.Fatalf("messages = %#v, want one event-linked transcript", messages)
	}
}

func TestListUnqueuedAssistantTranscriptMessages(t *testing.T) {
	store, err := OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	project, err := store.EnsureProject(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTask(project.ID, "task", nil, "codex", StatusActive)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		role, body, eventKey string
	}{
		{role: "assistant", body: "missed", eventKey: "event-missed"},
		{role: "assistant", body: "queued", eventKey: "event-queued"},
		{role: "user", body: "user message", eventKey: "event-user"},
	} {
		if err := store.AppendTaskTranscriptEventMessage(task.ID, item.role, item.body, nil, item.eventKey); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.AppendTaskTranscriptMessage(task.ID, "assistant", "legacy", nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.EnqueueDiscordDeliveries([]DiscordDelivery{{
		DeliveryKey: "delivery-queued",
		TaskID:      task.ID,
		ChannelID:   "channel-1",
		Kind:        DiscordDeliveryMessage,
		Content:     "queued",
		EventKey:    "event-queued",
	}}); err != nil {
		t.Fatal(err)
	}

	messages, err := store.ListUnqueuedAssistantTranscriptMessages(task.ID, time.Now().Add(-time.Hour), 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].Body != "missed" || messages[0].EventKey == nil || *messages[0].EventKey != "event-missed" {
		t.Fatalf("messages = %#v, want only missed assistant event", messages)
	}
}

func TestListUnqueuedAssistantTranscriptMessagesAppliesWindowAndLimit(t *testing.T) {
	store, err := OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	project, err := store.EnsureProject(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTask(project.ID, "task", nil, "codex", StatusActive)
	if err != nil {
		t.Fatal(err)
	}
	for _, eventKey := range []string{"event-old", "event-first", "event-second"} {
		if err := store.AppendTaskTranscriptEventMessage(task.ID, "assistant", eventKey, nil, eventKey); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.db.Exec(`UPDATE task_transcript_messages SET created_at = datetime('now', '-2 days') WHERE event_key = 'event-old'`); err != nil {
		t.Fatal(err)
	}
	messages, err := store.ListUnqueuedAssistantTranscriptMessages(task.ID, time.Now().Add(-24*time.Hour), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].Body != "event-second" {
		t.Fatalf("messages = %#v, want newest recent event only", messages)
	}
}
