package runtime

import (
	"context"
	"log"
	"time"

	"github.com/nashory/agx/internal/db"
	"github.com/nashory/agx/internal/display"
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
	if task.Interface != db.TaskInterfaceDiscord || s.discord == nil || !s.discord.Status().Connected {
		return
	}
	taskID := task.ID
	go func() {
		ctx, cancel := s.backgroundTimeout(15 * time.Second)
		defer cancel()
		if err := s.discord.DeleteTaskChannelWithFallback(ctx, taskID, fallbackChannelID); err != nil {
			log.Printf("operation=%q task=%s error=%v", "discord_task_channel_cleanup", display.ShortID(taskID), err)
		}
		s.bus.Publish("discord.status", s.discord.Status())
	}()
}
