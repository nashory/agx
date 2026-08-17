package agent

func KnownAgents() []Agent {
	return []Agent{
		{Name: "claude", Command: "claude", Description: "Claude Code"},
		{Name: "codex", Command: "codex", Description: "OpenAI Codex CLI"},
		{Name: "gemini", Command: "gemini", Description: "Gemini CLI", Env: map[string]string{"GEMINI_TRUST_WORKSPACE": "true"}},
		{Name: "muse", Command: "muse", Description: "Muse Code"},
	}
}
