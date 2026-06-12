package session

import (
	"os"
	"testing"
	"time"

	"github.com/nashory/agx/internal/db"
	"github.com/nashory/agx/internal/session/script"
)

func TestShellStatusUsesExitCode(t *testing.T) {
	taskID := "status-test-success"
	path := script.TaskExitStatusPath(taskID)
	t.Cleanup(func() { _ = os.Remove(path) })
	if err := os.WriteFile(path, []byte("0\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if got := shellStatus(taskID, "", "", false); got != db.StatusComplete {
		t.Fatalf("shellStatus() = %s, want complete", got)
	}

	if err := os.WriteFile(path, []byte("130\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := shellStatus(taskID, "", "", false); got != db.StatusOffline {
		t.Fatalf("shellStatus() = %s, want offline", got)
	}
}

func TestShellStatusFallsBackToInterruptMarker(t *testing.T) {
	if got := shellStatus("status-test-missing", "running\n^C\n$ ", "running\n", false); got != db.StatusOffline {
		t.Fatalf("shellStatus() = %s, want offline", got)
	}
	if got := shellStatus("status-test-missing", "done\n$ ", "done\n", false); got != db.StatusComplete {
		t.Fatalf("shellStatus() = %s, want complete", got)
	}
}

func TestTaskWrapperShellIsLiveUntilExitStatusExists(t *testing.T) {
	taskID := "status-test-wrapper"
	path := script.TaskExitStatusPath(taskID)
	t.Cleanup(func() { _ = os.Remove(path) })
	_ = os.Remove(path)

	for _, cmd := range []string{"sh", "zsh", "bash"} {
		if !isTaskWrapperShell(cmd, taskID, false) {
			t.Fatalf("isTaskWrapperShell(%q) = false, want true before exit status exists", cmd)
		}
	}
	if err := os.WriteFile(path, []byte("0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if isTaskWrapperShell("sh", taskID, false) {
		t.Fatal("isTaskWrapperShell() = true, want false after exit status exists")
	}
	if !isTaskWrapperShell("sh", taskID, true) {
		t.Fatal("isTaskWrapperShell() = false, want true when exit status is ignored")
	}
}

func TestActiveOrWaitingStatus(t *testing.T) {
	lastActivity := time.Now().Add(-16 * time.Second)
	if status, output := activeOrWaitingStatus("new", "old", lastActivity); status != db.StatusActive || output != "new" {
		t.Fatalf("changed output status=%s output=%q, want active/new", status, output)
	}
	if status, output := activeOrWaitingStatus("same", "same", lastActivity); status != db.StatusWaiting || output != "same" {
		t.Fatalf("stale output status=%s output=%q, want waiting/same", status, output)
	}
	if status, output := activeOrWaitingStatus("same", "same", time.Now()); status != db.StatusActive || output != "same" {
		t.Fatalf("recent output status=%s output=%q, want active/same", status, output)
	}
}

func TestShellStatusCanIgnoreExitStatus(t *testing.T) {
	taskID := "status-test-ignore"
	path := script.TaskExitStatusPath(taskID)
	t.Cleanup(func() { _ = os.Remove(path) })
	if err := os.WriteFile(path, []byte("130\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := shellStatus(taskID, "done\n$ ", "done\n", true); got != db.StatusComplete {
		t.Fatalf("shellStatus(ignore exit) = %s, want complete fallback", got)
	}
}
