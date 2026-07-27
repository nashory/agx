package runtime

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/nashory/agx/internal/agent"
	"github.com/nashory/agx/internal/db"
	agxdiscord "github.com/nashory/agx/internal/discord"
	"github.com/nashory/agx/internal/session"
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

func (s discordCommandService) CreateProject(ctx context.Context, path, name, defaultAgent string) (agxdiscord.ProjectSummary, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return agxdiscord.ProjectSummary{}, fmt.Errorf("project path is required")
	}
	// Discord slash-command input never passes through a shell, so expand a
	// leading ~ before probing access; otherwise os.Stat/git see a literal "~".
	path = db.ExpandHomePath(path)
	if err := validateOrRepairProjectAccess(path); err != nil {
		return agxdiscord.ProjectSummary{}, err
	}
	var defaultAgentPtr *string
	if strings.TrimSpace(defaultAgent) != "" {
		agentName := strings.TrimSpace(defaultAgent)
		defaultAgentPtr = &agentName
	}
	project, err := s.runtime.store.EnsureProjectDetails(path, strings.TrimSpace(name), nil, defaultAgentPtr)
	if err != nil {
		return agxdiscord.ProjectSummary{}, err
	}
	if err := s.runtime.store.MarkProjectAccessGranted(project.Path); err != nil {
		return agxdiscord.ProjectSummary{}, err
	}
	dto := s.runtime.projectDTO(project)
	s.runtime.bus.Publish("project.changed", dto)
	logRuntimeOperation("discord_project_create",
		"project", shortDiagnosticID(project.ID),
		"path", project.Path,
		"default_agent", valueOrEmptyString(project.DefaultAgent),
	)
	return agxdiscord.ProjectSummary{ID: project.ID, Name: project.Name, Path: project.Path}, nil
}

func (s discordCommandService) DeleteProject(ctx context.Context, projectRef string) (agxdiscord.ProjectSummary, error) {
	project, err := s.resolveProject(projectRef)
	if err != nil {
		return agxdiscord.ProjectSummary{}, err
	}
	tasks, err := s.runtime.store.ListTasks(project.ID, nil)
	if err != nil {
		return agxdiscord.ProjectSummary{}, err
	}
	for _, task := range tasks {
		if err := s.runtime.stopStructuredTaskForDelete(ctx, task); err != nil {
			return agxdiscord.ProjectSummary{}, err
		}
		if err := s.runtime.removeTaskAttachmentFiles(task.ID); err != nil {
			return agxdiscord.ProjectSummary{}, err
		}
		s.runtime.deleteDiscordChannelForTaskAsync(task, "")
	}
	if err := s.runtime.managerForProject(project).StopProject(project); err != nil {
		return agxdiscord.ProjectSummary{}, err
	}
	if err := s.runtime.store.DeleteProject(project.ID); err != nil {
		return agxdiscord.ProjectSummary{}, err
	}
	s.runtime.bus.Publish("project.deleted", map[string]string{"id": project.ID})
	logRuntimeOperation("discord_project_delete",
		"project", shortDiagnosticID(project.ID),
		"path", project.Path,
		"tasks", len(tasks),
	)
	return agxdiscord.ProjectSummary{ID: project.ID, Name: project.Name, Path: project.Path}, nil
}

func (s discordCommandService) CreateTask(ctx context.Context, projectRef, title, prompt, agentName, workspaceModeRaw string, allMighty bool) (agxdiscord.TaskSummary, error) {
	project, err := s.resolveProject(projectRef)
	if err != nil {
		return agxdiscord.TaskSummary{}, err
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return agxdiscord.TaskSummary{}, fmt.Errorf("task title is required")
	}
	workspaceMode, err := parseWorkspaceMode(workspaceModeRaw)
	if err != nil {
		return agxdiscord.TaskSummary{}, err
	}
	projectLock := s.runtime.taskLock("project:" + project.ID)
	projectLock.Lock()
	defer projectLock.Unlock()
	if err := s.runtime.ensureProjectWorkspaceAvailable(project.ID, workspaceMode); err != nil {
		return agxdiscord.TaskSummary{}, err
	}
	agentName = strings.TrimSpace(agentName)
	if agentName == "" {
		registry := agent.RegistryForProject(project.Path)
		agentName = registry.DefaultName()
	}
	var description *string
	if strings.TrimSpace(prompt) != "" {
		trimmed := strings.TrimSpace(prompt)
		description = &trimmed
	}
	req := createTaskRequest{
		ProjectID:      project.ID,
		Title:          title,
		Description:    description,
		Agent:          agentName,
		AllMighty:      allMighty,
		WorkspaceMode:  string(workspaceMode),
		RunImmediately: true,
		Discord:        true,
	}
	var task db.Task
	if isStructuredAgentName(agentName) {
		task, err = s.runtime.createStructuredDiscordTaskQueued(project, req, agentName)
		if err != nil {
			return agxdiscord.TaskSummary{}, err
		}
	} else {
		manager := s.runtime.managerForProject(project)
		task, err = manager.RunNewTaskWithOptions(project, req.Title, req.Description, agentName, session.RunOptions{AllMighty: req.AllMighty, WorkspaceMode: workspaceMode})
		if err != nil {
			return agxdiscord.TaskSummary{}, err
		}
		if err := s.runtime.store.UpdateTaskInterface(task.ID, db.TaskInterfaceDiscord); err != nil {
			return agxdiscord.TaskSummary{}, err
		}
		if err := s.markTaskLogStream(task.ID); err != nil {
			return agxdiscord.TaskSummary{}, err
		}
		s.runtime.syncDiscordTaskBestEffort(task.ID)
		task, err = s.runtime.store.GetTask(task.ID)
		if err != nil {
			return agxdiscord.TaskSummary{}, err
		}
	}
	dto := s.runtime.taskDTO(task)
	s.runtime.bus.Publish("task.changed", dto)
	logRuntimeOperation("discord_task_create",
		"task", shortDiagnosticID(task.ID),
		"project", shortDiagnosticID(project.ID),
		"workspace_mode", workspaceMode,
		"agent", agentName,
		"all_mighty", allMighty,
	)
	return s.taskSummary(task, project.Name), nil
}

func (s discordCommandService) DeleteTask(ctx context.Context, taskRef string) (agxdiscord.TaskSummary, error) {
	task, err := s.resolveTask(taskRef)
	if err != nil {
		return agxdiscord.TaskSummary{}, err
	}
	project, err := s.runtime.store.GetProject(task.ProjectID)
	if err != nil {
		return agxdiscord.TaskSummary{}, err
	}
	summary := s.taskSummary(task, project.Name)
	if err := s.deleteTask(ctx, task.ID, ""); err != nil {
		return summary, err
	}
	return summary, nil
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

func (s discordCommandService) ScheduleRuntimeRestart(ctx context.Context) error {
	manager := CurrentRuntimeServiceManager()
	status := manager.Status(ctx)
	if !isRuntimeServiceRestartable(status) {
		detail := strings.TrimSpace(status.Detail)
		if detail != "" {
			detail = ": " + detail
		}
		return fmt.Errorf("AGX runtime service is not active (%s=%s%s); start AGX with `agx runtime install-service` before using Discord restart", unknownIfEmpty(status.Manager), unknownIfEmpty(status.State), detail)
	}
	logRuntimeOperation("discord_runtime_restart", "status", "scheduled", "manager", status.Manager)
	go func() {
		time.Sleep(1500 * time.Millisecond)
		restartCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		message, err := manager.Restart(restartCtx)
		if err != nil {
			logRuntimeOperation("discord_runtime_restart", "status", "failed", "manager", status.Manager, "error", err)
			return
		}
		logRuntimeOperation("discord_runtime_restart", "status", "requested", "manager", status.Manager, "message", message)
	}()
	return nil
}

func (s discordCommandService) RuntimeDoctor(ctx context.Context) (agxdiscord.RuntimeDoctorSummary, error) {
	agxdiscord.ReportProgress(ctx, "AGX runtime doctor started.")
	defer agxdiscord.ReportProgress(ctx, "AGX runtime doctor finished.")
	manager := CurrentRuntimeServiceManager()
	agxdiscord.ReportProgress(ctx, "Step 1/4: checking runtime service manager.")
	serviceStatus := manager.Status(ctx)
	agxdiscord.ReportProgress(ctx, fmt.Sprintf("Step 1/4 complete: service manager `%s` is `%s`.", unknownIfEmpty(serviceStatus.Manager), unknownIfEmpty(serviceStatus.State)))
	before := s.runtime.discord.Status()
	summary := agxdiscord.RuntimeDoctorSummary{
		ServiceManager: serviceStatus.Manager,
		ServiceState:   serviceStatus.State,
		DiscordEnabled: before.Enabled,
		DiscordBefore:  before.Connected,
		DiscordAfter:   before.Connected,
		Checks: []string{
			"runtime API is reachable",
			fmt.Sprintf("service manager %s state %s", unknownIfEmpty(serviceStatus.Manager), unknownIfEmpty(serviceStatus.State)),
		},
	}
	if !isRuntimeServiceRestartable(serviceStatus) {
		summary.Warnings = append(summary.Warnings, "runtime service is not active; `/runtime restart` may not work until the service is installed and running")
		agxdiscord.ReportProgress(ctx, "Warning: runtime service is not active, so `/runtime restart` may not work.")
	}
	agxdiscord.ReportProgress(ctx, "Step 2/4: checking Discord bridge configuration.")
	if !before.Enabled {
		summary.Warnings = append(summary.Warnings, "Discord is disabled in AGX config")
		logRuntimeOperation("discord_runtime_doctor", "discord_enabled", false, "service_state", serviceStatus.State)
		agxdiscord.ReportProgress(ctx, "Step 2/4 complete: Discord is disabled in AGX config; no repair attempted.")
		return summary, nil
	}
	agxdiscord.ReportProgress(ctx, fmt.Sprintf("Step 2/4 complete: Discord is enabled, connected=%t.", before.Connected))
	agxdiscord.ReportProgress(ctx, "Step 3/4: checking Discord bridge connection.")
	if !before.Connected {
		agxdiscord.ReportProgress(ctx, "Step 3/4 repair: reconnecting Discord bridge.")
		if err := s.runtime.ensureDiscordStarted(false); err != nil {
			summary.Warnings = append(summary.Warnings, "Discord reconnect failed: "+err.Error())
			logRuntimeOperation("discord_runtime_doctor", "discord_reconnect", "failed", "error", err)
			agxdiscord.ReportProgress(ctx, "Step 3/4 failed: Discord reconnect did not complete.")
			return summary, nil
		}
		summary.Repairs = append(summary.Repairs, "reconnected Discord bridge")
		agxdiscord.ReportProgress(ctx, "Step 3/4 repaired: Discord bridge reconnected.")
	} else {
		agxdiscord.ReportProgress(ctx, "Step 3/4 complete: Discord bridge is already connected.")
	}
	afterStart := s.runtime.discord.Status()
	summary.DiscordAfter = afterStart.Connected
	agxdiscord.ReportProgress(ctx, "Step 4/4: checking Discord channel sync.")
	if afterStart.Connected {
		if err := s.runtime.discord.SoftSync(ctx); err != nil {
			if errors.Is(err, agxdiscord.ErrSyncInProgress) {
				summary.Checks = append(summary.Checks, "Discord sync already running")
				agxdiscord.ReportProgress(ctx, "Step 4/4 complete: Discord sync is already running.")
			} else {
				summary.Warnings = append(summary.Warnings, "Discord soft sync failed: "+err.Error())
				agxdiscord.ReportProgress(ctx, "Step 4/4 failed: Discord soft sync did not complete.")
			}
		} else {
			summary.Repairs = append(summary.Repairs, "ran Discord soft sync")
			agxdiscord.ReportProgress(ctx, "Step 4/4 repaired: Discord soft sync completed.")
		}
	} else {
		summary.Warnings = append(summary.Warnings, "Discord bridge is still disconnected; skipped channel sync")
		agxdiscord.ReportProgress(ctx, "Step 4/4 skipped: Discord bridge is still disconnected.")
	}
	final := s.runtime.discord.Status()
	summary.DiscordAfter = final.Connected
	s.runtime.bus.Publish("discord.status", final)
	logRuntimeOperation("discord_runtime_doctor",
		"service_manager", serviceStatus.Manager,
		"service_state", serviceStatus.State,
		"discord_enabled", final.Enabled,
		"discord_before", before.Connected,
		"discord_after", final.Connected,
		"repairs", len(summary.Repairs),
		"warnings", len(summary.Warnings),
	)
	return summary, nil
}

func isRuntimeServiceRestartable(status RuntimeServiceStatus) bool {
	switch strings.ToLower(strings.TrimSpace(status.State)) {
	case "active", "loaded":
		return true
	default:
		return false
	}
}

func unknownIfEmpty(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	return value
}

// ResetEverything deletes every AGX project and task (stopping their sessions
// and agents and removing their Discord channels) and then rebuilds managed
// Discord state from the now-empty project set, preserving #agx-control. It is
// the Discord equivalent of the desktop "reset everything" action, performed
// in-process so the runtime and bridge stay up.
func (s discordCommandService) ResetEverything(ctx context.Context) (agxdiscord.ResetSummary, error) {
	projects, err := s.runtime.store.ListProjects()
	if err != nil {
		return agxdiscord.ResetSummary{}, err
	}
	summary := agxdiscord.ResetSummary{Projects: len(projects)}
	for _, project := range projects {
		tasks, err := s.runtime.store.ListTasks(project.ID, nil)
		if err != nil {
			return summary, err
		}
		summary.Tasks += len(tasks)
		if _, err := s.DeleteProject(ctx, project.ID); err != nil {
			return summary, err
		}
	}
	// Rebuild managed Discord channels from the empty state so orphaned project
	// categories are cleared while #agx-control is preserved. Best-effort: the
	// store is already wiped even if the async rebuild has not finished.
	if err := s.runtime.startDiscordHardSync(""); err != nil {
		logRuntimeOperation("discord_reset_everything", "status", "hard_sync_skipped", "error", err)
	}
	logRuntimeOperation("discord_reset_everything",
		"projects", summary.Projects,
		"tasks", summary.Tasks,
	)
	return summary, nil
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

func (s discordCommandService) ResolveTask(ctx context.Context, ref string) (agxdiscord.TaskSummary, error) {
	task, err := s.resolveTaskRef(ref)
	if err != nil {
		return agxdiscord.TaskSummary{}, err
	}
	project, err := s.runtime.store.GetProject(task.ProjectID)
	if err != nil {
		return agxdiscord.TaskSummary{}, err
	}
	return s.taskSummary(task, project.Name), nil
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
		var cleanupErr session.TaskCleanupError
		if errors.As(err, &cleanupErr) {
			s.runtime.deleteDiscordChannelForTaskAsync(task, fallbackChannelID)
			s.runtime.bus.Publish("task.deleted", map[string]string{"id": task.ID, "projectId": task.ProjectID})
			logRuntimeOperation("discord_task_delete",
				"task", shortDiagnosticID(task.ID),
				"project", shortDiagnosticID(project.ID),
				"workspace_mode", task.WorkspaceMode,
				"agent", task.Agent,
				"interface", task.Interface,
				"partial_success", true,
				"error", cleanupErr,
			)
			return cleanupErr
		}
		return err
	}
	s.runtime.deleteDiscordChannelForTaskAsync(task, fallbackChannelID)
	s.runtime.bus.Publish("task.deleted", map[string]string{"id": task.ID, "projectId": task.ProjectID})
	logRuntimeOperation("discord_task_delete",
		"task", shortDiagnosticID(task.ID),
		"project", shortDiagnosticID(project.ID),
		"workspace_mode", task.WorkspaceMode,
		"agent", task.Agent,
		"interface", task.Interface,
	)
	return nil
}

func (s discordCommandService) TaskLogs(ctx context.Context, taskID string, lines int) (string, error) {
	task, project, err := s.runtime.taskAndProject(taskID)
	if err != nil {
		return "", err
	}
	if isStructuredStreamTask(task) {
		return s.runtime.structuredTaskTranscript(task, lines)
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

func (s discordCommandService) resolveProject(ref string) (db.Project, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return db.Project{}, fmt.Errorf("project is required")
	}
	projects, err := s.runtime.store.ListProjects()
	if err != nil {
		return db.Project{}, err
	}
	var matches []db.Project
	for _, project := range projects {
		if project.ID == ref || project.Name == ref || sameProjectPath(project.Path, ref) {
			matches = append(matches, project)
		}
	}
	switch len(matches) {
	case 0:
		return db.Project{}, db.ErrProjectNotFound
	case 1:
		return matches[0], nil
	default:
		return db.Project{}, db.AmbiguousProjectError{Ref: ref, Matches: matches}
	}
}

func (s discordCommandService) resolveTask(ref string) (db.Task, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return db.Task{}, fmt.Errorf("task is required")
	}
	return s.runtime.store.ResolveTask(ref)
}

// resolveTaskRef resolves a task by id, id prefix, or exact title (case
// insensitive). Id resolution wins; a title match is only attempted when the ref
// does not match any task id, so operators can reference a task by its channel
// name without needing to copy the id.
func (s discordCommandService) resolveTaskRef(ref string) (db.Task, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return db.Task{}, fmt.Errorf("task is required")
	}
	task, err := s.runtime.store.ResolveTask(ref)
	if err == nil {
		return task, nil
	}
	if !errors.Is(err, db.ErrTaskNotFound) && !errors.Is(err, db.ErrTaskIDTooShort) {
		return db.Task{}, err
	}
	return s.resolveTaskByTitle(ref)
}

func (s discordCommandService) resolveTaskByTitle(ref string) (db.Task, error) {
	projects, err := s.runtime.store.ListProjects()
	if err != nil {
		return db.Task{}, err
	}
	var matches []db.Task
	for _, project := range projects {
		tasks, err := s.runtime.store.ListTasks(project.ID, nil)
		if err != nil {
			return db.Task{}, err
		}
		for _, task := range tasks {
			if task.Interface != db.TaskInterfaceDiscord {
				continue
			}
			if strings.EqualFold(strings.TrimSpace(task.Title), ref) {
				matches = append(matches, task)
			}
		}
	}
	switch len(matches) {
	case 0:
		return db.Task{}, db.ErrTaskNotFound
	case 1:
		return matches[0], nil
	default:
		return db.Task{}, db.AmbiguousTaskError{Prefix: ref, Matches: matches}
	}
}

func sameProjectPath(a, b string) bool {
	aa, errA := filepath.Abs(a)
	if errA == nil {
		a = aa
	}
	bb, errB := filepath.Abs(b)
	if errB == nil {
		b = bb
	}
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}
