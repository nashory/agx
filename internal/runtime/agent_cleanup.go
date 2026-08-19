package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/nashory/agx/internal/db"
	"github.com/nashory/agx/internal/session"
)

type cleanupAgentTasksRequest struct {
	Agent string `json:"agent"`
}

func (s *Service) handleCleanupAgentTasks(w http.ResponseWriter, r *http.Request) {
	var req cleanupAgentTasksRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorStatus(w, http.StatusBadRequest, fmt.Errorf("decode agent cleanup request: %w", err))
		return
	}
	agentName := strings.TrimSpace(req.Agent)
	if agentName == "" {
		writeErrorStatus(w, http.StatusBadRequest, fmt.Errorf("agent is required"))
		return
	}
	result, err := s.cleanupLiveTasksByAgent(r.Context(), agentName)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, result)
}

func (s *Service) cleanupLiveTasksByAgent(ctx context.Context, agentName string) (AgentCleanupResult, error) {
	agentName = strings.TrimSpace(agentName)
	result := AgentCleanupResult{Agent: agentName}
	live, err := s.store.ListLiveTasks()
	if err != nil {
		return result, err
	}
	var matched []db.Task
	for _, item := range live {
		if strings.EqualFold(strings.TrimSpace(item.Agent), agentName) {
			matched = append(matched, item.Task)
		}
	}
	result.Matched = len(matched)
	hadDiscordTask := false
	for _, task := range matched {
		if task.Interface == db.TaskInterfaceDiscord {
			hadDiscordTask = true
		}
		deleted, warning, err := s.cleanupAgentTask(ctx, task)
		if warning != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("%s: %v", task.Title, warning))
		}
		if err != nil {
			result.Failed++
			result.Failures = append(result.Failures, AgentCleanupFailure{TaskID: task.ID, Title: task.Title, Error: err.Error()})
			continue
		}
		if deleted {
			result.Deleted++
		}
	}
	if hadDiscordTask && s.discord != nil && s.discord.Status().Connected {
		reconcileCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := s.discord.SoftSync(reconcileCtx)
		cancel()
		if err != nil {
			result.DiscordReconcileErr = err.Error()
		} else {
			result.DiscordReconciled = true
		}
	}
	logRuntimeOperation("agent_task_cleanup",
		"agent", agentName,
		"matched", result.Matched,
		"deleted", result.Deleted,
		"failed", result.Failed,
		"warnings", len(result.Warnings),
		"discord_reconciled", result.DiscordReconciled,
		"discord_error", result.DiscordReconcileErr,
	)
	return result, nil
}

func (s *Service) cleanupAgentTask(ctx context.Context, task db.Task) (deleted bool, warning error, err error) {
	lock := s.taskLock(task.ID)
	lock.Lock()
	defer lock.Unlock()

	project, err := s.store.GetProject(task.ProjectID)
	if err != nil {
		return false, nil, err
	}
	channelDeleted := false
	if task.Interface == db.TaskInterfaceDiscord {
		if s.discord == nil || !s.discord.Status().Connected {
			return false, nil, fmt.Errorf("Discord is disconnected; task kept to avoid leaving its channel orphaned")
		}
		fallbackChannelID := s.discordTaskChannelFallback(task.ID)
		deleteCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		err := s.discord.DeleteTaskChannelWithFallback(deleteCtx, task.ID, fallbackChannelID)
		cancel()
		if err != nil {
			return false, nil, fmt.Errorf("delete Discord task channel: %w", err)
		}
		channelDeleted = true
	}
	if err := s.stopStructuredTaskForDelete(ctx, task); err != nil {
		return false, nil, s.agentCleanupFailureWithDiscordRestore(task, channelDeleted, fmt.Errorf("stop agent session: %w", err))
	}
	if err := s.removeTaskAttachmentFiles(task.ID); err != nil {
		return false, nil, s.agentCleanupFailureWithDiscordRestore(task, channelDeleted, fmt.Errorf("remove task attachments: %w", err))
	}

	err = s.managerForProject(project).DeleteTask(task)
	if err != nil {
		var cleanupErr session.TaskCleanupError
		if errors.As(err, &cleanupErr) {
			s.bus.Publish("task.deleted", map[string]string{"id": task.ID, "projectId": task.ProjectID})
			return true, cleanupErr, nil
		}
		return false, nil, s.agentCleanupFailureWithDiscordRestore(task, channelDeleted, err)
	}
	s.bus.Publish("task.deleted", map[string]string{"id": task.ID, "projectId": task.ProjectID})
	return true, nil, nil
}

func (s *Service) agentCleanupFailureWithDiscordRestore(task db.Task, channelDeleted bool, cause error) error {
	if !channelDeleted {
		return cause
	}
	if _, err := s.store.GetTask(task.ID); err != nil {
		return cause
	}
	if restoreErr := s.syncDiscordTaskNow(task.ID); restoreErr != nil {
		return errors.Join(cause, fmt.Errorf("restore Discord task channel: %w", restoreErr))
	}
	return cause
}

func (s *Service) discordTaskChannelFallback(taskID string) string {
	state, err := s.store.GetDiscordTaskSyncState(taskID)
	if err != nil || state.DiscordChannelID == nil {
		return ""
	}
	return strings.TrimSpace(*state.DiscordChannelID)
}
