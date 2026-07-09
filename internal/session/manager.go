package session

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nashory/agx/internal/agent"
	"github.com/nashory/agx/internal/config"
	"github.com/nashory/agx/internal/db"
	"github.com/nashory/agx/internal/display"
	"github.com/nashory/agx/internal/session/script"
	"github.com/nashory/agx/internal/tmux"
	"github.com/nashory/agx/internal/worktree"
)

// Manager coordinates legacy terminal-backed tasks for one runtime process. It
// translates task rows into backend sessions/windows and keeps generated
// worktrees in sync with task lifecycle changes.
type Manager struct {
	store          *db.Store
	backend        Backend
	registry       *agent.Registry
	forceWorktrees bool
}

// TaskCleanupError reports that the task row was removed but generated runtime
// resources such as the tmux window or worktree could not be fully cleaned up.
type TaskCleanupError struct {
	TaskID string
	Err    error
}

func (e TaskCleanupError) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("task %s deleted, but cleanup failed", display.ShortID(e.TaskID))
	}
	return fmt.Sprintf("task %s deleted, but cleanup failed: %v", display.ShortID(e.TaskID), e.Err)
}

func (e TaskCleanupError) Unwrap() error {
	return e.Err
}

func (e TaskCleanupError) PartialSuccess() bool {
	return true
}

// RunOptions controls how a new task is started in tmux.
type RunOptions struct {
	AllMighty     bool
	InitialPrompt *string
	WorkspaceMode db.WorkspaceMode
}

// NewManager creates a session manager using the provided store, session
// backend, and agent registry. Callers are expected to pass project-specific
// registries when agent configuration can vary by repository. A *tmux.Controller
// satisfies Backend, so existing callers pass one unchanged.
func NewManager(store *db.Store, backend Backend, registry *agent.Registry) *Manager {
	return &Manager{store: store, backend: backend, registry: registry}
}

// ForceTaskWorktrees makes subsequent workspace preparation create task
// worktrees even when config disables them. The runtime uses this for desktop
// direct-mode validation paths where isolation must be enforced.
func (m *Manager) ForceTaskWorktrees() *Manager {
	m.forceWorktrees = true
	return m
}

// RunNewTaskWithOptions creates the database row, prepares the workspace, and
// starts the agent in a tmux window. Any failure after workspace or script
// creation attempts to roll back those generated resources before returning.
func (m *Manager) RunNewTaskWithOptions(project db.Project, title string, description *string, agentName string, options RunOptions) (db.Task, error) {
	ag, err := m.registry.Get(agentName)
	if err != nil {
		return db.Task{}, err
	}
	if !ag.IsAvailable() {
		return db.Task{}, fmt.Errorf("agent %q is not available on PATH", ag.Name)
	}
	taskID := db.NewTaskID()
	windowName := taskWindowName(taskID)
	sessionName := projectSessionName(project)
	prompt := taskInitialPrompt(title, description, options.InitialPrompt)
	command, err := script.BuildTmuxCommandMode(ag, prompt, taskID, options.AllMighty)
	if err != nil {
		return db.Task{}, err
	}
	workspaceMode := normalizeWorkspaceMode(options.WorkspaceMode)
	prepared, err := m.prepareWorktree(project, taskID, workspaceMode)
	if err != nil {
		script.RemoveCommandScript(command)
		return db.Task{}, err
	}
	task, err := m.store.CreateTaskRuntimeModeInterfaceWorkspace(taskID, project.ID, title, description, agentName, options.AllMighty, db.TaskInterfaceLocal, workspaceMode, db.StatusActive, &windowName, prepared.Path, prepared.Branch)
	if err != nil {
		script.RemoveCommandScript(command)
		return db.Task{}, m.withTaskStartupCleanupError(err, "create task row", func() error {
			return removePreparedWorktreeForCleanup(project, prepared)
		})
	}
	if prepared.Base != nil {
		if err := m.store.UpdateTaskRuntimeBase(task.ID, task.SessionName, task.Status, task.WorktreePath, task.BranchName, prepared.Base); err != nil {
			script.RemoveCommandScript(command)
			return db.Task{}, m.withTaskStartupCleanupError(err, "update task runtime", func() error {
				return errors.Join(
					removePreparedWorktreeForCleanup(project, prepared),
					deleteTaskRowForCleanup(m.store, taskID),
				)
			})
		}
		task.BaseBranch = prepared.Base
	}
	target := tmux.Target(sessionName, windowName)
	if err := m.ensureSession(sessionName, prepared.WorkingDir); err != nil {
		script.RemoveCommandScript(command)
		return db.Task{}, m.withTaskStartupCleanupError(err, "create task session", func() error {
			return errors.Join(
				removePreparedWorktreeForCleanup(project, prepared),
				deleteTaskRowForCleanup(m.store, taskID),
			)
		})
	}
	if err := m.createTaskWindow(sessionName, windowName, prepared.WorkingDir, command); err != nil {
		script.RemoveCommandScript(command)
		return db.Task{}, m.withTaskStartupCleanupError(err, "create task window", func() error {
			return errors.Join(
				removePreparedWorktreeForCleanup(project, prepared),
				killTaskWindowForCleanup(m.backend, target),
				deleteTaskRowForCleanup(m.store, taskID),
			)
		})
	}
	if err := m.verifyTaskWindowStarted(task, target, ag.ShouldInjectInitialPrompt()); err != nil {
		return db.Task{}, m.withTaskStartupCleanupError(err, "verify task window", func() error {
			return stopTaskForCleanup(m, task)
		})
	}
	if ag.ShouldInjectInitialPrompt() {
		if err := m.prepareInjectedPromptSession(target, prompt); err != nil {
			return db.Task{}, m.withTaskStartupCleanupError(err, "prepare injected prompt", func() error {
				return stopTaskForCleanup(m, task)
			})
		}
		if prompt != "" {
			if err := m.verifyTaskWindowStarted(task, target, true); err != nil {
				return db.Task{}, m.withTaskStartupCleanupError(err, "verify injected prompt", func() error {
					return stopTaskForCleanup(m, task)
				})
			}
		}
	}
	_ = m.closeBootstrapWindow(sessionName)
	return task, nil
}

// RunTask restarts an offline or completed task using its stored metadata. It
// refuses active/waiting tasks so callers do not accidentally attach a second
// process to the same logical task.
func (m *Manager) RunTask(task db.Task) error {
	if task.Status != db.StatusOffline && task.Status != db.StatusComplete {
		return fmt.Errorf("%w: task must be offline or complete, got %s", db.ErrTaskNotRunnable, task.Status)
	}
	return m.restartTask(task, nil)
}

func (m *Manager) restartTask(task db.Task, promptOverride *string) error {
	project, err := m.store.GetProject(task.ProjectID)
	if err != nil {
		return err
	}
	ag, err := m.registry.Get(task.Agent)
	if err != nil {
		return err
	}
	if !ag.IsAvailable() {
		return fmt.Errorf("agent %q is not available on PATH", ag.Name)
	}
	sessionName := projectSessionName(project)
	windowName := taskWindowName(task.ID)
	target := tmux.Target(sessionName, windowName)
	if m.backend.WindowExists(target) {
		if err := m.backend.KillWindow(target); err != nil {
			return fmt.Errorf("clear stale task window %s: %w", target, err)
		}
	}
	prompt := taskInitialPrompt(task.Title, task.Description, promptOverride)
	command, err := script.BuildTmuxCommandMode(ag, prompt, task.ID, task.AllMighty)
	if err != nil {
		return err
	}
	prepared, err := m.prepareWorktreeForTask(project, task)
	if err != nil {
		script.RemoveCommandScript(command)
		return err
	}
	if err := m.store.UpdateTaskRuntimeBase(task.ID, &windowName, db.StatusActive, prepared.Path, prepared.Branch, prepared.Base); err != nil {
		script.RemoveCommandScript(command)
		return m.withTaskStartupCleanupError(err, "restart task runtime update", func() error {
			return removePreparedWorktreeForCleanup(project, prepared)
		})
	}
	if err := m.ensureSession(sessionName, prepared.WorkingDir); err != nil {
		script.RemoveCommandScript(command)
		return m.withTaskStartupCleanupError(err, "restart task session", func() error {
			return errors.Join(
				removePreparedWorktreeForCleanup(project, prepared),
				restoreTaskRuntimeForCleanup(m.store, task),
			)
		})
	}
	if err := m.createTaskWindow(sessionName, windowName, prepared.WorkingDir, command); err != nil {
		script.RemoveCommandScript(command)
		return m.withTaskStartupCleanupError(err, "restart task window", func() error {
			return errors.Join(
				removePreparedWorktreeForCleanup(project, prepared),
				killTaskWindowForCleanup(m.backend, target),
				restoreTaskRuntimeForCleanup(m.store, task),
			)
		})
	}
	refreshed := task
	refreshed.SessionName = &windowName
	refreshed.Status = db.StatusActive
	refreshed.WorktreePath = prepared.Path
	refreshed.BranchName = prepared.Branch
	refreshed.BaseBranch = prepared.Base
	if err := m.verifyTaskWindowStarted(refreshed, target, ag.ShouldInjectInitialPrompt()); err != nil {
		return m.withTaskStartupCleanupError(err, "verify restarted task window", func() error {
			return errors.Join(
				stopTaskForCleanup(m, refreshed),
				restoreTaskRuntimeForCleanup(m.store, task),
			)
		})
	}
	if ag.ShouldInjectInitialPrompt() {
		if err := m.prepareInjectedPromptSession(target, prompt); err != nil {
			return m.withTaskStartupCleanupError(err, "prepare restarted task injected prompt", func() error {
				return errors.Join(
					stopTaskForCleanup(m, refreshed),
					restoreTaskRuntimeForCleanup(m.store, task),
				)
			})
		}
		if prompt != "" {
			if err := m.verifyTaskWindowStarted(refreshed, target, true); err != nil {
				return m.withTaskStartupCleanupError(err, "verify restarted task injected prompt", func() error {
					return errors.Join(
						stopTaskForCleanup(m, refreshed),
						restoreTaskRuntimeForCleanup(m.store, task),
					)
				})
			}
		}
	}
	_ = m.closeBootstrapWindow(sessionName)
	return nil
}

func taskInitialPrompt(title string, description, override *string) string {
	if override != nil {
		return *override
	}
	if description != nil {
		return *description
	}
	if strings.EqualFold(strings.TrimSpace(title), "vanilla") {
		return ""
	}
	return title
}

// StopTask stops the tmux window for a task and marks the persisted runtime
// state offline while preserving worktree metadata for later inspection or
// restart.
func (m *Manager) StopTask(task db.Task) error {
	if task.Status == db.StatusOffline && task.SessionName == nil {
		return nil
	}
	project, err := m.store.GetProject(task.ProjectID)
	if err != nil {
		return err
	}
	if task.SessionName != nil {
		target := tmux.Target(projectSessionName(project), *task.SessionName)
		if err := m.stopTaskWindow(target); err != nil {
			return err
		}
	}
	return m.store.UpdateTaskRuntimeBase(task.ID, nil, db.StatusOffline, task.WorktreePath, task.BranchName, task.BaseBranch)
}

// SendMessage delivers a user message to a live task. If the stored tmux window
// is missing, the task is restarted with text as the initial prompt override.
func (m *Manager) SendMessage(task db.Task, text string) error {
	target, err := m.taskTarget(task)
	if err != nil {
		return m.restartTask(task, &text)
	}
	if !m.backend.WindowExists(target) {
		return m.restartTask(task, &text)
	}
	if err := m.validateTaskTargetCWD(task, target); err != nil {
		return err
	}
	return m.backend.SendKeys(target, text)
}

func (m *Manager) ResizeTaskTerminal(task db.Task, cols, rows int) error {
	target, err := m.taskTarget(task)
	if err != nil {
		return err
	}
	if !m.backend.WindowExists(target) {
		return nil
	}
	return m.backend.ResizeWindow(target, cols, rows)
}

func (m *Manager) SendInput(task db.Task, data string) error {
	target, err := m.taskTarget(task)
	if err != nil {
		return err
	}
	if !m.backend.WindowExists(target) {
		return fmt.Errorf("task window does not exist: %s", target)
	}
	if err := m.validateTaskTargetCWD(task, target); err != nil {
		return err
	}
	return m.backend.SendInput(target, data)
}

func (m *Manager) InterruptTask(task db.Task) error {
	target, err := m.taskTarget(task)
	if err != nil {
		return err
	}
	if !m.backend.WindowExists(target) {
		return fmt.Errorf("task window does not exist: %s", target)
	}
	return m.backend.SendKey(target, "C-c")
}

func (m *Manager) GetLogs(task db.Task, lines int) (string, error) {
	target, err := m.taskTarget(task)
	if err != nil {
		return "", err
	}
	if lines > 0 {
		return m.backend.CapturePaneWithHistory(target, lines)
	}
	return m.backend.CapturePane(target)
}

// StreamLogs captures the current pane into path and attaches tmux pipe-pane so
// subsequent output is appended to the same file. If the window is already gone,
// it falls back to a one-shot log capture.
func (m *Manager) StreamLogs(task db.Task, path string, lines int) (string, error) {
	target, err := m.taskTarget(task)
	if err != nil {
		return "", err
	}
	if !m.backend.WindowExists(target) {
		return m.GetLogs(task, lines)
	}
	initial, captureErr := m.GetLogs(task, lines)
	if captureErr == nil {
		_ = os.WriteFile(path, []byte(initial), 0o600)
	} else if _, err := os.Stat(path); os.IsNotExist(err) {
		return "", captureErr
	}
	if err := m.backend.ReplacePipePane(target, "cat >> "+script.ShellQuote(path)); err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// DetectStatus samples tmux output and shell exit markers to classify a task as
// active, waiting, complete, or offline. The caller supplies the previous output
// and activity timestamp so waiting detection can be based on output staleness.
func (m *Manager) DetectStatus(task db.Task, lastOutput string, lastActivity time.Time) (db.TaskStatus, string, error) {
	target, err := m.taskTarget(task)
	if err != nil {
		return db.StatusOffline, lastOutput, nil
	}
	ignoreExitStatus := false
	if ag, err := m.registry.Get(task.Agent); err == nil {
		ignoreExitStatus = ag.ShouldInjectInitialPrompt()
	}
	status, output := DetectTaskStatus(m.backend, target, task.ID, lastOutput, lastActivity, ignoreExitStatus)
	return status, output, nil
}

// DeleteTask removes the task row and force-cleans generated runtime resources,
// including the tmux window and task worktree.
func (m *Manager) DeleteTask(task db.Task) error {
	project, err := m.store.GetProject(task.ProjectID)
	if err != nil {
		return err
	}
	var cleanupErrs []error
	if task.SessionName != nil {
		target := tmux.Target(projectSessionName(project), *task.SessionName)
		if err := m.stopTaskWindow(target); err != nil {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("stop task window %s: %w", target, err))
		}
	}
	if err := worktree.RemoveForce(project, task.WorktreePath, task.BranchName); err != nil {
		cleanupErrs = append(cleanupErrs, fmt.Errorf("remove task worktree: %w", err))
	}
	if err := m.store.DeleteTask(task.ID); err != nil {
		return errors.Join(append(cleanupErrs, err)...)
	}
	if cleanupErr := errors.Join(cleanupErrs...); cleanupErr != nil {
		err := TaskCleanupError{TaskID: task.ID, Err: cleanupErr}
		log.Printf("operation=%q task=%s error=%v", "delete_task_cleanup", display.ShortID(task.ID), err)
		return err
	}
	return nil
}

// StopProject stops all task windows for a project, removes generated
// worktrees, clears persisted runtime fields, and finally kills the project tmux
// session if it still exists.
func (m *Manager) StopProject(project db.Project) error {
	tasks, err := m.store.ListTasks(project.ID, nil)
	if err != nil {
		return err
	}
	var errs []error
	for _, task := range tasks {
		if task.SessionName != nil {
			target := tmux.Target(projectSessionName(project), *task.SessionName)
			if err := m.stopTaskWindow(target); err != nil {
				errs = append(errs, fmt.Errorf("kill %s: %w", target, err))
			}
		}
		if err := worktree.RemoveForce(project, task.WorktreePath, task.BranchName); err != nil {
			errs = append(errs, fmt.Errorf("remove task %s worktree: %w", display.ShortID(task.ID), err))
		}
		if task.Status != db.StatusOffline || task.SessionName != nil || task.WorktreePath != nil || task.BranchName != nil || task.BaseBranch != nil {
			if err := m.store.UpdateTaskRuntimeBase(task.ID, nil, db.StatusOffline, nil, nil, nil); err != nil {
				errs = append(errs, fmt.Errorf("update task %s runtime: %w", display.ShortID(task.ID), err))
			}
		}
	}
	sessionName := projectSessionName(project)
	if m.backend.HasSession(sessionName) {
		if err := m.backend.KillSession(sessionName); err != nil {
			errs = append(errs, fmt.Errorf("kill session %s: %w", sessionName, err))
		}
	}
	return errors.Join(errs...)
}

func (m *Manager) withTaskStartupCleanupError(primary error, operation string, cleanup func() error) error {
	cleanupErr := cleanup()
	if cleanupErr == nil {
		return primary
	}
	log.Printf("operation=%q error=%v", operation, cleanupErr)
	return errors.Join(primary, fmt.Errorf("%s cleanup failed: %w", operation, cleanupErr))
}

func removePreparedWorktreeForCleanup(project db.Project, prepared worktree.Prepared) error {
	if err := removePreparedWorktree(project, prepared); err != nil {
		return fmt.Errorf("remove prepared worktree: %w", err)
	}
	return nil
}

func deleteTaskRowForCleanup(store *db.Store, taskID string) error {
	if err := store.DeleteTask(taskID); err != nil {
		return fmt.Errorf("delete task row: %w", err)
	}
	return nil
}

func killTaskWindowForCleanup(backend Backend, target string) error {
	if !backend.WindowExists(target) {
		return nil
	}
	if err := backend.KillWindow(target); err != nil {
		return fmt.Errorf("kill task window %s: %w", target, err)
	}
	return nil
}

func stopTaskForCleanup(m *Manager, task db.Task) error {
	if err := m.StopTask(task); err != nil {
		return fmt.Errorf("stop task %s: %w", display.ShortID(task.ID), err)
	}
	return nil
}

func restoreTaskRuntimeForCleanup(store *db.Store, task db.Task) error {
	if err := store.UpdateTaskRuntimeBase(task.ID, task.SessionName, task.Status, task.WorktreePath, task.BranchName, task.BaseBranch); err != nil {
		return fmt.Errorf("restore task runtime %s: %w", display.ShortID(task.ID), err)
	}
	return nil
}

func (m *Manager) taskTarget(task db.Task) (string, error) {
	if task.SessionName == nil || *task.SessionName == "" {
		return "", fmt.Errorf("task has no session")
	}
	project, err := m.store.GetProject(task.ProjectID)
	if err != nil {
		return "", err
	}
	return tmux.Target(projectSessionName(project), *task.SessionName), nil
}

func (m *Manager) validateTaskTargetCWD(task db.Task, target string) error {
	if task.WorktreePath == nil || strings.TrimSpace(*task.WorktreePath) == "" {
		return nil
	}
	expected, err := canonicalExistingPath(*task.WorktreePath)
	if err != nil {
		return fmt.Errorf("task worktree is missing; run the task again to recreate it: %s", *task.WorktreePath)
	}
	actualRaw, err := m.backend.PaneCurrentPath(target)
	if err != nil {
		return fmt.Errorf("read task pane cwd: %w", err)
	}
	actual, err := canonicalExistingPath(actualRaw)
	if err != nil {
		return fmt.Errorf("task pane cwd is unavailable: %s", actualRaw)
	}
	if actual != expected {
		return fmt.Errorf("refusing to send input to %s: pane cwd is %s, expected task worktree %s", target, actual, expected)
	}
	return nil
}

func canonicalExistingPath(path string) (string, error) {
	clean := filepath.Clean(strings.TrimSpace(path))
	if clean == "." || clean == "" {
		return "", fmt.Errorf("empty path")
	}
	real, err := filepath.EvalSymlinks(clean)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(real)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("not a directory: %s", real)
	}
	return filepath.Clean(real), nil
}

func (m *Manager) ensureSession(sessionName, workingDir string) error {
	if m.backend.HasSession(sessionName) {
		return nil
	}
	if err := m.backend.CreateSession(sessionName, workingDir); err != nil {
		return err
	}
	return m.backend.SetOption("history-limit", "50000")
}

func (m *Manager) createTaskWindow(sessionName, windowName, workingDir, command string) error {
	return m.backend.CreateWindow(sessionName, windowName, workingDir, command)
}

func (m *Manager) verifyTaskWindowStarted(task db.Task, target string, ignoreExitStatus bool) error {
	started := time.Now()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if !m.backend.WindowExists(target) {
			return fmt.Errorf("task session exited immediately: tmux window %s is not running", target)
		}
		if !ignoreExitStatus {
			if code, ok := script.ReadTaskExitStatus(task.ID); ok {
				logs, _ := m.backend.CapturePaneWithHistory(target, 80)
				logs = strings.TrimSpace(logs)
				if logs == "" {
					return fmt.Errorf("task agent exited immediately with status %d", code)
				}
				return fmt.Errorf("task agent exited immediately with status %d:\n%s", code, logs)
			}
		}
		if time.Since(started) >= 700*time.Millisecond {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out verifying task window %s startup", target)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (m *Manager) prepareInjectedPromptSession(target, prompt string) error {
	if err := m.waitForInjectedPromptReady(target); err != nil {
		return err
	}
	if prompt == "" {
		return nil
	}
	if err := m.backend.SendKeys(target, prompt); err != nil {
		return fmt.Errorf("send initial prompt: %w", err)
	}
	time.Sleep(250 * time.Millisecond)
	if err := m.backend.SendEnter(target); err != nil {
		return fmt.Errorf("confirm initial prompt: %w", err)
	}
	return nil
}

func (m *Manager) waitForInjectedPromptReady(target string) error {
	deadline := time.Now().Add(30 * time.Second)
	var lastLogs string
	acceptedTrustPrompt := false
	for {
		if !m.backend.WindowExists(target) {
			return fmt.Errorf("task session exited immediately: tmux window %s is not running", target)
		}
		logs, err := m.backend.CapturePaneWithHistory(target, 80)
		if err == nil {
			lastLogs = strings.TrimSpace(logs)
			if injectedPromptReady(logs) {
				return nil
			}
			if !acceptedTrustPrompt && claudeTrustPromptReady(logs) {
				if err := m.backend.SendEnter(target); err != nil {
					return fmt.Errorf("confirm Claude workspace trust prompt: %w", err)
				}
				acceptedTrustPrompt = true
				time.Sleep(500 * time.Millisecond)
				continue
			}
		}
		if time.Now().After(deadline) {
			if lastLogs != "" {
				return fmt.Errorf("timed out waiting for task prompt to become ready:\n%s", lastLogs)
			}
			return fmt.Errorf("timed out waiting for task prompt to become ready")
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func injectedPromptReady(logs string) bool {
	return strings.Contains(logs, "accept edits on") ||
		strings.Contains(logs, "bypass permissions on")
}

func claudeTrustPromptReady(logs string) bool {
	return strings.Contains(logs, "Accessing workspace:") &&
		strings.Contains(logs, "Yes, I trust this folder") &&
		strings.Contains(logs, "Enter to confirm")
}

func (m *Manager) closeBootstrapWindow(sessionName string) error {
	count, err := m.backend.WindowCount(sessionName)
	if err != nil || count <= 1 {
		return err
	}
	name, err := m.backend.WindowName(sessionName + ":0")
	if err != nil {
		return err
	}
	if strings.HasPrefix(name, "task-") {
		return nil
	}
	return m.backend.KillWindow(sessionName + ":0")
}

func (m *Manager) stopTaskWindow(target string) error {
	if !m.backend.WindowExists(target) {
		return nil
	}
	_ = m.backend.StopPipePane(target)
	return m.backend.KillWindow(target)
}

func (m *Manager) prepareWorktree(project db.Project, taskID string, workspaceMode db.WorkspaceMode) (worktree.Prepared, error) {
	worktreeConfig, err := m.worktreeConfig(project.Path, workspaceMode)
	if err != nil {
		return worktree.Prepared{}, err
	}
	return worktree.Prepare(project, taskID, worktreeConfig)
}

func (m *Manager) prepareWorktreeForTask(project db.Project, task db.Task) (worktree.Prepared, error) {
	worktreeConfig, err := m.worktreeConfig(project.Path, normalizeWorkspaceMode(task.WorkspaceMode))
	if err != nil {
		return worktree.Prepared{}, err
	}
	return worktree.PrepareForTask(project, task.ID, worktreeConfig, task.WorktreePath, task.BranchName)
}

func (m *Manager) worktreeConfig(projectPath string, workspaceMode db.WorkspaceMode) (config.WorktreeConfig, error) {
	projectConfig, err := config.LoadEffectiveProjectConfig(projectPath)
	if err != nil {
		return config.WorktreeConfig{}, err
	}
	worktreeConfig := projectConfig.Worktree
	switch normalizeWorkspaceMode(workspaceMode) {
	case db.WorkspaceModeProject:
		worktreeConfig.Enabled = false
	default:
		worktreeConfig.Enabled = true
	}
	if m.forceWorktrees {
		worktreeConfig.Enabled = true
	}
	return worktreeConfig, nil
}

func normalizeWorkspaceMode(mode db.WorkspaceMode) db.WorkspaceMode {
	if mode == "" {
		return db.WorkspaceModeWorktree
	}
	return mode
}

func removePreparedWorktree(project db.Project, prepared worktree.Prepared) error {
	if !prepared.Created {
		return nil
	}
	return worktree.Remove(project, prepared.Path, prepared.Branch, prepared.Base)
}

func projectSessionName(project db.Project) string {
	return tmux.SafeSessionName(project.Name + "-" + display.ShortID(project.ID))
}

func taskWindowName(taskID string) string {
	return "task-" + display.ShortID(taskID)
}
