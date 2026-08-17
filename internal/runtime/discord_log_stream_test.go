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

func TestLatestMuseAssistantMessageExtractsCompletedReply(t *testing.T) {
	logs := strings.Join([]string{
		"Muse Code at Meta",
		"",
		"⟩ 야",
		"",
		"◆ 야 뭐해? 뭘 도와줄까?",
		"",
		"── Voice input (⌥V to start) ───────────────────────────────────────────────────",
		"⟩",
		"────────────────────────────────────────────────────────────────────────────────",
		"  muse-spark-1.2-internal · high · /repo/agx · YOLO",
	}, "\n")

	message, ok := latestMuseAssistantMessage(logs)
	if !ok {
		t.Fatal("latestMuseAssistantMessage() ok = false, want true")
	}
	if message != "야 뭐해? 뭘 도와줄까?" {
		t.Fatalf("latestMuseAssistantMessage() = %q", message)
	}
}

func TestLatestMuseAssistantMessageSkipsToolStatus(t *testing.T) {
	logs := strings.Join([]string{
		"◆ Ran command · List workspace · ✓ · 0.2s · ctrl+o",
		"",
		"◆ Wrote leakage-feature-quarantine-selector/environment/quarantine.py (+76) · ctrl+o",
		"",
		"◆ Fixed leakage-feature-quarantine-selector/environment/quarantine.py",
		"  Verification passed.",
		"",
		"── Voice input (⌥V to start) ───────────────────────────────────────────────────",
		"⟩",
	}, "\n")

	message, ok := latestMuseAssistantMessage(logs)
	if !ok {
		t.Fatal("latestMuseAssistantMessage() ok = false, want true")
	}
	if !strings.Contains(message, "Fixed leakage-feature-quarantine-selector") || !strings.Contains(message, "Verification passed.") {
		t.Fatalf("latestMuseAssistantMessage() = %q", message)
	}
	if strings.Contains(message, "Ran command") || strings.Contains(message, "Wrote ") {
		t.Fatalf("latestMuseAssistantMessage() included tool status: %q", message)
	}
}

func TestLatestMuseAssistantMessageWaitsForComposer(t *testing.T) {
	if message, ok := latestMuseAssistantMessage("◆ still generating"); ok || message != "" {
		t.Fatalf("latestMuseAssistantMessage() = (%q, %v), want empty until composer returns", message, ok)
	}
}
