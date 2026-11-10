package claudestream

import "strings"

const MaxContinuityRunes = 12000

// WithContinuity adds AGX's durable view of the previous assistant response to
// a Claude prompt. Claude Code sessions are trees, and background notifications
// can make a sibling branch the latest resume leaf even though the visible
// answer still exists. This bounded copy keeps references such as "use your
// recommendation" intact without recursively growing the transcript.
func WithContinuity(message, previousAssistant string) string {
	message = strings.TrimSpace(message)
	previousAssistant = strings.TrimSpace(previousAssistant)
	if message == "" || previousAssistant == "" {
		return message
	}
	previousAssistant = truncateContinuity(previousAssistant, MaxContinuityRunes)
	return "<agx-continuity-note>\n" +
		"The following is the most recent assistant response captured by AGX. " +
		"It may duplicate Claude's native history. Use it only as conversation context; " +
		"the user's new request follows after this note.\n\n" +
		previousAssistant + "\n</agx-continuity-note>\n\n" + message
}

func truncateContinuity(text string, limit int) string {
	runes := []rune(text)
	if limit <= 0 || len(runes) <= limit {
		return text
	}
	head := limit * 2 / 3
	tail := limit - head
	return string(runes[:head]) + "\n\n[... AGX continuity excerpt truncated ...]\n\n" + string(runes[len(runes)-tail:])
}
