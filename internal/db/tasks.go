package db

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/nashory/agx/internal/display"
)

var ErrTaskNotFound = errors.New("task not found")
var ErrTaskAmbiguous = errors.New("ambiguous task id")
var ErrTaskIDTooShort = errors.New("task id prefix too short")
var ErrTaskNotRunnable = errors.New("task is not runnable")

const minTaskIDPrefixLength = 4

// AmbiguousTaskError reports all tasks matching a short ID prefix so the caller
// can show a useful disambiguation message.
type AmbiguousTaskError struct {
	Prefix  string
	Matches []Task
}

func (e AmbiguousTaskError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %q. Use a longer task id:", ErrTaskAmbiguous, e.Prefix)
	for _, task := range e.Matches {
		fmt.Fprintf(&b, "\n  %s  %-8s  %s", display.ShortID(task.ID), task.Status, task.Title)
	}
	return b.String()
}

func (e AmbiguousTaskError) Unwrap() error {
	return ErrTaskAmbiguous
}

// CreateTask creates a local task with a generated ID and no live runtime
// session.
func (s *Store) CreateTask(projectID, title string, description *string, agent string, status TaskStatus) (Task, error) {
	id := uuid.NewString()
	return s.CreateTaskWithSession(id, projectID, title, description, agent, status, nil)
}

// NewTaskID returns a globally unique task ID.
func NewTaskID() string {
	return uuid.NewString()
}

func (s *Store) CreateTaskWithSession(id, projectID, title string, description *string, agent string, status TaskStatus, sessionName *string) (Task, error) {
	return s.CreateTaskRuntime(id, projectID, title, description, agent, status, sessionName, nil, nil)
}

func (s *Store) CreateTaskRuntime(id, projectID, title string, description *string, agent string, status TaskStatus, sessionName, worktreePath, branchName *string) (Task, error) {
	return s.CreateTaskRuntimeMode(id, projectID, title, description, agent, false, status, sessionName, worktreePath, branchName)
}

func (s *Store) CreateTaskRuntimeMode(id, projectID, title string, description *string, agent string, allMighty bool, status TaskStatus, sessionName, worktreePath, branchName *string) (Task, error) {
	return s.CreateTaskRuntimeModeInterface(id, projectID, title, description, agent, allMighty, TaskInterfaceLocal, status, sessionName, worktreePath, branchName)
}

func (s *Store) CreateTaskRuntimeModeInterface(id, projectID, title string, description *string, agent string, allMighty bool, iface TaskInterface, status TaskStatus, sessionName, worktreePath, branchName *string) (Task, error) {
	return s.CreateTaskRuntimeModeInterfaceWorkspace(id, projectID, title, description, agent, allMighty, iface, WorkspaceModeWorktree, status, sessionName, worktreePath, branchName)
}

// CreateTaskRuntimeModeInterfaceWorkspace is the canonical task creation path.
// Narrower CreateTask* helpers delegate here so status/interface/workspace
// validation remains centralized.
func (s *Store) CreateTaskRuntimeModeInterfaceWorkspace(id, projectID, title string, description *string, agent string, allMighty bool, iface TaskInterface, workspaceMode WorkspaceMode, status TaskStatus, sessionName, worktreePath, branchName *string) (Task, error) {
	if status == "" {
		status = StatusActive
	}
	if !IsValidTaskStatus(status) {
		return Task{}, fmt.Errorf("invalid task status %q", status)
	}
	if iface == "" {
		iface = TaskInterfaceLocal
	}
	if !IsValidTaskInterface(iface) {
		return Task{}, fmt.Errorf("invalid task interface %q", iface)
	}
	if workspaceMode == "" {
		workspaceMode = WorkspaceModeWorktree
	}
	if !IsValidWorkspaceMode(workspaceMode) {
		return Task{}, fmt.Errorf("invalid workspace mode %q", workspaceMode)
	}
	if _, err := s.db.Exec(`
INSERT INTO tasks (id, project_id, title, description, interface, workspace_mode, status, agent, all_mighty, session_name, worktree_path, branch_name)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`, id, projectID, title, description, string(iface), string(workspaceMode), string(status), agent, boolToInt(allMighty), sessionName, worktreePath, branchName); err != nil {
		return Task{}, err
	}
	return s.GetTask(id)
}

// GetTask loads a task by full ID.
func (s *Store) GetTask(id string) (Task, error) {
	row := s.db.QueryRow(`
SELECT id, project_id, title, description, last_user_prompt, interface, workspace_mode, status, agent, all_mighty, session_name, worktree_path, branch_name, base_branch, agent_thread_id, agent_event_cursor, agent_stream_kind, created_at, updated_at
FROM tasks
WHERE id = ?
`, id)
	return scanTask(row)
}

// ResolveTask resolves a task by ID prefix across all projects. Prefixes shorter
// than minTaskIDPrefixLength are rejected to avoid surprising matches.
func (s *Store) ResolveTask(prefix string) (Task, error) {
	prefix = strings.TrimSpace(prefix)
	if len(prefix) < minTaskIDPrefixLength {
		return Task{}, fmt.Errorf("%w: use at least %d characters", ErrTaskIDTooShort, minTaskIDPrefixLength)
	}
	return s.resolveTask(prefix, "")
}

// ResolveTaskInProject resolves a task ID prefix scoped to one project.
func (s *Store) ResolveTaskInProject(projectID, prefix string) (Task, error) {
	prefix = strings.TrimSpace(prefix)
	if len(prefix) < minTaskIDPrefixLength {
		return Task{}, fmt.Errorf("%w: use at least %d characters", ErrTaskIDTooShort, minTaskIDPrefixLength)
	}
	return s.resolveTask(prefix, projectID)
}

func (s *Store) resolveTask(prefix, projectID string) (Task, error) {
	rows, err := s.db.Query(`
SELECT id, project_id, title, description, last_user_prompt, interface, workspace_mode, status, agent, all_mighty, session_name, worktree_path, branch_name, base_branch, agent_thread_id, agent_event_cursor, agent_stream_kind, created_at, updated_at
FROM tasks
WHERE id LIKE ? || '%'
  AND (? = '' OR project_id = ?)
ORDER BY created_at ASC, id ASC
LIMIT 2
`, prefix, projectID, projectID)
	if err != nil {
		return Task{}, err
	}
	defer rows.Close()
	var tasks []Task
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			return Task{}, err
		}
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		return Task{}, err
	}
	switch len(tasks) {
	case 0:
		return Task{}, ErrTaskNotFound
	case 1:
		return tasks[0], nil
	default:
		return Task{}, AmbiguousTaskError{Prefix: prefix, Matches: tasks}
	}
}

// ListTasks returns tasks ordered for operator attention: active, waiting,
// complete, then offline.
func (s *Store) ListTasks(projectID string, status *TaskStatus) ([]Task, error) {
	query := `
SELECT id, project_id, title, description, last_user_prompt, interface, workspace_mode, status, agent, all_mighty, session_name, worktree_path, branch_name, base_branch, agent_thread_id, agent_event_cursor, agent_stream_kind, created_at, updated_at
FROM tasks
WHERE (? = '' OR project_id = ?)
  AND (? = '' OR status = ?)
ORDER BY
	CASE status
		WHEN 'active' THEN 0
		WHEN 'waiting' THEN 1
		WHEN 'complete' THEN 2
		WHEN 'offline' THEN 3
	END,
	created_at ASC
`
	statusValue := ""
	if status != nil {
		statusValue = string(*status)
	}
	rows, err := s.db.Query(query, projectID, projectID, statusValue, statusValue)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTasks(rows)
}

// ListLiveTasks returns active/waiting tasks with project metadata for recovery
// and monitor views.
func (s *Store) ListLiveTasks() ([]LiveTask, error) {
	rows, err := s.db.Query(`
SELECT t.id, t.project_id, t.title, t.description, t.last_user_prompt, t.interface, t.workspace_mode, t.status, t.agent, t.all_mighty, t.session_name, t.worktree_path, t.branch_name, t.base_branch, t.agent_thread_id, t.agent_event_cursor, t.agent_stream_kind, t.created_at, t.updated_at,
       p.name, p.path
FROM tasks t
JOIN projects p ON t.project_id = p.id
WHERE t.status IN ('active', 'waiting')
ORDER BY t.created_at ASC
`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tasks []LiveTask
	for rows.Next() {
		var task LiveTask
		base, err := scanTaskColumns(rows, &task.ProjectName, &task.ProjectPath)
		if err != nil {
			return nil, err
		}
		task.Task = base
		tasks = append(tasks, task)
	}
	return tasks, rows.Err()
}

// ActiveProjectWorkspaceTask returns the active/waiting project-mode task for a
// project. Project mode is single-owner because tasks share the project checkout.
func (s *Store) ActiveProjectWorkspaceTask(projectID string) (Task, error) {
	row := s.db.QueryRow(`
SELECT id, project_id, title, description, last_user_prompt, interface, workspace_mode, status, agent, all_mighty, session_name, worktree_path, branch_name, base_branch, agent_thread_id, agent_event_cursor, agent_stream_kind, created_at, updated_at
FROM tasks
WHERE project_id = ?
  AND workspace_mode = 'project'
  AND status IN ('active', 'waiting')
ORDER BY created_at ASC
LIMIT 1
`, projectID)
	return scanTask(row)
}

func (s *Store) UpdateTaskStatus(id string, status TaskStatus) error {
	if !IsValidTaskStatus(status) {
		return fmt.Errorf("invalid task status %q", status)
	}
	return s.execTaskUpdate(`
UPDATE tasks SET status = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?
`, string(status), id)
}

func (s *Store) UpdateTaskInterface(id string, iface TaskInterface) error {
	if !IsValidTaskInterface(iface) {
		return fmt.Errorf("invalid task interface %q", iface)
	}
	return s.execTaskUpdate(`
UPDATE tasks SET interface = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?
`, string(iface), id)
}

func (s *Store) UpdateTaskSession(id string, sessionName *string) error {
	return s.execTaskUpdate(`
UPDATE tasks SET session_name = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?
`, sessionName, id)
}

func (s *Store) UpdateTaskSessionAndStatus(id string, sessionName *string, status TaskStatus) error {
	if !IsValidTaskStatus(status) {
		return fmt.Errorf("invalid task status %q", status)
	}
	return s.execTaskUpdate(`
UPDATE tasks SET session_name = ?, status = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?
`, sessionName, string(status), id)
}

func (s *Store) UpdateTaskRuntime(id string, sessionName *string, status TaskStatus, worktreePath, branchName *string) error {
	if !IsValidTaskStatus(status) {
		return fmt.Errorf("invalid task status %q", status)
	}
	return s.UpdateTaskRuntimeBase(id, sessionName, status, worktreePath, branchName, nil)
}

// UpdateTaskRuntimeBase atomically updates runtime ownership fields. Passing nil
// clears a field; callers should pass existing pointers when preserving
// worktree/branch metadata during stop or recovery.
func (s *Store) UpdateTaskRuntimeBase(id string, sessionName *string, status TaskStatus, worktreePath, branchName, baseBranch *string) error {
	if !IsValidTaskStatus(status) {
		return fmt.Errorf("invalid task status %q", status)
	}
	return s.execTaskUpdate(`
UPDATE tasks SET session_name = ?, status = ?, worktree_path = ?, branch_name = ?, base_branch = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?
`, sessionName, string(status), worktreePath, branchName, baseBranch, id)
}

func (s *Store) UpdateTaskAgentStream(id string, threadID, cursor, streamKind *string) error {
	return s.execTaskUpdate(`
UPDATE tasks SET agent_thread_id = ?, agent_event_cursor = ?, agent_stream_kind = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?
`, threadID, cursor, streamKind, id)
}

func (s *Store) UpdateTaskAgentEventCursor(id string, cursor *string) error {
	return s.execTaskUpdate(`
UPDATE tasks SET agent_event_cursor = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?
`, cursor, id)
}

func (s *Store) UpdateTaskLastUserPrompt(id, prompt string) error {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return nil
	}
	return s.execTaskUpdate(`
UPDATE tasks SET last_user_prompt = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?
`, prompt, id)
}

func (s *Store) UpdateTask(id string, title *string, description **string, agent *string) error {
	task, err := s.GetTask(id)
	if err != nil {
		return err
	}
	if title == nil {
		title = &task.Title
	}
	var desc *string
	if description == nil {
		desc = task.Description
	} else {
		desc = *description
	}
	if agent == nil {
		agent = &task.Agent
	}
	var descValue any
	if desc != nil {
		descValue = *desc
	}
	return s.execTaskUpdate(`
UPDATE tasks SET title = ?, description = ?, agent = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?
`, *title, descValue, *agent, id)
}

func (s *Store) DeleteTask(id string) error {
	return s.execTaskUpdate(`DELETE FROM tasks WHERE id = ?`, id)
}

func (s *Store) execTaskUpdate(query string, args ...any) error {
	result, err := s.db.Exec(query, args...)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("get task rows affected: %w", err)
	}
	if n == 0 {
		return ErrTaskNotFound
	}
	return nil
}

func scanTasks(rows *sql.Rows) ([]Task, error) {
	var tasks []Task
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	return tasks, rows.Err()
}

func scanTask(scanner interface {
	Scan(dest ...any) error
}) (Task, error) {
	task, err := scanTaskColumns(scanner)
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, ErrTaskNotFound
	}
	return task, err
}

func scanTaskColumns(scanner interface {
	Scan(dest ...any) error
}, extra ...any) (Task, error) {
	var t Task
	var iface string
	var workspaceMode string
	var status string
	var allMighty int
	dest := []any{&t.ID, &t.ProjectID, &t.Title, &t.Description, &t.LastUserPrompt, &iface, &workspaceMode, &status, &t.Agent, &allMighty, &t.SessionName, &t.WorktreePath, &t.BranchName, &t.BaseBranch, &t.AgentThreadID, &t.AgentEventCursor, &t.AgentStreamKind, &t.CreatedAt, &t.UpdatedAt}
	dest = append(dest, extra...)
	err := scanner.Scan(dest...)
	t.Interface = TaskInterface(iface)
	if t.Interface == "" {
		t.Interface = TaskInterfaceLocal
	}
	t.WorkspaceMode = WorkspaceMode(workspaceMode)
	if t.WorkspaceMode == "" {
		t.WorkspaceMode = WorkspaceModeWorktree
	}
	t.Status = TaskStatus(status)
	t.AllMighty = allMighty != 0
	return t, err
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
