package agentstream

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestEventQueuePreservesCriticalBurst(t *testing.T) {
	queue := NewEventQueue(context.Background(), 2)
	t.Cleanup(queue.Close)
	for i := 0; i < 20; i++ {
		queue.Publish(Event{Kind: EventAssistantMessage, Text: fmt.Sprint(i)}, true)
	}
	for i := 0; i < 20; i++ {
		select {
		case event := <-queue.Events():
			if event.Text != fmt.Sprint(i) {
				t.Fatalf("event %d = %q", i, event.Text)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for critical event %d", i)
		}
	}
}

func TestEventQueueBoundsIntermediateEvents(t *testing.T) {
	queue := NewEventQueue(context.Background(), 2)
	t.Cleanup(queue.Close)
	for i := 0; i < 100; i++ {
		queue.Publish(Event{Kind: EventAssistantDelta, Text: fmt.Sprint(i)}, false)
	}
	queue.mu.Lock()
	pending := len(queue.pending)
	queue.mu.Unlock()
	if pending > 2 {
		t.Fatalf("pending intermediate events = %d, want at most 2", pending)
	}
}
