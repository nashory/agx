package claudestream

import (
	"strings"
	"testing"
)

func TestWithContinuitySeparatesPreviousResponseAndNewMessage(t *testing.T) {
	got := WithContinuity("use option B", "A: first\nB: recommended")
	for _, want := range []string{"<agx-continuity-note>", "B: recommended", "</agx-continuity-note>\n\nuse option B"} {
		if !strings.Contains(got, want) {
			t.Fatalf("WithContinuity() = %q, want %q", got, want)
		}
	}
}

func TestWithContinuityBoundsUnicodeExcerpt(t *testing.T) {
	previous := strings.Repeat("가", MaxContinuityRunes+100)
	got := WithContinuity("next", previous)
	if strings.Count(got, "가") != MaxContinuityRunes {
		t.Fatalf("continuity rune count = %d, want %d", strings.Count(got, "가"), MaxContinuityRunes)
	}
	if !strings.Contains(got, "continuity excerpt truncated") {
		t.Fatal("WithContinuity() did not mark truncation")
	}
}

func TestWithContinuityLeavesMessageAloneWithoutHistory(t *testing.T) {
	if got := WithContinuity(" hello ", " "); got != "hello" {
		t.Fatalf("WithContinuity() = %q, want hello", got)
	}
}
