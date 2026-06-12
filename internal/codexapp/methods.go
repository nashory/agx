package codexapp

import (
	"context"
)

const (
	MethodInitialize    = "initialize"
	MethodThreadStart   = "thread/start"
	MethodThreadResume  = "thread/resume"
	MethodTurnStart     = "turn/start"
	MethodTurnSteer     = "turn/steer"
	MethodTurnInterrupt = "turn/interrupt"
)

type InitializeResponse struct {
	UserAgent string `json:"userAgent"`
	CodexHome string `json:"codexHome"`
}

type ThreadStartResponse struct {
	Thread Thread `json:"thread"`
}

type TurnStartResponse struct {
	Turn Turn `json:"turn"`
}

type TurnSteerResponse struct {
	TurnID string `json:"turnId"`
}

type Thread struct {
	ID  string `json:"id"`
	Cwd string `json:"cwd"`
}

type Turn struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// Initialize performs the app-server handshake and returns server metadata.
func (c *Client) Initialize(ctx context.Context) (InitializeResponse, error) {
	var out InitializeResponse
	err := c.Call(ctx, MethodInitialize, map[string]any{
		"clientInfo":   map[string]any{"name": "agx", "title": "AGX", "version": "dev"},
		"capabilities": nil,
	}, &out)
	return out, err
}

// ThreadStart creates a new Codex thread rooted at cwd. When allMighty is true,
// AGX requests no approval prompts and danger-full-access sandboxing.
func (c *Client) ThreadStart(ctx context.Context, cwd string, allMighty bool) (ThreadStartResponse, error) {
	params := map[string]any{
		"cwd": cwd,
	}
	if allMighty {
		params["approvalPolicy"] = "never"
		params["sandboxPolicy"] = map[string]any{"type": "dangerFullAccess"}
	}
	var out ThreadStartResponse
	err := c.Call(ctx, MethodThreadStart, params, &out)
	return out, err
}

// ThreadResume reconnects to an existing Codex thread by ID.
func (c *Client) ThreadResume(ctx context.Context, threadID string) (ThreadStartResponse, error) {
	var out ThreadStartResponse
	err := c.Call(ctx, MethodThreadResume, map[string]any{"threadId": threadID}, &out)
	return out, err
}

// TurnStart sends a new user input turn to a thread.
func (c *Client) TurnStart(ctx context.Context, threadID, text, cwd string, allMighty bool) (TurnStartResponse, error) {
	params := map[string]any{
		"threadId": threadID,
		"input":    []any{textInput(text)},
	}
	if cwd != "" {
		params["cwd"] = cwd
	}
	if allMighty {
		params["approvalPolicy"] = "never"
		params["sandboxPolicy"] = map[string]any{"type": "dangerFullAccess"}
	}
	var out TurnStartResponse
	err := c.Call(ctx, MethodTurnStart, params, &out)
	return out, err
}

// TurnSteer appends user input to an in-flight turn.
func (c *Client) TurnSteer(ctx context.Context, threadID, turnID, text string) (TurnSteerResponse, error) {
	var out TurnSteerResponse
	err := c.Call(ctx, MethodTurnSteer, map[string]any{
		"threadId":       threadID,
		"expectedTurnId": turnID,
		"input":          []any{textInput(text)},
	}, &out)
	return out, err
}

// TurnInterrupt requests cancellation of an in-flight turn.
func (c *Client) TurnInterrupt(ctx context.Context, threadID, turnID string) error {
	return c.Call(ctx, MethodTurnInterrupt, map[string]any{
		"threadId": threadID,
		"turnId":   turnID,
	}, nil)
}

func textInput(text string) map[string]any {
	return map[string]any{
		"type":          "text",
		"text":          text,
		"text_elements": []any{},
	}
}
