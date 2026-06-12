package discord

import (
	"strings"
	"testing"
)

func TestFormatLogOutputMessageStripsANSIAndHandlesEmpty(t *testing.T) {
	if got := FormatLogOutputMessage("\x1b]0;title\x07\x1b[31mhello\x1b[0m"); got != "```\nhello\n```" {
		t.Fatalf("FormatLogOutputMessage = %q", got)
	}
	if got := FormatLogOutputMessage(" \n\t"); got != "```\n(no output captured)\n```" {
		t.Fatalf("empty FormatLogOutputMessage = %q, want no-output block", got)
	}
}

func TestFormatLogOutputMessageDropsAgentStartupNoise(t *testing.T) {
	input := strings.Join([]string{
		"Claude Code Enterprise (https://example.com/claude-code)",
		"--dangerously-disable-osx-sandbox flag is enabled, please use caution!",
		"Using AI Gateway (Vertex upstream)",
		"SessionStart:startup says: Discovered 10 skills (/llm-rules-info for more)",
		"actual useful output",
		">> accept edits on (shift+tab to cycle) · esc to interrupt",
	}, "\n")
	got := FormatLogOutputMessage(input)
	if strings.Contains(got, "Claude Code Enterprise") || strings.Contains(got, "Using AI Gateway") || strings.Contains(got, "accept edits") {
		t.Fatalf("startup noise was not removed: %q", got)
	}
	if !strings.Contains(got, "actual useful output") {
		t.Fatalf("useful output was removed: %q", got)
	}
}

func TestFormatLogOutputMessageDropsTerminalRedrawNoise(t *testing.T) {
	input := strings.Join([]string{
		"헤이",
		"› 헤이",
		"* Noodling...",
		">",
		">>accepteditson(shift+tabtocycle)·esctointerrupt",
		"*",
		"+",
		".",
		"(3s·↓1tokens)",
		"2",
		"4",
		"5",
		"●안녕하세요! 뭘 도와드릴까요?😀",
		"작업 내용 있으시면 말씀해 주세요.",
		"+ Boondoggling… (4s · ↓ 10 tokens)",
		"?2026;0$y",
		"*running stop hooks…0/3-4s·↓25tokens)",
		"19",
		"32",
		"*Crushed for 4s",
		"← for agents",
	}, "\n")
	got := FormatLogOutputMessage(input)
	for _, noise := range []string{"Noodling", "accepteditson", "tokens", "?2026", "Crushed", "for agents", "\n2\n"} {
		if strings.Contains(got, noise) {
			t.Fatalf("terminal noise %q was not removed: %q", noise, got)
		}
	}
	if !strings.Contains(got, "안녕하세요") || !strings.Contains(got, "작업 내용") {
		t.Fatalf("assistant output was removed: %q", got)
	}
}

func TestFormatLogOutputMessageEscapesCodeFence(t *testing.T) {
	got := FormatLogOutputMessage("before ``` after")
	if strings.Contains(got, "before ``` after") {
		t.Fatalf("code fence was not escaped: %q", got)
	}
}

func TestFormatLogOutputMessageTruncatesLargeLogs(t *testing.T) {
	first := strings.Repeat("a", 1000)
	second := strings.Repeat("b", 1000)
	message := FormatLogOutputMessage(first + "\n" + second)
	if !strings.Contains(message, "log output truncated") {
		t.Fatalf("message did not include truncation notice: %q", message)
	}
}

func TestFormatLogOutputMessageDoesNotSplitUTF8Runes(t *testing.T) {
	message := FormatLogOutputMessage(strings.Repeat("한", 1000))
	if strings.ToValidUTF8(message, "") != message {
		t.Fatalf("message contains invalid UTF-8: %q", message)
	}
}
