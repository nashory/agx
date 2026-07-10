package codexapp

import (
	"context"
	"errors"
	"strings"
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
	var out ThreadStartResponse
	err := c.callWithAllMightyOverride(ctx, MethodThreadStart, params, allMighty, &out)
	return out, err
}

// callWithAllMightyOverride issues an app-server call, adding the no-approval /
// danger-full-access override when allMighty. A managed Codex config (for example
// a corp-managed install) can lock approval_policy/sandbox and reject the
// override; in that case AGX retries without it and lets the managed config
// govern, since such installs already sandbox externally.
func (c *Client) callWithAllMightyOverride(ctx context.Context, method string, params map[string]any, allMighty bool, out any) error {
	if allMighty {
		params["approvalPolicy"] = "never"
		params["sandboxPolicy"] = map[string]any{"type": "dangerFullAccess"}
	}
	err := c.Call(ctx, method, params, out)
	if err != nil && allMighty && isManagedOverrideRejected(err) {
		delete(params, "approvalPolicy")
		delete(params, "sandboxPolicy")
		err = c.Call(ctx, method, params, out)
	}
	return err
}

// isManagedOverrideRejected reports whether err is the app-server rejecting AGX's
// approval/sandbox override because a managed config controls those settings.
func isManagedOverrideRejected(err error) bool {
	var ce *CallError
	if !errors.As(err, &ce) || ce.Code != -32600 {
		return false
	}
	msg := strings.ToLower(ce.Message)
	return strings.Contains(msg, "thread settings override") ||
		strings.Contains(msg, "approval_policy") ||
		strings.Contains(msg, "sandbox")
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
	var out TurnStartResponse
	err := c.callWithAllMightyOverride(ctx, MethodTurnStart, params, allMighty, &out)
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
