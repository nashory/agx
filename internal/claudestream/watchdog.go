package claudestream

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/nashory/agx/internal/agentstream"
)

const DefaultQuickToolTimeout = 5 * time.Minute

var ErrToolStalled = errors.New("Claude tool stalled")

// ToolWatchdog bounds tools that should always return quickly. Commands and
// delegated agents are deliberately excluded because they may run for hours.
type ToolWatchdog struct {
	timeout  time.Duration
	onStall  func()
	mu       sync.Mutex
	pending  map[string]*time.Timer
	stopped  bool
	stallErr error
}

func NewToolWatchdog(timeout time.Duration, onStall func()) *ToolWatchdog {
	return &ToolWatchdog{
		timeout: timeout,
		onStall: onStall,
		pending: make(map[string]*time.Timer),
	}
}

func IsQuickTool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "read", "edit", "write", "glob", "grep":
		return true
	default:
		return false
	}
}

func (w *ToolWatchdog) Observe(event agentstream.Event) {
	switch event.Kind {
	case agentstream.EventToolStarted:
		if event.Tool != nil && IsQuickTool(event.Tool.Name) {
			w.start(toolEventID(event), event.Tool.Name)
		}
	case agentstream.EventCommandCompleted, agentstream.EventToolCompleted:
		w.complete(toolEventID(event))
	case agentstream.EventTurnCompleted, agentstream.EventError, agentstream.EventInterrupted:
		w.Stop()
	}
}

func (w *ToolWatchdog) Stop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.stopped = true
	for id, timer := range w.pending {
		timer.Stop()
		delete(w.pending, id)
	}
}

func (w *ToolWatchdog) Err() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.stallErr
}

func (w *ToolWatchdog) start(id, name string) {
	id = strings.TrimSpace(id)
	if id == "" || w.timeout <= 0 {
		return
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopped || w.stallErr != nil {
		return
	}
	if previous := w.pending[id]; previous != nil {
		previous.Stop()
	}
	var timer *time.Timer
	timer = time.AfterFunc(w.timeout, func() {
		w.stall(id, strings.TrimSpace(name), timer)
	})
	w.pending[id] = timer
}

func (w *ToolWatchdog) complete(id string) {
	id = strings.TrimSpace(id)
	if id == "" {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if timer := w.pending[id]; timer != nil {
		timer.Stop()
		delete(w.pending, id)
	}
}

func (w *ToolWatchdog) stall(id, name string, timer *time.Timer) {
	w.mu.Lock()
	if w.stopped || w.stallErr != nil || w.pending[id] != timer {
		w.mu.Unlock()
		return
	}
	delete(w.pending, id)
	w.stopped = true
	w.stallErr = fmt.Errorf("%w: %s produced no result within %s", ErrToolStalled, name, w.timeout)
	for pendingID, pendingTimer := range w.pending {
		pendingTimer.Stop()
		delete(w.pending, pendingID)
	}
	onStall := w.onStall
	w.mu.Unlock()

	if onStall != nil {
		onStall()
	}
}

func toolEventID(event agentstream.Event) string {
	if id := strings.TrimSpace(event.ItemID); id != "" {
		return id
	}
	if event.Tool != nil {
		return strings.TrimSpace(event.Tool.ID)
	}
	if event.Command != nil {
		return strings.TrimSpace(event.Command.ID)
	}
	return ""
}
