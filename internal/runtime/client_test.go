package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nashory/agx/internal/db"
)

func TestClientIncludesRuntimeErrorBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeErrorStatus(w, http.StatusBadRequest, errTestClient("task title is required"))
	}))
	defer server.Close()

	client := &Client{baseURL: server.URL, http: server.Client()}
	err := client.do(context.Background(), http.MethodPost, "/v1/tasks", nil, nil)
	if err == nil {
		t.Fatal("client.do error = nil, want runtime error")
	}
	if !strings.Contains(err.Error(), "400 Bad Request") || !strings.Contains(err.Error(), "task title is required") {
		t.Fatalf("client.do error = %q, want status and response body", err.Error())
	}
	var runtimeErr *RuntimeError
	if !errors.As(err, &runtimeErr) {
		t.Fatalf("client.do error type = %T, want RuntimeError", err)
	}
	if runtimeErr.Code != errorCodeValidation || runtimeErr.StatusCode != http.StatusBadRequest || runtimeErr.Retryable || runtimeErr.PartialSuccess {
		t.Fatalf("RuntimeError = %#v, want structured validation details", runtimeErr)
	}
}

func TestClientParsesTaskLogStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: task.log\n")
		fmt.Fprint(w, `data: {"taskId":"task-1","data":"hello","reset":true}`+"\n\n")
	}))
	defer server.Close()

	client := &Client{baseURL: server.URL, http: server.Client()}
	events, err := client.TaskLogStream(context.Background(), "task-1", 20)
	if err != nil {
		t.Fatal(err)
	}
	event, ok := <-events
	if !ok {
		t.Fatal("task log stream closed without event")
	}
	if event.TaskID != "task-1" || event.Data != "hello" || !event.Reset {
		t.Fatalf("event = %#v, want parsed task log event", event)
	}
}

func TestClientParsesLargeTaskLogStreamEvent(t *testing.T) {
	large := strings.Repeat("x", 5*1024*1024)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		data, _ := json.Marshal(TaskLogEvent{TaskID: "task-1", Data: large, Reset: true})
		fmt.Fprintf(w, "event: task.log\ndata: %s\n\n", data)
	}))
	defer server.Close()

	client := &Client{baseURL: server.URL, http: server.Client()}
	events, err := client.TaskLogStream(context.Background(), "task-1", 20)
	if err != nil {
		t.Fatal(err)
	}
	event, ok := <-events
	if !ok {
		t.Fatal("task log stream closed without event")
	}
	if event.Data != large {
		t.Fatalf("event data length = %d, want %d", len(event.Data), len(large))
	}
}

func TestClientMethodsUseRuntimeRoutes(t *testing.T) {
	requests := make([]string, 0)
	bodies := map[string]string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uri := r.URL.RequestURI()
		requests = append(requests, r.Method+" "+uri)
		if r.Body != nil {
			data, _ := io.ReadAll(r.Body)
			bodies[r.Method+" "+uri] = string(data)
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v1/status":
			_ = json.NewEncoder(w).Encode(Status{Running: true, Version: "test"})
		case r.URL.Path == "/v1/config":
			_ = json.NewEncoder(w).Encode(RuntimeConfig{DefaultAgent: "codex"})
		case r.URL.Path == "/v1/agents":
			_ = json.NewEncoder(w).Encode([]Agent{{Name: "codex", Command: "codex", Available: true}})
		case r.URL.Path == "/v1/projects" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode([]Project{{ID: "project-1", Name: "Project"}})
		case r.URL.Path == "/v1/projects" && r.Method == http.MethodPost:
			_ = json.NewEncoder(w).Encode(Project{ID: "project-1", Name: "Project"})
		case strings.HasPrefix(r.URL.Path, "/v1/projects/") && r.Method == http.MethodDelete:
			_ = json.NewEncoder(w).Encode(map[string]bool{"deleted": true})
		case strings.HasPrefix(r.URL.Path, "/v1/projects/"):
			_ = json.NewEncoder(w).Encode(Project{ID: "project-1", Name: "Project"})
		case r.URL.Path == "/v1/tasks/monitor":
			_ = json.NewEncoder(w).Encode([]MonitorTask{{Task: Task{ID: "task-1", ProjectID: "project-1", Status: db.StatusActive}, ProjectName: "Project"}})
		case r.URL.Path == "/v1/tasks" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode([]Task{{ID: "task-1", ProjectID: "project-1", Status: db.StatusActive}})
		case r.URL.Path == "/v1/tasks" && r.Method == http.MethodPost:
			_ = json.NewEncoder(w).Encode(Task{ID: "task-1", ProjectID: "project-1", Title: "Task", Status: db.StatusOffline})
		case strings.HasSuffix(r.URL.Path, "/logs"):
			_ = json.NewEncoder(w).Encode(taskLogsResponse{Logs: "tail"})
		case strings.HasSuffix(r.URL.Path, "/transcript"):
			_ = json.NewEncoder(w).Encode([]TaskTranscriptMessage{{TaskID: "task-1", Role: "user", Body: "hello"}})
		case strings.HasPrefix(r.URL.Path, "/v1/tasks/") && r.Method == http.MethodDelete:
			_ = json.NewEncoder(w).Encode(map[string]bool{"deleted": true})
		case strings.HasSuffix(r.URL.Path, "/input") || strings.HasSuffix(r.URL.Path, "/resize"):
			_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
		case strings.HasPrefix(r.URL.Path, "/v1/tasks/"):
			_ = json.NewEncoder(w).Encode(Task{ID: "task-1", ProjectID: "project-1", Title: "Task", Status: db.StatusActive})
		case r.URL.Path == "/v1/discord/invite-url":
			_ = json.NewEncoder(w).Encode(discordInviteResponse{URL: "https://discord.com/oauth2/authorize?client_id=123"})
		case r.URL.Path == "/v1/discord/tasks/task-1/sync":
			_ = json.NewEncoder(w).Encode(map[string]any{"enabled": true, "connected": true, "guildId": "guild"})
		case strings.HasPrefix(r.URL.Path, "/v1/discord/"):
			_ = json.NewEncoder(w).Encode(map[string]any{"enabled": true, "connected": true, "guildId": "guild"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := &Client{baseURL: server.URL, http: server.Client()}
	ctx := context.Background()
	description := "description"
	defaultAgent := "codex"
	title := "updated"
	nextDescription := "next"
	nextDescriptionPtr := &nextDescription
	agentName := "claude"

	if _, err := client.Status(ctx); err != nil {
		t.Fatal(err)
	}
	if cfg, err := client.Config(ctx); err != nil || cfg.DefaultAgent != "codex" {
		t.Fatalf("Config() = (%#v, %v), want codex", cfg, err)
	}
	if cfg, err := client.UpdateDefaultAgent(ctx, "codex"); err != nil || cfg.DefaultAgent != "codex" {
		t.Fatalf("UpdateDefaultAgent() = (%#v, %v), want codex", cfg, err)
	}
	if _, err := client.ListAgents(ctx, "project-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ListAgentsForPath(ctx, "/tmp/project"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ListProjects(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CreateProject(ctx, "/tmp/project", "Project", &description, &defaultAgent); err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetProject(ctx, "project-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.UpdateProjectDetails(ctx, "project-1", "Renamed", &description); err != nil {
		t.Fatal(err)
	}
	if _, err := client.UpdateProjectDefaultAgent(ctx, "project-1", &defaultAgent); err != nil {
		t.Fatal(err)
	}
	if _, err := client.GrantProjectAccess(ctx, "project-1"); err != nil {
		t.Fatal(err)
	}
	if err := client.DeleteProject(ctx, "project-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ListTasksStatus(ctx, "project-1", string(db.StatusActive)); err != nil {
		t.Fatal(err)
	}
	if _, err := client.MonitorTasks(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CreateTask(ctx, "project-1", "Task", &description, "claude", true); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CreateTaskWithWorkspace(ctx, "project-1", "Task", &description, "claude", true, db.WorkspaceModeProject); err != nil {
		t.Fatal(err)
	}
	if _, err := client.RunNewTaskWithInitialPromptWorkspace(ctx, "project-1", "Task", &description, "claude", true, &description, db.WorkspaceModeWorktree); err != nil {
		t.Fatal(err)
	}
	if _, err := client.RunNewDiscordTaskWithWorkspace(ctx, "project-1", "Task", &description, "codex", true, db.WorkspaceModeWorktree); err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetTask(ctx, "task-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.UpdateTaskTitle(ctx, "task-1", "updated"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.UpdateTaskMetadata(ctx, "task-1", &title, &nextDescriptionPtr, false, &agentName); err != nil {
		t.Fatal(err)
	}
	if _, err := client.RunTask(ctx, "task-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.StopTask(ctx, "task-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.InterruptTask(ctx, "task-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.SendTaskMessage(ctx, "task-1", "hello"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.RecordTaskInput(ctx, "task-1", "prompt"); err != nil {
		t.Fatal(err)
	}
	if err := client.SendTaskInput(ctx, "task-1", "abc"); err != nil {
		t.Fatal(err)
	}
	if err := client.ResizeTaskTerminal(ctx, "task-1", 120, 40); err != nil {
		t.Fatal(err)
	}
	if logs, err := client.TaskLogs(ctx, "task-1", 50); err != nil || logs != "tail" {
		t.Fatalf("TaskLogs() = (%q, %v), want tail", logs, err)
	}
	if messages, err := client.TaskTranscript(ctx, "task-1", 10); err != nil || len(messages) != 1 {
		t.Fatalf("TaskTranscript() = (%#v, %v), want one message", messages, err)
	}
	if err := client.DeleteTask(ctx, "task-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.DiscordStatus(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := client.DiscordConnect(ctx, "token", "guild", "user"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.DiscordDisconnect(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := client.DiscordSoftSync(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := client.DiscordHardSync(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := client.DiscordTaskSync(ctx, "task-1"); err != nil {
		t.Fatal(err)
	}
	if inviteURL, err := client.DiscordInviteURL(ctx, "token"); err != nil || !strings.Contains(inviteURL, "discord.com") {
		t.Fatalf("DiscordInviteURL() = (%q, %v), want Discord URL", inviteURL, err)
	}

	for _, want := range []string{
		"GET /v1/status",
		"GET /v1/config",
		"PATCH /v1/config",
		"GET /v1/agents?project_id=project-1",
		"GET /v1/agents?project_path=%2Ftmp%2Fproject",
		"POST /v1/projects",
		"PATCH /v1/projects/project-1",
		"POST /v1/tasks/task-1/run",
		"POST /v1/tasks/task-1/input",
		"GET /v1/tasks/task-1/logs?lines=50",
		"GET /v1/tasks/task-1/transcript?limit=10",
		"POST /v1/discord/connect",
		"POST /v1/discord/tasks/task-1/sync",
		"POST /v1/discord/invite-url",
	} {
		if !containsRequest(requests, want) {
			t.Fatalf("requests missing %q:\n%s", want, strings.Join(requests, "\n"))
		}
	}
	if body := bodies["POST /v1/tasks"]; !strings.Contains(body, `"projectId":"project-1"`) || !strings.Contains(body, `"workspaceMode":"worktree"`) {
		t.Fatalf("POST /v1/tasks body = %s, want project id and workspace mode", body)
	}
	if body := bodies["POST /v1/discord/connect"]; !strings.Contains(body, `"allowedUserId":"user"`) {
		t.Fatalf("DiscordConnect body = %s, want allowed user", body)
	}
}

func containsRequest(requests []string, want string) bool {
	for _, request := range requests {
		if request == want {
			return true
		}
	}
	return false
}

type errTestClient string

func (e errTestClient) Error() string {
	return string(e)
}
