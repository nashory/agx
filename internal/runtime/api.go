package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/nashory/agx/internal/agent"
	"github.com/nashory/agx/internal/db"
	"github.com/nashory/agx/internal/display"
	"github.com/nashory/agx/internal/session"
)

func (s *Service) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/status", s.handleStatus)
	mux.HandleFunc("POST /v1/shutdown", s.handleShutdown)
	mux.HandleFunc("GET /v1/events", s.handleEvents)
	mux.HandleFunc("GET /v1/config", s.handleGetConfig)
	mux.HandleFunc("PATCH /v1/config", s.handlePatchConfig)
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
	mux.HandleFunc("POST /v1/discord/tasks/{id}/sync", s.handleDiscordTaskSync)
	mux.HandleFunc("POST /v1/discord/invite-url", s.handleDiscordInviteURL)
	return mux
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
		out = append(out, s.taskDTO(task))
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
			Task:        s.taskDTO(refreshed),
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
			dto := s.taskDTO(task)
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
		dto := s.taskDTO(task)
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
	dto := s.taskDTO(task)
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
	writeJSON(w, s.taskDTO(task))
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
	dto := s.taskDTO(updated)
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
	dto := s.taskDTO(refreshed)
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
	dto := s.taskDTO(refreshed)
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
	dto := s.taskDTO(task)
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
		var cleanupErr session.TaskCleanupError
		if errors.As(err, &cleanupErr) {
			s.bus.Publish("task.deleted", map[string]string{"id": task.ID, "projectId": task.ProjectID})
			s.deleteDiscordChannelForTaskAsync(task, "")
			writeErrorStatus(w, http.StatusInternalServerError, cleanupErr)
			return
		}
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
	dto := s.taskDTO(refreshed)
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
	dto := s.taskDTO(updated)
	s.bus.Publish("task.changed", dto)
	writeJSON(w, dto)
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
		ctx, cancel := s.backgroundTimeout(15 * time.Second)
		defer cancel()
		if err := s.discord.DeleteTaskChannelWithFallback(ctx, taskID, fallbackChannelID); err != nil {
			log.Printf("operation=%q task=%s error=%v", "discord_task_channel_cleanup", display.ShortID(taskID), err)
		}
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
