package runtime

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nashory/agx/internal/agentstream"
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

func TestLatestMuseProgressExtractsStatusBlocks(t *testing.T) {
	logs := strings.Join([]string{
		"◆ Ran 4 commands · ✓ ×4 · 0.7s · ctrl+o",
		"",
		"◆ Backgrounded · Try local classifier · 30s · ctrl+o",
		"",
		"◆ 업데이트 — 로컬 재현 결과 공유:",
		"  p_3p: 0.999396",
	}, "\n")

	progress, ok := latestMuseProgress(logs)
	if !ok {
		t.Fatal("latestMuseProgress() ok = false, want true")
	}
	if !strings.Contains(progress, "Backgrounded") || strings.Contains(progress, "업데이트") {
		t.Fatalf("latestMuseProgress() = %q", progress)
	}
}

func TestMuseLogEventsPublishesProgressAndMessageOnce(t *testing.T) {
	logs := strings.Join([]string{
		"◆ Ran 4 commands · ✓ ×4 · 0.7s · ctrl+o",
		"",
		"◆ 태스크 SUSPECT 이슈 확인하고 수정해줄게.",
		"",
		"◆ Ran command · Inspect first 2000 chars truncation · ✓ · 0.2s · ctrl+o",
		"",
		"◆ 최근 지정된 ado-fyi 기준으로 해당 태스크의 소스 신호를 확인했습니다.",
		"",
		"── Voice input (⌥V to start) ───────────────────────────────────────────────────",
		"⟩",
	}, "\n")
	state := &museLogState{}

	events := museLogEvents("task-1", "turn-1", "muse", logs, state, time.Unix(1, 0))
	if len(events) != 6 {
		t.Fatalf("len(events) = %d, want all progress/message blocks", len(events))
	}
	if events[0].Kind != agentstream.EventToolStarted || events[0].Tool == nil || !strings.Contains(events[0].Tool.Input, "Ran 4 commands") {
		t.Fatalf("events[0] = %#v, want first Muse progress", events[0])
	}
	if events[1].Kind != agentstream.EventAssistantMessage || !strings.Contains(events[1].Text, "SUSPECT") {
		t.Fatalf("events[1] = %#v, want first assistant message", events[1])
	}
	if events[2].Kind != agentstream.EventTurnCompleted {
		t.Fatalf("events[2] = %#v, want first turn completed", events[2])
	}
	if events[3].Kind != agentstream.EventToolStarted || events[3].Tool == nil || !strings.Contains(events[3].Tool.Input, "Inspect first 2000") {
		t.Fatalf("events[3] = %#v, want second Muse progress", events[3])
	}
	if events[4].Kind != agentstream.EventAssistantMessage || !strings.Contains(events[4].Text, "소스 신호") {
		t.Fatalf("events[4] = %#v, want second assistant message", events[4])
	}
	if again := museLogEvents("task-1", "turn-1", "muse", logs, state, time.Unix(2, 0)); len(again) != 0 {
		t.Fatalf("second museLogEvents() = %#v, want no duplicate events", again)
	}
}

func TestMuseLogEventsPublishesOnlyNewBlocks(t *testing.T) {
	state := &museLogState{}
	first := strings.Join([]string{
		"◆ Ran 2 commands · ✓ ×2 · 0.3s · ctrl+o",
		"",
		"◆ 첫 답변",
	}, "\n")
	second := first + "\n\n" + strings.Join([]string{
		"◆ Ran command · Test classifier · ✓ · 4.5s · ctrl+o",
		"",
		"◆ 두 번째 답변",
	}, "\n")

	if events := museLogEvents("task-1", "turn-1", "muse", first, state, time.Unix(1, 0)); len(events) != 3 {
		t.Fatalf("first museLogEvents() len = %d, want 3", len(events))
	}
	events := museLogEvents("task-1", "turn-1", "muse", second, state, time.Unix(2, 0))
	if len(events) != 3 {
		t.Fatalf("second museLogEvents() len = %d, want only new progress/message", len(events))
	}
	if events[0].Kind != agentstream.EventToolStarted || events[1].Text != "두 번째 답변" {
		t.Fatalf("second museLogEvents() = %#v, want only new blocks", events)
	}
}
