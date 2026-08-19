package runtime

import (
	"context"
	"net/http"
	"testing"

	"github.com/nashory/agx/internal/db"
)

func TestCleanupAgentTasksDeletesOnlyMatchingLiveTasks(t *testing.T) {
	service, project := newRuntimeAPITestService(t)
	codex, err := service.store.CreateTaskRuntimeModeInterface(db.NewTaskID(), project.ID, "live codex", nil, "codex", true, db.TaskInterfaceLocal, db.StatusActive, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	claude, err := service.store.CreateTaskRuntimeModeInterface(db.NewTaskID(), project.ID, "live claude", nil, "claude", true, db.TaskInterfaceLocal, db.StatusWaiting, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	completed, err := service.store.CreateTaskRuntimeModeInterface(db.NewTaskID(), project.ID, "completed codex", nil, "codex", true, db.TaskInterfaceLocal, db.StatusComplete, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	var result AgentCleanupResult
	status := runtimeAPIRequest(t, service, http.MethodPost, "/v1/tasks/cleanup-agent", cleanupAgentTasksRequest{Agent: "Codex"}, &result)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if result.Matched != 1 || result.Deleted != 1 || result.Failed != 0 {
		t.Fatalf("result = %#v", result)
	}
	if len(result.DeletedTaskIDs) != 1 || result.DeletedTaskIDs[0] != codex.ID {
		t.Fatalf("deleted task ids = %#v", result.DeletedTaskIDs)
	}
	if _, err := service.store.GetTask(codex.ID); err == nil {
		t.Fatal("matching live Codex task was not deleted")
	}
	if _, err := service.store.GetTask(claude.ID); err != nil {
		t.Fatalf("nonmatching Claude task was deleted: %v", err)
	}
	if _, err := service.store.GetTask(completed.ID); err != nil {
		t.Fatalf("completed Codex task was deleted: %v", err)
	}
}

func TestCleanupAgentTasksKeepsDiscordTaskWhenDisconnected(t *testing.T) {
	service, project := newRuntimeAPITestService(t)
	task, err := service.store.CreateTaskRuntimeModeInterface(db.NewTaskID(), project.ID, "discord codex", nil, "codex", true, db.TaskInterfaceDiscord, db.StatusActive, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	result, err := service.cleanupLiveTasksByAgent(context.Background(), "codex")
	if err != nil {
		t.Fatal(err)
	}
	if result.Matched != 1 || result.Deleted != 0 || result.Failed != 1 {
		t.Fatalf("result = %#v", result)
	}
	if len(result.Failures) != 1 || result.Failures[0].TaskID != task.ID {
		t.Fatalf("failures = %#v", result.Failures)
	}
	if _, err := service.store.GetTask(task.ID); err != nil {
		t.Fatalf("Discord task was deleted while channel cleanup was unavailable: %v", err)
	}
}

func TestCleanupAgentTasksValidatesAgent(t *testing.T) {
	service, _ := newRuntimeAPITestService(t)
	status, message := runtimeAPIError(t, service, http.MethodPost, "/v1/tasks/cleanup-agent", cleanupAgentTasksRequest{})
	if status != http.StatusBadRequest || message != "agent is required" {
		t.Fatalf("response = (%d, %q)", status, message)
	}
}
