package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/nashory/agx/internal/db"
	agxdiscord "github.com/nashory/agx/internal/discord"
)

type discordCommandService struct {
	runtime *Service
}

func (s discordCommandService) ListTasks(ctx context.Context) ([]agxdiscord.TaskSummary, error) {
	projects, err := s.runtime.store.ListProjects()
	if err != nil {
		return nil, err
	}
	var out []agxdiscord.TaskSummary
	for _, project := range projects {
		tasks, err := s.runtime.store.ListTasks(project.ID, nil)
		if err != nil {
			return nil, err
		}
		for _, task := range tasks {
			if task.Interface != db.TaskInterfaceDiscord {
				continue
			}
			out = append(out, s.taskSummary(task, project.Name))
		}
	}
	return out, nil
}

func (s discordCommandService) ListProjects(ctx context.Context) ([]agxdiscord.ProjectSummary, error) {
	projects, err := s.runtime.store.ListProjects()
	if err != nil {
		return nil, err
	}
	out := make([]agxdiscord.ProjectSummary, 0, len(projects))
	for _, project := range projects {
		out = append(out, agxdiscord.ProjectSummary{ID: project.ID, Name: project.Name, Path: project.Path})
	}
	return out, nil
}

func (s discordCommandService) IsControlChannel(ctx context.Context, channelID string) (bool, error) {
	mapping, err := s.runtime.store.GetDiscordMapping(db.DiscordAGXControl, db.DiscordControlAGXID)
	if err != nil {
		if errors.Is(err, db.ErrDiscordMappingNotFound) {
			return false, nil
		}
		return false, err
	}
	return strings.TrimSpace(channelID) == mapping.DiscordID, nil
}

func (s discordCommandService) SoftSync(ctx context.Context) error {
	s.runtime.syncDiscordAsync()
	return nil
}

func (s discordCommandService) HardSync(ctx context.Context, preserveControlChannelID string) error {
	return s.runtime.startDiscordHardSync(preserveControlChannelID)
}

func (s discordCommandService) SyncStatus(ctx context.Context) agxdiscord.SyncStatusSummary {
	return s.runtime.discordSyncStatus()
}

func (s discordCommandService) ResolveTaskByChannel(ctx context.Context, channelID string) (string, error) {
	mapping, err := s.runtime.store.GetDiscordMappingByDiscordID(channelID)
	if err != nil {
		return "", err
	}
	if mapping.AGXType != db.DiscordAGXTask {
		return "", fmt.Errorf("discord channel is not mapped to a task")
	}
	return mapping.AGXID, nil
}

func (s discordCommandService) GetTask(ctx context.Context, taskID string) (agxdiscord.TaskSummary, error) {
	task, project, err := s.runtime.taskAndProject(taskID)
	if err != nil {
		return agxdiscord.TaskSummary{}, err
	}
	if task.Interface != db.TaskInterfaceDiscord {
		return agxdiscord.TaskSummary{}, fmt.Errorf("this AGX task is local-only and cannot be controlled from Discord")
	}
	return s.taskSummary(task, project.Name), nil
}

func (s discordCommandService) InterruptTask(ctx context.Context, taskID string) error {
	task, project, err := s.runtime.taskAndProject(taskID)
	if err != nil {
		return err
	}
	if task.Interface != db.TaskInterfaceDiscord {
		return fmt.Errorf("this AGX task is local-only and cannot be controlled from Discord")
	}
	if isRuntimeStructuredDBTask(task) {
		return s.runtime.agents.InterruptTask(ctx, task)
	}
	return s.runtime.managerForProject(project).InterruptTask(task)
}

func (s discordCommandService) KillTask(ctx context.Context, taskID, channelID string) error {
	return s.deleteTask(ctx, taskID, channelID)
}

func (s discordCommandService) deleteTask(ctx context.Context, taskID, fallbackChannelID string) error {
	task, project, err := s.runtime.taskAndProject(taskID)
	if err != nil {
		return err
	}
	if err := s.runtime.stopStructuredTaskForDelete(ctx, task); err != nil {
		return err
	}
	if err := s.runtime.removeTaskAttachmentFiles(task.ID); err != nil {
		return err
	}
	if err := s.runtime.managerForProject(project).DeleteTask(task); err != nil {
		return err
	}
	s.runtime.deleteDiscordChannelForTaskAsync(task, fallbackChannelID)
	return nil
}

func (s discordCommandService) TaskLogs(ctx context.Context, taskID string, lines int) (string, error) {
	task, project, err := s.runtime.taskAndProject(taskID)
	if err != nil {
		return "", err
	}
	return s.runtime.managerForProject(project).GetLogs(task, lines)
}

func (s discordCommandService) SendTaskMessage(ctx context.Context, taskID string, message agxdiscord.IncomingTaskMessage) (agxdiscord.SendTaskMessageResult, error) {
	return s.runtime.sendDiscordTaskMessage(ctx, taskID, message)
}

func (s discordCommandService) markTaskLogStream(taskID string) error {
	threadID := strings.TrimSpace(taskID)
	streamKind := tmuxLogStreamKind
	return s.runtime.store.UpdateTaskAgentStream(taskID, &threadID, nil, &streamKind)
}

func (s discordCommandService) taskSummary(task db.Task, projectName string) agxdiscord.TaskSummary {
	channelID := ""
	if mapping, err := s.runtime.store.GetDiscordMapping(db.DiscordAGXTask, task.ID); err == nil {
		channelID = mapping.DiscordID
	}
	return agxdiscord.TaskSummary{
		ID:               task.ID,
		Title:            task.Title,
		ProjectName:      projectName,
		Status:           string(task.Status),
		Agent:            task.Agent,
		AllMighty:        task.AllMighty,
		Interface:        string(task.Interface),
		SessionName:      task.SessionName,
		ChannelID:        channelID,
		AgentThreadID:    task.AgentThreadID,
		AgentEventCursor: task.AgentEventCursor,
		AgentStreamKind:  task.AgentStreamKind,
	}
}
