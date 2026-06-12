package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nashory/agx/internal/db"
)

func TestCleanupOrphanAttachmentDirs(t *testing.T) {
	store, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	project, err := store.EnsureProject(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTask(project.ID, "task", nil, "claude", db.StatusActive)
	if err != nil {
		t.Fatal(err)
	}
	paths := Paths{ConfigDir: t.TempDir()}
	root := attachmentRoot(paths)
	knownPath, err := attachmentPath(root, task.ID, "msg-1", "known.png")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(knownPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(knownPath, tinyPNG, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateTaskAttachment(db.TaskAttachment{
		TaskID:              task.ID,
		DiscordMessageID:    "msg-1",
		DiscordAttachmentID: "att-1",
		Filename:            "known.png",
		SizeBytes:           int64(len(tinyPNG)),
		LocalPath:           knownPath,
	}); err != nil {
		t.Fatal(err)
	}
	unknownPath := filepath.Join(filepath.Dir(knownPath), "unknown.tmp")
	if err := os.WriteFile(unknownPath, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	orphanDir := filepath.Join(root, "task-missing")
	if err := os.MkdirAll(orphanDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := cleanupOrphanAttachmentDirs(paths, store); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(knownPath); err != nil {
		t.Fatalf("known attachment removed: %v", err)
	}
	if _, err := os.Stat(unknownPath); !os.IsNotExist(err) {
		t.Fatalf("unknown file error = %v, want not exist", err)
	}
	if _, err := os.Stat(orphanDir); !os.IsNotExist(err) {
		t.Fatalf("orphan dir error = %v, want not exist", err)
	}
}

func TestPruneTaskAttachmentsRemovesRowsAndFiles(t *testing.T) {
	store, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	project, err := store.EnsureProject(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTask(project.ID, "task", nil, "claude", db.StatusActive)
	if err != nil {
		t.Fatal(err)
	}
	paths := Paths{ConfigDir: t.TempDir()}
	localPath, err := attachmentPath(attachmentRoot(paths), task.ID, "msg-1", "known.png")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(localPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(localPath, tinyPNG, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateTaskAttachment(db.TaskAttachment{
		TaskID:              task.ID,
		DiscordMessageID:    "msg-1",
		DiscordAttachmentID: "att-1",
		Filename:            "known.png",
		SizeBytes:           int64(len(tinyPNG)),
		LocalPath:           localPath,
	}); err != nil {
		t.Fatal(err)
	}
	result, err := pruneTaskAttachments(paths, store, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.AttachmentsDeleted != 1 || result.FilesDeleted != 1 {
		t.Fatalf("result = %#v, want one row and file", result)
	}
	if attachments, err := store.ListTaskAttachments(task.ID); err != nil || len(attachments) != 0 {
		t.Fatalf("attachments = %#v, err = %v; want none", attachments, err)
	}
	if _, err := os.Stat(localPath); !os.IsNotExist(err) {
		t.Fatalf("local path error = %v, want not exist", err)
	}
}

func TestPruneOldAttachmentsRejectsPathOutsideRoot(t *testing.T) {
	store, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	project, err := store.EnsureProject(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTask(project.ID, "task", nil, "claude", db.StatusComplete)
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.png")
	if err := os.WriteFile(outside, tinyPNG, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateTaskAttachment(db.TaskAttachment{
		TaskID:              task.ID,
		DiscordMessageID:    "msg-1",
		DiscordAttachmentID: "att-1",
		Filename:            "outside.png",
		SizeBytes:           int64(len(tinyPNG)),
		LocalPath:           outside,
	}); err != nil {
		t.Fatal(err)
	}
	_, err = pruneOldAttachments(Paths{ConfigDir: t.TempDir()}, store, time.Now().UTC().Add(time.Hour))
	if err == nil || !strings.Contains(err.Error(), "escapes root") {
		t.Fatalf("err = %v, want escapes root", err)
	}
	if _, statErr := os.Stat(outside); statErr != nil {
		t.Fatalf("outside file was removed: %v", statErr)
	}
}
