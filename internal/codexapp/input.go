package codexapp

const (
	NotifyUserInputRequest      = "item/tool/requestUserInput"
	NotifyMCPElicitationRequest = "mcpServer/elicitation/request"
)

// IsInputRequest reports whether the app-server is waiting for user input that
// AGX cannot provide interactively through its current Discord transport.
func IsInputRequest(n Notification) bool {
	if n.RequestID == "" {
		return false
	}
	switch n.Method {
	case NotifyUserInputRequest, NotifyMCPElicitationRequest:
		return true
	default:
		return false
	}
}

// CancelInputRequest unblocks a headless Codex turn without inventing a user
// choice. The model can then surface the missing question in its normal reply.
func (c *Client) CancelInputRequest(n Notification) error {
	if n.Method == NotifyMCPElicitationRequest {
		return c.Respond(n, map[string]any{"action": "cancel"})
	}
	return c.Respond(n, map[string]any{"answers": map[string]any{}})
}
