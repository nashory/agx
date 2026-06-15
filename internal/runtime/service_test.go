package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nashory/agx/internal/codexapp"
	"github.com/nashory/agx/internal/config"
	"github.com/nashory/agx/internal/db"
	agxdiscord "github.com/nashory/agx/internal/discord"
)

func newRuntimeAPITestService(t *testing.T) (*Service, db.Project) {
	t.Helper()
	store, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	project, err := store.EnsureProjectDetails(t.TempDir(), "Runtime API", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService("test-version")
	service.store = store
	return service, project
}

func runtimeAPIRequest(t *testing.T, service *Service, method, path string, body any, out any) int {
	t.Helper()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(data)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	service.routes().ServeHTTP(rec, req)
	if out != nil {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			t.Fatalf("decode %s %s response: %v\nbody: %s", method, path, err, rec.Body.String())
		}
	}
	return rec.Code
}

func runtimeAPIError(t *testing.T, service *Service, method, path string, body any) (int, string) {
	t.Helper()
	status, payload := runtimeAPIErrorResponse(t, service, method, path, body)
	return status, payload.Error
}

func runtimeAPIErrorResponse(t *testing.T, service *Service, method, path string, body any) (int, errorResponse) {
	t.Helper()
	var payload errorResponse
	status := runtimeAPIRequest(t, service, method, path, body, &payload)
	return status, payload
}

func TestServiceStatusAndShutdown(t *testing.T) {
	configDir := shortTempDir(t)
	t.Setenv("AGX_CONFIG_DIR", configDir)
	service := NewService("test-version")
	errCh := make(chan error, 1)
	go func() {
		errCh <- service.Start(context.Background())
	}()
	waitForSocket(t, filepath.Join(configDir, socketFile))

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	status, err := NewClient().Status(ctx)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if !status.Running {
		t.Fatal("Status().Running = false, want true")
	}
	if status.Version != "test-version" {
		t.Fatalf("Status().Version = %q, want test-version", status.Version)
	}
	if status.ConfigDir != configDir {
		t.Fatalf("Status().ConfigDir = %q, want %q", status.ConfigDir, configDir)
	}
	if err := NewClient().Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Service.Start() returned error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("runtime did not stop after shutdown")
	}
	if _, err := os.Stat(filepath.Join(configDir, socketFile)); !os.IsNotExist(err) {
		t.Fatalf("socket still exists or stat failed differently: %v", err)
	}
}

func TestServiceProjectAndTaskEndpoints(t *testing.T) {
	configDir := shortTempDir(t)
	t.Setenv("AGX_CONFIG_DIR", configDir)
	service := NewService("test-version")
	errCh := make(chan error, 1)
	go func() {
		errCh <- service.Start(context.Background())
	}()
	waitForSocket(t, filepath.Join(configDir, socketFile))
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = NewClient().Shutdown(ctx)
		<-errCh
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	project, err := NewClient().CreateProject(ctx, t.TempDir(), "Runtime Test", nil, nil)
	if err != nil {
		t.Fatalf("CreateProject() error = %v", err)
	}
	projects, err := NewClient().ListProjects(ctx)
	if err != nil {
		t.Fatalf("ListProjects() error = %v", err)
	}
	if len(projects) != 1 || projects[0].ID != project.ID {
		t.Fatalf("ListProjects() = %#v, want created project %s", projects, project.ID)
	}
	createdTask, err := NewClient().CreateTask(ctx, project.ID, "runtime task", nil, "claude", true)
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	if createdTask.Status != "offline" {
		t.Fatalf("CreateTask().Status = %q, want offline", createdTask.Status)
	}
	tasks, err := NewClient().ListTasks(ctx, project.ID)
	if err != nil {
		t.Fatalf("ListTasks() error = %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != createdTask.ID || tasks[0].Title != "runtime task" {
		t.Fatalf("ListTasks() = %#v, want created task", tasks)
	}
	if err := NewClient().DeleteTask(ctx, createdTask.ID); err != nil {
		t.Fatalf("DeleteTask() error = %v", err)
	}
	tasks, err = NewClient().ListTasks(ctx, project.ID)
	if err != nil {
		t.Fatalf("ListTasks() after delete error = %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("ListTasks() after delete = %#v, want none", tasks)
	}
}

func TestServiceConfigDefaultAgentEndpoints(t *testing.T) {
	configDir := shortTempDir(t)
	t.Setenv("AGX_CONFIG_DIR", configDir)
	service, _ := newRuntimeAPITestService(t)

	var cfg RuntimeConfig
	if code := runtimeAPIRequest(t, service, http.MethodGet, "/v1/config", nil, &cfg); code != http.StatusOK {
		t.Fatalf("get config status = %d, want 200", code)
	}
	if cfg.DefaultAgent != "codex" {
		t.Fatalf("DefaultAgent = %q, want codex", cfg.DefaultAgent)
	}

	agentName := "claude"
	if code := runtimeAPIRequest(t, service, http.MethodPatch, "/v1/config", patchConfigRequest{DefaultAgent: &agentName}, &cfg); code != http.StatusOK {
		t.Fatalf("patch config status = %d, want 200", code)
	}
	if cfg.DefaultAgent != "claude" {
		t.Fatalf("DefaultAgent = %q, want claude", cfg.DefaultAgent)
	}
	saved, warnings := config.LoadGlobal()
	if len(warnings) > 0 {
		t.Fatal(warnings[0])
	}
	if saved.DefaultAgent != "claude" {
		t.Fatalf("saved DefaultAgent = %q, want claude", saved.DefaultAgent)
	}
}

func TestServiceConfigRejectsUnknownDefaultAgent(t *testing.T) {
	configDir := shortTempDir(t)
	t.Setenv("AGX_CONFIG_DIR", configDir)
	service, _ := newRuntimeAPITestService(t)

	agentName := "missing"
	status, message := runtimeAPIError(t, service, http.MethodPatch, "/v1/config", patchConfigRequest{DefaultAgent: &agentName})
	if status != http.StatusBadRequest || !strings.Contains(message, `unknown agent "missing"`) {
		t.Fatalf("patch config error = (%d, %q), want unknown agent bad request", status, message)
	}
}

func TestDiscordTaskSyncRejectsLocalTask(t *testing.T) {
	service, project := newRuntimeAPITestService(t)
	task, err := service.store.CreateTask(project.ID, "local task", nil, "codex", db.StatusActive)
	if err != nil {
		t.Fatal(err)
	}

	status, message := runtimeAPIError(t, service, http.MethodPost, "/v1/discord/tasks/"+task.ID+"/sync", nil)
	if status != http.StatusBadRequest || !strings.Contains(message, "is not a Discord task") {
		t.Fatalf("task sync error = (%d, %q), want non-Discord task bad request", status, message)
	}
}

func TestRuntimeDeleteTaskReturnsCleanupFailureAfterDeletingRow(t *testing.T) {
	t.Setenv("AGX_CONFIG_DIR", t.TempDir())
	service, project := newRuntimeAPITestService(t)
	escapedWorktreePath := t.TempDir()
	branchName := "agx/task-cleanup"
	task, err := service.store.CreateTaskRuntime(db.NewTaskID(), project.ID, "cleanup failure", nil, "claude", db.StatusActive, nil, &escapedWorktreePath, &branchName)
	if err != nil {
		t.Fatal(err)
	}

	status, payload := runtimeAPIErrorResponse(t, service, http.MethodDelete, "/v1/tasks/"+task.ID, nil)
	if status != http.StatusInternalServerError || !strings.Contains(payload.Error, "deleted, but cleanup failed") {
		t.Fatalf("delete error = (%d, %#v), want cleanup failure", status, payload)
	}
	if payload.Code != ErrorCodeCleanupFailed || !payload.PartialSuccess {
		t.Fatalf("delete error payload = %#v, want cleanup code and partial success", payload)
	}
	if _, err := service.store.GetTask(task.ID); !errors.Is(err, db.ErrTaskNotFound) {
		t.Fatalf("GetTask after cleanup failure = %v, want ErrTaskNotFound", err)
	}
}

func TestRuntimeCreateTaskValidation(t *testing.T) {
	service, project := newRuntimeAPITestService(t)

	tests := []struct {
		name string
		body createTaskRequest
		want string
	}{
		{
			name: "missing project",
			body: createTaskRequest{Title: "ship it"},
			want: "project id is required",
		},
		{
			name: "blank title",
			body: createTaskRequest{ProjectID: project.ID, Title: " \t"},
			want: "task title is required",
		},
		{
			name: "invalid workspace",
			body: createTaskRequest{ProjectID: project.ID, Title: "ship it", WorkspaceMode: "spaceship"},
			want: "invalid workspace mode",
		},
		{
			name: "discord disconnected",
			body: createTaskRequest{ProjectID: project.ID, Title: "ship it", Discord: true},
			want: "Discord is not connected",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, message := runtimeAPIError(t, service, http.MethodPost, "/v1/tasks", tt.body)
			if status != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; error=%q", status, http.StatusBadRequest, message)
			}
			if !strings.Contains(message, tt.want) {
				t.Fatalf("error = %q, want substring %q", message, tt.want)
			}
		})
	}
}

func TestRuntimeErrorResponseIncludesValidationCode(t *testing.T) {
	service, _ := newRuntimeAPITestService(t)

	status, payload := runtimeAPIErrorResponse(t, service, http.MethodPost, "/v1/tasks", createTaskRequest{Title: "missing project"})
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want bad request", status)
	}
	if payload.Code != ErrorCodeValidation || payload.Retryable || payload.PartialSuccess {
		t.Fatalf("payload = %#v, want validation code without retry/partial flags", payload)
	}
}

func TestRuntimeCreateProjectModeTaskConflictResponse(t *testing.T) {
	service, project := newRuntimeAPITestService(t)
	if _, err := service.store.CreateTaskRuntimeModeInterfaceWorkspace(
		db.NewTaskID(),
		project.ID,
		"active project task",
		nil,
		"codex",
		false,
		db.TaskInterfaceLocal,
		db.WorkspaceModeProject,
		db.StatusActive,
		nil,
		nil,
		nil,
	); err != nil {
		t.Fatal(err)
	}

	status, payload := runtimeAPIErrorResponse(t, service, http.MethodPost, "/v1/tasks", createTaskRequest{
		ProjectID:     project.ID,
		Title:         "second project task",
		Agent:         "codex",
		WorkspaceMode: string(db.WorkspaceModeProject),
	})
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want conflict; payload=%#v", status, payload)
	}
	if payload.Code != ErrorCodeConflict || !payload.Retryable || payload.PartialSuccess {
		t.Fatalf("payload = %#v, want retryable conflict without partial success", payload)
	}
	if !strings.Contains(payload.Error, "another project-mode task is already active for this project") {
		t.Fatalf("payload error = %q, want project-mode conflict guidance", payload.Error)
	}
}

func TestRuntimeTaskDTOIncludesDiscordSyncState(t *testing.T) {
	service, project := newRuntimeAPITestService(t)
	task, err := service.store.CreateTaskRuntimeModeInterface(db.NewTaskID(), project.ID, "discord task", nil, "codex", false, db.TaskInterfaceDiscord, db.StatusWaiting, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.store.MarkDiscordTaskSyncFailure(task.ID, errors.New("discord timeout")); err != nil {
		t.Fatal(err)
	}

	var dto Task
	status := runtimeAPIRequest(t, service, http.MethodGet, "/v1/tasks/"+task.ID, nil, &dto)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want OK", status)
	}
	if dto.DiscordSync == nil || dto.DiscordSync.Status != string(db.DiscordTaskSyncFailed) || dto.DiscordSync.LastError == nil || *dto.DiscordSync.LastError != "discord timeout" {
		t.Fatalf("DiscordSync = %#v, want failed timeout state", dto.DiscordSync)
	}
}

func TestRuntimePatchTaskMetadata(t *testing.T) {
	service, project := newRuntimeAPITestService(t)
	description := "original"
	task, err := service.store.CreateTaskRuntimeModeInterfaceWorkspace(db.NewTaskID(), project.ID, " original ", &description, "claude", true, db.TaskInterfaceLocal, db.WorkspaceModeWorktree, db.StatusOffline, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	status, message := runtimeAPIError(t, service, http.MethodPatch, "/v1/tasks/"+task.ID, map[string]any{"title": " \t"})
	if status != http.StatusBadRequest || !strings.Contains(message, "task title is required") {
		t.Fatalf("blank title patch status=%d error=%q, want bad request", status, message)
	}
	unchanged, err := service.store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Title != " original " {
		t.Fatalf("title changed after rejected patch: %q", unchanged.Title)
	}

	var updated Task
	status = runtimeAPIRequest(t, service, http.MethodPatch, "/v1/tasks/"+task.ID, map[string]any{
		"title":       "  renamed  ",
		"description": "new description",
		"agent":       "codex",
	}, &updated)
	if status != http.StatusOK {
		t.Fatalf("metadata patch status = %d, want %d", status, http.StatusOK)
	}
	if updated.Title != "renamed" || updated.Description == nil || *updated.Description != "new description" || updated.Agent != "codex" {
		t.Fatalf("updated task = %#v, want trimmed title, description, and agent", updated)
	}

	var cleared Task
	status = runtimeAPIRequest(t, service, http.MethodPatch, "/v1/tasks/"+task.ID, map[string]any{"clearDescription": true}, &cleared)
	if status != http.StatusOK {
		t.Fatalf("clear description status = %d, want %d", status, http.StatusOK)
	}
	if cleared.Description != nil {
		t.Fatalf("Description = %#v, want nil after clearDescription", cleared.Description)
	}
}

func TestRuntimeTranscriptAndRecordInputEndpoints(t *testing.T) {
	service, project := newRuntimeAPITestService(t)
	task, err := service.store.CreateTask(project.ID, "transcript task", nil, "claude", db.StatusOffline)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.store.AppendTaskTranscriptMessage(task.ID, "user", "first", nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := service.store.AppendTaskTranscriptMessage(task.ID, "assistant", "second", nil, nil); err != nil {
		t.Fatal(err)
	}

	var messages []TaskTranscriptMessage
	status := runtimeAPIRequest(t, service, http.MethodGet, "/v1/tasks/"+task.ID+"/transcript?limit=1", nil, &messages)
	if status != http.StatusOK {
		t.Fatalf("transcript status = %d, want %d", status, http.StatusOK)
	}
	if len(messages) != 1 || messages[0].Body != "second" {
		t.Fatalf("messages = %#v, want latest transcript message", messages)
	}

	status, message := runtimeAPIError(t, service, http.MethodGet, "/v1/tasks/"+task.ID+"/transcript?limit=bad", nil)
	if status != http.StatusBadRequest || !strings.Contains(message, "invalid limit") {
		t.Fatalf("invalid limit status=%d error=%q, want bad request", status, message)
	}

	var updated Task
	status = runtimeAPIRequest(t, service, http.MethodPost, "/v1/tasks/"+task.ID+"/record-input", taskMessageRequest{Message: "last prompt"}, &updated)
	if status != http.StatusOK {
		t.Fatalf("record input status = %d, want %d", status, http.StatusOK)
	}
	if updated.LastUserPrompt == nil || *updated.LastUserPrompt != "last prompt" {
		t.Fatalf("LastUserPrompt = %#v, want last prompt", updated.LastUserPrompt)
	}
}

func TestRuntimeTaskMutationHandlersUseTaskLock(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   func(string) string
		body   any
	}{
		{
			name:   "interrupt",
			method: http.MethodPost,
			path:   func(id string) string { return "/v1/tasks/" + id + "/interrupt" },
		},
		{
			name:   "input",
			method: http.MethodPost,
			path:   func(id string) string { return "/v1/tasks/" + id + "/input" },
			body:   taskInputRequest{Data: "x"},
		},
		{
			name:   "resize",
			method: http.MethodPost,
			path:   func(id string) string { return "/v1/tasks/" + id + "/resize" },
			body:   taskResizeRequest{Cols: 80, Rows: 24},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, project := newRuntimeAPITestService(t)
			task, err := service.store.CreateTask(project.ID, "locked task", nil, "claude", db.StatusActive)
			if err != nil {
				t.Fatal(err)
			}
			lock := service.taskLock(task.ID)
			lock.Lock()
			done := make(chan struct{})
			go func() {
				defer close(done)
				runtimeAPIRequest(t, service, tt.method, tt.path(task.ID), tt.body, nil)
			}()
			select {
			case <-done:
				lock.Unlock()
				t.Fatal("handler completed while task lock was held")
			case <-time.After(100 * time.Millisecond):
			}
			lock.Unlock()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("handler did not complete after task lock was released")
			}
		})
	}
}

func TestRuntimeMonitorTasksFiltersToLiveTasks(t *testing.T) {
	service, project := newRuntimeAPITestService(t)
	if _, err := service.store.CreateTask(project.ID, "offline", nil, "claude", db.StatusOffline); err != nil {
		t.Fatal(err)
	}
	if _, err := service.store.CreateTask(project.ID, "waiting legacy", nil, "claude", db.StatusWaiting); err != nil {
		t.Fatal(err)
	}
	streamKind := codexapp.StreamKind
	if _, err := service.store.CreateTaskRuntimeModeInterface(db.NewTaskID(), project.ID, "structured", nil, "codex", true, db.TaskInterfaceDiscord, db.StatusWaiting, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	tasks, err := service.store.ListTasks(project.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range tasks {
		if task.Title == "structured" {
			threadID := "thread-1"
			if err := service.store.UpdateTaskAgentStream(task.ID, &threadID, nil, &streamKind); err != nil {
				t.Fatal(err)
			}
		}
	}

	var monitor []MonitorTask
	status := runtimeAPIRequest(t, service, http.MethodGet, "/v1/tasks/monitor", nil, &monitor)
	if status != http.StatusOK {
		t.Fatalf("monitor status = %d, want %d", status, http.StatusOK)
	}
	if len(monitor) != 1 || monitor[0].Title != "structured" || monitor[0].ProjectName != project.Name {
		t.Fatalf("monitor tasks = %#v, want only structured live task", monitor)
	}
}

func TestServiceProjectWorkspaceTaskSingleton(t *testing.T) {
	configDir := shortTempDir(t)
	t.Setenv("AGX_CONFIG_DIR", configDir)
	service := NewService("test-version")
	errCh := make(chan error, 1)
	go func() {
		errCh <- service.Start(context.Background())
	}()
	waitForSocket(t, filepath.Join(configDir, socketFile))
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = NewClient().Shutdown(ctx)
		<-errCh
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	project, err := NewClient().CreateProject(ctx, t.TempDir(), "Runtime Test", nil, nil)
	if err != nil {
		t.Fatalf("CreateProject() error = %v", err)
	}
	first, err := NewClient().CreateTaskWithWorkspace(ctx, project.ID, "project task", nil, "claude", true, db.WorkspaceModeProject)
	if err != nil {
		t.Fatalf("CreateTaskWithWorkspace(project) error = %v", err)
	}
	if first.WorkspaceMode != string(db.WorkspaceModeProject) {
		t.Fatalf("WorkspaceMode = %q, want project", first.WorkspaceMode)
	}
	if first.WorktreePath != nil {
		t.Fatalf("WorktreePath = %#v, want nil for project mode", first.WorktreePath)
	}
	if err := service.store.UpdateTaskStatus(first.ID, db.StatusActive); err != nil {
		t.Fatalf("UpdateTaskStatus(active) error = %v", err)
	}
	if _, err := NewClient().CreateTaskWithWorkspace(ctx, project.ID, "second project task", nil, "claude", true, db.WorkspaceModeProject); err == nil || !strings.Contains(err.Error(), "another project-mode task") {
		t.Fatalf("second project task error = %v, want singleton rejection", err)
	}
	if _, err := NewClient().CreateTaskWithWorkspace(ctx, project.ID, "worktree task", nil, "claude", true, db.WorkspaceModeWorktree); err != nil {
		t.Fatalf("worktree task while project task active error = %v", err)
	}
}

func TestServiceListAgentsUsesRuntimeProjectConfig(t *testing.T) {
	configDir := shortTempDir(t)
	projectRoot := t.TempDir()
	projectConfigDir := filepath.Join(projectRoot, ".agx")
	if err := os.MkdirAll(projectConfigDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectConfigDir, "config.toml"), []byte(`
[agents.runtime-test]
command = "runtime-test-agent"
description = "runtime configured agent"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("AGX_CONFIG_DIR", configDir)
	service := NewService("test-version")
	errCh := make(chan error, 1)
	go func() {
		errCh <- service.Start(context.Background())
	}()
	waitForSocket(t, filepath.Join(configDir, socketFile))
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = NewClient().Shutdown(ctx)
		<-errCh
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	project, err := NewClient().CreateProject(ctx, projectRoot, "Runtime Test", nil, nil)
	if err != nil {
		t.Fatalf("CreateProject() error = %v", err)
	}
	agents, err := NewClient().ListAgents(ctx, project.ID)
	if err != nil {
		t.Fatalf("ListAgents() error = %v", err)
	}
	for _, agent := range agents {
		if agent.Name == "runtime-test" && agent.Command == "runtime-test-agent" {
			return
		}
	}
	t.Fatalf("ListAgents() = %#v, want runtime-test from project config", agents)
}

func TestServiceGrantProjectAccess(t *testing.T) {
	configDir := shortTempDir(t)
	t.Setenv("AGX_CONFIG_DIR", configDir)
	service := NewService("test-version")
	errCh := make(chan error, 1)
	go func() {
		errCh <- service.Start(context.Background())
	}()
	waitForSocket(t, filepath.Join(configDir, socketFile))
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = NewClient().Shutdown(ctx)
		<-errCh
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	project, err := NewClient().CreateProject(ctx, initRuntimeGitRepo(t), "Runtime Test", nil, nil)
	if err != nil {
		t.Fatalf("CreateProject() error = %v", err)
	}
	if project.AccessGranted {
		t.Fatal("CreateProject().AccessGranted = true, want false before grant")
	}
	granted, err := NewClient().GrantProjectAccess(ctx, project.ID)
	if err != nil {
		t.Fatalf("GrantProjectAccess() error = %v", err)
	}
	if !granted.AccessGranted {
		t.Fatalf("GrantProjectAccess().AccessGranted = false: %s", valueOrEmpty(granted.AccessError))
	}
}

func TestMergedDiscordConnectConfigUsesExistingValues(t *testing.T) {
	next := mergedDiscordConnectConfig(discordConnectRequest{}, config.DiscordConfig{
		Enabled:        true,
		BotToken:       " token ",
		GuildID:        " guild ",
		AllowedUserIDs: []string{" ", "user-1"},
	})
	if err := agxdiscord.ValidateConfig(next); err != nil {
		t.Fatal(err)
	}
	if next.BotToken != "token" || next.GuildID != "guild" || len(next.AllowedUserIDs) != 1 || next.AllowedUserIDs[0] != "user-1" {
		t.Fatalf("merged config = %#v, want trimmed existing token/guild/allowlist", next)
	}
}

func TestMergedDiscordConnectConfigRequiresTokenWhenDisabled(t *testing.T) {
	next := mergedDiscordConnectConfig(discordConnectRequest{}, config.DiscordConfig{
		Enabled:        false,
		BotToken:       " token ",
		GuildID:        " guild ",
		AllowedUserIDs: []string{"user-1"},
	})
	if next.BotToken != "" || next.GuildID != "guild" || len(next.AllowedUserIDs) != 1 || next.AllowedUserIDs[0] != "user-1" {
		t.Fatalf("merged config = %#v, want disabled config to keep ids but require a fresh token", next)
	}
	if err := agxdiscord.ValidateConfig(next); err == nil || !strings.Contains(err.Error(), "discord bot token is required") {
		t.Fatalf("ValidateConfig() error = %v, want missing token", err)
	}
}

func TestMergedDiscordConnectConfigOverridesAllowedUser(t *testing.T) {
	next := mergedDiscordConnectConfig(discordConnectRequest{AllowedUserID: " user-2 "}, config.DiscordConfig{
		Enabled:        true,
		BotToken:       "token",
		GuildID:        "guild",
		AllowedUserIDs: []string{"user-1"},
	})
	if len(next.AllowedUserIDs) != 1 || next.AllowedUserIDs[0] != "user-2" {
		t.Fatalf("AllowedUserIDs = %#v, want override user-2", next.AllowedUserIDs)
	}
}

func TestDiscordDisconnectClearsStoredTokenOnly(t *testing.T) {
	configDir := shortTempDir(t)
	t.Setenv("AGX_CONFIG_DIR", configDir)
	if err := config.SaveDiscord(config.DiscordConfig{
		Enabled:        true,
		BotToken:       "token",
		GuildID:        "guild",
		AllowedUserIDs: []string{"user-1"},
	}); err != nil {
		t.Fatal(err)
	}
	service := NewService("test-version")
	var status agxdiscord.Status
	if code := runtimeAPIRequest(t, service, http.MethodPost, "/v1/discord/disconnect", nil, &status); code != http.StatusOK {
		t.Fatalf("disconnect status = %d, want 200", code)
	}
	cfg, warnings := config.LoadGlobal()
	if len(warnings) > 0 {
		t.Fatal(warnings[0])
	}
	if cfg.Discord.Enabled || cfg.Discord.BotToken != "" || cfg.Discord.GuildID != "guild" || len(cfg.Discord.AllowedUserIDs) != 1 || cfg.Discord.AllowedUserIDs[0] != "user-1" {
		t.Fatalf("discord config after disconnect = %#v, want disabled, token cleared, ids preserved", cfg.Discord)
	}
}

func TestDiscordConnectRejectsEmptyTokenAfterDisconnect(t *testing.T) {
	configDir := shortTempDir(t)
	t.Setenv("AGX_CONFIG_DIR", configDir)
	if err := config.SaveDiscord(config.DiscordConfig{
		Enabled:        false,
		BotToken:       "",
		GuildID:        "guild",
		AllowedUserIDs: []string{"user-1"},
	}); err != nil {
		t.Fatal(err)
	}
	service := NewService("test-version")
	status, message := runtimeAPIError(t, service, http.MethodPost, "/v1/discord/connect", discordConnectRequest{})
	if status != http.StatusBadRequest || !strings.Contains(message, "discord bot token is required") {
		t.Fatalf("connect error = (%d, %q), want missing token bad request", status, message)
	}
}

func waitForSocket(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("socket %s was not created", path)
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "agxrt-*")
	if err != nil {
		t.Fatalf("MkdirTemp() error = %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}
