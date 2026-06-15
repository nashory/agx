package runtime

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/nashory/agx/internal/agent"
	"github.com/nashory/agx/internal/codexapp"
	"github.com/nashory/agx/internal/config"
	"github.com/nashory/agx/internal/db"
	"github.com/nashory/agx/internal/display"
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
		return db.Task{}, s.withStructuredCleanupError(err, "create structured task", func() error {
			return removeStructuredWorktreeForCleanup(project, prepared)
		})
	}
	if prepared.Base != nil {
		if err := s.store.UpdateTaskRuntimeBase(task.ID, nil, task.Status, task.WorktreePath, task.BranchName, prepared.Base); err != nil {
			return db.Task{}, s.withStructuredCleanupError(err, "update structured task runtime", func() error {
				return errors.Join(
					removeStructuredWorktreeForCleanup(project, prepared),
					deleteStructuredTaskRowForCleanup(s.store, task.ID),
				)
			})
		}
		task.BaseBranch = prepared.Base
	}
	s.syncDiscordTaskBestEffort(task.ID)
	if err := s.agents.PrepareTask(ctx, task, project); err != nil {
		return db.Task{}, s.rollbackStructuredDiscordTask(err, project, task, prepared)
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

func (s *Service) rollbackStructuredDiscordTask(primary error, project db.Project, task db.Task, prepared worktree.Prepared) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return s.withStructuredCleanupError(primary, fmt.Sprintf("rollback structured Discord task %s", display.ShortID(task.ID)), func() error {
		var discordErr error
		if s.discord != nil {
			discordErr = s.discord.DeleteTaskChannel(ctx, task.ID)
		}
		return errors.Join(
			discordErr,
			removeStructuredWorktreeForCleanup(project, prepared),
			deleteStructuredTaskRowForCleanup(s.store, task.ID),
		)
	})
}

func removeStructuredWorktreeForCleanup(project db.Project, prepared worktree.Prepared) error {
	if err := removeStructuredWorktree(project, prepared); err != nil {
		return fmt.Errorf("remove structured worktree: %w", err)
	}
	return nil
}

func deleteStructuredTaskRowForCleanup(store *db.Store, taskID string) error {
	if err := store.DeleteTask(taskID); err != nil {
		return fmt.Errorf("delete structured task row: %w", err)
	}
	return nil
}

func (s *Service) withStructuredCleanupError(primary error, operation string, cleanup func() error) error {
	cleanupErr := cleanup()
	if cleanupErr == nil {
		return primary
	}
	log.Printf("operation=%q error=%v", operation, cleanupErr)
	return errors.Join(primary, fmt.Errorf("%s cleanup failed: %w", operation, cleanupErr))
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
