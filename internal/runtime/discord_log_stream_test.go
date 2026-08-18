package runtime

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
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

func TestFindMuseSessionLogPathUsesLatestMatchingWorkspace(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	museDir := filepath.Join(dataHome, "muse")
	if err := os.MkdirAll(museDir, 0o700); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	oldLog := filepath.Join(t.TempDir(), "old-session.jsonl")
	newLog := filepath.Join(t.TempDir(), "new-session.jsonl")
	if err := os.WriteFile(oldLog, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newLog, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	index, err := sql.Open("sqlite", filepath.Join(museDir, "session-index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer index.Close()
	_, err = index.Exec(`CREATE TABLE sessions (
		session_id TEXT PRIMARY KEY,
		workspace_root TEXT,
		session_log_path TEXT,
		updated_at_us INTEGER
	)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = index.Exec(`INSERT INTO sessions(session_id, workspace_root, session_log_path, updated_at_us) VALUES
		('old', ?, ?, 10),
		('new', ?, ?, 20),
		('other', ?, ?, 30)`, workspace, oldLog, workspace, newLog, t.TempDir(), filepath.Join(t.TempDir(), "other.jsonl"))
	if err != nil {
		t.Fatal(err)
	}

	path, err := findMuseSessionLogPath(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if path != newLog {
		t.Fatalf("findMuseSessionLogPath() = %q, want newest matching %q", path, newLog)
	}
}

func TestFindMuseSessionLogPathFallsBackToRecentSessionMetadata(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	workspace := t.TempDir()
	unrelatedWorkspace := t.TempDir()

	oldLog := writeMuseSessionLog(t, dataHome, "2026", "08", "16", "old", unrelatedWorkspace)
	if err := os.Chtimes(oldLog, time.Unix(10, 0), time.Unix(10, 0)); err != nil {
		t.Fatal(err)
	}
	newLog := writeMuseSessionLog(t, dataHome, "2026", "08", "17", "new", workspace)
	if err := os.Chtimes(newLog, time.Unix(20, 0), time.Unix(20, 0)); err != nil {
		t.Fatal(err)
	}

	path, err := findMuseSessionLogPath(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if path != newLog {
		t.Fatalf("findMuseSessionLogPath() = %q, want recent metadata match %q", path, newLog)
	}
}

func TestMuseSessionLogEventsSwitchesToRestartedSession(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	workspace := t.TempDir()

	oldLog := writeMuseSessionLog(t, dataHome, "2026", "08", "16", "old", workspace)
	oldInfo, err := os.Stat(oldLog)
	if err != nil {
		t.Fatal(err)
	}
	newLog := writeMuseSessionLog(t, dataHome, "2026", "08", "17", "new", workspace)
	contents, err := os.ReadFile(newLog)
	if err != nil {
		t.Fatal(err)
	}
	contents = append(contents, []byte(strings.Join([]string{
		`{"sequence":3,"recorded_at":3000000,"payload_type":"runtime.user_intent.materialized","payload":{"outcome":{"kind":"top_level_turn_started","run_id":"run-1"}}}`,
		`{"sequence":4,"recorded_at":4000000,"payload_type":"runtime.session","payload":{"kind":"run","run_id":"run-1","event":{"kind":"assistant_message_committed","message_id":"message-1","text":"new reply"}}}`,
		`{"sequence":5,"recorded_at":5000000,"payload_type":"runtime.session","payload":{"kind":"run","run_id":"run-1","event":{"kind":"terminal","terminal":"completed"}}}`,
		"",
	}, "\n"))...)
	if err := os.WriteFile(newLog, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(oldLog, time.Unix(10, 0), time.Unix(10, 0)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(newLog, time.Unix(20, 0), time.Unix(20, 0)); err != nil {
		t.Fatal(err)
	}

	state := &museSessionLogState{path: oldLog, offset: oldInfo.Size(), initialized: true}
	task := db.Task{ID: "task-1", Agent: "muse", WorktreePath: &workspace}
	events, ok := (&Service{}).museSessionLogEvents(task, db.Project{}, state, time.Now())
	if !ok {
		t.Fatal("museSessionLogEvents() did not use the restarted session")
	}
	if state.path != newLog {
		t.Fatalf("state.path = %q, want %q", state.path, newLog)
	}
	wantKinds := []agentstream.EventKind{agentstream.EventTurnStarted, agentstream.EventAssistantMessage, agentstream.EventTurnCompleted}
	if len(events) != len(wantKinds) {
		t.Fatalf("events = %#v, want %d events", events, len(wantKinds))
	}
	for i, want := range wantKinds {
		if events[i].Kind != want {
			t.Fatalf("events[%d].Kind = %s, want %s", i, events[i].Kind, want)
		}
	}
}

func TestReadMuseSessionLogEventsMapsStructuredMuseEvents(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "session.jsonl")
	initial := `{"sequence":1,"recorded_at":1000000,"payload_type":"runtime.session.metadata","payload":{"kind":"metadata"}}` + "\n"
	if err := os.WriteFile(logPath, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}
	state := &museSessionLogState{path: logPath}
	task := db.Task{ID: "task-1", Agent: "muse"}
	if events, err := readMuseSessionLogEvents(task, state, time.Unix(10, 0)); err != nil || len(events) != 0 {
		t.Fatalf("initial read = (%#v, %v), want no backfill", events, err)
	}

	appendLines := strings.Join([]string{
		`{"sequence":2,"recorded_at":2000000,"payload_type":"runtime.user_intent.materialized","payload":{"outcome":{"kind":"top_level_turn_started","run_id":"run-1"}}}`,
		`{"sequence":3,"recorded_at":3000000,"payload_type":"runtime.session","payload":{"kind":"run","run_id":"run-1","event":{"kind":"reasoning_committed","text":"private reasoning must not leak"}}}`,
		`{"sequence":4,"recorded_at":4000000,"payload_type":"runtime.session","payload":{"kind":"run","run_id":"run-1","event":{"kind":"assistant_message_committed","message_id":"msg-1","text":"중간 진행 상황"}}}`,
		`{"sequence":5,"recorded_at":5000000,"payload_type":"runtime.session","payload":{"kind":"run","run_id":"run-1","event":{"kind":"assistant_tool_calls_committed","tool_calls":[{"call_id":"call-1","name":"bash","args":"{\"command\":\"git status --short\",\"description\":\"Check status\"}"}]}}}`,
		`{"sequence":6,"recorded_at":6000000,"payload_type":"runtime.session","payload":{"kind":"task","event":{"kind":"output","chunk":"{\"chunk_id\":\"exec-1\",\"command\":\"git status --short\",\"description\":\"Check status\",\"exit_code\":0,\"terminal_status\":\"completed\",\"output\":\" M file.go\"}"}}}`,
		`{"sequence":7,"recorded_at":7000000,"payload_type":"runtime.session","payload":{"kind":"run","run_id":"run-1","event":{"kind":"terminal","terminal":"completed"}}}`,
		"",
	}, "\n")
	file, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(appendLines); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	events, err := readMuseSessionLogEvents(task, state, time.Unix(10, 0))
	if err != nil {
		t.Fatal(err)
	}
	kinds := make([]agentstream.EventKind, 0, len(events))
	for _, event := range events {
		kinds = append(kinds, event.Kind)
	}
	wantKinds := []agentstream.EventKind{
		agentstream.EventTurnStarted,
		agentstream.EventThinkingDelta,
		agentstream.EventAssistantMessage,
		agentstream.EventToolStarted,
		agentstream.EventCommandCompleted,
		agentstream.EventTurnCompleted,
	}
	if len(kinds) != len(wantKinds) {
		t.Fatalf("event kinds = %#v, want %#v", kinds, wantKinds)
	}
	for i := range wantKinds {
		if kinds[i] != wantKinds[i] {
			t.Fatalf("event kinds = %#v, want %#v", kinds, wantKinds)
		}
	}
	if events[1].Text != "" {
		t.Fatalf("thinking event leaked reasoning text: %q", events[1].Text)
	}
	if events[2].Text != "중간 진행 상황" {
		t.Fatalf("assistant text = %q", events[2].Text)
	}
	if events[3].Tool == nil || !strings.Contains(events[3].Tool.Input, "git status --short") {
		t.Fatalf("tool event = %#v, want parsed command input", events[3])
	}
	if events[4].Command == nil || events[4].Command.Stdout != " M file.go" {
		t.Fatalf("command event = %#v, want command output", events[4])
	}
}

func writeMuseSessionLog(t *testing.T, dataHome, year, month, day, sessionID, workspace string) string {
	t.Helper()
	logDir := filepath.Join(dataHome, "muse", "sessions", year, month, day, sessionID)
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(logDir, "session.jsonl")
	contents := strings.Join([]string{
		`{"sequence":1,"payload_type":"runtime.session.metadata","payload":{"kind":"metadata","record":{"workspace_root":` + strconv.Quote(workspace) + `}}}`,
		`{"sequence":2,"payload_type":"runtime.session.route_facts","payload":{"kind":"route_facts","record":{"cwd":` + strconv.Quote(workspace) + `}}}`,
		"",
	}, "\n")
	if err := os.WriteFile(logPath, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return logPath
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

func TestMuseLogEventsClassifiesThinkingWorkingAndDone(t *testing.T) {
	logs := strings.Join([]string{
		"◆ Thinking (3m 17s · esc to interrupt)",
		"",
		"◆ Ran command · Read new file and parent commit · ✓ · 0.7s · ctrl+o",
		"",
		"◆ Finished · Check PR and jf status · ✓ · ctrl+o",
	}, "\n")
	state := &museLogState{}

	events := museLogEvents("task-1", "turn-1", "muse", logs, state, time.Unix(1, 0))
	if len(events) != 3 {
		t.Fatalf("len(events) = %d, want thinking, working, done", len(events))
	}
	if events[0].Kind != agentstream.EventThinkingDelta || !strings.Contains(events[0].Text, "Thinking") {
		t.Fatalf("events[0] = %#v, want thinking progress", events[0])
	}
	if events[1].Kind != agentstream.EventToolStarted || events[1].Tool == nil || !strings.Contains(events[1].Tool.Input, "Read new file") {
		t.Fatalf("events[1] = %#v, want working progress", events[1])
	}
	if events[2].Kind != agentstream.EventCommandCompleted || events[2].Command == nil || !strings.Contains(events[2].Command.Stdout, "Finished") {
		t.Fatalf("events[2] = %#v, want done progress", events[2])
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
