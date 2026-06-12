package runtime

import (
	"context"
	"fmt"

	"github.com/nashory/agx/internal/agent"
	"github.com/nashory/agx/internal/codexapp"
	"github.com/nashory/agx/internal/config"
	"github.com/nashory/agx/internal/db"
	"github.com/nashory/agx/internal/worktree"
)

// createStructuredDiscordTask creates a Discord-controlled task backed by a
// structured agent event stream instead of a tmux window. It prepares workspace
// state, persists the task before agent startup, and rolls back generated
// resources if preparation fails.
func (s *Service) createStructuredDiscordTask(ctx context.Context, project db.Project, req createTaskRequest, agentName string) (db.Task, error) {
	registry := agent.RegistryForProject(project.Path)
	ag, err := registry.Get(agentName)
	if err != nil {
		return db.Task{}, err
	}
	if !ag.IsAvailable() {
		return db.Task{}, fmt.Errorf("agent %q is not available on PATH", ag.Name)
	}
	taskID := db.NewTaskID()
	workspaceMode, err := parseWorkspaceMode(req.WorkspaceMode)
	if err != nil {
		return db.Task{}, err
	}
	prepared, err := prepareStructuredWorkspace(project, taskID, workspaceMode)
	if err != nil {
		return db.Task{}, err
	}
	task, err := s.store.CreateTaskRuntimeModeInterfaceWorkspace(taskID, project.ID, req.Title, req.Description, agentName, req.AllMighty, db.TaskInterfaceDiscord, workspaceMode, db.StatusWaiting, nil, prepared.Path, prepared.Branch)
	if err != nil {
		_ = removeStructuredWorktree(project, prepared)
		return db.Task{}, err
	}
	if prepared.Base != nil {
		if err := s.store.UpdateTaskRuntimeBase(task.ID, nil, task.Status, task.WorktreePath, task.BranchName, prepared.Base); err != nil {
			_ = removeStructuredWorktree(project, prepared)
			_ = s.store.DeleteTask(task.ID)
			return db.Task{}, err
		}
		task.BaseBranch = prepared.Base
	}
	s.syncDiscordTaskBestEffort(task.ID)
	if err := s.agents.PrepareTask(ctx, task, project); err != nil {
		_ = s.discord.DeleteTaskChannel(context.Background(), task.ID)
		_ = removeStructuredWorktree(project, prepared)
		_ = s.store.DeleteTask(task.ID)
		return db.Task{}, err
	}
	prompt := structuredInitialPrompt(req.Description, req.InitialPrompt)
	if prompt != "" {
		refreshed, err := s.store.GetTask(task.ID)
		if err != nil {
			return db.Task{}, err
		}
		if err := s.agents.SendTaskMessage(ctx, refreshed, project, prompt); err != nil {
			return db.Task{}, err
		}
		_ = s.store.AppendTaskTranscriptMessage(task.ID, "user", prompt, nil, nil)
		_ = s.store.UpdateTaskLastUserPrompt(task.ID, prompt)
	}
	return s.store.GetTask(task.ID)
}

// prepareStructuredWorkspace mirrors session workspace semantics for structured
// agents: project mode uses the project checkout, worktree mode creates a
// generated per-task worktree.
func prepareStructuredWorkspace(project db.Project, taskID string, workspaceMode db.WorkspaceMode) (worktree.Prepared, error) {
	projectConfig, err := config.LoadEffectiveProjectConfig(project.Path)
	if err != nil {
		return worktree.Prepared{}, err
	}
	projectConfig.Worktree.Enabled = normalizeWorkspaceMode(workspaceMode) == db.WorkspaceModeWorktree
	return worktree.Prepare(project, taskID, projectConfig.Worktree)
}

func removeStructuredWorktree(project db.Project, prepared worktree.Prepared) error {
	if !prepared.Created {
		return nil
	}
	return worktree.Remove(project, prepared.Path, prepared.Branch, prepared.Base)
}

// structuredInitialPrompt chooses the first user message sent to a newly-created
// structured agent. An explicit override, including an empty string, takes
// precedence over the task description.
func structuredInitialPrompt(description, override *string) string {
	if override != nil {
		return *override
	}
	if description != nil {
		return *description
	}
	return ""
}

// isStructuredAgentName reports whether an agent can be controlled through the
// structured event pipeline instead of legacy tmux input.
func isStructuredAgentName(agentName string) bool {
	return isCodexTask(agentName) || isClaudeTask(agentName)
}

// isRuntimeStructuredDBTask identifies tasks whose runtime state is owned by an
// agent event stream and therefore should not be controlled through tmux.
func isRuntimeStructuredDBTask(task db.Task) bool {
	if task.AgentStreamKind == nil {
		return false
	}
	kind := *task.AgentStreamKind
	return kind == claudeStreamKind || kind == codexapp.StreamKind
}
