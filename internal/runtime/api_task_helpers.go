package runtime

import (
	"context"
	"time"

	"github.com/nashory/agx/internal/db"
)

func (s *Service) taskAndProject(taskID string) (db.Task, db.Project, error) {
	task, err := s.store.GetTask(taskID)
	if err != nil {
		return db.Task{}, db.Project{}, err
	}
	project, err := s.store.GetProject(task.ProjectID)
	if err != nil {
		return db.Task{}, db.Project{}, err
	}
	return task, project, nil
}

func (s *Service) stopStructuredTaskForDelete(ctx context.Context, task db.Task) error {
	if !isRuntimeStructuredDBTask(task) {
		return nil
	}
	if err := s.agents.StopTask(ctx, task); err != nil {
		return err
	}
	s.agents.forgetTask(task.ID)
	return nil
}

func (s *Service) deleteDiscordChannelForTaskAsync(task db.Task, fallbackChannelID string) {
	if task.Interface != db.TaskInterfaceDiscord || s.discord == nil {
		return
	}
	taskID := task.ID
	go func() {
		if !s.discord.Status().Connected {
			logRuntimeOperation("discord_task_channel_cleanup",
				"task", shortDiagnosticID(taskID),
				"status", "skipped",
				"reason", "discord_disconnected",
			)
			return
		}
		for attempt := 1; attempt <= 3; attempt++ {
			ctx, cancel := s.backgroundTimeout(3 * time.Second)
			err := s.discord.DeleteTaskChannelWithFallback(ctx, taskID, fallbackChannelID)
			cancel()
			if err == nil {
				logRuntimeOperation("discord_task_channel_cleanup",
					"task", shortDiagnosticID(taskID),
					"status", "deleted",
					"attempt", attempt,
				)
				s.bus.Publish("discord.status", s.discord.Status())
				return
			}
			logRuntimeOperation("discord_task_channel_cleanup",
				"task", shortDiagnosticID(taskID),
				"attempt", attempt,
				"error", err,
			)
			if attempt < 3 {
				time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
			}
		}
		s.cleanupDiscordTaskChannelBySync(taskID)
	}()
}

func (s *Service) cleanupDiscordTaskChannelBySync(taskID string) {
	for attempt := 1; attempt <= 3; attempt++ {
		if s.discord == nil || !s.discord.Status().Connected {
			logRuntimeOperation("discord_task_channel_cleanup",
				"task", shortDiagnosticID(taskID),
				"status", "skipped",
				"reason", "discord_disconnected",
				"cleanup_attempt", attempt,
			)
			return
		}
		ctx, cancel := s.backgroundTimeout(15 * time.Second)
		err := s.discord.SoftSync(ctx)
		cancel()
		if err == nil {
			logRuntimeOperation("discord_task_channel_cleanup",
				"task", shortDiagnosticID(taskID),
				"status", "cleaned_by_soft_sync",
				"cleanup_attempt", attempt,
			)
			s.bus.Publish("discord.status", s.discord.Status())
			return
		}
		logRuntimeOperation("discord_task_channel_cleanup",
			"task", shortDiagnosticID(taskID),
			"status", "soft_sync_failed",
			"cleanup_attempt", attempt,
			"error", err,
		)
		if attempt < 3 {
			time.Sleep(time.Duration(attempt) * time.Second)
		}
	}
	s.bus.Publish("discord.status", s.discord.Status())
}
