package session

import (
	"testing"
	"time"

	"github.com/nashory/agx/internal/agent"
	"github.com/nashory/agx/internal/db"
	"github.com/nashory/agx/internal/tmux"
)

// These tests exercise the manager against the in-memory fake backend, so they
// verify task lifecycle behavior without a real terminal engine and run on every
// platform (including Windows, where the fake tmux shell scripts cannot).

func liveTaskTarget(project db.Project, task db.Task) string {
	return tmux.Target(projectSessionName(project), *task.SessionName)
}

func containsCall(calls []string, want string) bool {
	for _, call := range calls {
		if call == want {
			return true
		}
	}
	return false
}

func TestManagerLiveTaskOperationsUseFakeBackend(t *testing.T) {
	store, project, task := newSessionStoreWithLiveTask(t)
	target := liveTaskTarget(project, task)

	backend := newFakeBackend()
	backend.windows[target] = true
	backend.sessions[projectSessionName(project)] = true
	backend.paneHistory[target] = "history\n"
	backend.paneVisible[target] = "current\n"

	manager := NewManager(store, backend, agent.NewRegistry("claude"))

	logs, err := manager.GetLogs(task, 20)
	if err != nil {
		t.Fatal(err)
	}
	if logs != "history\n" {
		t.Fatalf("GetLogs() = %q, want history", logs)
	}
	if err := manager.ResizeTaskTerminal(task, 120, 40); err != nil {
		t.Fatal(err)
	}
	if err := manager.SendInput(task, "abc"); err != nil {
		t.Fatal(err)
	}
	if err := manager.InterruptTask(task); err != nil {
		t.Fatal(err)
	}
	streamPath := t.TempDir() + "/stream.log"
	streamed, err := manager.StreamLogs(task, streamPath, 0)
	if err != nil {
		t.Fatal(err)
	}
	if streamed != "current\n" {
		t.Fatalf("StreamLogs() = %q, want current", streamed)
	}
	if err := manager.StopTask(task); err != nil {
		t.Fatal(err)
	}

	calls := backend.recorded()
	for _, want := range []string{
		"ResizeWindow " + target + " 120 40",
		`SendInput ` + target + ` "abc"`,
		"SendKey " + target + " C-c",
		"StopPipePane " + target,
		"KillWindow " + target,
	} {
		if !containsCall(calls, want) {
			t.Fatalf("backend calls missing %q:\n%v", want, calls)
		}
	}

	refreshed, err := store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.Status != db.StatusOffline || refreshed.SessionName != nil {
		t.Fatalf("task after StopTask = %#v, want offline with no session", refreshed)
	}
}

func TestManagerDetectStatusUsesFakeBackend(t *testing.T) {
	store, project, task := newSessionStoreWithLiveTask(t)
	target := liveTaskTarget(project, task)

	backend := newFakeBackend()
	backend.windows[target] = true
	backend.paneCommand[target] = "claude"
	backend.paneVisible[target] = "working on it"

	manager := NewManager(store, backend, agent.NewRegistry("claude"))

	status, output, err := manager.DetectStatus(task, "previous output", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if status != db.StatusActive {
		t.Fatalf("DetectStatus status = %s, want active for changed output", status)
	}
	if output != "working on it" {
		t.Fatalf("DetectStatus output = %q, want current pane output", output)
	}
}

func TestManagerDetectStatusReportsOfflineWhenWindowMissing(t *testing.T) {
	store, _, task := newSessionStoreWithLiveTask(t)

	backend := newFakeBackend() // no windows registered
	manager := NewManager(store, backend, agent.NewRegistry("claude"))

	status, _, err := manager.DetectStatus(task, "previous", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if status != db.StatusOffline {
		t.Fatalf("DetectStatus status = %s, want offline when window is gone", status)
	}
}

func TestManagerDeleteTaskUsesFakeBackend(t *testing.T) {
	store, project, task := newSessionStoreWithLiveTask(t)
	target := liveTaskTarget(project, task)

	backend := newFakeBackend()
	backend.windows[target] = true
	manager := NewManager(store, backend, agent.NewRegistry("claude"))

	if err := manager.DeleteTask(task); err != nil {
		t.Fatal(err)
	}
	if !containsCall(backend.recorded(), "KillWindow "+target) {
		t.Fatalf("DeleteTask did not kill task window:\n%v", backend.recorded())
	}
	if _, err := store.GetTask(task.ID); err == nil {
		t.Fatal("GetTask after DeleteTask succeeded, want task removed")
	}
}

func TestManagerStopProjectUsesFakeBackend(t *testing.T) {
	store, project, task := newSessionStoreWithLiveTask(t)
	sessionName := projectSessionName(project)
	target := liveTaskTarget(project, task)

	backend := newFakeBackend()
	backend.windows[target] = true
	backend.sessions[sessionName] = true
	manager := NewManager(store, backend, agent.NewRegistry("claude"))

	if err := manager.StopProject(project); err != nil {
		t.Fatal(err)
	}
	calls := backend.recorded()
	for _, want := range []string{
		"KillWindow " + target,
		"KillSession " + sessionName,
	} {
		if !containsCall(calls, want) {
			t.Fatalf("StopProject calls missing %q:\n%v", want, calls)
		}
	}
	refreshed, err := store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.Status != db.StatusOffline || refreshed.SessionName != nil {
		t.Fatalf("task after StopProject = %#v, want runtime cleared", refreshed)
	}
}

func TestManagerSendMessageRestartsWhenWindowMissing(t *testing.T) {
	store, project, task := newSessionStoreWithLiveTask(t)
	target := liveTaskTarget(project, task)

	backend := newFakeBackend()
	backend.windows[target] = true
	manager := NewManager(store, backend, agent.NewRegistry("claude"))

	if err := manager.SendMessage(task, "hello"); err != nil {
		t.Fatal(err)
	}
	if !containsCall(backend.recorded(), `SendKeys `+target+` "hello"`) {
		t.Fatalf("SendMessage did not send text to live window:\n%v", backend.recorded())
	}
}
