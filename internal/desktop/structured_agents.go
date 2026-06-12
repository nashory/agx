package desktop

import "strings"

func isCodexAgentName(agent string) bool {
	return strings.EqualFold(strings.TrimSpace(agent), "codex")
}

func isClaudeAgentName(agent string) bool {
	return strings.EqualFold(strings.TrimSpace(agent), "claude")
}

func isStructuredAgentName(agent string) bool {
	return isCodexAgentName(agent) || isClaudeAgentName(agent)
}
