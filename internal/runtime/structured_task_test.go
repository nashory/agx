package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
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
	threadCwd    string
	turnCwd      string
	startedText  string
	steeredText  string
	steeredTurn  string
	interrupted  string
	nextThreadID string
	nextTurnID   string
	threadErr    error
	resumeErr    error
	resumeResult codexapp.ThreadStartResponse
	dirtyThread  bool
	stderr       string
	inputCancels chan string
	approvals    chan codexapp.ReviewDecision
}

func newFakeCodexRuntime() *fakeCodexRuntime {
	return &fakeCodexRuntime{
		events:       make(chan codexapp.Notification, 1),
		nextThreadID: "thread-1",
		nextTurnID:   "turn-1",
		inputCancels: make(chan string, 1),
		approvals:    make(chan codexapp.ReviewDecision, 1),
	}
}

func (f *fakeCodexRuntime) Initialize(context.Context) (codexapp.InitializeResponse, error) {
	return codexapp.InitializeResponse{}, nil
}

func (f *fakeCodexRuntime) ThreadStart(_ context.Context, cwd string, allMighty bool) (codexapp.ThreadStartResponse, error) {
	f.threadCwd = cwd
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
	return f.resumeResult, f.resumeErr
}

func (f *fakeCodexRuntime) TurnStart(_ context.Context, threadID, text, cwd string, allMighty bool) (codexapp.TurnStartResponse, error) {
	f.startedText = text
	f.turnCwd = cwd
	return codexapp.TurnStartResponse{Turn: codexapp.Turn{ID: f.nextTurnID, Status: "running"}}, nil
}

func (f *fakeCodexRuntime) TurnSteer(_ context.Context, _ string, turnID, text string) (codexapp.TurnSteerResponse, error) {
	f.steeredTurn = turnID
	f.steeredText = text
	return codexapp.TurnSteerResponse{TurnID: turnID}, nil
}

func (f *fakeCodexRuntime) TurnInterrupt(ctx context.Context, threadID, turnID string) error {
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

func (f *fakeCodexRuntime) CancelInputRequest(notification codexapp.Notification) error {
	f.inputCancels <- notification.Method
	return nil
}

func (f *fakeCodexRuntime) RecentStderr() string {
	return f.stderr
}

func (f *fakeCodexRuntime) Close() error {
	close(f.events)
	return nil
}

func TestEnsureCodexThreadPreservesContextOnTransientResumeFailure(t *testing.T) {
	store, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	project, err := store.EnsureProject(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTask(project.ID, "structured", nil, "codex", db.StatusActive)
	if err != nil {
		t.Fatal(err)
	}
	threadID := "existing-thread"
	streamKind := codexapp.StreamKind
	if err := store.UpdateTaskAgentStream(task.ID, &threadID, nil, &streamKind); err != nil {
		t.Fatal(err)
	}
	task, err = store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService("test")
	service.store = store
	t.Cleanup(func() { _ = service.agents.Close() })
	fake := newFakeCodexRuntime()
	fake.resumeErr = errors.New("app-server connection closed")

	if _, err := service.agents.ensureCodexThread(context.Background(), fake, task, project); err == nil || !strings.Contains(err.Error(), "connection closed") {
		t.Fatalf("ensureCodexThread() error = %v, want resume failure", err)
	}
	if fake.threadCwd != "" {
		t.Fatalf("ThreadStart cwd = %q, want no replacement thread", fake.threadCwd)
	}
	updated, err := store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.AgentThreadID == nil || *updated.AgentThreadID != threadID {
		t.Fatalf("AgentThreadID = %#v, want preserved thread", updated.AgentThreadID)
	}
}

func TestEnsureCodexThreadRestoresInProgressTurn(t *testing.T) {
	store, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	project, err := store.EnsureProject(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTask(project.ID, "structured", nil, "codex", db.StatusActive)
	if err != nil {
		t.Fatal(err)
	}
	threadID := "existing-thread"
	streamKind := codexapp.StreamKind
	if err := store.UpdateTaskAgentStream(task.ID, &threadID, nil, &streamKind); err != nil {
		t.Fatal(err)
	}
	task, err = store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService("test")
	service.store = store
	t.Cleanup(func() { _ = service.agents.Close() })
	fake := newFakeCodexRuntime()
	fake.resumeResult = codexapp.ThreadStartResponse{Thread: codexapp.Thread{
		ID:    threadID,
		Turns: []codexapp.Turn{{ID: "turn-live", Status: codexapp.TurnStatusInProgress}},
	}}
	service.agents.codex = fake

	if err := service.agents.SendTaskMessage(context.Background(), task, project, "follow up"); err != nil {
		t.Fatal(err)
	}
	if fake.steeredTurn != "turn-live" || fake.steeredText != "follow up" {
		t.Fatalf("steered turn=%q text=%q, want recovered active turn", fake.steeredTurn, fake.steeredText)
	}
	if fake.startedText != "" {
		t.Fatalf("TurnStart text = %q, want no new turn", fake.startedText)
	}
}

func TestEnsureCodexThreadMarksInterruptedTurnWithoutRetry(t *testing.T) {
	store, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	project, err := store.EnsureProject(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTask(project.ID, "structured", nil, "codex", db.StatusActive)
	if err != nil {
		t.Fatal(err)
	}
	threadID := "existing-thread"
	streamKind := codexapp.StreamKind
	if err := store.UpdateTaskAgentStream(task.ID, &threadID, nil, &streamKind); err != nil {
		t.Fatal(err)
	}
	task, err = store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService("test")
	service.store = store
	t.Cleanup(func() { _ = service.agents.Close() })
	service.agents.activeTurns[task.ID] = "stale-turn"
	fake := newFakeCodexRuntime()
	fake.resumeResult = codexapp.ThreadStartResponse{Thread: codexapp.Thread{
		ID:    threadID,
		Turns: []codexapp.Turn{{ID: "turn-interrupted", Status: codexapp.TurnStatusInterrupted}},
	}}

	if _, err := service.agents.ensureCodexThread(context.Background(), fake, task, project); err != nil {
		t.Fatal(err)
	}
	if active := service.agents.activeTurns[task.ID]; active != "" {
		t.Fatalf("active turn = %q, want cleared", active)
	}
	updated, err := store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != db.StatusWaiting {
		t.Fatalf("task status = %q, want waiting", updated.Status)
	}
	messages, err := store.ListTaskTranscriptMessages(task.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].Role != "status" || !strings.Contains(messages[0].Body, "not retried automatically") {
		t.Fatalf("transcript messages = %#v, want interruption notice", messages)
	}
}

func TestDelayedCodexCompletionDoesNotClearNewerTurn(t *testing.T) {
	store, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	project, err := store.EnsureProject(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTask(project.ID, "structured", nil, "codex", db.StatusActive)
	if err != nil {
		t.Fatal(err)
	}
	threadID := "thread-1"
	streamKind := codexapp.StreamKind
	if err := store.UpdateTaskAgentStream(task.ID, &threadID, nil, &streamKind); err != nil {
		t.Fatal(err)
	}
	service := NewService("test")
	service.store = store
	fake := newFakeCodexRuntime()
	service.agents.threadToTask[threadID] = task.ID
	service.agents.activeTurns[task.ID] = "turn-new"
	done := make(chan struct{})
	go func() {
		service.agents.forwardCodexEvents(fake)
		close(done)
	}()
	fake.events <- codexapp.Notification{
		Method: codexapp.NotifyTurnCompleted,
		Params: json.RawMessage(`{"threadId":"thread-1","turn":{"id":"turn-old","status":"completed"}}`),
	}
	close(fake.events)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Codex event forwarder did not stop")
	}
	if active := service.agents.activeTurns[task.ID]; active != "turn-new" {
		t.Fatalf("active turn = %q, want turn-new", active)
	}
	updated, err := store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != db.StatusActive {
		t.Fatalf("task status = %q, want active", updated.Status)
	}
}

// newCodexTurnTestService wires a task whose Codex thread is already mapped, so
// a test can push app-server notifications straight through the forwarder.
func newCodexTurnTestService(t *testing.T) (*Service, *fakeCodexRuntime, db.Task) {
	t.Helper()
	store, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	project, err := store.EnsureProject(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTask(project.ID, "structured", nil, "codex", db.StatusActive)
	if err != nil {
		t.Fatal(err)
	}
	threadID := "thread-1"
	streamKind := codexapp.StreamKind
	if err := store.UpdateTaskAgentStream(task.ID, &threadID, nil, &streamKind); err != nil {
		t.Fatal(err)
	}
	task, err = store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService("test")
	service.store = store
	service.paths.ConfigDir = t.TempDir()
	fake := newFakeCodexRuntime()
	service.agents.codex = fake
	service.agents.threadToTask[threadID] = task.ID
	return service, fake, task
}

func runCodexNotifications(t *testing.T, service *Service, fake *fakeCodexRuntime, notifications ...codexapp.Notification) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		service.agents.forwardCodexEvents(fake)
		close(done)
	}()
	for _, notification := range notifications {
		fake.events <- notification
	}
	close(fake.events)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Codex event forwarder did not stop")
	}
}

func codexTurnStarted(turnID string) codexapp.Notification {
	return codexapp.Notification{
		Method: codexapp.NotifyTurnStarted,
		Params: json.RawMessage(fmt.Sprintf(`{"threadId":"thread-1","turn":{"id":%q,"status":"inProgress"}}`, turnID)),
	}
}

func codexTurnCompleted(turnID string) codexapp.Notification {
	return codexapp.Notification{
		Method: codexapp.NotifyTurnCompleted,
		Params: json.RawMessage(fmt.Sprintf(`{"threadId":"thread-1","turn":{"id":%q,"status":"completed"}}`, turnID)),
	}
}

func codexStatusMessages(t *testing.T, service *Service, taskID string) []string {
	t.Helper()
	messages, err := service.store.ListTaskTranscriptMessages(taskID, 20)
	if err != nil {
		t.Fatal(err)
	}
	var bodies []string
	for _, message := range messages {
		if message.Role == "status" {
			bodies = append(bodies, message.Body)
		}
	}
	return bodies
}

func TestCodexTurnWithoutOutputIsReported(t *testing.T) {
	service, fake, task := newCodexTurnTestService(t)
	fake.stderr = "codex: dropped turn input"

	runCodexNotifications(t, service, fake, codexTurnStarted("turn-1"), codexTurnCompleted("turn-1"))

	bodies := codexStatusMessages(t, service, task.ID)
	if len(bodies) != 1 {
		t.Fatalf("status messages = %#v, want one empty-turn report", bodies)
	}
	if !strings.Contains(bodies[0], "without producing any output") {
		t.Fatalf("status message = %q, want empty-turn report", bodies[0])
	}
	if !strings.Contains(bodies[0], "codex: dropped turn input") {
		t.Fatalf("status message = %q, want recent codex stderr", bodies[0])
	}
	updated, err := service.store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != db.StatusWaiting {
		t.Fatalf("task status = %q, want waiting", updated.Status)
	}
}

func TestCodexTurnWithOutputIsNotReported(t *testing.T) {
	service, fake, task := newCodexTurnTestService(t)

	runCodexNotifications(t, service, fake,
		codexTurnStarted("turn-1"),
		codexapp.Notification{
			Method: codexapp.NotifyItemCompleted,
			Params: json.RawMessage(`{"threadId":"thread-1","turnId":"turn-1","item":{"type":"agentMessage","id":"item-1","text":"done"}}`),
		},
		codexTurnCompleted("turn-1"),
	)

	if bodies := codexStatusMessages(t, service, task.ID); len(bodies) != 0 {
		t.Fatalf("status messages = %#v, want none for a turn that produced output", bodies)
	}
}

func TestInterruptedCodexTurnIsNotReportedAsEmpty(t *testing.T) {
	service, fake, task := newCodexTurnTestService(t)
	done := make(chan struct{})
	go func() {
		service.agents.forwardCodexEvents(fake)
		close(done)
	}()

	fake.events <- codexTurnStarted("turn-1")
	// Interrupting is the expected way for a turn to end with no agent output.
	waitForCodexTurn(t, service, task.ID)
	if err := service.agents.InterruptTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	fake.events <- codexTurnCompleted("turn-1")
	close(fake.events)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Codex event forwarder did not stop")
	}

	if fake.interrupted != "turn-1" {
		t.Fatalf("interrupted turn = %q, want turn-1", fake.interrupted)
	}
	if bodies := codexStatusMessages(t, service, task.ID); len(bodies) != 0 {
		t.Fatalf("status messages = %#v, want none for an interrupted turn", bodies)
	}
}

func waitForCodexTurn(t *testing.T, service *Service, taskID string) {
	t.Helper()
	for i := 0; i < 100; i++ {
		service.agents.mu.Lock()
		started := service.agents.codexTurns[taskID] != nil
		service.agents.mu.Unlock()
		if started {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("Codex turn was not registered")
}

func TestIdleCodexAppServerIsReplaced(t *testing.T) {
	service := NewService("test")
	t.Cleanup(func() { _ = service.agents.Close() })
	idle := newFakeCodexRuntime()
	fresh := newFakeCodexRuntime()
	service.agents.codex = idle
	service.agents.codexLastTurn = time.Now().Add(-2 * codexAppServerIdleTTL)
	service.agents.startCodex = func(context.Context) (codexRuntime, error) {
		return fresh, nil
	}

	client, err := service.agents.ensureCodex(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if client != fresh {
		t.Fatal("ensureCodex reused the idle app-server, want a fresh one")
	}
	select {
	case _, open := <-idle.events:
		if open {
			t.Fatal("idle app-server event channel is still open, want it closed")
		}
	case <-time.After(time.Second):
		t.Fatal("idle app-server was not closed")
	}
}

func TestCodexAppServerIsKeptWhileUsable(t *testing.T) {
	service := NewService("test")
	t.Cleanup(func() { _ = service.agents.Close() })
	existing := newFakeCodexRuntime()
	service.agents.codex = existing
	service.agents.startCodex = func(context.Context) (codexRuntime, error) {
		t.Fatal("ensureCodex started a new app-server")
		return nil, nil
	}

	// Never ran a turn, so it is already fresh.
	client, err := service.agents.ensureCodex(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if client != existing {
		t.Fatal("fresh app-server was replaced")
	}

	// Long idle, but a turn is still live.
	service.agents.codexLastTurn = time.Now().Add(-2 * codexAppServerIdleTTL)
	service.agents.codexTurns["task-1"] = &codexTurn{}
	client, err = service.agents.ensureCodex(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if client != existing {
		t.Fatal("app-server with a live turn was replaced")
	}
}

func TestForgetCodexRuntimeKeepsClaudeTurns(t *testing.T) {
	service := NewService("test")
	t.Cleanup(func() { _ = service.agents.Close() })
	fake := newFakeCodexRuntime()
	service.agents.codex = fake
	service.agents.activeTurns["codex-task"] = "codex-turn"
	service.agents.codexTurns["codex-task"] = &codexTurn{}
	service.agents.activeTurns["claude-task"] = "claude-turn"

	service.agents.forgetRuntime(fake)

	if turn := service.agents.activeTurns["codex-task"]; turn != "" {
		t.Fatalf("codex active turn = %q, want cleared", turn)
	}
	if turn := service.agents.activeTurns["claude-task"]; turn != "claude-turn" {
		t.Fatalf("claude active turn = %q, want claude-turn", turn)
	}
}

func TestCodexInputRequestIsCancelledHeadlessly(t *testing.T) {
	service := NewService("test")
	fake := newFakeCodexRuntime()
	done := make(chan struct{})
	go func() {
		service.agents.forwardCodexEvents(fake)
		close(done)
	}()
	fake.events <- codexapp.Notification{Method: codexapp.NotifyUserInputRequest, RequestID: "input-1"}
	select {
	case method := <-fake.inputCancels:
		if method != codexapp.NotifyUserInputRequest {
			t.Fatalf("cancelled method = %q", method)
		}
	case <-time.After(time.Second):
		t.Fatal("Codex input request was not cancelled")
	}
	close(fake.events)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Codex event forwarder did not stop")
	}
}

func TestCodexApprovalDefaultsToDeclineWhenTaskIsUnknown(t *testing.T) {
	service := NewService("test")
	fake := newFakeCodexRuntime()
	service.agents.answerCodexApproval(fake, codexapp.Notification{Method: codexapp.NotifyCommandApprovalRequest})
	select {
	case decision := <-fake.approvals:
		if decision != codexapp.DecisionDecline {
			t.Fatalf("decision = %q, want decline", decision)
		}
	case <-time.After(time.Second):
		t.Fatal("approval request was not answered")
	}
}

func TestEnsureCodexThreadReplacesMissingThread(t *testing.T) {
	store, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	project, err := store.EnsureProject(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTask(project.ID, "structured", nil, "codex", db.StatusActive)
	if err != nil {
		t.Fatal(err)
	}
	threadID := "missing-thread"
	streamKind := codexapp.StreamKind
	if err := store.UpdateTaskAgentStream(task.ID, &threadID, nil, &streamKind); err != nil {
		t.Fatal(err)
	}
	task, err = store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService("test")
	service.store = store
	t.Cleanup(func() { _ = service.agents.Close() })
	fake := newFakeCodexRuntime()
	fake.nextThreadID = "replacement-thread"
	fake.resumeErr = &codexapp.CallError{Code: -32600, Message: "no rollout found for thread id missing-thread"}

	got, err := service.agents.ensureCodexThread(context.Background(), fake, task, project)
	if err != nil {
		t.Fatal(err)
	}
	if got != "replacement-thread" || fake.threadCwd != project.Path {
		t.Fatalf("thread = %q cwd = %q, want replacement in project", got, fake.threadCwd)
	}
}

func TestCreateStructuredDiscordTaskStartsCodexTurn(t *testing.T) {
	t.Setenv("AGX_CONFIG_DIR", t.TempDir())
	addExecutableToPath(t, "codex")
	store, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	projectRoot := initRuntimeGitRepo(t)
	project, err := store.EnsureProject(projectRoot, nil)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService("test")
	service.store = store
	fake := newFakeCodexRuntime()
	service.agents.startCodex = func(context.Context) (codexRuntime, error) {
		return fake, nil
	}

	prompt := "ship it"
	task, err := service.createStructuredDiscordTask(context.Background(), project, createTaskRequest{
		ProjectID:      project.ID,
		Title:          "ship it",
		Agent:          "codex",
		AllMighty:      true,
		InitialPrompt:  &prompt,
		RunImmediately: true,
		Discord:        true,
	}, "codex")
	if err != nil {
		t.Fatal(err)
	}
	if task.Interface != db.TaskInterfaceDiscord {
		t.Fatalf("Interface = %s, want discord", task.Interface)
	}
	if task.AgentStreamKind == nil || *task.AgentStreamKind != codexapp.StreamKind {
		t.Fatalf("AgentStreamKind = %#v, want %s", task.AgentStreamKind, codexapp.StreamKind)
	}
	if fake.startedText != "ship it" {
		t.Fatalf("startedText = %q, want explicit initial prompt", fake.startedText)
	}
	if fake.threadCwd == projectRoot || fake.turnCwd == projectRoot {
		t.Fatalf("structured task used project root, want isolated worktree: thread=%q turn=%q", fake.threadCwd, fake.turnCwd)
	}
	if task.WorktreePath == nil || *task.WorktreePath == "" {
		t.Fatal("WorktreePath is empty, want runtime-owned task worktree")
	}
}

func TestCreateStructuredDiscordTaskDoesNotSendTitleAsPrompt(t *testing.T) {
	t.Setenv("AGX_CONFIG_DIR", t.TempDir())
	addExecutableToPath(t, "codex")
	store, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	projectRoot := initRuntimeGitRepo(t)
	project, err := store.EnsureProject(projectRoot, nil)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService("test")
	service.store = store
	fake := newFakeCodexRuntime()
	service.agents.startCodex = func(context.Context) (codexRuntime, error) {
		return fake, nil
	}

	task, err := service.createStructuredDiscordTask(context.Background(), project, createTaskRequest{
		ProjectID:      project.ID,
		Title:          "main",
		Agent:          "codex",
		AllMighty:      true,
		RunImmediately: true,
		Discord:        true,
	}, "codex")
	if err != nil {
		t.Fatal(err)
	}
	if fake.startedText != "" || fake.turnCwd != "" {
		t.Fatalf("unexpected initial turn: text=%q cwd=%q", fake.startedText, fake.turnCwd)
	}
	messages, err := store.ListTaskTranscriptMessages(task.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 0 {
		t.Fatalf("transcript messages = %d, want none", len(messages))
	}
}

func TestCreateStructuredDiscordTaskCanUseProjectWorkspace(t *testing.T) {
	t.Setenv("AGX_CONFIG_DIR", t.TempDir())
	addExecutableToPath(t, "codex")
	store, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	projectRoot := initRuntimeGitRepo(t)
	project, err := store.EnsureProject(projectRoot, nil)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService("test")
	service.store = store
	fake := newFakeCodexRuntime()
	service.agents.startCodex = func(context.Context) (codexRuntime, error) {
		return fake, nil
	}

	task, err := service.createStructuredDiscordTask(context.Background(), project, createTaskRequest{
		ProjectID:      project.ID,
		Title:          "ship it",
		Agent:          "codex",
		AllMighty:      true,
		WorkspaceMode:  string(db.WorkspaceModeProject),
		RunImmediately: true,
		Discord:        true,
	}, "codex")
	if err != nil {
		t.Fatal(err)
	}
	if task.WorkspaceMode != db.WorkspaceModeProject {
		t.Fatalf("WorkspaceMode = %s, want project", task.WorkspaceMode)
	}
	if task.WorktreePath != nil {
		t.Fatalf("WorktreePath = %#v, want nil", task.WorktreePath)
	}
	if fake.threadCwd != projectRoot {
		t.Fatalf("structured project task thread cwd=%q, want %q", fake.threadCwd, projectRoot)
	}
	if fake.turnCwd != "" {
		t.Fatalf("structured project task started unexpected turn cwd=%q", fake.turnCwd)
	}
}

func TestCreateStructuredDiscordTaskQueuedReturnsBeforeCodexStartup(t *testing.T) {
	t.Setenv("AGX_CONFIG_DIR", t.TempDir())
	addExecutableToPath(t, "codex")
	store, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	projectRoot := initRuntimeGitRepo(t)
	project, err := store.EnsureProject(projectRoot, nil)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService("test")
	service.store = store
	fake := newFakeCodexRuntime()
	started := make(chan struct{})
	unblock := make(chan struct{})
	service.agents.startCodex = func(context.Context) (codexRuntime, error) {
		close(started)
		<-unblock
		return fake, nil
	}

	prompt := "ship it"
	type createResult struct {
		task db.Task
		err  error
	}
	done := make(chan createResult, 1)
	go func() {
		task, err := service.createStructuredDiscordTaskQueued(project, createTaskRequest{
			ProjectID:      project.ID,
			Title:          "queued",
			Agent:          "codex",
			AllMighty:      true,
			WorkspaceMode:  string(db.WorkspaceModeProject),
			InitialPrompt:  &prompt,
			RunImmediately: true,
			Discord:        true,
		}, "codex")
		done <- createResult{task: task, err: err}
	}()

	var task db.Task
	select {
	case result := <-done:
		if result.err != nil {
			t.Fatal(result.err)
		}
		task = result.task
	case <-time.After(200 * time.Millisecond):
		t.Fatal("queued create waited for Codex startup")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("background startup did not start Codex")
	}
	close(unblock)
	waitForRuntimeTestCondition(t, time.Second, func() bool {
		updated, err := store.GetTask(task.ID)
		return err == nil && updated.AgentThreadID != nil && *updated.AgentThreadID == "thread-1"
	})
	if fake.startedText != "ship it" {
		t.Fatalf("startedText = %q, want initial prompt", fake.startedText)
	}
}

func TestPendingStructuredDiscordTaskRejectsMessagesUntilStartup(t *testing.T) {
	store, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	project, err := store.EnsureProject(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTaskRuntimeModeInterfaceWorkspace(db.NewTaskID(), project.ID, "queued", nil, "codex", true, db.TaskInterfaceDiscord, db.WorkspaceModeWorktree, db.StatusWaiting, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService("test")
	service.store = store

	_, err = service.sendDiscordTaskMessage(context.Background(), task.ID, agxdiscord.IncomingTaskMessage{Text: "hey"})
	if err == nil || !strings.Contains(err.Error(), "still starting") {
		t.Fatalf("sendDiscordTaskMessage error = %v, want still starting", err)
	}
}

func TestDiscordDeliveryFailureDoesNotFailStructuredTaskStartup(t *testing.T) {
	store, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	project, err := store.EnsureProject(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTaskRuntimeModeInterface(db.NewTaskID(), project.ID, "queued", nil, "codex", true, db.TaskInterfaceDiscord, db.StatusWaiting, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService("test")
	service.store = store

	service.prepareStructuredDiscordDelivery(task, func(string) error {
		return context.DeadlineExceeded
	})

	updated, err := store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != db.StatusWaiting {
		t.Fatalf("task status = %q, want waiting after deferred Discord delivery", updated.Status)
	}
	messages, err := store.ListTaskTranscriptMessages(task.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].Role != "status" || !strings.Contains(messages[0].Body, "agent startup is continuing") {
		t.Fatalf("transcript messages = %#v, want delayed Discord delivery notice", messages)
	}
}

func TestCreateStructuredDiscordTaskReportsRollbackCleanupFailure(t *testing.T) {
	t.Setenv("AGX_CONFIG_DIR", t.TempDir())
	addExecutableToPath(t, "codex")
	store, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	projectRoot := initRuntimeGitRepo(t)
	project, err := store.EnsureProject(projectRoot, nil)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService("test")
	service.store = store
	fake := newFakeCodexRuntime()
	fake.dirtyThread = true
	fake.threadErr = errors.New("codex thread failed")
	service.agents.startCodex = func(context.Context) (codexRuntime, error) {
		return fake, nil
	}

	_, err = service.createStructuredDiscordTask(context.Background(), project, createTaskRequest{
		ProjectID:      project.ID,
		Title:          "rollback",
		Agent:          "codex",
		AllMighty:      true,
		RunImmediately: true,
		Discord:        true,
	}, "codex")
	if err == nil {
		t.Fatal("createStructuredDiscordTask succeeded, want rollback cleanup error")
	}
	message := err.Error()
	if !strings.Contains(message, "codex thread failed") || !strings.Contains(message, "rollback structured Discord task") || !strings.Contains(message, "remove structured worktree") {
		t.Fatalf("error = %q, want primary and cleanup details", message)
	}
	tasks, err := store.ListTasks(project.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 0 {
		t.Fatalf("tasks = %d, want rollback to delete task row", len(tasks))
	}
	if fake.threadCwd == "" {
		t.Fatal("thread cwd is empty")
	}
	if _, err := os.Stat(fake.threadCwd); err != nil {
		t.Fatalf("dirty worktree stat error = %v, want leftover worktree for cleanup warning", err)
	}
}

func TestInterruptInactiveCodexTaskDoesNotStartRuntime(t *testing.T) {
	t.Setenv("AGX_CONFIG_DIR", t.TempDir())
	store, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	projectRoot := initRuntimeGitRepo(t)
	project, err := store.EnsureProject(projectRoot, nil)
	if err != nil {
		t.Fatal(err)
	}
	threadID := "thread-1"
	streamKind := codexapp.StreamKind
	task, err := store.CreateTaskRuntimeModeInterface(db.NewTaskID(), project.ID, "stale codex", nil, "codex", true, db.TaskInterfaceDiscord, db.StatusWaiting, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateTaskAgentStream(task.ID, &threadID, nil, &streamKind); err != nil {
		t.Fatal(err)
	}
	task, err = store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService("test")
	service.store = store
	service.agents.startCodex = func(context.Context) (codexRuntime, error) {
		return nil, fmt.Errorf("codex runtime should not start")
	}

	if err := service.agents.InterruptTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
}

func TestAgentEventServiceClearResetsCodexContext(t *testing.T) {
	store, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	project, err := store.EnsureProject(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTaskRuntimeModeInterface(db.NewTaskID(), project.ID, "codex task", nil, "codex", true, db.TaskInterfaceDiscord, db.StatusActive, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	oldThreadID := "old-thread"
	oldCursor := "old-cursor"
	streamKind := codexapp.StreamKind
	if err := store.UpdateTaskAgentStream(task.ID, &oldThreadID, &oldCursor, &streamKind); err != nil {
		t.Fatal(err)
	}
	task, err = store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService("test")
	service.store = store
	fake := newFakeCodexRuntime()
	fake.nextThreadID = "new-thread"
	service.agents.startCodex = func(context.Context) (codexRuntime, error) {
		return fake, nil
	}
	service.agents.activeTurns[task.ID] = "turn-1"

	if err := service.agents.SendTaskMessage(context.Background(), task, project, "/clear"); err != nil {
		t.Fatal(err)
	}
	if fake.interrupted != "turn-1" {
		t.Fatalf("interrupted = %q, want turn-1", fake.interrupted)
	}
	if fake.startedText != "" {
		t.Fatalf("startedText = %q, want no model turn for /clear", fake.startedText)
	}
	updated, err := store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.AgentThreadID == nil || *updated.AgentThreadID != "new-thread" {
		t.Fatalf("AgentThreadID = %#v, want new-thread", updated.AgentThreadID)
	}
	if updated.AgentEventCursor != nil {
		t.Fatalf("AgentEventCursor = %#v, want nil after /clear", updated.AgentEventCursor)
	}
	if updated.Status != db.StatusWaiting {
		t.Fatalf("Status = %q, want waiting", updated.Status)
	}
	messages, err := store.ListTaskTranscriptMessages(task.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].Role != "status" || messages[0].Body != "Context cleared." {
		t.Fatalf("messages = %#v, want context cleared status", messages)
	}
}

func TestAgentEventServiceClearResetsClaudeContext(t *testing.T) {
	store, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	project, err := store.EnsureProject(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTaskRuntimeModeInterface(db.NewTaskID(), project.ID, "claude task", nil, "claude", true, db.TaskInterfaceDiscord, db.StatusActive, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	oldThreadID := "old-thread"
	oldCursor := "old-cursor"
	streamKind := claudeStreamKind
	if err := store.UpdateTaskAgentStream(task.ID, &oldThreadID, &oldCursor, &streamKind); err != nil {
		t.Fatal(err)
	}
	task, err = store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService("test")
	service.store = store
	service.agents.activeTurns[task.ID] = "turn-1"
	service.agents.claudeQueues[task.ID] = []string{"queued"}

	if err := service.agents.SendTaskMessage(context.Background(), task, project, "/clear"); err != nil {
		t.Fatal(err)
	}
	updated, err := store.GetTask(task.ID)
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
	service.agents.mu.Lock()
	activeTurn := service.agents.activeTurns[task.ID]
	queued := service.agents.claudeQueues[task.ID]
	service.agents.mu.Unlock()
	if activeTurn != "" || len(queued) != 0 {
		t.Fatalf("activeTurn=%q queued=%#v, want cleared runtime state", activeTurn, queued)
	}
}

func addExecutableToPath(t *testing.T, name string) {
	t.Helper()
	writeStubCommandOnPath(t, name, stubExitZeroPosix, stubExitZeroBatch)
}

func initRuntimeGitRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	runRuntimeTestCommand(t, root, "git", "init", "-q")
	runRuntimeTestCommand(t, root, "git", "config", "user.email", "agx@example.com")
	runRuntimeTestCommand(t, root, "git", "config", "user.name", "AGX Test")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runRuntimeTestCommand(t, root, "git", "add", "README.md")
	runRuntimeTestCommand(t, root, "git", "commit", "-q", "-m", "initial")
	return root
}

func runRuntimeTestCommand(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}

func waitForRuntimeTestCondition(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition was not met before timeout")
}
