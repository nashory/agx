package runtime

import (
	"testing"

	"github.com/nashory/agx/internal/agentstream"
)

func TestPublishAgentEventToSubscriberKeepsCriticalEvents(t *testing.T) {
	ch := make(chan agentstream.Event, 1)
	ch <- agentstream.Event{Kind: agentstream.EventAssistantDelta, Text: "queued"}

	publishAgentEventToSubscriber(ch, agentstream.Event{Kind: agentstream.EventTurnCompleted})

	event := <-ch
	if event.Kind != agentstream.EventTurnCompleted {
		t.Fatalf("event.Kind = %s, want turn_completed", event.Kind)
	}
}

func TestPublishAgentEventToSubscriberDropsNonCriticalWhenFull(t *testing.T) {
	ch := make(chan agentstream.Event, 1)
	ch <- agentstream.Event{Kind: agentstream.EventAssistantDelta, Text: "queued"}

	publishAgentEventToSubscriber(ch, agentstream.Event{Kind: agentstream.EventAssistantDelta, Text: "dropped"})

	event := <-ch
	if event.Text != "queued" {
		t.Fatalf("event.Text = %q, want existing queued event", event.Text)
	}
}
