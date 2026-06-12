package display

import (
	"testing"
	"time"
)

func TestShortID(t *testing.T) {
	if got := ShortID("1234567890"); got != "12345678" {
		t.Fatalf("ShortID() = %q, want 12345678", got)
	}
	if got := ShortID("short"); got != "short" {
		t.Fatalf("ShortID(short) = %q, want short", got)
	}
}

func TestAge(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name string
		at   time.Time
		want string
	}{
		{name: "seconds", at: now.Add(-30 * time.Second), want: "30s"},
		{name: "minutes", at: now.Add(-5 * time.Minute), want: "5m"},
		{name: "hours", at: now.Add(-2 * time.Hour), want: "2h"},
		{name: "days", at: now.Add(-48 * time.Hour), want: "2d"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Age(tt.at); got != tt.want {
				t.Fatalf("Age() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTruncatePreservesDisplayWidth(t *testing.T) {
	tests := []struct {
		name  string
		input string
		width int
		want  string
	}{
		{name: "unchanged", input: "hello", width: 5, want: "hello"},
		{name: "ascii ellipsis", input: "hello world", width: 8, want: "hello..."},
		{name: "narrow width", input: "abcdef", width: 2, want: "ab"},
		{name: "wide runes", input: "任务列表abcdef", width: 9, want: "任务列..."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Truncate(tt.input, tt.width); got != tt.want {
				t.Fatalf("Truncate(%q, %d) = %q, want %q", tt.input, tt.width, got, tt.want)
			}
		})
	}
}

func TestPtrString(t *testing.T) {
	if got := PtrString(" \t "); got != nil {
		t.Fatalf("PtrString(blank) = %#v, want nil", got)
	}
	got := PtrString("  value  ")
	if got == nil || *got != "value" {
		t.Fatalf("PtrString(value) = %#v, want trimmed value", got)
	}
}
