package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/nashory/agx/internal/agent"
	"github.com/nashory/agx/internal/config"
	"github.com/nashory/agx/internal/db"
	agxdiscord "github.com/nashory/agx/internal/discord"
	"github.com/nashory/agx/internal/session"
)

func (s *Service) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/status", s.handleStatus)
	mux.HandleFunc("POST /v1/shutdown", s.handleShutdown)
	mux.HandleFunc("GET /v1/events", s.handleEvents)
	mux.HandleFunc("GET /v1/agents", s.handleListAgents)
	mux.HandleFunc("GET /v1/projects", s.handleListProjects)
	mux.HandleFunc("POST /v1/projects", s.handleCreateProject)
	mux.HandleFunc("GET /v1/projects/{id}", s.handleGetProject)
	mux.HandleFunc("PATCH /v1/projects/{id}", s.handlePatchProject)
	mux.HandleFunc("POST /v1/projects/{id}/grant-access", s.handleGrantProjectAccess)
	mux.HandleFunc("DELETE /v1/projects/{id}", s.handleDeleteProject)
	mux.HandleFunc("GET /v1/tasks", s.handleListTasks)
	mux.HandleFunc("GET /v1/tasks/monitor", s.handleMonitorTasks)
	mux.HandleFunc("POST /v1/tasks", s.handleCreateTask)
	mux.HandleFunc("GET /v1/tasks/{id}", s.handleGetTask)
	mux.HandleFunc("PATCH /v1/tasks/{id}", s.handlePatchTask)
	mux.HandleFunc("POST /v1/tasks/{id}/run", s.handleRunTask)
	mux.HandleFunc("POST /v1/tasks/{id}/stop", s.handleStopTask)
	mux.HandleFunc("POST /v1/tasks/{id}/interrupt", s.handleInterruptTask)
	mux.HandleFunc("DELETE /v1/tasks/{id}", s.handleDeleteTask)
	mux.HandleFunc("POST /v1/tasks/{id}/message", s.handleSendTaskMessage)
	mux.HandleFunc("POST /v1/tasks/{id}/input", s.handleSendTaskInput)
	mux.HandleFunc("POST /v1/tasks/{id}/resize", s.handleResizeTask)
	mux.HandleFunc("GET /v1/tasks/{id}/logs", s.handleTaskLogs)
	mux.HandleFunc("GET /v1/tasks/{id}/stream", s.handleTaskLogStream)
	mux.HandleFunc("GET /v1/tasks/{id}/transcript", s.handleTaskTranscript)
	mux.HandleFunc("POST /v1/tasks/{id}/record-input", s.handleRecordTaskInput)
	mux.HandleFunc("GET /v1/discord/status", s.handleDiscordStatus)
	mux.HandleFunc("POST /v1/discord/connect", s.handleDiscordConnect)
	mux.HandleFunc("POST /v1/discord/disconnect", s.handleDiscordDisconnect)
	mux.HandleFunc("POST /v1/discord/soft-sync", s.handleDiscordSoftSync)
	mux.HandleFunc("POST /v1/discord/hard-sync", s.handleDiscordHardSync)
	mux.HandleFunc("POST /v1/discord/invite-url", s.handleDiscordInviteURL)
	return mux
}

// Project is the JSON representation of a registered repository returned by the
// runtime API.
type Project struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Path          string    `json:"path"`
	Description   *string   `json:"description,omitempty"`
	DefaultAgent  *string   `json:"defaultAgent,omitempty"`
	AccessGranted bool      `json:"accessGranted"`
	AccessError   *string   `json:"accessError,omitempty"`
	LastOpened    time.Time `json:"lastOpened"`
	CreatedAt     time.Time `json:"createdAt"`
}

// Task is the JSON representation of a persisted task returned by the runtime
// API.
type Task struct {
	ID               string        `json:"id"`
	ProjectID        string        `json:"projectId"`
	Title            string        `json:"title"`
	Description      *string       `json:"description,omitempty"`
	LastUserPrompt   *string       `json:"lastUserPrompt,omitempty"`
	Interface        string        `json:"interface"`
	Status           db.TaskStatus `json:"status"`
	Agent            string        `json:"agent"`
	AllMighty        bool          `json:"allMighty"`
	WorkspaceMode    string        `json:"workspaceMode"`
	SessionName      *string       `json:"sessionName,omitempty"`
	WorktreePath     *string       `json:"worktreePath,omitempty"`
	BranchName       *string       `json:"branchName,omitempty"`
	BaseBranch       *string       `json:"baseBranch,omitempty"`
	AgentThreadID    *string       `json:"agentThreadId,omitempty"`
	AgentEventCursor *string       `json:"agentEventCursor,omitempty"`
	AgentStreamKind  *string       `json:"agentStreamKind,omitempty"`
	CreatedAt        time.Time     `json:"createdAt"`
	UpdatedAt        time.Time     `json:"updatedAt"`
}

// MonitorTask augments a live task with project context for monitor clients.
type MonitorTask struct {
	Task
	ProjectName string `json:"projectName"`
	ProjectPath string `json:"projectPath"`
}

// TaskTranscriptMessage is a JSON transcript entry for structured tasks.
type TaskTranscriptMessage struct {
	ID        int64     `json:"id"`
	TaskID    string    `json:"taskId"`
	TurnID    *string   `json:"turnId,omitempty"`
	Role      string    `json:"role"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Agent is a JSON-safe view of an agent registry entry.
type Agent struct {
	Name        string `json:"name"`
	Command     string `json:"command"`
	Description string `json:"description"`
	Available   bool   `json:"available"`
}

type createProjectRequest struct {
	Path         string  `json:"path"`
	Name         string  `json:"name"`
	Description  *string `json:"description"`
	DefaultAgent *string `json:"defaultAgent"`
}

type patchProjectRequest struct {
	Name         *string `json:"name"`
	Description  *string `json:"description"`
	DefaultAgent *string `json:"defaultAgent"`
}

type createTaskRequest struct {
	ProjectID      string  `json:"projectId"`
	Title          string  `json:"title"`
	Description    *string `json:"description"`
	Agent          string  `json:"agent"`
	AllMighty      bool    `json:"allMighty"`
	WorkspaceMode  string  `json:"workspaceMode"`
	InitialPrompt  *string `json:"initialPrompt"`
	RunImmediately bool    `json:"runImmediately"`
	Discord        bool    `json:"discord"`
}

type patchTaskRequest struct {
	Title            *string  `json:"title"`
	Description      **string `json:"description"`
	ClearDescription bool     `json:"clearDescription"`
	Agent            *string  `json:"agent"`
}

type taskMessageRequest struct {
	Message string `json:"message"`
}

type taskInputRequest struct {
	Data string `json:"data"`
}

type taskResizeRequest struct {
	Cols int `json:"cols"`
	Rows int `json:"rows"`
}

type taskLogsResponse struct {
	Logs string `json:"logs"`
}

// TaskLogEvent is streamed as server-sent events for task log updates.
type TaskLogEvent struct {
	TaskID string `json:"taskId"`
	Data   string `json:"data,omitempty"`
	Reset  bool   `json:"reset"`
	Error  string `json:"error,omitempty"`
}

type discordConnectRequest struct {
	Token         string `json:"token"`
	GuildID       string `json:"guildId"`
	AllowedUserID string `json:"allowedUserId"`
}

type discordInviteRequest struct {
	Token string `json:"token"`
}

type discordInviteResponse struct {
	URL string `json:"url"`
}

func (s *Service) handleStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.Status())
}

func (s *Service) handleShutdown(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]bool{"shuttingDown": true})
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.Shutdown(ctx)
	}()
}

func (s *Service) handleListAgents(w http.ResponseWriter, r *http.Request) {
	projectPath := strings.TrimSpace(r.URL.Query().Get("project_path"))
	if projectID := strings.TrimSpace(r.URL.Query().Get("project_id")); projectID != "" {
		project, err := s.store.GetProject(projectID)
		if err != nil {
			writeError(w, err)
			return
		}
		projectPath = project.Path
	}
	registry := agent.RegistryForProject(projectPath)
	agents := registry.All()
	out := make([]Agent, 0, len(agents))
	for _, ag := range agents {
		out = append(out, Agent{
			Name:        ag.Name,
			Command:     ag.Command,
			Description: ag.Description,
			Available:   ag.IsAvailable(),
		})
	}
	writeJSON(w, out)
}

func (s *Service) handleListProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := s.store.ListProjects()
	if err != nil {
		writeError(w, err)
		return
	}
	out := make([]Project, 0, len(projects))
	for _, project := range projects {
		out = append(out, s.projectDTO(project))
	}
	writeJSON(w, out)
}

func (s *Service) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	var req createProjectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorStatus(w, http.StatusBadRequest, fmt.Errorf("decode project request: %w", err))
		return
	}
	if strings.TrimSpace(req.Path) == "" {
		writeErrorStatus(w, http.StatusBadRequest, fmt.Errorf("project path is required"))
		return
	}
	project, err := s.store.EnsureProjectDetails(req.Path, req.Name, req.Description, req.DefaultAgent)
	if err != nil {
		writeError(w, err)
		return
	}
	dto := s.projectDTO(project)
	s.bus.Publish("project.changed", dto)
	writeJSON(w, dto)
}

func (s *Service) handleGetProject(w http.ResponseWriter, r *http.Request) {
	project, err := s.store.GetProject(r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, s.projectDTO(project))
}

func (s *Service) handlePatchProject(w http.ResponseWriter, r *http.Request) {
	project, err := s.store.GetProject(r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	var req patchProjectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorStatus(w, http.StatusBadRequest, fmt.Errorf("decode project patch request: %w", err))
		return
	}
	if req.Name != nil || req.Description != nil {
		name := project.Name
		if req.Name != nil {
			name = *req.Name
		}
		description := project.Description
		if req.Description != nil {
			description = req.Description
		}
		if err := s.store.UpdateProjectDetails(project.ID, name, description); err != nil {
			writeError(w, err)
			return
		}
	}
	if req.DefaultAgent != nil {
		if err := s.store.UpdateProjectDefaultAgent(project.ID, req.DefaultAgent); err != nil {
			writeError(w, err)
			return
		}
	}
	refreshed, err := s.store.GetProject(project.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	dto := s.projectDTO(refreshed)
	s.bus.Publish("project.changed", dto)
	writeJSON(w, dto)
}

func (s *Service) handleGrantProjectAccess(w http.ResponseWriter, r *http.Request) {
	project, err := s.store.GetProject(r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	if err := validateOrRepairProjectAccess(project.Path); err != nil {
		writeError(w, err)
		return
	}
	if err := s.store.MarkProjectAccessGranted(project.Path); err != nil {
		writeError(w, err)
		return
	}
	dto := s.projectDTO(project)
	s.bus.Publish("project.changed", dto)
	writeJSON(w, dto)
}

func (s *Service) handleDeleteProject(w http.ResponseWriter, r *http.Request) {
	project, err := s.store.GetProject(r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	tasks, err := s.store.ListTasks(project.ID, nil)
	if err != nil {
		writeError(w, err)
		return
	}
	for _, task := range tasks {
		if err := s.stopStructuredTaskForDelete(r.Context(), task); err != nil {
			writeError(w, err)
			return
		}
		if err := s.removeTaskAttachmentFiles(task.ID); err != nil {
			writeError(w, err)
			return
		}
		s.deleteDiscordChannelForTaskAsync(task, "")
	}
	if err := s.managerForProject(project).StopProject(project); err != nil {
		writeError(w, err)
		return
	}
	if err := s.store.DeleteProject(project.ID); err != nil {
		writeError(w, err)
		return
	}
	s.bus.Publish("project.deleted", map[string]string{"id": project.ID})
	writeJSON(w, map[string]bool{"deleted": true})
}

func (s *Service) handleListTasks(w http.ResponseWriter, r *http.Request) {
	var status *db.TaskStatus
	if raw := strings.TrimSpace(r.URL.Query().Get("status")); raw != "" {
		parsed, err := db.ParseTaskStatus(raw)
		if err != nil {
			writeErrorStatus(w, http.StatusBadRequest, err)
			return
		}
		status = &parsed
	}
	tasks, err := s.store.ListTasks(r.URL.Query().Get("project_id"), status)
	if err != nil {
		writeError(w, err)
		return
	}
	out := make([]Task, 0, len(tasks))
	for _, task := range tasks {
		out = append(out, taskDTO(task))
	}
	writeJSON(w, out)
}

func (s *Service) handleMonitorTasks(w http.ResponseWriter, r *http.Request) {
	tasks, err := s.store.ListLiveTasks()
	if err != nil {
		writeError(w, err)
		return
	}
	out := make([]MonitorTask, 0, len(tasks))
	for _, task := range tasks {
		refreshed := s.refreshTaskStatuses([]db.Task{task.Task})[0]
		if !isRuntimeLiveTask(refreshed) {
			continue
		}
		out = append(out, MonitorTask{
			Task:        taskDTO(refreshed),
			ProjectName: task.ProjectName,
			ProjectPath: task.ProjectPath,
		})
	}
	writeJSON(w, out)
}

func (s *Service) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	var req createTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorStatus(w, http.StatusBadRequest, fmt.Errorf("decode task request: %w", err))
		return
	}
	if strings.TrimSpace(req.ProjectID) == "" {
		writeErrorStatus(w, http.StatusBadRequest, fmt.Errorf("project id is required"))
		return
	}
	if strings.TrimSpace(req.Title) == "" {
		writeErrorStatus(w, http.StatusBadRequest, fmt.Errorf("task title is required"))
		return
	}
	if req.Discord && !s.discord.Status().Connected {
		writeErrorStatus(w, http.StatusBadRequest, fmt.Errorf("Discord is not connected"))
		return
	}
	workspaceMode, err := parseWorkspaceMode(req.WorkspaceMode)
	if err != nil {
		writeErrorStatus(w, http.StatusBadRequest, err)
		return
	}
	project, err := s.store.GetProject(req.ProjectID)
	if err != nil {
		writeError(w, err)
		return
	}
	projectLock := s.taskLock("project:" + project.ID)
	projectLock.Lock()
	defer projectLock.Unlock()
	if err := s.ensureProjectWorkspaceAvailable(project.ID, workspaceMode); err != nil {
		writeErrorStatus(w, http.StatusConflict, err)
		return
	}
	req.WorkspaceMode = string(workspaceMode)
	agentName := strings.TrimSpace(req.Agent)
	if agentName == "" {
		registry := agent.RegistryForProject(project.Path)
		agentName = registry.DefaultName()
	}
	status := db.StatusOffline
	if req.RunImmediately {
		if req.Discord && isStructuredAgentName(agentName) {
			task, err := s.createStructuredDiscordTask(r.Context(), project, req, agentName)
			if err != nil {
				writeError(w, err)
				return
			}
			dto := taskDTO(task)
			s.bus.Publish("task.changed", dto)
			writeJSON(w, dto)
			return
		}
		manager := s.managerForProject(project)
		task, err := manager.RunNewTaskWithOptions(project, req.Title, req.Description, agentName, session.RunOptions{AllMighty: req.AllMighty, InitialPrompt: req.InitialPrompt, WorkspaceMode: workspaceMode})
		if err != nil {
			writeError(w, err)
			return
		}
		if req.Discord {
			if err := s.store.UpdateTaskInterface(task.ID, db.TaskInterfaceDiscord); err != nil {
				writeError(w, err)
				return
			}
			if err := (discordCommandService{runtime: s}).markTaskLogStream(task.ID); err != nil {
				writeError(w, err)
				return
			}
			s.syncDiscordTaskBestEffort(task.ID)
			task, err = s.store.GetTask(task.ID)
			if err != nil {
				writeError(w, err)
				return
			}
		}
		dto := taskDTO(task)
		s.bus.Publish("task.changed", dto)
		writeJSON(w, dto)
		return
	}
	iface := db.TaskInterfaceLocal
	if req.Discord {
		iface = db.TaskInterfaceDiscord
	}
	task, err := s.store.CreateTaskRuntimeModeInterfaceWorkspace(db.NewTaskID(), project.ID, req.Title, req.Description, agentName, req.AllMighty, iface, workspaceMode, status, nil, nil, nil)
	if err != nil {
		writeError(w, err)
		return
	}
	if req.Discord {
		if err := (discordCommandService{runtime: s}).markTaskLogStream(task.ID); err != nil {
			writeError(w, err)
			return
		}
		task, err = s.store.GetTask(task.ID)
		if err != nil {
			writeError(w, err)
			return
		}
	}
	dto := taskDTO(task)
	s.bus.Publish("task.changed", dto)
	writeJSON(w, dto)
}

func (s *Service) handleGetTask(w http.ResponseWriter, r *http.Request) {
	task, err := s.store.GetTask(r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	s.detectAndStoreStatus(task)
	refreshed, err := s.store.GetTask(task.ID)
	if err == nil {
		task = refreshed
	}
	writeJSON(w, taskDTO(task))
}

func (s *Service) handlePatchTask(w http.ResponseWriter, r *http.Request) {
	task, err := s.store.GetTask(r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	var req patchTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorStatus(w, http.StatusBadRequest, fmt.Errorf("decode task patch request: %w", err))
		return
	}
	if req.Title != nil {
		title := strings.TrimSpace(*req.Title)
		if title == "" {
			writeErrorStatus(w, http.StatusBadRequest, fmt.Errorf("task title is required"))
			return
		}
		req.Title = &title
	}
	description := req.Description
	if req.ClearDescription {
		var empty *string
		description = &empty
	}
	if err := s.store.UpdateTask(task.ID, req.Title, description, req.Agent); err != nil {
		writeError(w, err)
		return
	}
	updated, err := s.store.GetTask(task.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	dto := taskDTO(updated)
	s.bus.Publish("task.changed", dto)
	writeJSON(w, dto)
}

func (s *Service) handleRunTask(w http.ResponseWriter, r *http.Request) {
	task, project, err := s.taskAndProject(r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	lock := s.taskLock(task.ID)
	lock.Lock()
	defer lock.Unlock()
	if err := s.managerForProject(project).RunTask(task); err != nil {
		writeError(w, err)
		return
	}
	refreshed, err := s.store.GetTask(task.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	dto := taskDTO(refreshed)
	s.bus.Publish("task.changed", dto)
	writeJSON(w, dto)
}

func (s *Service) handleStopTask(w http.ResponseWriter, r *http.Request) {
	task, project, err := s.taskAndProject(r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	if isRuntimeStructuredDBTask(task) {
		if err := s.agents.StopTask(r.Context(), task); err != nil {
			writeError(w, err)
			return
		}
		if err := s.store.UpdateTaskStatus(task.ID, db.StatusOffline); err != nil {
			writeError(w, err)
			return
		}
	} else {
		lock := s.taskLock(task.ID)
		lock.Lock()
		defer lock.Unlock()
		if err := s.managerForProject(project).StopTask(task); err != nil {
			writeError(w, err)
			return
		}
	}
	refreshed, err := s.store.GetTask(task.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	dto := taskDTO(refreshed)
	s.bus.Publish("task.changed", dto)
	s.deleteDiscordChannelForTaskAsync(task, "")
	writeJSON(w, dto)
}

func (s *Service) handleInterruptTask(w http.ResponseWriter, r *http.Request) {
	task, project, err := s.taskAndProject(r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	lock := s.taskLock(task.ID)
	lock.Lock()
	defer lock.Unlock()
	if isRuntimeStructuredDBTask(task) {
		if err := s.agents.InterruptTask(r.Context(), task); err != nil {
			writeError(w, err)
			return
		}
	} else {
		if err := s.managerForProject(project).InterruptTask(task); err != nil {
			writeError(w, err)
			return
		}
	}
	task.Status = db.StatusWaiting
	if err := s.store.UpdateTaskStatus(task.ID, db.StatusWaiting); err != nil {
		writeError(w, err)
		return
	}
	dto := taskDTO(task)
	s.bus.Publish("task.changed", dto)
	writeJSON(w, dto)
}

func (s *Service) handleDeleteTask(w http.ResponseWriter, r *http.Request) {
	task, project, err := s.taskAndProject(r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	lock := s.taskLock(task.ID)
	lock.Lock()
	defer lock.Unlock()
	if err := s.stopStructuredTaskForDelete(r.Context(), task); err != nil {
		writeError(w, err)
		return
	}
	if err := s.removeTaskAttachmentFiles(task.ID); err != nil {
		writeError(w, err)
		return
	}
	if err := s.managerForProject(project).DeleteTask(task); err != nil {
		writeError(w, err)
		return
	}
	s.bus.Publish("task.deleted", map[string]string{"id": task.ID, "projectId": task.ProjectID})
	s.deleteDiscordChannelForTaskAsync(task, "")
	writeJSON(w, map[string]bool{"deleted": true})
}

func (s *Service) handleSendTaskMessage(w http.ResponseWriter, r *http.Request) {
	task, project, err := s.taskAndProject(r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	var req taskMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorStatus(w, http.StatusBadRequest, fmt.Errorf("decode message request: %w", err))
		return
	}
	if isRuntimeStructuredDBTask(task) {
		if err := s.agents.SendTaskMessage(r.Context(), task, project, req.Message); err != nil {
			writeError(w, err)
			return
		}
		if !isAgentContextClearCommand(req.Message) {
			_ = s.store.AppendTaskTranscriptMessage(task.ID, "user", req.Message, nil, nil)
			_ = s.store.UpdateTaskLastUserPrompt(task.ID, req.Message)
		}
	} else {
		lock := s.taskLock(task.ID)
		lock.Lock()
		defer lock.Unlock()
		if err := s.managerForProject(project).SendMessage(task, req.Message); err != nil {
			writeError(w, err)
			return
		}
	}
	refreshed, err := s.store.GetTask(task.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	dto := taskDTO(refreshed)
	s.bus.Publish("task.changed", dto)
	writeJSON(w, dto)
}

func (s *Service) handleSendTaskInput(w http.ResponseWriter, r *http.Request) {
	task, project, err := s.taskAndProject(r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	var req taskInputRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorStatus(w, http.StatusBadRequest, fmt.Errorf("decode input request: %w", err))
		return
	}
	lock := s.taskLock(task.ID)
	lock.Lock()
	defer lock.Unlock()
	if err := s.managerForProject(project).SendInput(task, req.Data); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, map[string]bool{"sent": true})
}

func (s *Service) handleResizeTask(w http.ResponseWriter, r *http.Request) {
	task, project, err := s.taskAndProject(r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	var req taskResizeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorStatus(w, http.StatusBadRequest, fmt.Errorf("decode resize request: %w", err))
		return
	}
	lock := s.taskLock(task.ID)
	lock.Lock()
	defer lock.Unlock()
	if err := s.managerForProject(project).ResizeTaskTerminal(task, req.Cols, req.Rows); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, map[string]bool{"resized": true})
}

func (s *Service) handleTaskLogs(w http.ResponseWriter, r *http.Request) {
	task, project, err := s.taskAndProject(r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	lines := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("lines")); raw != "" {
		if _, err := fmt.Sscanf(raw, "%d", &lines); err != nil {
			writeErrorStatus(w, http.StatusBadRequest, fmt.Errorf("invalid lines value %q", raw))
			return
		}
	}
	logs, err := s.managerForProject(project).GetLogs(task, lines)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, taskLogsResponse{Logs: logs})
}

func (s *Service) handleTaskLogStream(w http.ResponseWriter, r *http.Request) {
	task, project, err := s.taskAndProject(r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	lines := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("lines")); raw != "" {
		if _, err := fmt.Sscanf(raw, "%d", &lines); err != nil {
			writeErrorStatus(w, http.StatusBadRequest, fmt.Errorf("invalid lines value %q", raw))
			return
		}
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	send := func(event TaskLogEvent) bool {
		data, err := json.Marshal(event)
		if err != nil {
			return true
		}
		if _, err := fmt.Fprintf(w, "event: task.log\ndata: %s\n\n", data); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}
	logs, err := s.managerForProject(project).GetLogs(task, lines)
	if err != nil {
		_ = send(TaskLogEvent{TaskID: task.ID, Reset: false, Error: err.Error()})
		return
	}
	if !send(TaskLogEvent{TaskID: task.ID, Data: logs, Reset: true}) {
		return
	}
	last := logs
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
		refreshed, err := s.store.GetTask(task.ID)
		if err == nil {
			task = refreshed
		}
		logs, err := s.managerForProject(project).GetLogs(task, lines)
		if err != nil {
			if !send(TaskLogEvent{TaskID: task.ID, Reset: false, Error: err.Error()}) {
				return
			}
			continue
		}
		if logs == last {
			continue
		}
		data := logs
		reset := true
		if strings.HasPrefix(logs, last) {
			data = strings.TrimPrefix(logs, last)
			reset = false
		}
		last = logs
		if data != "" && !send(TaskLogEvent{TaskID: task.ID, Data: data, Reset: reset}) {
			return
		}
	}
}

func (s *Service) handleTaskTranscript(w http.ResponseWriter, r *http.Request) {
	limit := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if _, err := fmt.Sscanf(raw, "%d", &limit); err != nil {
			writeErrorStatus(w, http.StatusBadRequest, fmt.Errorf("invalid limit value %q", raw))
			return
		}
	}
	messages, err := s.store.ListTaskTranscriptMessages(r.PathValue("id"), limit)
	if err != nil {
		writeError(w, err)
		return
	}
	out := make([]TaskTranscriptMessage, 0, len(messages))
	for _, message := range messages {
		out = append(out, transcriptDTO(message))
	}
	writeJSON(w, out)
}

func (s *Service) handleRecordTaskInput(w http.ResponseWriter, r *http.Request) {
	task, err := s.store.GetTask(r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	var req taskMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorStatus(w, http.StatusBadRequest, fmt.Errorf("decode record input request: %w", err))
		return
	}
	if err := s.store.UpdateTaskLastUserPrompt(task.ID, req.Message); err != nil {
		writeError(w, err)
		return
	}
	updated, err := s.store.GetTask(task.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	dto := taskDTO(updated)
	s.bus.Publish("task.changed", dto)
	writeJSON(w, dto)
}

func (s *Service) handleDiscordStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.discordStatus())
}

func (s *Service) handleDiscordConnect(w http.ResponseWriter, r *http.Request) {
	var req discordConnectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorStatus(w, http.StatusBadRequest, fmt.Errorf("decode discord connect request: %w", err))
		return
	}
	cfg, warnings := config.LoadGlobal()
	if len(warnings) > 0 {
		writeError(w, warnings[0])
		return
	}
	next := mergedDiscordConnectConfig(req, cfg.Discord)
	if err := agxdiscord.ValidateConfig(next); err != nil {
		writeErrorStatus(w, http.StatusBadRequest, err)
		return
	}
	if err := config.SaveDiscord(next); err != nil {
		writeError(w, err)
		return
	}
	s.discord.Configure(next)
	s.discord.SetStore(s.store)
	if err := s.discord.Start(r.Context(), "runtime"); err != nil {
		writeError(w, err)
		return
	}
	status := s.discord.Status()
	s.bus.Publish("discord.status", status)
	writeJSON(w, status)
}

func (s *Service) handleDiscordDisconnect(w http.ResponseWriter, r *http.Request) {
	cfg, warnings := config.LoadGlobal()
	if len(warnings) > 0 {
		writeError(w, warnings[0])
		return
	}
	cfg.Discord.Enabled = false
	cfg.Discord.BotToken = ""
	if err := config.SaveDiscord(cfg.Discord); err != nil {
		writeError(w, err)
		return
	}
	if err := s.discord.Stop(); err != nil {
		writeError(w, err)
		return
	}
	s.discord.Configure(cfg.Discord)
	status := s.discord.Status()
	s.bus.Publish("discord.status", status)
	writeJSON(w, status)
}

func (s *Service) handleDiscordSoftSync(w http.ResponseWriter, r *http.Request) {
	if !s.discord.Status().Connected {
		cfg, _ := config.LoadGlobal()
		s.discord.Configure(cfg.Discord)
		s.discord.SetStore(s.store)
		if err := s.discord.Start(r.Context(), "runtime"); err != nil {
			writeError(w, err)
			return
		}
	}
	if err := s.discord.SoftSync(r.Context()); err != nil {
		writeError(w, err)
		return
	}
	status := s.discord.Status()
	s.bus.Publish("discord.status", status)
	writeJSON(w, status)
}

func (s *Service) handleDiscordHardSync(w http.ResponseWriter, r *http.Request) {
	if err := s.startDiscordHardSync(""); err != nil {
		writeError(w, err)
		return
	}
	status := s.discordStatus()
	s.bus.Publish("discord.status", status)
	writeJSON(w, status)
}

func (s *Service) handleDiscordInviteURL(w http.ResponseWriter, r *http.Request) {
	var req discordInviteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorStatus(w, http.StatusBadRequest, fmt.Errorf("decode discord invite request: %w", err))
		return
	}
	token := strings.TrimSpace(req.Token)
	if token == "" {
		cfg, _ := config.LoadGlobal()
		if cfg.Discord.Enabled {
			token = cfg.Discord.BotToken
		}
	}
	clientID, err := agxdiscord.BotApplicationID(token)
	if err != nil {
		writeError(w, err)
		return
	}
	url, err := agxdiscord.InviteURL(clientID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, discordInviteResponse{URL: url})
}

func mergedDiscordConnectConfig(req discordConnectRequest, current config.DiscordConfig) config.DiscordConfig {
	token := strings.TrimSpace(req.Token)
	if token == "" && current.Enabled {
		token = strings.TrimSpace(current.BotToken)
	}
	guildID := strings.TrimSpace(req.GuildID)
	if guildID == "" {
		guildID = strings.TrimSpace(current.GuildID)
	}
	allowedUserID := strings.TrimSpace(req.AllowedUserID)
	allowedUsers := current.AllowedUserIDs
	if allowedUserID != "" {
		allowedUsers = []string{allowedUserID}
	}
	return config.DiscordConfig{
		Enabled:        true,
		BotToken:       token,
		GuildID:        guildID,
		AllowedUserIDs: cleanDiscordAllowedUsers(allowedUsers),
	}
}

func cleanDiscordAllowedUsers(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func (s *Service) handleEvents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	events, cancel := s.bus.Subscribe()
	defer cancel()
	fmt.Fprintf(w, "event: runtime.status\n")
	fmt.Fprintf(w, "id: 0000000000000000\n")
	data, _ := json.Marshal(Event{ID: "0000000000000000", Type: "runtime.status", Timestamp: s.started, Seq: 0, Payload: mustJSON(s.Status())})
	fmt.Fprintf(w, "data: %s\n\n", data)
	flusher.Flush()
	for {
		select {
		case <-r.Context().Done():
			return
		case event := <-events:
			data, err := json.Marshal(event)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: %s\n", event.Type)
			fmt.Fprintf(w, "id: %s\n", event.ID)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func writeError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	if err == db.ErrProjectNotFound || err == db.ErrTaskNotFound {
		status = http.StatusNotFound
	}
	writeErrorStatus(w, status, err)
}

func writeErrorStatus(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

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
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = s.discord.DeleteTaskChannelWithFallback(ctx, taskID, fallbackChannelID)
		s.bus.Publish("discord.status", s.discord.Status())
	}()
}

func mustJSON(value any) json.RawMessage {
	data, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage(`null`)
	}
	return data
}

func (s *Service) projectDTO(project db.Project) Project {
	dto := Project{
		ID:           project.ID,
		Name:         project.Name,
		Path:         project.Path,
		Description:  project.Description,
		DefaultAgent: project.DefaultAgent,
		LastOpened:   project.LastOpened,
		CreatedAt:    project.CreatedAt,
	}
	granted, err := s.store.HasProjectAccessGrant(project.Path)
	if err != nil {
		message := err.Error()
		dto.AccessError = &message
		return dto
	}
	if !granted {
		message := "Grant access before creating tasks so AGX can create Git worktrees."
		dto.AccessError = &message
		return dto
	}
	dto.AccessGranted = true
	return dto
}

func taskDTO(task db.Task) Task {
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
