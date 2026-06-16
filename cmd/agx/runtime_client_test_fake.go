package main

import (
	"context"

	"github.com/nashory/agx/internal/db"
	agxdiscord "github.com/nashory/agx/internal/discord"
	agxruntime "github.com/nashory/agx/internal/runtime"
)

type fakeRuntimeTaskCreateClient struct {
	projects            []agxruntime.Project
	tasks               []agxruntime.Task
	monitorTasks        []agxruntime.MonitorTask
	discordStatus       agxdiscord.Status
	deletedTaskID       string
	discordSoftSynced   bool
	discordDisconnected bool
	runTask             func(context.Context, string, string, *string, string, bool, *string, db.WorkspaceMode) (agxruntime.Task, error)
	runDiscordTask      func(context.Context, string, string, *string, string, bool, db.WorkspaceMode) (agxruntime.Task, error)
	listTasksStatus     func(context.Context, string, string) ([]agxruntime.Task, error)
	discordConnect      func(context.Context, string, string, string) (agxdiscord.Status, error)
}

func (f *fakeRuntimeTaskCreateClient) ListProjects(context.Context) ([]agxruntime.Project, error) {
	return f.projects, nil
}

func (f *fakeRuntimeTaskCreateClient) CreateProject(context.Context, string, string, *string, *string) (agxruntime.Project, error) {
	return agxruntime.Project{}, db.ErrProjectNotFound
}

func (f *fakeRuntimeTaskCreateClient) GrantProjectAccess(context.Context, string) (agxruntime.Project, error) {
	return agxruntime.Project{}, db.ErrProjectNotFound
}

func (f *fakeRuntimeTaskCreateClient) RunNewTaskWithInitialPromptWorkspace(ctx context.Context, projectID, title string, description *string, agentName string, allMighty bool, initialPrompt *string, workspaceMode db.WorkspaceMode) (agxruntime.Task, error) {
	if f.runTask != nil {
		return f.runTask(ctx, projectID, title, description, agentName, allMighty, initialPrompt, workspaceMode)
	}
	return agxruntime.Task{}, nil
}

func (f *fakeRuntimeTaskCreateClient) RunNewDiscordTaskWithWorkspace(ctx context.Context, projectID, title string, description *string, agentName string, allMighty bool, workspaceMode db.WorkspaceMode) (agxruntime.Task, error) {
	if f.runDiscordTask != nil {
		return f.runDiscordTask(ctx, projectID, title, description, agentName, allMighty, workspaceMode)
	}
	return agxruntime.Task{}, nil
}

func (f *fakeRuntimeTaskCreateClient) ListTasks(context.Context, string) ([]agxruntime.Task, error) {
	return f.tasks, nil
}

func (f *fakeRuntimeTaskCreateClient) ListTasksStatus(ctx context.Context, projectID, status string) ([]agxruntime.Task, error) {
	if f.listTasksStatus != nil {
		return f.listTasksStatus(ctx, projectID, status)
	}
	return f.tasks, nil
}

func (f *fakeRuntimeTaskCreateClient) MonitorTasks(context.Context) ([]agxruntime.MonitorTask, error) {
	return f.monitorTasks, nil
}

func (f *fakeRuntimeTaskCreateClient) DeleteTask(_ context.Context, taskID string) error {
	f.deletedTaskID = taskID
	return nil
}

func (f *fakeRuntimeTaskCreateClient) DiscordConnect(ctx context.Context, token, guildID, allowedUserID string) (agxdiscord.Status, error) {
	if f.discordConnect != nil {
		return f.discordConnect(ctx, token, guildID, allowedUserID)
	}
	return f.discordStatus, nil
}

func (f *fakeRuntimeTaskCreateClient) DiscordDisconnect(context.Context) (agxdiscord.Status, error) {
	f.discordDisconnected = true
	return f.discordStatus, nil
}

func (f *fakeRuntimeTaskCreateClient) DiscordStatus(context.Context) (agxdiscord.Status, error) {
	return f.discordStatus, nil
}

func (f *fakeRuntimeTaskCreateClient) DiscordSoftSync(context.Context) (agxdiscord.Status, error) {
	f.discordSoftSynced = true
	return f.discordStatus, nil
}
