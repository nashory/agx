package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nashory/agx/internal/codexapp"
	"github.com/nashory/agx/internal/db"
	agxdiscord "github.com/nashory/agx/internal/discord"
)

type fakeCodexRuntime struct {
	events       chan codexapp.Notification
	startedCwd   string
	turnCwd      string
	startedText  string
	steeredText  string
	interrupted  string
	nextThreadID string
	nextTurnID   string
	threadErr    error
	resumeErr    error
	dirtyThread  bool
	approvals    chan codexapp.ReviewDecision
}

func newFakeCodexRuntime() *fakeCodexRuntime {
	return &fakeCodexRuntime{
		events:       make(chan codexapp.Notification, 16),
		nextThreadID: "thread-1",
		nextTurnID:   "turn-1",
		approvals:    make(chan codexapp.ReviewDecision, 1),
	}
}

func (f *fakeCodexRuntime) Initialize(context.Context) (codexapp.InitializeResponse, error) {
	return codexapp.InitializeResponse{}, nil
}

func (f *fakeCodexRuntime) ThreadStart(_ context.Context, cwd string, allMighty bool) (codexapp.ThreadStartResponse, error) {
	f.startedCwd = cwd
	if f.dirtyThread {
		if err := os.WriteFile(filepath.Join(cwd, "dirty.txt"), []byte("dirty"), 0o644); err != nil {
			return codexapp.ThreadStartResponse{}, err
		}
	}
	if f.threadErr != nil {
		return codexapp.ThreadStartResponse{}, f.threadErr
	}
	return codexapp.ThreadStartResponse{Thread: codexapp.Thread{ID: f.nextThreadID, Cwd: cwd}}, nil
}

func (f *fakeCodexRuntime) ThreadResume(context.Context, string) (codexapp.ThreadStartResponse, error) {
	return codexapp.ThreadStartResponse{}, f.resumeErr
}

func (f *fakeCodexRuntime) TurnStart(_ context.Context, threadID, text, cwd string, allMighty bool) (codexapp.TurnStartResponse, error) {
	f.startedText = text
	f.turnCwd = cwd
	return codexapp.TurnStartResponse{Turn: codexapp.Turn{ID: f.nextTurnID, Status: "running"}}, nil
}

func (f *fakeCodexRuntime) TurnSteer(_ context.Context, threadID, turnID, text string) (codexapp.TurnSteerResponse, error) {
	f.steeredText = text
	return codexapp.TurnSteerResponse{TurnID: turnID}, nil
}

func (f *fakeCodexRuntime) TurnInterrupt(_ context.Context, threadID, turnID string) error {
	f.interrupted = turnID
	return nil
}

func (f *fakeCodexRuntime) Events() <-chan codexapp.Notification {
	return f.events
}

func (f *fakeCodexRuntime) ApproveRequest(_ codexapp.Notification, decision codexapp.ReviewDecision) error {
	f.approvals <- decision
	return nil
}

func (f *fakeCodexRuntime) CancelInputRequest(codexapp.Notification) error {
	return nil
}

func (f *fakeCodexRuntime) Close() error {
	close(f.events)
	return nil
}

func TestEnsureCodexThreadPreservesContextOnTransientResumeFailure(t *testing.T) {
	app, project := newTestApp(t)
	threadID := "existing-thread"
	streamKind := codexapp.StreamKind
	task, err := app.store.CreateTask(project.ID, "structured", nil, "codex", db.StatusActive)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.store.UpdateTaskAgentStream(task.ID, &threadID, nil, &streamKind); err != nil {
		t.Fatal(err)
	}
	task, err = app.store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	fake := newFakeCodexRuntime()
	fake.resumeErr = errors.New("app-server connection closed")

	if _, err := app.agentEvents.ensureCodexThread(context.Background(), fake, task, project); err == nil || !strings.Contains(err.Error(), "connection closed") {
		t.Fatalf("ensureCodexThread() error = %v, want resume failure", err)
	}
	if fake.startedCwd != "" {
		t.Fatalf("ThreadStart cwd = %q, want no replacement thread", fake.startedCwd)
	}
	updated, err := app.store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.AgentThreadID == nil || *updated.AgentThreadID != threadID {
		t.Fatalf("AgentThreadID = %#v, want preserved thread", updated.AgentThreadID)
	}
}

func TestDesktopCodexApprovalUsesTaskSafetyMode(t *testing.T) {
	app, project := newTestApp(t)
	task, err := app.store.CreateTaskRuntimeMode(db.NewTaskID(), project.ID, "structured", nil, "codex", true, db.StatusActive, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	threadID := "thread-approval"
	app.agentEvents.rememberThread(task.ID, threadID)
	fake := newFakeCodexRuntime()
	notification := codexapp.Notification{
		Method: codexapp.NotifyCommandApprovalRequest,
		Params: json.RawMessage(`{"threadId":"thread-approval"}`),
	}
	app.agentEvents.answerCodexApproval(fake, notification)
	select {
	case decision := <-fake.approvals:
		if decision != codexapp.DecisionAccept {
			t.Fatalf("decision = %q, want accept", decision)
		}
	case <-time.After(time.Second):
		t.Fatal("approval request was not answered")
	}
}

func TestAgentEventServiceStartsCodexThreadAndPersistsMetadata(t *testing.T) {
	app, project := newTestApp(t)
	fake := newFakeCodexRuntime()
	app.agentEvents.startCodex = func(context.Context) (codexRuntime, error) {
		return fake, nil
	}
	task, err := app.store.CreateTask(project.ID, "structured", nil, "codex", db.StatusActive)
	if err != nil {
		t.Fatal(err)
	}

	if err := app.agentEvents.SendTaskMessage(context.Background(), task, project, "hello"); err != nil {
		t.Fatal(err)
	}
	if fake.startedCwd != project.Path || fake.startedText != "hello" {
		t.Fatalf("startedCwd=%q startedText=%q", fake.startedCwd, fake.startedText)
	}
	updated, err := app.store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.AgentThreadID == nil || *updated.AgentThreadID != "thread-1" {
		t.Fatalf("AgentThreadID = %#v, want thread-1", updated.AgentThreadID)
	}
	if updated.AgentStreamKind == nil || *updated.AgentStreamKind != codexapp.StreamKind {
		t.Fatalf("AgentStreamKind = %#v, want %s", updated.AgentStreamKind, codexapp.StreamKind)
	}
}

func TestAppSendMessageRejectsDiscordStructuredTask(t *testing.T) {
	app, project := newTestApp(t)
	fake := newFakeCodexRuntime()
	app.agentEvents.startCodex = func(context.Context) (codexRuntime, error) {
		return fake, nil
	}
	task, err := app.store.CreateTaskRuntimeModeInterface(db.NewTaskID(), project.ID, "structured", nil, "codex", false, db.TaskInterfaceDiscord, db.StatusActive, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	threadID := "thread-1"
	streamKind := codexapp.StreamKind
	if err := app.store.UpdateTaskAgentStream(task.ID, &threadID, nil, &streamKind); err != nil {
		t.Fatal(err)
	}

	if err := app.SendMessage(task.ID, "from desktop"); err == nil || !strings.Contains(err.Error(), "controlled by Discord") {
		t.Fatalf("SendMessage error = %v, want controlled by Discord", err)
	}
}

func TestAppDeleteTaskStopsStructuredRuntime(t *testing.T) {
	app, project := newTestApp(t)
	fake := newFakeCodexRuntime()
	app.agentEvents.startCodex = func(context.Context) (codexRuntime, error) {
		return fake, nil
	}
	task, err := app.store.CreateTaskRuntimeModeInterface(db.NewTaskID(), project.ID, "structured", nil, "codex", false, db.TaskInterfaceDiscord, db.StatusActive, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.agentEvents.SendTaskMessage(context.Background(), task, project, "hello"); err != nil {
		t.Fatal(err)
	}

	if err := app.DeleteTask(task.ID); err != nil {
		t.Fatal(err)
	}
	if fake.interrupted != "turn-1" {
		t.Fatalf("interrupted = %q, want turn-1", fake.interrupted)
	}
	if _, err := app.store.GetTask(task.ID); err != db.ErrTaskNotFound {
		t.Fatalf("GetTask error = %v, want ErrTaskNotFound", err)
	}
}

func TestCreateStructuredAgentTaskReportsCleanupFailure(t *testing.T) {
	app, project := newTestApp(t)
	fake := newFakeCodexRuntime()
	fake.dirtyThread = true
	fake.threadErr = errors.New("codex thread failed")
	app.agentEvents.startCodex = func(context.Context) (codexRuntime, error) {
		return fake, nil
	}

	_, err := app.createStructuredAgentTask(context.Background(), project.ID, "cleanup", "", "codex", true, db.WorkspaceModeWorktree)
	if err == nil {
		t.Fatal("createStructuredAgentTask succeeded, want cleanup failure")
	}
	message := err.Error()
	if !strings.Contains(message, "codex thread failed") || !strings.Contains(message, "prepare structured desktop task cleanup failed") || !strings.Contains(message, "remove prepared desktop worktree") {
		t.Fatalf("error = %q, want primary and cleanup details", message)
	}
	tasks, err := app.store.ListTasks(project.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 0 {
		t.Fatalf("tasks = %d, want cleanup to delete task row", len(tasks))
	}
	if fake.startedCwd == "" {
		t.Fatal("started cwd is empty")
	}
	if _, err := os.Stat(fake.startedCwd); err != nil {
		t.Fatalf("dirty worktree stat error = %v, want leftover worktree for cleanup warning", err)
	}
}

func TestAgentEventServiceUsesTaskWorktreeCwd(t *testing.T) {
	app, project := newTestApp(t)
	fake := newFakeCodexRuntime()
	app.agentEvents.startCodex = func(context.Context) (codexRuntime, error) {
		return fake, nil
	}
	task, err := app.store.CreateTask(project.ID, "structured", nil, "codex", db.StatusActive)
	if err != nil {
		t.Fatal(err)
	}
	worktreePath := t.TempDir()
	if err := app.store.UpdateTaskRuntimeBase(task.ID, nil, task.Status, &worktreePath, nil, nil); err != nil {
		t.Fatal(err)
	}
	task, err = app.store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}

	if err := app.agentEvents.SendTaskMessage(context.Background(), task, project, "hello"); err != nil {
		t.Fatal(err)
	}
	if fake.startedCwd != worktreePath {
		t.Fatalf("startedCwd=%q, want worktree %q", fake.startedCwd, worktreePath)
	}
	if fake.turnCwd != worktreePath {
		t.Fatalf("turnCwd=%q, want worktree %q", fake.turnCwd, worktreePath)
	}
}

func TestAgentEventServiceSubscribesAndMapsCodexEvents(t *testing.T) {
	app, project := newTestApp(t)
	fake := newFakeCodexRuntime()
	app.agentEvents.startCodex = func(context.Context) (codexRuntime, error) {
		return fake, nil
	}
	threadID := "thread-1"
	streamKind := codexapp.StreamKind
	task, err := app.store.CreateTask(project.ID, "structured", nil, "codex", db.StatusActive)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.store.UpdateTaskAgentStream(task.ID, &threadID, nil, &streamKind); err != nil {
		t.Fatal(err)
	}
	events, err := app.agentEvents.SubscribeAgentEvents(context.Background(), agxdiscord.TaskSummary{
		ID:              task.ID,
		Agent:           task.Agent,
		AgentThreadID:   &threadID,
		AgentStreamKind: &streamKind,
	})
	if err != nil {
		t.Fatal(err)
	}
	fake.events <- codexapp.Notification{
		Method: codexapp.NotifyAgentMessageDelta,
		Params: []byte(`{"threadId":"thread-1","turnId":"turn-1","itemId":"item-1","delta":"hello"}`),
	}

	event := <-events
	if event.Text != "hello" || event.TaskID != task.ID {
		t.Fatalf("event = %#v", event)
	}
}

func TestAgentEventServiceStopClearsStructuredRuntime(t *testing.T) {
	app, project := newTestApp(t)
	fake := newFakeCodexRuntime()
	app.agentEvents.startCodex = func(context.Context) (codexRuntime, error) {
		return fake, nil
	}
	threadID := "thread-1"
	streamKind := codexapp.StreamKind
	task, err := app.store.CreateTask(project.ID, "structured", nil, "codex", db.StatusActive)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.store.UpdateTaskAgentStream(task.ID, &threadID, nil, &streamKind); err != nil {
		t.Fatal(err)
	}
	task, err = app.store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	app.agentEvents.activeTurns[task.ID] = "turn-1"

	if err := app.agentEvents.StopTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if fake.interrupted != "turn-1" {
		t.Fatalf("interrupted = %q, want turn-1", fake.interrupted)
	}
	updated, err := app.store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.AgentStreamKind != nil || updated.AgentThreadID != nil {
		t.Fatalf("stream metadata was not cleared: %#v", updated)
	}
}

func TestAgentEventServiceClearResetsCodexContext(t *testing.T) {
	app, project := newTestApp(t)
	fake := newFakeCodexRuntime()
	fake.nextThreadID = "new-thread"
	app.agentEvents.startCodex = func(context.Context) (codexRuntime, error) {
		return fake, nil
	}
	oldThreadID := "old-thread"
	oldCursor := "old-cursor"
	streamKind := codexapp.StreamKind
	task, err := app.store.CreateTask(project.ID, "structured", nil, "codex", db.StatusActive)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.store.UpdateTaskAgentStream(task.ID, &oldThreadID, &oldCursor, &streamKind); err != nil {
		t.Fatal(err)
	}
	task, err = app.store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	app.agentEvents.activeTurns[task.ID] = "turn-1"

	if err := app.agentEvents.SendTaskMessage(context.Background(), task, project, "/clear"); err != nil {
		t.Fatal(err)
	}
	if fake.interrupted != "turn-1" {
		t.Fatalf("interrupted = %q, want turn-1", fake.interrupted)
	}
	if fake.startedText != "" {
		t.Fatalf("startedText = %q, want no model turn for /clear", fake.startedText)
	}
	updated, err := app.store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.AgentThreadID == nil || *updated.AgentThreadID != "new-thread" {
		t.Fatalf("AgentThreadID = %#v, want new-thread", updated.AgentThreadID)
	}
	if updated.AgentEventCursor != nil {
		t.Fatalf("AgentEventCursor = %#v, want nil after /clear", updated.AgentEventCursor)
	}
}

func TestAgentEventServiceClearResetsClaudeContext(t *testing.T) {
	app, project := newTestApp(t)
	oldThreadID := "old-thread"
	oldCursor := "old-cursor"
	streamKind := claudeStreamKind
	task, err := app.store.CreateTask(project.ID, "structured", nil, "claude", db.StatusActive)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.store.UpdateTaskAgentStream(task.ID, &oldThreadID, &oldCursor, &streamKind); err != nil {
		t.Fatal(err)
	}
	task, err = app.store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	app.agentEvents.activeTurns[task.ID] = "turn-1"
	app.agentEvents.claudeQueues[task.ID] = []string{"queued"}

	if err := app.agentEvents.SendTaskMessage(context.Background(), task, project, "/clear"); err != nil {
		t.Fatal(err)
	}
	updated, err := app.store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.AgentThreadID == nil || *updated.AgentThreadID == oldThreadID {
		t.Fatalf("AgentThreadID = %#v, want fresh Claude session id", updated.AgentThreadID)
	}
	if updated.AgentEventCursor != nil {
		t.Fatalf("AgentEventCursor = %#v, want nil after /clear", updated.AgentEventCursor)
	}
	if updated.AgentStreamKind == nil || *updated.AgentStreamKind != claudeStreamKind {
		t.Fatalf("AgentStreamKind = %#v, want %s", updated.AgentStreamKind, claudeStreamKind)
	}
	app.agentEvents.mu.Lock()
	activeTurn := app.agentEvents.activeTurns[task.ID]
	queued := app.agentEvents.claudeQueues[task.ID]
	app.agentEvents.mu.Unlock()
	if activeTurn != "" || len(queued) != 0 {
		t.Fatalf("activeTurn=%q queued=%#v, want cleared runtime state", activeTurn, queued)
	}
}

func TestMapClaudeStreamLineMapsAssistantText(t *testing.T) {
	task := db.Task{ID: "task-1", Agent: "claude"}
	events := mapClaudeStreamLine(task, "turn-1", []byte(`{"type":"assistant","message":{"id":"msg-1","content":[{"type":"text","text":"hello\nworld"}]}}`))
	if len(events) != 1 {
		t.Fatalf("events = %#v, want one", events)
	}
	event := events[0]
	if event.Kind != "assistant_message" || event.Text != "hello\nworld" || event.TaskID != "task-1" {
		t.Fatalf("event = %#v", event)
	}
}

func TestMapClaudeStreamLineMapsToolUse(t *testing.T) {
	task := db.Task{ID: "task-1", Agent: "claude"}
	events := mapClaudeStreamLine(task, "turn-1", []byte(`{"type":"assistant","message":{"id":"msg-1","content":[{"type":"tool_use","id":"tool-1","name":"Bash","input":{"command":"git status"}}]}}`))
	if len(events) != 1 {
		t.Fatalf("events = %#v, want one", events)
	}
	event := events[0]
	if event.Kind != "tool_started" || event.Tool == nil || event.Tool.Name != "Bash" || event.Tool.Input != `{"command":"git status"}` {
		t.Fatalf("event = %#v", event)
	}
}

func TestMapClaudeStreamLineMapsResult(t *testing.T) {
	task := db.Task{ID: "task-1", Agent: "claude"}
	events := mapClaudeStreamLine(task, "turn-1", []byte(`{"type":"result","subtype":"success","is_error":false,"duration_ms":1500,"session_id":"session-1","usage":{"input_tokens":2,"output_tokens":3,"cache_creation_input_tokens":5,"cache_read_input_tokens":7}}`))
	if len(events) != 1 {
		t.Fatalf("events = %#v, want one", events)
	}
	event := events[0]
	if event.Kind != "turn_completed" || event.Result == nil || event.Result.Tokens != 17 || event.Cursor != "session-1" {
		t.Fatalf("event = %#v", event)
	}
}

func TestMapClaudeStreamLineSkipsNonJSONAndSystemEvents(t *testing.T) {
	task := db.Task{ID: "task-1", Agent: "claude"}
	if events := mapClaudeStreamLine(task, "turn-1", []byte("Claude Code Enterprise")); len(events) != 0 {
		t.Fatal("banner line should be skipped")
	}
	if events := mapClaudeStreamLine(task, "turn-1", []byte(`{"type":"system","subtype":"init"}`)); len(events) != 0 {
		t.Fatal("system line should be skipped")
	}
}

func TestMapClaudeStreamLineKeepsTextAndAllToolUses(t *testing.T) {
	task := db.Task{ID: "task-1", Agent: "claude"}
	events := mapClaudeStreamLine(task, "turn-1", []byte(`{"type":"assistant","message":{"id":"msg-1","content":[{"type":"text","text":"checking"},{"type":"tool_use","id":"tool-1","name":"Read","input":{"file_path":"a"}},{"type":"tool_use","id":"tool-2","name":"Grep","input":{"pattern":"b"}}]}}`))
	if len(events) != 3 {
		t.Fatalf("events = %#v, want text and two tools", events)
	}
	if events[0].Kind != "assistant_message" || events[0].Text != "checking" || events[1].Tool == nil || events[1].Tool.ID != "tool-1" || events[2].Tool == nil || events[2].Tool.ID != "tool-2" {
		t.Fatalf("events = %#v, want every content block represented", events)
	}
}

func TestMapClaudeStreamLineMapsToolResults(t *testing.T) {
	task := db.Task{ID: "task-1", Agent: "claude"}
	events := mapClaudeStreamLine(task, "turn-1", []byte(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"tool-1","content":"ok","is_error":false},{"type":"tool_result","tool_use_id":"tool-2","content":[{"type":"text","text":"failed"}],"is_error":true}]}}`))
	if len(events) != 2 {
		t.Fatalf("events = %#v, want two tool results", events)
	}
	if events[0].Command == nil || events[0].Command.ExitCode == nil || *events[0].Command.ExitCode != 0 || events[0].Command.Stdout != "ok" {
		t.Fatalf("success event = %#v", events[0])
	}
	if events[1].Command == nil || events[1].Command.ExitCode == nil || *events[1].Command.ExitCode != 1 || events[1].Command.Stderr != "failed" {
		t.Fatalf("failure event = %#v", events[1])
	}
}

func TestClaudeStreamArgsCreatesSessionBeforeCursor(t *testing.T) {
	threadID := "11111111-1111-1111-1111-111111111111"
	task := db.Task{ID: "task-1", Agent: "claude", AgentThreadID: &threadID, AllMighty: true}

	args := strings.Join(claudeStreamArgs(task), " ")
	if !strings.Contains(args, "--session-id "+threadID) {
		t.Fatalf("args = %q, want --session-id", args)
	}
	if strings.Contains(args, "--resume") {
		t.Fatalf("args = %q, did not expect --resume", args)
	}
	if !strings.Contains(args, "--permission-mode bypassPermissions") || !strings.Contains(args, "--dangerously-skip-permissions") {
		t.Fatalf("args = %q, want all-mighty flags", args)
	}
	if !strings.Contains(args, "--disallowedTools AskUserQuestion") {
		t.Fatalf("args = %q, want headless question tool disabled", args)
	}
}

func TestClaudeStreamArgsResumesAfterCursor(t *testing.T) {
	threadID := "11111111-1111-1111-1111-111111111111"
	cursor := threadID
	task := db.Task{ID: "task-1", Agent: "claude", AgentThreadID: &threadID, AgentEventCursor: &cursor}

	args := strings.Join(claudeStreamArgs(task), " ")
	if !strings.Contains(args, "--resume "+threadID) {
		t.Fatalf("args = %q, want --resume", args)
	}
	if strings.Contains(args, "--session-id") {
		t.Fatalf("args = %q, did not expect --session-id", args)
	}
	if !strings.Contains(args, "--disallowedTools AskUserQuestion") {
		t.Fatalf("args = %q, want headless question tool disabled", args)
	}
}

func TestClaudeSessionAlreadyInUse(t *testing.T) {
	err := fmt.Errorf("Claude stream failed: Error: Session ID abc is already in use.")
	if !claudeSessionAlreadyInUse(err) {
		t.Fatal("expected already-in-use error to be detected")
	}
}

func TestMergeQueuedClaudeMessages(t *testing.T) {
	got := mergeQueuedClaudeMessages([]string{" first ", "", "second\nline", " third "})
	want := "first\n\nsecond\nline\n\nthird"
	if got != want {
		t.Fatalf("mergeQueuedClaudeMessages() = %q, want %q", got, want)
	}
}

func TestClaudeTurnCleanupDoesNotClearReplacementTurn(t *testing.T) {
	service := newAgentEventService(&App{})
	t.Cleanup(func() { _ = service.Close() })
	task := db.Task{ID: "task-1"}
	_, replacementCancel := context.WithCancel(context.Background())
	t.Cleanup(replacementCancel)
	service.activeTurns[task.ID] = "replacement-turn"
	service.turnCancels[task.ID] = replacementCancel

	service.finishClaudeTurn(task, db.Project{}, "old-turn", false)

	if got := service.activeTurns[task.ID]; got != "replacement-turn" {
		t.Fatalf("active turn = %q, want replacement-turn", got)
	}
	if service.turnCancels[task.ID] == nil {
		t.Fatal("replacement turn cancel was cleared")
	}
}
