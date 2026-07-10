package runtime

import (
	"context"
	"strings"
	"testing"

	"github.com/nashory/agx/internal/codexapp"
	"github.com/nashory/agx/internal/db"
)

// TestTaskLogsReadsStructuredTranscript verifies that /task logs on a structured
// (codex/claude stream) task returns the persisted transcript instead of failing
// with "task has no session" from the tmux backend.
func TestTaskLogsReadsStructuredTranscript(t *testing.T) {
	t.Setenv("AGX_CONFIG_DIR", t.TempDir())
	store, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	project, err := store.EnsureProject(initRuntimeGitRepo(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTaskRuntimeModeInterfaceWorkspace(db.NewTaskID(), project.ID, "read docs", nil, "codex", true, db.TaskInterfaceDiscord, db.WorkspaceModeProject, db.StatusWaiting, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	threadID := task.ID
	streamKind := codexapp.StreamKind
	if err := store.UpdateTaskAgentStream(task.ID, &threadID, nil, &streamKind); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendTaskTranscriptMessage(task.ID, "user", "read the doc", nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendTaskTranscriptMessage(task.ID, "assistant", "the doc says hello", nil, nil); err != nil {
		t.Fatal(err)
	}

	service := NewService("test")
	service.store = store
	svc := discordCommandService{runtime: service}

	logs, err := svc.TaskLogs(context.Background(), task.ID, 50)
	if err != nil {
		t.Fatalf("TaskLogs() error = %v", err)
	}
	if !strings.Contains(logs, "read the doc") || !strings.Contains(logs, "the doc says hello") {
		t.Fatalf("TaskLogs() = %q, want it to contain both transcript messages", logs)
	}
	if !strings.Contains(logs, "[assistant]") {
		t.Fatalf("TaskLogs() = %q, want role-labeled transcript", logs)
	}
}
