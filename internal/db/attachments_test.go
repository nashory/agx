package db

import (
	"errors"
	"testing"
)

func TestCreateTaskAttachmentIsIdempotent(t *testing.T) {
	store, err := OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	project, err := store.EnsureProject(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTask(project.ID, "attachment task", nil, "claude", StatusActive)
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.CreateTaskAttachment(TaskAttachment{
		TaskID:              task.ID,
		DiscordMessageID:    "msg-1",
		DiscordAttachmentID: "att-1",
		Filename:            "screen.png",
		ContentType:         "image/png",
		SizeBytes:           123,
		LocalPath:           "/tmp/screen.png",
		SourceURL:           "https://cdn.discordapp.com/attachments/1/2/screen.png",
		SHA256:              "abc",
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateTaskAttachment(TaskAttachment{
		TaskID:              task.ID,
		DiscordMessageID:    "msg-1",
		DiscordAttachmentID: "att-1",
		Filename:            "other.png",
		SizeBytes:           999,
		LocalPath:           "/tmp/other.png",
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID || second.Filename != first.Filename || second.LocalPath != first.LocalPath {
		t.Fatalf("duplicate insert = %#v, want existing %#v", second, first)
	}
	attachments, err := store.ListTaskAttachments(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(attachments) != 1 {
		t.Fatalf("attachments = %d, want 1", len(attachments))
	}
}

func TestTaskAttachmentCascadesOnTaskDelete(t *testing.T) {
	store, err := OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	project, err := store.EnsureProject(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTask(project.ID, "attachment task", nil, "claude", StatusActive)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateTaskAttachment(TaskAttachment{
		TaskID:              task.ID,
		DiscordMessageID:    "msg-1",
		DiscordAttachmentID: "att-1",
		Filename:            "screen.png",
		SizeBytes:           123,
		LocalPath:           "/tmp/screen.png",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteTask(task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetTaskAttachmentByDiscord(task.ID, "msg-1", "att-1"); !errors.Is(err, ErrTaskAttachmentNotFound) {
		t.Fatalf("GetTaskAttachmentByDiscord error = %v, want ErrTaskAttachmentNotFound", err)
	}
}
