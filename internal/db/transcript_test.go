package db

import "testing"

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
