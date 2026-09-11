package claudestream

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nashory/agx/internal/agentstream"
)

func TestIsQuickTool(t *testing.T) {
	for _, name := range []string{"Read", "Edit", "Write", "Glob", "Grep", " edit "} {
		if !IsQuickTool(name) {
			t.Errorf("IsQuickTool(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"Bash", "PowerShell", "Agent", "WebFetch", ""} {
		if IsQuickTool(name) {
			t.Errorf("IsQuickTool(%q) = true, want false", name)
		}
	}
}

func TestToolWatchdogTracksParallelToolsIndependently(t *testing.T) {
	stalled := make(chan struct{}, 1)
	watchdog := NewToolWatchdog(50*time.Millisecond, func() { stalled <- struct{}{} })
	watchdog.Observe(toolStartedEvent("tool-1", "Edit"))
	watchdog.Observe(toolStartedEvent("tool-2", "Edit"))
	watchdog.Observe(agentstream.Event{Kind: agentstream.EventCommandCompleted, ItemID: "tool-1"})

	select {
	case <-stalled:
	case <-time.After(time.Second):
		t.Fatal("watchdog did not report the still-pending tool")
	}
	if err := watchdog.Err(); !errors.Is(err, ErrToolStalled) || !strings.Contains(err.Error(), "Edit") {
		t.Fatalf("Err() = %v, want an Edit stall", err)
	}
}

func TestToolWatchdogStopsCompletedTool(t *testing.T) {
	stalled := make(chan struct{}, 1)
	watchdog := NewToolWatchdog(20*time.Millisecond, func() { stalled <- struct{}{} })
	watchdog.Observe(toolStartedEvent("tool-1", "Read"))
	watchdog.Observe(agentstream.Event{Kind: agentstream.EventCommandCompleted, ItemID: "tool-1"})
	defer watchdog.Stop()

	select {
	case <-stalled:
		t.Fatal("completed tool unexpectedly triggered the watchdog")
	case <-time.After(60 * time.Millisecond):
	}
	if err := watchdog.Err(); err != nil {
		t.Fatalf("Err() = %v, want nil", err)
	}
}

func TestToolWatchdogIgnoresLongRunningTools(t *testing.T) {
	stalled := make(chan struct{}, 1)
	watchdog := NewToolWatchdog(20*time.Millisecond, func() { stalled <- struct{}{} })
	watchdog.Observe(toolStartedEvent("tool-1", "PowerShell"))
	defer watchdog.Stop()

	select {
	case <-stalled:
		t.Fatal("long-running tool unexpectedly triggered the watchdog")
	case <-time.After(60 * time.Millisecond):
	}
}

func toolStartedEvent(id, name string) agentstream.Event {
	return agentstream.Event{
		Kind:   agentstream.EventToolStarted,
		ItemID: id,
		Tool:   &agentstream.ToolEvent{ID: id, Name: name},
	}
}
