package runtime

import (
	"encoding/json"
	"net/http"
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

func mustJSON(value any) json.RawMessage {
	data, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage(`null`)
	}
	return data
}
