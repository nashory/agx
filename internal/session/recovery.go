package session

import (
	"github.com/nashory/agx/internal/db"
	"github.com/nashory/agx/internal/tmux"
	"github.com/nashory/agx/internal/worktree"
)

type RecoveryResult struct {
	Offline int `json:"offline"`
	Cleared int `json:"cleared"`
	Orphans int `json:"orphans"`
}

func (m *Manager) RecoverLiveTasks() (RecoveryResult, error) {
	var result RecoveryResult
	tasks, err := m.store.ListLiveTasks()
	if err != nil {
		return result, err
	}
	if len(tasks) == 0 {
		return result, m.finishRecovery(&result)
	}
	if !m.tmux.HasTmux() || !m.tmux.HasServer() {
		for _, live := range tasks {
			if hasStructuredRuntime(live.Task) {
				continue
			}
			if err := m.recoverTaskWithoutSession(live.Task); err != nil {
				return result, err
			}
			result.Offline++
		}
		return result, m.finishRecovery(&result)
	}

	for _, live := range tasks {
		task := live.Task
		if hasStructuredRuntime(task) {
			continue
		}
		if task.SessionName == nil || *task.SessionName == "" {
			if err := m.recoverTaskWithoutSession(task); err != nil {
				return result, err
			}
			result.Offline++
			continue
		}
		project, err := m.store.GetProject(task.ProjectID)
		if err != nil {
			return result, err
		}
		sessionName := projectSessionName(project)
		if !m.tmux.HasSession(sessionName) {
			if err := m.recoverTaskWithoutSession(task); err != nil {
				return result, err
			}
			result.Offline++
			continue
		}
		target := tmux.Target(sessionName, *task.SessionName)
		if !m.tmux.WindowExists(target) {
			if err := m.recoverTaskWithoutSession(task); err != nil {
				return result, err
			}
			result.Offline++
		}
	}
	return result, m.finishRecovery(&result)
}

func hasStructuredRuntime(task db.Task) bool {
	return task.AgentStreamKind != nil && *task.AgentStreamKind != ""
}

func (m *Manager) recoverTaskWithoutSession(task db.Task) error {
	return m.store.UpdateTaskRuntimeBase(task.ID, nil, db.StatusOffline, task.WorktreePath, task.BranchName, task.BaseBranch)
}

func (m *Manager) finishRecovery(result *RecoveryResult) error {
	if err := m.clearStaleInactiveSessions(result); err != nil {
		return err
	}
	return m.cleanupOrphanWorktrees(result)
}

func (m *Manager) clearStaleInactiveSessions(result *RecoveryResult) error {
	tasks, err := m.store.ListTasks("", nil)
	if err != nil {
		return err
	}
	for _, task := range tasks {
		if task.Status == db.StatusActive || task.Status == db.StatusWaiting || task.SessionName == nil || *task.SessionName == "" {
			continue
		}
		project, err := m.store.GetProject(task.ProjectID)
		if err != nil {
			return err
		}
		sessionName := projectSessionName(project)
		target := tmux.Target(sessionName, *task.SessionName)
		if !m.tmux.HasSession(sessionName) || !m.tmux.WindowExists(target) {
			if err := m.store.UpdateTaskSession(task.ID, nil); err != nil {
				return err
			}
			result.Cleared++
		}
	}
	return nil
}

func (m *Manager) cleanupOrphanWorktrees(result *RecoveryResult) error {
	projects, err := m.store.ListProjects()
	if err != nil {
		return err
	}
	for _, project := range projects {
		tasks, err := m.store.ListTasks(project.ID, nil)
		if err != nil {
			return err
		}
		active := map[string]bool{}
		for _, task := range tasks {
			if task.WorktreePath != nil && *task.WorktreePath != "" {
				active[*task.WorktreePath] = true
			}
		}
		removed, err := worktree.CleanupOrphans(project, active)
		if err != nil {
			return err
		}
		result.Orphans += removed
	}
	return nil
}
