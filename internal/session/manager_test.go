package session

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/nashory/agx/internal/agent"
	"github.com/nashory/agx/internal/db"
	"github.com/nashory/agx/internal/tmux"
)

func TestProjectSessionNameIncludesShortProjectID(t *testing.T) {
	project := db.Project{
		ID:   "1234567890abcdef",
		Name: "my app",
	}

	got := projectSessionName(project)
	want := "my-app-12345678"
	if got != want {
		t.Fatalf("projectSessionName() = %q, want %q", got, want)
	}
}

func TestProjectSessionNameAvoidsDuplicateProjectNameCollision(t *testing.T) {
	first := db.Project{
		ID:   "aaaaaaaa00000000",
		Name: "web",
	}
	second := db.Project{
		ID:   "bbbbbbbb00000000",
		Name: "web",
	}

	firstName := projectSessionName(first)
	secondName := projectSessionName(second)
	if firstName == secondName {
		t.Fatalf("projectSessionName() returned duplicate session name %q", firstName)
	}
}

func TestInjectedPromptReadyWaitsForComposerFooter(t *testing.T) {
	if injectedPromptReady("Claude Code v2.1.159\nUsing AI Gateway") {
		t.Fatal("injectedPromptReady() = true before composer footer is visible")
	}
	if !injectedPromptReady("accept edits on (shift+tab to cycle)") {
		t.Fatal("injectedPromptReady() = false after composer footer is visible")
	}
	if !injectedPromptReady("bypass permissions on (shift+tab to cycle) · ↵ for agents") {
		t.Fatal("injectedPromptReady() = false for all-mighty composer footer")
	}
}

func TestClaudeTrustPromptReady(t *testing.T) {
	logs := `Accessing workspace:

/example/agx/worktrees/project/task-12345678

Quick safety check: Is this a project you created or one you trust?

❯ 1. Yes, I trust this folder
  2. No, exit

Enter to confirm · Esc to cancel`
	if !claudeTrustPromptReady(logs) {
		t.Fatal("claudeTrustPromptReady() = false for trust prompt")
	}
	if claudeTrustPromptReady("Claude Code v2.1.159\nUsing AI Gateway") {
		t.Fatal("claudeTrustPromptReady() = true before trust prompt")
	}
}

func TestTaskInitialPromptAllowsExplicitEmptyPrompt(t *testing.T) {
	empty := ""
	description := "details"

	if got := taskInitialPrompt("title", nil, nil); got != "title" {
		t.Fatalf("taskInitialPrompt() = %q, want title", got)
	}
	if got := taskInitialPrompt("title", &description, nil); got != "details" {
		t.Fatalf("taskInitialPrompt() = %q, want details", got)
	}
	if got := taskInitialPrompt("title", &description, &empty); got != "" {
		t.Fatalf("taskInitialPrompt() = %q, want explicit empty prompt", got)
	}
	if got := taskInitialPrompt("Vanilla", nil, nil); got != "" {
		t.Fatalf("taskInitialPrompt() = %q, want no prompt for Vanilla", got)
	}
}

func TestForceTaskWorktreesIgnoresDisabledGlobalConfig(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("AGX_CONFIG_DIR", configDir)
	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte("[worktree]\nenabled = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	initSessionGitRepo(t, root)
	project := db.Project{ID: "project-1", Name: "Project", Path: root}

	manager := NewManager(nil, nil, nil).ForceTaskWorktrees()
	prepared, err := manager.prepareWorktree(project, "12345678-aaaa", db.WorkspaceModeWorktree)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = removePreparedWorktree(project, prepared)
	})
	if prepared.Path == nil || prepared.WorkingDir == root {
		t.Fatalf("prepared = %#v, want external worktree despite disabled global config", prepared)
	}
	if prepared.Branch == nil || prepared.Base == nil {
		t.Fatalf("prepared branch/base = %#v/%#v, want task branch and base", prepared.Branch, prepared.Base)
	}
}

func TestRunNewTaskReportsStartupCleanupFailure(t *testing.T) {
	t.Setenv("AGX_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	initSessionGitRepo(t, root)
	store, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	project, err := store.EnsureProject(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	binDir := t.TempDir()
	agentPath := filepath.Join(binDir, "test-agent")
	if err := os.WriteFile(agentPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	dirtyPathFile := filepath.Join(t.TempDir(), "dirty-path")
	t.Setenv("AGX_DIRTY_PATH", dirtyPathFile)
	installSessionFakeTmux(t, `#!/bin/sh
printf '%s\n' "$*" >> "$AGX_TMUX_LOG"
case "$*" in
  *"has-session"*) exit 1 ;;
  *"new-window"*)
    previous=""
    cwd=""
    for arg in "$@"; do
      if [ "$previous" = "-c" ]; then
        cwd="$arg"
      fi
      previous="$arg"
    done
    printf '%s\n' "$cwd" > "$AGX_DIRTY_PATH"
    printf dirty > "$cwd/dirty.txt"
    printf 'new window failed\n' >&2
    exit 1
    ;;
esac
exit 0
`)
	manager := NewManager(store, tmux.NewController(), agent.NewRegistry("test", agent.Agent{Name: "test", Command: "test-agent"})).ForceTaskWorktrees()

	_, err = manager.RunNewTaskWithOptions(project, "cleanup", nil, "test", RunOptions{WorkspaceMode: db.WorkspaceModeWorktree})
	if err == nil {
		t.Fatal("RunNewTaskWithOptions succeeded, want startup cleanup error")
	}
	message := err.Error()
	if !strings.Contains(message, "new window failed") || !strings.Contains(message, "create task window cleanup failed") || !strings.Contains(message, "remove prepared worktree") {
		t.Fatalf("error = %q, want primary and cleanup details", message)
	}
	tasks, err := store.ListTasks(project.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 0 {
		t.Fatalf("tasks = %d, want cleanup to delete task row", len(tasks))
	}
	dirtyPath, err := os.ReadFile(dirtyPathFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(strings.TrimSpace(string(dirtyPath))); err != nil {
		t.Fatalf("dirty worktree stat error = %v, want leftover worktree for cleanup warning", err)
	}
}

func TestRecoverLiveTasksMarksLegacyTasksOfflineAndPreservesStructuredTasks(t *testing.T) {
	store, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	project, err := store.EnsureProject(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	sessionName := "task-legacy"
	legacy, err := store.CreateTaskWithSession(db.NewTaskID(), project.ID, "legacy", nil, "claude", db.StatusActive, &sessionName)
	if err != nil {
		t.Fatal(err)
	}
	structured, err := store.CreateTaskRuntimeModeInterface(db.NewTaskID(), project.ID, "structured", nil, "codex", true, db.TaskInterfaceDiscord, db.StatusWaiting, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	threadID := "thread-1"
	streamKind := "codex-app-server"
	if err := store.UpdateTaskAgentStream(structured.ID, &threadID, nil, &streamKind); err != nil {
		t.Fatal(err)
	}

	manager := NewManager(store, tmux.NewController(), agent.NewRegistry("claude"))
	result, err := manager.RecoverLiveTasks()
	if err != nil {
		t.Fatal(err)
	}
	if result.Offline != 1 {
		t.Fatalf("Offline = %d, want 1", result.Offline)
	}
	updatedLegacy, err := store.GetTask(legacy.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updatedLegacy.Status != db.StatusOffline || updatedLegacy.SessionName != nil {
		t.Fatalf("legacy task after recovery = %#v, want offline with no session", updatedLegacy)
	}
	updatedStructured, err := store.GetTask(structured.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updatedStructured.Status != db.StatusWaiting || updatedStructured.AgentStreamKind == nil {
		t.Fatalf("structured task after recovery = %#v, want preserved structured runtime", updatedStructured)
	}
}

func TestRecoverLiveTasksClearsInactiveStaleSessions(t *testing.T) {
	store, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	project, err := store.EnsureProject(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	sessionName := "task-complete"
	task, err := store.CreateTaskWithSession(db.NewTaskID(), project.ID, "complete", nil, "claude", db.StatusComplete, &sessionName)
	if err != nil {
		t.Fatal(err)
	}

	manager := NewManager(store, tmux.NewController(), agent.NewRegistry("claude"))
	result, err := manager.RecoverLiveTasks()
	if err != nil {
		t.Fatal(err)
	}
	if result.Cleared != 1 {
		t.Fatalf("Cleared = %d, want 1", result.Cleared)
	}
	updated, err := store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.SessionName != nil {
		t.Fatalf("SessionName = %#v, want nil after stale inactive cleanup", updated.SessionName)
	}
}

func TestManagerTaskOperationsHandleMissingSession(t *testing.T) {
	manager := NewManager(nil, nil, nil)
	task := db.Task{ID: db.NewTaskID(), Status: db.StatusActive}

	if err := manager.RunTask(task); !errors.Is(err, db.ErrTaskNotRunnable) {
		t.Fatalf("RunTask(active) error = %v, want ErrTaskNotRunnable", err)
	}
	if err := manager.StopTask(db.Task{Status: db.StatusOffline}); err != nil {
		t.Fatalf("StopTask(offline without session) error = %v", err)
	}
	for name, err := range map[string]error{
		"SendInput":          manager.SendInput(task, "x"),
		"InterruptTask":      manager.InterruptTask(task),
		"ResizeTaskTerminal": manager.ResizeTaskTerminal(task, 80, 24),
	} {
		if err == nil || !strings.Contains(err.Error(), "task has no session") {
			t.Fatalf("%s error = %v, want missing session", name, err)
		}
	}
	if _, err := manager.GetLogs(task, 10); err == nil || !strings.Contains(err.Error(), "task has no session") {
		t.Fatalf("GetLogs error = %v, want missing session", err)
	}
	status, output, err := manager.DetectStatus(task, "previous", time.Now())
	if err != nil {
		t.Fatalf("DetectStatus error = %v", err)
	}
	if status != db.StatusOffline || output != "previous" {
		t.Fatalf("DetectStatus = %s, %q; want offline, previous", status, output)
	}
}

func TestManagerLiveTaskOperationsUseTmuxTarget(t *testing.T) {
	store, project, task := newSessionStoreWithLiveTask(t)
	sessionName := projectSessionName(project)
	windowName := *task.SessionName
	logPath := installSessionFakeTmux(t, fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$*" >> "$AGX_TMUX_LOG"
case "$*" in
  *"list-windows -t %[1]s -F #{window_name}"*) printf '%[2]s\n' ;;
  *"capture-pane -t %[1]s:%[2]s -p -S -20"*) printf 'history\n' ;;
  *"capture-pane -t %[1]s:%[2]s -p"*) printf 'current\n' ;;
esac
`, sessionName, windowName))
	manager := NewManager(store, tmux.NewController(), agent.NewRegistry("claude"))

	logs, err := manager.GetLogs(task, 20)
	if err != nil {
		t.Fatal(err)
	}
	if logs != "history\n" {
		t.Fatalf("GetLogs() = %q, want history", logs)
	}
	if err := manager.ResizeTaskTerminal(task, 120, 40); err != nil {
		t.Fatal(err)
	}
	if err := manager.SendInput(task, "abc"); err != nil {
		t.Fatal(err)
	}
	if err := manager.InterruptTask(task); err != nil {
		t.Fatal(err)
	}
	streamPath := filepath.Join(t.TempDir(), "stream.log")
	streamed, err := manager.StreamLogs(task, streamPath, 0)
	if err != nil {
		t.Fatal(err)
	}
	if streamed != "current\n" {
		t.Fatalf("StreamLogs() = %q, want current logs", streamed)
	}
	if err := manager.StopTask(task); err != nil {
		t.Fatal(err)
	}
	refreshed, err := store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.Status != db.StatusOffline || refreshed.SessionName != nil {
		t.Fatalf("task after StopTask = %#v, want offline with no session", refreshed)
	}

	log := readSessionFakeTmuxLog(t, logPath)
	for _, want := range []string{
		"-L agx capture-pane -t " + sessionName + ":" + windowName + " -p -S -20",
		"-L agx resize-window -t " + sessionName + ":" + windowName + " -x 120 -y 40",
		"-L agx send-keys -t " + sessionName + ":" + windowName + " -l -- abc",
		"-L agx send-keys -t " + sessionName + ":" + windowName + " C-c",
		"-L agx pipe-pane -t " + sessionName + ":" + windowName,
		"-L agx kill-window -t " + sessionName + ":" + windowName,
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("fake tmux log missing %q:\n%s", want, log)
		}
	}
}

func TestManagerRefusesInputWhenPaneCWDDoesNotMatchTaskWorktree(t *testing.T) {
	store, project, task := newSessionStoreWithLiveTask(t)
	expected := t.TempDir()
	other := t.TempDir()
	if err := store.UpdateTaskRuntimeBase(task.ID, task.SessionName, task.Status, &expected, nil, nil); err != nil {
		t.Fatal(err)
	}
	task, err := store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	sessionName := projectSessionName(project)
	windowName := *task.SessionName
	installSessionFakeTmux(t, fmt.Sprintf(`#!/bin/sh
case "$*" in
  *"list-windows -t %[1]s -F #{window_name}"*) printf '%[2]s\n' ;;
  *"display-message -p -t %[1]s:%[2]s #{pane_current_path}"*) printf '%[3]s\n' ;;
esac
`, sessionName, windowName, other))
	manager := NewManager(store, tmux.NewController(), agent.NewRegistry("claude"))

	err = manager.SendInput(task, "abc")
	if err == nil || !strings.Contains(err.Error(), "refusing to send input") || !strings.Contains(err.Error(), expected) || !strings.Contains(err.Error(), other) {
		t.Fatalf("SendInput error = %v, want cwd mismatch", err)
	}
}

func TestManagerRefusesInputWhenTaskWorktreeIsMissing(t *testing.T) {
	store, project, task := newSessionStoreWithLiveTask(t)
	missing := filepath.Join(t.TempDir(), "missing-worktree")
	if err := store.UpdateTaskRuntimeBase(task.ID, task.SessionName, task.Status, &missing, nil, nil); err != nil {
		t.Fatal(err)
	}
	task, err := store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	sessionName := projectSessionName(project)
	windowName := *task.SessionName
	installSessionFakeTmux(t, fmt.Sprintf(`#!/bin/sh
case "$*" in
  *"list-windows -t %[1]s -F #{window_name}"*) printf '%[2]s\n' ;;
esac
`, sessionName, windowName))
	manager := NewManager(store, tmux.NewController(), agent.NewRegistry("claude"))

	err = manager.SendInput(task, "abc")
	if err == nil || !strings.Contains(err.Error(), "task worktree is missing") || !strings.Contains(err.Error(), missing) {
		t.Fatalf("SendInput error = %v, want missing worktree", err)
	}
}

func TestManagerDeleteTaskAndStopProjectCleanState(t *testing.T) {
	store, project, task := newSessionStoreWithLiveTask(t)
	sessionName := projectSessionName(project)
	windowName := *task.SessionName
	logPath := installSessionFakeTmux(t, fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$*" >> "$AGX_TMUX_LOG"
case "$*" in
  *"list-windows -t %[1]s -F #{window_name}"*) printf '%[2]s\n' ;;
esac
`, sessionName, windowName))
	manager := NewManager(store, tmux.NewController(), agent.NewRegistry("claude"))

	if err := manager.DeleteTask(task); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetTask(task.ID); !errors.Is(err, db.ErrTaskNotFound) {
		t.Fatalf("GetTask after DeleteTask error = %v, want ErrTaskNotFound", err)
	}

	secondSession := "task-second"
	second, err := store.CreateTaskWithSession(db.NewTaskID(), project.ID, "second", nil, "claude", db.StatusActive, &secondSession)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.StopProject(project); err != nil {
		t.Fatal(err)
	}
	refreshed, err := store.GetTask(second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.Status != db.StatusOffline || refreshed.SessionName != nil || refreshed.WorktreePath != nil || refreshed.BranchName != nil || refreshed.BaseBranch != nil {
		t.Fatalf("task after StopProject = %#v, want runtime state cleared", refreshed)
	}

	log := readSessionFakeTmuxLog(t, logPath)
	for _, want := range []string{
		"-L agx kill-window -t " + sessionName + ":" + windowName,
		"-L agx kill-session -t " + sessionName,
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("fake tmux log missing %q:\n%s", want, log)
		}
	}
}

func TestManagerDeleteTaskReportsCleanupFailureAfterDeletingRow(t *testing.T) {
	t.Setenv("AGX_CONFIG_DIR", t.TempDir())
	store, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	project, err := store.EnsureProject(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	escapedWorktreePath := t.TempDir()
	branchName := "agx/task-cleanup"
	task, err := store.CreateTaskRuntime(db.NewTaskID(), project.ID, "cleanup failure", nil, "claude", db.StatusActive, nil, &escapedWorktreePath, &branchName)
	if err != nil {
		t.Fatal(err)
	}
	manager := NewManager(store, tmux.NewController(), agent.NewRegistry("claude"))

	err = manager.DeleteTask(task)
	var cleanupErr TaskCleanupError
	if !errors.As(err, &cleanupErr) {
		t.Fatalf("DeleteTask() error = %v, want TaskCleanupError", err)
	}
	if _, err := store.GetTask(task.ID); !errors.Is(err, db.ErrTaskNotFound) {
		t.Fatalf("GetTask after partial cleanup error = %v, want ErrTaskNotFound", err)
	}
}

func newSessionStoreWithLiveTask(t *testing.T) (*db.Store, db.Project, db.Task) {
	t.Helper()
	store, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	project, err := store.EnsureProjectDetails(t.TempDir(), "Project", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	sessionName := "task-live"
	task, err := store.CreateTaskWithSession(db.NewTaskID(), project.ID, "live", nil, "claude", db.StatusActive, &sessionName)
	if err != nil {
		t.Fatal(err)
	}
	return store, project, task
}

func installSessionFakeTmux(t *testing.T, script string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake tmux shell script requires a Unix shell")
	}
	dir := t.TempDir()
	logPath := filepath.Join(dir, "tmux.log")
	path := filepath.Join(dir, "tmux")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGX_TMUX_LOG", logPath)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

func readSessionFakeTmuxLog(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func initSessionGitRepo(t *testing.T, root string) {
	t.Helper()
	runSessionCommand(t, root, "git", "init", "-q")
	runSessionCommand(t, root, "git", "config", "user.email", "agx@example.com")
	runSessionCommand(t, root, "git", "config", "user.name", "AGX Test")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runSessionCommand(t, root, "git", "add", "README.md")
	runSessionCommand(t, root, "git", "commit", "-q", "-m", "initial")
}

func runSessionCommand(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v failed: %v\n%s", name, args, err, out)
	}
}
