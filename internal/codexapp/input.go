package codexapp

import (
	"encoding/json"
	"strings"
)

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

// UserInputRequest is a structured question emitted by the Codex app-server.
type UserInputRequest struct {
	ThreadID  string              `json:"threadId"`
	TurnID    string              `json:"turnId"`
	ItemID    string              `json:"itemId"`
	Questions []UserInputQuestion `json:"questions"`
}

type UserInputQuestion struct {
	ID       string            `json:"id"`
	Header   string            `json:"header"`
	Question string            `json:"question"`
	Options  []UserInputOption `json:"options"`
	IsOther  bool              `json:"isOther"`
	IsSecret bool              `json:"isSecret"`
}

type UserInputOption struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

func ParseUserInputRequest(n Notification) (UserInputRequest, bool) {
	var request UserInputRequest
	if n.Method != NotifyUserInputRequest || n.RequestID == "" || json.Unmarshal(n.Params, &request) != nil || len(request.Questions) == 0 {
		return request, false
	}
	request.ThreadID = strings.TrimSpace(request.ThreadID)
	return request, true
}

// AnswerInputRequest resumes the blocked Codex turn with answers keyed by the
// question IDs supplied by the app-server.
func (c *Client) AnswerInputRequest(n Notification, answers map[string][]string) error {
	typed := make(map[string]any, len(answers))
	for id, values := range answers {
		typed[id] = map[string]any{"answers": values}
	}
	return c.Respond(n, map[string]any{"answers": typed})
}
