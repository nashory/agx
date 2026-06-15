package desktop

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	dirtyThread  bool
}

func newFakeCodexRuntime() *fakeCodexRuntime {
	return &fakeCodexRuntime{
		events:       make(chan codexapp.Notification, 16),
		nextThreadID: "thread-1",
		nextTurnID:   "turn-1",
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
	return codexapp.ThreadStartResponse{}, nil
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

func (f *fakeCodexRuntime) Close() error {
	close(f.events)
	return nil
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
	event, ok := mapClaudeStreamLine(task, "turn-1", []byte(`{"type":"assistant","message":{"id":"msg-1","content":[{"type":"text","text":"hello\nworld"}]}}`))
	if !ok {
		t.Fatal("mapClaudeStreamLine returned ok=false")
	}
	if event.Kind != "assistant_message" || event.Text != "hello\nworld" || event.TaskID != "task-1" {
		t.Fatalf("event = %#v", event)
	}
}

func TestMapClaudeStreamLineMapsToolUse(t *testing.T) {
	task := db.Task{ID: "task-1", Agent: "claude"}
	event, ok := mapClaudeStreamLine(task, "turn-1", []byte(`{"type":"assistant","message":{"id":"msg-1","content":[{"type":"tool_use","id":"tool-1","name":"Bash","input":{"command":"git status"}}]}}`))
	if !ok {
		t.Fatal("mapClaudeStreamLine returned ok=false")
	}
	if event.Kind != "tool_started" || event.Tool == nil || event.Tool.Name != "Bash" || event.Tool.Input != `{"command":"git status"}` {
		t.Fatalf("event = %#v", event)
	}
}

func TestMapClaudeStreamLineMapsResult(t *testing.T) {
	task := db.Task{ID: "task-1", Agent: "claude"}
	event, ok := mapClaudeStreamLine(task, "turn-1", []byte(`{"type":"result","subtype":"success","is_error":false,"duration_ms":1500,"session_id":"session-1","usage":{"input_tokens":2,"output_tokens":3,"cache_creation_input_tokens":5,"cache_read_input_tokens":7}}`))
	if !ok {
		t.Fatal("mapClaudeStreamLine returned ok=false")
	}
	if event.Kind != "turn_completed" || event.Result == nil || event.Result.Tokens != 17 || event.Cursor != "session-1" {
		t.Fatalf("event = %#v", event)
	}
}

func TestMapClaudeStreamLineSkipsNonJSONAndSystemEvents(t *testing.T) {
	task := db.Task{ID: "task-1", Agent: "claude"}
	if _, ok := mapClaudeStreamLine(task, "turn-1", []byte("Claude Code Enterprise")); ok {
		t.Fatal("banner line should be skipped")
	}
	if _, ok := mapClaudeStreamLine(task, "turn-1", []byte(`{"type":"system","subtype":"init"}`)); ok {
		t.Fatal("system line should be skipped")
	}
}

func TestClaudeStreamArgsCreatesSessionBeforeCursor(t *testing.T) {
	threadID := "11111111-1111-1111-1111-111111111111"
	task := db.Task{ID: "task-1", Agent: "claude", AgentThreadID: &threadID, AllMighty: true}

	args := strings.Join(claudeStreamArgs(task, "hello"), " ")
	if !strings.Contains(args, "--session-id "+threadID) {
		t.Fatalf("args = %q, want --session-id", args)
	}
	if strings.Contains(args, "--resume") {
		t.Fatalf("args = %q, did not expect --resume", args)
	}
	if !strings.Contains(args, "--permission-mode bypassPermissions") || !strings.Contains(args, "--dangerously-skip-permissions") {
		t.Fatalf("args = %q, want all-mighty flags", args)
	}
}

func TestClaudeStreamArgsResumesAfterCursor(t *testing.T) {
	threadID := "11111111-1111-1111-1111-111111111111"
	cursor := threadID
	task := db.Task{ID: "task-1", Agent: "claude", AgentThreadID: &threadID, AgentEventCursor: &cursor}

	args := strings.Join(claudeStreamArgs(task, "hello"), " ")
	if !strings.Contains(args, "--resume "+threadID) {
		t.Fatalf("args = %q, want --resume", args)
	}
	if strings.Contains(args, "--session-id") {
		t.Fatalf("args = %q, did not expect --session-id", args)
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
