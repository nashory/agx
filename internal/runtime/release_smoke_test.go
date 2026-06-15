package runtime

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nashory/agx/internal/db"
)

func TestReleaseSmokeRuntimeAPI(t *testing.T) {
	service, project := newRuntimeAPITestService(t)

	agentName := "codex"
	var cfg RuntimeConfig
	if code := runtimeAPIRequest(t, service, http.MethodPatch, "/v1/config", patchConfigRequest{DefaultAgent: &agentName}, &cfg); code != http.StatusOK {
		t.Fatalf("patch config status = %d, want OK", code)
	}
	if cfg.DefaultAgent != agentName {
		t.Fatalf("DefaultAgent = %q, want %q", cfg.DefaultAgent, agentName)
	}

	var task Task
	create := createTaskRequest{
		ProjectID:      project.ID,
		Title:          "release smoke task",
		Agent:          agentName,
		WorkspaceMode:  string(db.WorkspaceModeProject),
		RunImmediately: false,
	}
	if code := runtimeAPIRequest(t, service, http.MethodPost, "/v1/tasks", create, &task); code != http.StatusOK {
		t.Fatalf("create task status = %d, want OK", code)
	}
	if task.Title != create.Title || task.Status != db.StatusOffline {
		t.Fatalf("created task = %#v, want offline release smoke task", task)
	}

	activeTask, err := service.store.CreateTaskRuntimeModeInterfaceWorkspace(db.NewTaskID(), project.ID, "active project task", nil, agentName, false, db.TaskInterfaceLocal, db.WorkspaceModeProject, db.StatusActive, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	status, payload := runtimeAPIErrorResponse(t, service, http.MethodPost, "/v1/tasks", createTaskRequest{
		ProjectID:      project.ID,
		Title:          "conflicting project task",
		Agent:          agentName,
		WorkspaceMode:  string(db.WorkspaceModeProject),
		RunImmediately: false,
	})
	if status != http.StatusConflict || payload.Code != ErrorCodeConflict || !payload.Retryable || !strings.Contains(payload.Error, activeTask.ID) {
		t.Fatalf("conflict payload = status %d %#v, want retryable project-mode conflict with active task id", status, payload)
	}

	discordStatus, discordPayload := runtimeAPIErrorResponse(t, service, http.MethodPost, "/v1/discord/tasks/"+task.ID+"/sync", nil)
	if discordStatus != http.StatusBadRequest || discordPayload.Code != ErrorCodeValidation || !strings.Contains(discordPayload.Error, "not a Discord task") {
		t.Fatalf("discord sync error = status %d %#v, want validation for non-Discord task", discordStatus, discordPayload)
	}

	if code := runtimeAPIRequest(t, service, http.MethodDelete, "/v1/tasks/"+task.ID, nil, nil); code != http.StatusOK {
		t.Fatalf("delete task status = %d, want OK", code)
	}
	if _, err := service.store.GetTask(task.ID); err != db.ErrTaskNotFound {
		t.Fatalf("GetTask after delete error = %v, want ErrTaskNotFound", err)
	}
}

func TestReleaseSmokeRuntimeRestartRecovery(t *testing.T) {
	configDir := shortTempDir(t)
	t.Setenv("AGX_CONFIG_DIR", configDir)

	startRuntime := func(t *testing.T) (chan error, context.CancelFunc) {
		t.Helper()
		service := NewService("release-smoke")
		errCh := make(chan error, 1)
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			errCh <- service.Start(ctx)
		}()
		socketPath := filepath.Join(configDir, socketFile)
		deadline := time.After(3 * time.Second)
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case err := <-errCh:
				cancel()
				t.Fatalf("runtime exited before socket was ready: %v", err)
			case <-deadline:
				cancel()
				t.Fatalf("socket %s was not created", socketPath)
			case <-ticker.C:
				status, err := NewClient().Status(context.Background())
				if err == nil && status.Running {
					return errCh, cancel
				}
			}
		}
	}

	errCh, cancelService := startRuntime(t)
	client := NewClient()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	project, err := client.CreateProject(ctx, t.TempDir(), "Release Smoke", nil, nil)
	if err != nil {
		t.Fatalf("CreateProject() error = %v", err)
	}
	task, err := client.CreateTask(ctx, project.ID, "restart recovery task", nil, "codex", false)
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	if err := client.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("first runtime returned error = %v", err)
		}
	case <-time.After(3 * time.Second):
		cancelService()
		t.Fatal("first runtime did not stop")
	}
	waitForRuntimeLockRelease(t, filepath.Join(configDir, lockFile))

	errCh, cancelService = startRuntime(t)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = NewClient().Shutdown(ctx)
		select {
		case <-errCh:
		case <-time.After(3 * time.Second):
			cancelService()
		}
	}()

	tasks, err := NewClient().ListTasks(ctx, project.ID)
	if err != nil {
		t.Fatalf("ListTasks() after restart error = %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != task.ID || tasks[0].Title != task.Title {
		t.Fatalf("tasks after restart = %#v, want persisted task %s", tasks, task.ID)
	}
}

func waitForRuntimeLockRelease(t *testing.T, lockPath string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		held, err := LockHeld(lockPath)
		if err == nil && !held {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("runtime lock %s was not released", lockPath)
}
