package runtime

import (
	"strings"

	"github.com/nashory/agx/internal/db"
)

func (s *Service) taskDTO(task db.Task) Task {
	dto := baseTaskDTO(task)
	if task.Interface != db.TaskInterfaceDiscord {
		return dto
	}
	state, err := s.store.GetDiscordTaskSyncState(task.ID)
	if err != nil {
		return dto
	}
	dto.DiscordSync = &DiscordSync{
		Status:           string(state.Status),
		Attempts:         state.Attempts,
		DiscordChannelID: state.DiscordChannelID,
		LastSuccessAt:    state.LastSuccessAt,
		LastFailureAt:    state.LastFailureAt,
		LastError:        state.LastError,
		UpdatedAt:        state.UpdatedAt,
	}
	return dto
}

func baseTaskDTO(task db.Task) Task {
	return Task{
		ID:               task.ID,
		ProjectID:        task.ProjectID,
		Title:            task.Title,
		Description:      task.Description,
		LastUserPrompt:   task.LastUserPrompt,
		Interface:        string(task.Interface),
		Status:           task.Status,
		Agent:            task.Agent,
		AllMighty:        task.AllMighty,
		WorkspaceMode:    string(normalizeWorkspaceMode(task.WorkspaceMode)),
		SessionName:      task.SessionName,
		WorktreePath:     task.WorktreePath,
		BranchName:       task.BranchName,
		BaseBranch:       task.BaseBranch,
		AgentThreadID:    task.AgentThreadID,
		AgentEventCursor: task.AgentEventCursor,
		AgentStreamKind:  task.AgentStreamKind,
		CreatedAt:        task.CreatedAt,
		UpdatedAt:        task.UpdatedAt,
	}
}

func isRuntimeLiveTask(task db.Task) bool {
	if task.SessionName != nil && strings.TrimSpace(*task.SessionName) != "" {
		return true
	}
	if task.Status != db.StatusActive && task.Status != db.StatusWaiting {
		return false
	}
	return task.AgentStreamKind != nil && strings.TrimSpace(*task.AgentStreamKind) != ""
}

func transcriptDTO(message db.TaskTranscriptMessage) TaskTranscriptMessage {
	return TaskTranscriptMessage{
		ID:        message.ID,
		TaskID:    message.TaskID,
		TurnID:    message.TurnID,
		Role:      message.Role,
		Body:      message.Body,
		CreatedAt: message.CreatedAt,
		UpdatedAt: message.UpdatedAt,
	}
}
