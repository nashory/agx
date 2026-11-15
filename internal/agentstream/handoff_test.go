package agentstream

import (
	"strings"
	"testing"
)

func TestBuildConversationHandoffPreservesRolesAndOrder(t *testing.T) {
	got := BuildConversationHandoff("continue", []ConversationMessage{
		{Role: "user", Text: "first"},
		{Role: "assistant", Text: "second"},
	}, 100)
	if strings.Index(got, "[user]\nfirst") > strings.Index(got, "[assistant]\nsecond") {
		t.Fatalf("handoff order is wrong: %q", got)
	}
	if !strings.HasSuffix(got, "[current user request]\ncontinue") {
		t.Fatalf("handoff current request missing: %q", got)
	}
}

func TestBuildConversationHandoffKeepsNewestHistoryWithinBudget(t *testing.T) {
	got := BuildConversationHandoff("next", []ConversationMessage{
		{Role: "assistant", Text: "oldest"},
		{Role: "user", Text: strings.Repeat("x", 8)},
		{Role: "assistant", Text: "newest"},
	}, 10)
	if strings.Contains(got, "[assistant]\noldest") || !strings.Contains(got, "[assistant]\nnewest") {
		t.Fatalf("handoff budget selected wrong history: %q", got)
	}
}
