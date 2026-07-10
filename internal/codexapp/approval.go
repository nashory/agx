package codexapp

// Approval request methods the Codex app-server sends to the client. Under an
// on-request approval policy (for example a managed config that forbids the
// "never" policy AGX prefers) Codex asks the client to approve each command or
// file change, and the turn blocks until the client responds.
const (
	NotifyCommandApprovalRequest    = "item/commandExecution/requestApproval"
	NotifyFileChangeApprovalRequest = "item/fileChange/requestApproval"
)

// ReviewDecision is the client's answer to a Codex approval request. Values
// serialize to the snake_case forms the app-server expects.
type ReviewDecision string

const (
	DecisionApproved ReviewDecision = "approved"
	DecisionDenied   ReviewDecision = "denied"
	DecisionAbort    ReviewDecision = "abort"
)

// IsApprovalRequest reports whether the notification is a Codex approval request
// the client must answer to unblock the turn.
func IsApprovalRequest(n Notification) bool {
	if n.RequestID == "" {
		return false
	}
	switch n.Method {
	case NotifyCommandApprovalRequest, NotifyFileChangeApprovalRequest:
		return true
	default:
		return false
	}
}

// ApproveRequest answers a Codex approval request with the given decision.
func (c *Client) ApproveRequest(n Notification, decision ReviewDecision) error {
	return c.Respond(n, map[string]any{"decision": string(decision)})
}
