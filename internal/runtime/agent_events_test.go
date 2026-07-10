package runtime

import (
	"strings"
	"testing"

	"github.com/nashory/agx/internal/agentstream"
	"github.com/nashory/agx/internal/codexapp"
)

func TestEnrichCodexError(t *testing.T) {
	if got := enrichCodexError("boom", ""); got != "boom" {
		t.Fatalf("no stderr: got %q, want unchanged message", got)
	}
	got := enrichCodexError(codexapp.ErrorNoDetail, "panic: nil map\ngoroutine 1")
	if !strings.Contains(got, "panic: nil map") || strings.Contains(got, codexapp.ErrorNoDetail) {
		t.Fatalf("fallback+stderr: got %q, want stderr replacing the no-detail fallback", got)
	}
	got = enrichCodexError("stream closed", "auth token expired")
	if !strings.Contains(got, "stream closed") || !strings.Contains(got, "auth token expired") {
		t.Fatalf("message+stderr: got %q, want both", got)
	}
	if got := enrichCodexError("dup context", "dup context"); got != "dup context" {
		t.Fatalf("duplicate stderr: got %q, want no duplication", got)
	}
}

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
