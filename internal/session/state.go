package session

import (
	"strings"
	"time"

	"github.com/nashory/agx/internal/db"
	"github.com/nashory/agx/internal/session/script"
)

// DetectTaskStatus classifies a task by sampling its backend window: whether the
// window exists, its foreground command, and its recent output.
func DetectTaskStatus(backend Backend, target, taskID, lastOutput string, lastActivity time.Time, ignoreExitStatus bool) (db.TaskStatus, string) {
	if !backend.WindowExists(target) {
		return db.StatusOffline, ""
	}
	cmd, err := backend.PaneCurrentCommand(target)
	if err != nil {
		return db.StatusOffline, lastOutput
	}
	currentOutput, err := backend.CapturePane(target)
	if err != nil {
		return db.StatusOffline, lastOutput
	}
	if isTaskWrapperShell(cmd, taskID, ignoreExitStatus) {
		return activeOrWaitingStatus(currentOutput, lastOutput, lastActivity)
	}
	if isShell(cmd) {
		return shellStatus(taskID, currentOutput, lastOutput, ignoreExitStatus), currentOutput
	}
	return activeOrWaitingStatus(currentOutput, lastOutput, lastActivity)
}

func activeOrWaitingStatus(currentOutput, lastOutput string, lastActivity time.Time) (db.TaskStatus, string) {
	if currentOutput != lastOutput {
		return db.StatusActive, currentOutput
	}
	if time.Since(lastActivity) > 15*time.Second {
		return db.StatusWaiting, currentOutput
	}
	return db.StatusActive, currentOutput
}

func isTaskWrapperShell(cmd, taskID string, ignoreExitStatus bool) bool {
	return isShell(cmd) && (ignoreExitStatus || !script.HasTaskExitStatus(taskID))
}

func shellStatus(taskID, currentOutput, lastOutput string, ignoreExitStatus bool) db.TaskStatus {
	if code, ok := script.ReadTaskExitStatus(taskID); ok && !ignoreExitStatus {
		if code == 0 {
			return db.StatusComplete
		}
		return db.StatusOffline
	}
	if hasNewInterruptMarker(currentOutput, lastOutput) {
		return db.StatusOffline
	}
	return db.StatusComplete
}

func hasNewInterruptMarker(currentOutput, lastOutput string) bool {
	return strings.Count(currentOutput, "^C") > strings.Count(lastOutput, "^C")
}

func isShell(cmd string) bool {
	switch cmd {
	case "bash", "zsh", "sh", "fish":
		return true
	default:
		return false
	}
}
