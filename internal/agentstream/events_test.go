package agentstream

import "testing"

func TestEventValidateRequiresTaskAndKind(t *testing.T) {
	if err := (Event{Kind: EventTurnStarted}).Validate(); err == nil {
		t.Fatal("expected missing task id error")
	}
	if err := (Event{TaskID: "task-1"}).Validate(); err == nil {
		t.Fatal("expected missing kind error")
	}
	if err := (Event{TaskID: "task-1", Kind: EventTurnStarted}).Validate(); err != nil {
		t.Fatalf("expected valid event: %v", err)
	}
}

func TestDeduperPrefersCursorThenID(t *testing.T) {
	deduper := NewDeduper("cursor-1")
	if deduper.Accept(Event{TaskID: "task-1", Kind: EventAssistantDelta, Cursor: "cursor-1"}) {
		t.Fatal("expected existing cursor to be rejected")
	}
	if !deduper.Accept(Event{TaskID: "task-1", Kind: EventAssistantDelta, ID: "event-1"}) {
		t.Fatal("expected new event id to be accepted")
	}
	if deduper.Accept(Event{TaskID: "task-1", Kind: EventAssistantDelta, ID: "event-1"}) {
		t.Fatal("expected repeated event id to be rejected")
	}
}

func TestDeduperBuildsStableFallbackKey(t *testing.T) {
	deduper := NewDeduper()
	event := Event{TaskID: "task-1", Kind: EventAssistantMessage, TurnID: "turn-1", ItemID: "item-1", Text: "hello"}
	if !deduper.Accept(event) {
		t.Fatal("expected first event to be accepted")
	}
	if deduper.Accept(event) {
		t.Fatal("expected duplicate fallback key to be rejected")
	}
}
