package museapp

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
)

func IsSessionNotFound(err error) bool {
	var callErr *CallError
	return errors.As(err, &callErr) && callErr.Code == -32020
}

func IsMethodNotFound(err error) bool {
	var callErr *CallError
	return errors.As(err, &callErr) && callErr.Code == -32601
}

const (
	MethodInitialize    = "initialize"
	MethodInitialized   = "initialized"
	MethodSessionStart  = "session/start"
	MethodSessionResume = "session/resume"
	MethodTurnStart     = "turn/start"
	MethodTurnSteer     = "turn/steer"
	MethodTurnInterrupt = "turn/interrupt"
)

type InitializeResponse struct {
	ServerInfo struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"serverInfo"`
	Schema struct {
		Version     int    `json:"version"`
		Fingerprint string `json:"fingerprint"`
	} `json:"schema"`
}

type Session struct {
	ID           string  `json:"sessionId"`
	Status       string  `json:"status"`
	ActiveTurnID *string `json:"activeTurnId"`
}

type SessionResponse struct {
	Session    Session `json:"session"`
	ViewCursor string  `json:"viewCursor"`
}

type TurnResponse struct {
	CommandID   string `json:"commandId"`
	TurnID      string `json:"turnId"`
	Status      string `json:"status"`
	Disposition string `json:"disposition"`
}

type ViewPageResponse struct {
	Events     []Notification `json:"events"`
	NextCursor *string        `json:"nextCursor"`
}

func NewCommandID() string {
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.NewString()
	}
	return id.String()
}

func (c *Client) Initialize(ctx context.Context) (InitializeResponse, error) {
	var out InitializeResponse
	err := c.Call(ctx, MethodInitialize, map[string]any{
		"clientInfo": map[string]any{"name": "agx", "title": "AGX", "version": "dev"},
		"capabilities": map[string]any{
			"requestedCapabilities": []string{"userShell"},
			"userInputDialogs":      true,
		},
	}, &out)
	if err == nil {
		err = c.Notify(MethodInitialized, nil)
	}
	return out, err
}

func (c *Client) SessionStart(ctx context.Context, sessionID, workspace string, allMighty bool) (SessionResponse, error) {
	mode := "onRequest"
	if allMighty {
		mode = "allowAll"
	}
	params := map[string]any{
		"commandId":     NewCommandID(),
		"sessionId":     sessionID,
		"workspaceRoot": workspace,
		"approvalMode":  mode,
	}
	var out SessionResponse
	err := c.Call(ctx, MethodSessionStart, params, &out)
	return out, err
}

func (c *Client) SessionResume(ctx context.Context, sessionID, cursor string) (SessionResponse, error) {
	params := map[string]any{"commandId": NewCommandID(), "sessionId": sessionID}
	if strings.TrimSpace(cursor) != "" {
		params["cursor"] = cursor
	}
	var out SessionResponse
	err := c.Call(ctx, MethodSessionResume, params, &out)
	return out, err
}

func (c *Client) ViewPage(ctx context.Context, sessionID, cursor string, limit int) (ViewPageResponse, error) {
	params := map[string]any{"sessionId": sessionID, "cursor": cursor, "direction": "forward", "limit": limit}
	var out ViewPageResponse
	err := c.Call(ctx, "view/page", params, &out)
	return out, err
}

func (c *Client) ViewSubscribe(ctx context.Context, sessionID, after string) error {
	return c.Call(ctx, "view/subscribe", map[string]any{"sessionId": sessionID, "after": after}, nil)
}

func (c *Client) ViewUnsubscribe(ctx context.Context, sessionID string) error {
	return c.Call(ctx, "view/unsubscribe", map[string]any{"sessionId": sessionID}, nil)
}

func (c *Client) TurnStart(ctx context.Context, sessionID, text, ifBusy string) (TurnResponse, error) {
	params := map[string]any{
		"commandId": NewCommandID(),
		"sessionId": sessionID,
		"input":     []any{map[string]any{"type": "text", "text": text}},
	}
	if strings.TrimSpace(ifBusy) != "" {
		params["ifBusy"] = ifBusy
	}
	var out TurnResponse
	err := c.Call(ctx, MethodTurnStart, params, &out)
	return out, err
}

func (c *Client) TurnSteer(ctx context.Context, sessionID, turnID, text string) (TurnResponse, error) {
	var out TurnResponse
	err := c.Call(ctx, MethodTurnSteer, map[string]any{
		"commandId":      NewCommandID(),
		"sessionId":      sessionID,
		"expectedTurnId": turnID,
		"input":          []any{map[string]any{"type": "text", "text": text}},
	}, &out)
	return out, err
}

func (c *Client) ApprovalDecide(ctx context.Context, sessionID, approvalID string, requirement any, choiceID string) error {
	return c.Call(ctx, "approval/decide", map[string]any{
		"commandId": NewCommandID(), "sessionId": sessionID,
		"approvalId": approvalID, "requirementId": requirement, "choiceId": choiceID,
	}, nil)
}

type UserInputAnswer struct {
	QuestionID string
	Value      string
	Values     []string
	FreeText   bool
}

func (c *Client) UserInputAnswer(ctx context.Context, sessionID, userInputID string, answers []UserInputAnswer) error {
	values := make([]any, 0, len(answers))
	for _, answer := range answers {
		value := map[string]any{"questionId": answer.QuestionID}
		if len(answer.Values) != 0 {
			value["selectedLabels"] = answer.Values
		} else if answer.FreeText {
			value["freeText"] = answer.Value
		} else {
			value["selectedLabel"] = answer.Value
		}
		values = append(values, value)
	}
	return c.Call(ctx, "userInput/answer", map[string]any{
		"commandId": NewCommandID(), "sessionId": sessionID, "userInputId": userInputID,
		"answers": values,
	}, nil)
}

func (c *Client) TurnInterrupt(ctx context.Context, sessionID, turnID string) error {
	return c.Call(ctx, MethodTurnInterrupt, map[string]any{
		"commandId": NewCommandID(),
		"sessionId": sessionID,
		"turnId":    turnID,
	}, nil)
}
