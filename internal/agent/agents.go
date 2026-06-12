package agent

func KnownAgents() []Agent {
	return []Agent{
		{Name: "claude", Command: "claude", Description: "Claude Code"},
		{Name: "codex", Command: "codex", Description: "OpenAI Codex CLI"},
		{Name: "gemini", Command: "gemini", Description: "Gemini CLI", Env: map[string]string{"GEMINI_TRUST_WORKSPACE": "true"}},
		{Name: "cursor", Command: "agent", Description: "Cursor Agent"},
		{Name: "copilot", Command: "copilot", Description: "GitHub Copilot CLI"},
		{Name: "opencode", Command: "opencode", Description: "OpenCode"},
	}
}
