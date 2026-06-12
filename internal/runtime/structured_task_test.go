package runtime

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/nashory/agx/internal/codexapp"
	"github.com/nashory/agx/internal/db"
)

type fakeCodexRuntime struct {
	events       chan codexapp.Notification
	threadCwd    string
	turnCwd      string
	startedText  string
	interrupted  string
	nextThreadID string
	nextTurnID   string
}

func newFakeCodexRuntime() *fakeCodexRuntime {
	return &fakeCodexRuntime{
		events:       make(chan codexapp.Notification, 1),
		nextThreadID: "thread-1",
		nextTurnID:   "turn-1",
	}
}

func (f *fakeCodexRuntime) Initialize(context.Context) (codexapp.InitializeResponse, error) {
	return codexapp.InitializeResponse{}, nil
}

func (f *fakeCodexRuntime) ThreadStart(_ context.Context, cwd string, allMighty bool) (codexapp.ThreadStartResponse, error) {
	f.threadCwd = cwd
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

func (f *fakeCodexRuntime) TurnSteer(context.Context, string, string, string) (codexapp.TurnSteerResponse, error) {
	return codexapp.TurnSteerResponse{}, nil
}

func (f *fakeCodexRuntime) TurnInterrupt(ctx context.Context, threadID, turnID string) error {
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
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
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
