package runtime

import (
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// Event is the envelope streamed to desktop and CLI clients for runtime,
// project, task, and Discord changes. Payload is the JSON representation of the
// domain object for Type and may be ignored by clients that only need a refresh
// signal.
type Event struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	Timestamp time.Time       `json:"ts"`
	ProjectID string          `json:"project_id,omitempty"`
	TaskID    string          `json:"task_id,omitempty"`
	Seq       uint64          `json:"seq"`
	Payload   json.RawMessage `json:"payload"`
}

// EventBus is an in-process best-effort pub/sub bus for runtime API events.
// Slow subscribers are skipped rather than blocking task or Discord operations.
type EventBus struct {
	seq         atomic.Uint64
	mu          sync.Mutex
	subscribers map[chan Event]struct{}
}

// NewEventBus creates an empty event bus.
func NewEventBus() *EventBus {
	return &EventBus{subscribers: map[chan Event]struct{}{}}
}

// Publish sends an event to all current subscribers. Payload marshal failures
// are converted into an error payload so the stream shape remains valid JSON.
func (b *EventBus) Publish(eventType string, payload any) {
	if b == nil {
		return
	}
	seq := b.seq.Add(1)
	data, err := json.Marshal(payload)
	if err != nil {
		data = []byte(`{"error":"event payload marshal failed"}`)
	}
	event := Event{
		ID:        fmt.Sprintf("%016d", seq),
		Type:      eventType,
		Timestamp: time.Now().UTC(),
		Seq:       seq,
		Payload:   data,
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subscribers {
		select {
		case ch <- event:
		default:
		}
	}
}

// Subscribe returns a buffered event channel and a cancellation function. The
// caller must invoke the cancel function to remove and close the subscriber.
func (b *EventBus) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 32)
	b.mu.Lock()
	b.subscribers[ch] = struct{}{}
	b.mu.Unlock()
	cancel := func() {
		b.mu.Lock()
		if _, ok := b.subscribers[ch]; ok {
			delete(b.subscribers, ch)
			close(ch)
		}
		b.mu.Unlock()
	}
	return ch, cancel
}
