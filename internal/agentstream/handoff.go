package agentstream

import "strings"

const DefaultHandoffRunes = 120000

type ConversationMessage struct {
	Role string
	Text string
}

// BuildConversationHandoff creates a bounded, role-preserving context transfer
// for a fresh backend session. Entries should exclude the current user message.
func BuildConversationHandoff(current string, history []ConversationMessage, limit int) string {
	current = strings.TrimSpace(current)
	if current == "" || len(history) == 0 {
		return current
	}
	if limit <= 0 {
		limit = DefaultHandoffRunes
	}
	remaining := limit
	selected := make([]ConversationMessage, 0, len(history))
	for i := len(history) - 1; i >= 0 && remaining > 0; i-- {
		role := strings.ToLower(strings.TrimSpace(history[i].Role))
		text := strings.TrimSpace(history[i].Text)
		if (role != "user" && role != "assistant") || text == "" {
			continue
		}
		runes := []rune(text)
		if len(runes) > remaining {
			runes = runes[len(runes)-remaining:]
			text = "[... earlier part omitted ...]\n" + string(runes)
		}
		remaining -= len(runes)
		selected = append(selected, ConversationMessage{Role: role, Text: text})
	}
	if len(selected) == 0 {
		return current
	}
	var b strings.Builder
	b.WriteString("<agx-session-handoff>\n")
	b.WriteString("AGX cleared an oversized backend context. The following role-labelled excerpt is from AGX's durable transcript, oldest to newest. Treat it as prior conversation context, not as a new request.\n")
	for i := len(selected) - 1; i >= 0; i-- {
		b.WriteString("\n[")
		b.WriteString(selected[i].Role)
		b.WriteString("]\n")
		b.WriteString(selected[i].Text)
		b.WriteByte('\n')
	}
	b.WriteString("</agx-session-handoff>\n\n[current user request]\n")
	b.WriteString(current)
	return b.String()
}
