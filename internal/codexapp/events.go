package codexapp

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/nashory/agx/internal/agentstream"
)

const (
	NotifyTurnStarted       = "turn/started"
	NotifyTurnCompleted     = "turn/completed"
	NotifyAgentMessageDelta = "item/agentMessage/delta"
	NotifyItemCompleted     = "item/completed"
	NotifyCommandOutput     = "item/commandExecution/outputDelta"
	NotifyThreadStatus      = "thread/status/changed"
	NotifyError             = "error"
)

func MapNotification(task agentstream.TaskSummary, notification Notification) (agentstream.Event, bool, error) {
	switch notification.Method {
	case NotifyTurnStarted:
		var params struct {
			ThreadID string `json:"threadId"`
			Turn     Turn   `json:"turn"`
		}
		if err := json.Unmarshal(notification.Params, &params); err != nil {
			return agentstream.Event{}, false, err
		}
		return agentstream.Event{
			ID:        agentstream.StableEventID(task.ID, agentstream.EventTurnStarted, params.Turn.ID),
			TaskID:    task.ID,
			TurnID:    params.Turn.ID,
			Kind:      agentstream.EventTurnStarted,
			Agent:     "codex",
			CreatedAt: time.Now(),
			Cursor:    notificationCursor(notification.Method, params.ThreadID, params.Turn.ID, ""),
		}, true, nil
	case NotifyAgentMessageDelta:
		var params struct {
			ThreadID string `json:"threadId"`
			TurnID   string `json:"turnId"`
			ItemID   string `json:"itemId"`
			Delta    string `json:"delta"`
		}
		if err := json.Unmarshal(notification.Params, &params); err != nil {
			return agentstream.Event{}, false, err
		}
		return agentstream.Event{
			ID:        agentstream.StableEventID(task.ID, agentstream.EventAssistantDelta, params.TurnID, params.ItemID, params.Delta),
			TaskID:    task.ID,
			TurnID:    params.TurnID,
			ItemID:    params.ItemID,
			Kind:      agentstream.EventAssistantDelta,
			Agent:     "codex",
			CreatedAt: time.Now(),
			Cursor:    notificationCursor(notification.Method, params.ThreadID, params.TurnID, params.ItemID),
			Text:      params.Delta,
		}, true, nil
	case NotifyCommandOutput:
		var params struct {
			ThreadID string `json:"threadId"`
			TurnID   string `json:"turnId"`
			ItemID   string `json:"itemId"`
			Delta    string `json:"delta"`
		}
		if err := json.Unmarshal(notification.Params, &params); err != nil {
			return agentstream.Event{}, false, err
		}
		return agentstream.Event{
			ID:        agentstream.StableEventID(task.ID, agentstream.EventCommandOutputDelta, params.TurnID, params.ItemID, params.Delta),
			TaskID:    task.ID,
			TurnID:    params.TurnID,
			ItemID:    params.ItemID,
			Kind:      agentstream.EventCommandOutputDelta,
			Agent:     "codex",
			CreatedAt: time.Now(),
			Cursor:    notificationCursor(notification.Method, params.ThreadID, params.TurnID, params.ItemID),
			Command:   &agentstream.CommandEvent{ID: params.ItemID, Stdout: params.Delta},
		}, true, nil
	case NotifyItemCompleted:
		var params struct {
			ThreadID string `json:"threadId"`
			TurnID   string `json:"turnId"`
			Item     struct {
				Type      string          `json:"type"`
				ID        string          `json:"id"`
				Text      string          `json:"text"`
				Summary   string          `json:"summary"`
				Message   string          `json:"message"`
				Name      string          `json:"name"`
				Command   string          `json:"command"`
				Input     json.RawMessage `json:"input"`
				Arguments json.RawMessage `json:"arguments"`
			} `json:"item"`
		}
		if err := json.Unmarshal(notification.Params, &params); err != nil {
			return agentstream.Event{}, false, err
		}
		text := firstNonEmpty(params.Item.Text, params.Item.Summary, params.Item.Message)
		if params.Item.Type == "agentMessage" && strings.TrimSpace(text) != "" {
			return agentstream.Event{
				ID:        agentstream.StableEventID(task.ID, agentstream.EventAssistantMessage, params.TurnID, params.Item.ID, text),
				TaskID:    task.ID,
				TurnID:    params.TurnID,
				ItemID:    params.Item.ID,
				Kind:      agentstream.EventAssistantMessage,
				Agent:     "codex",
				CreatedAt: time.Now(),
				Cursor:    notificationCursor(notification.Method, params.ThreadID, params.TurnID, params.Item.ID),
				Text:      text,
			}, true, nil
		}
		if command := strings.TrimSpace(params.Item.Command); command != "" {
			return agentstream.Event{
				ID:        agentstream.StableEventID(task.ID, agentstream.EventCommandStarted, params.TurnID, params.Item.ID, command),
				TaskID:    task.ID,
				TurnID:    params.TurnID,
				ItemID:    params.Item.ID,
				Kind:      agentstream.EventCommandStarted,
				Agent:     "codex",
				CreatedAt: time.Now(),
				Cursor:    notificationCursor(notification.Method, params.ThreadID, params.TurnID, params.Item.ID),
				Command:   &agentstream.CommandEvent{ID: params.Item.ID, Command: command},
			}, true, nil
		}
		if name := strings.TrimSpace(params.Item.Name); name != "" {
			input := strings.TrimSpace(string(params.Item.Input))
			if input == "" || input == "null" {
				input = strings.TrimSpace(string(params.Item.Arguments))
			}
			return agentstream.Event{
				ID:        agentstream.StableEventID(task.ID, agentstream.EventToolStarted, params.TurnID, params.Item.ID, name, input),
				TaskID:    task.ID,
				TurnID:    params.TurnID,
				ItemID:    params.Item.ID,
				Kind:      agentstream.EventToolStarted,
				Agent:     "codex",
				CreatedAt: time.Now(),
				Cursor:    notificationCursor(notification.Method, params.ThreadID, params.TurnID, params.Item.ID),
				Tool:      &agentstream.ToolEvent{ID: params.Item.ID, Name: name, Input: input},
			}, true, nil
		}
		if strings.TrimSpace(text) == "" {
			return agentstream.Event{}, false, nil
		}
		return agentstream.Event{
			ID:        agentstream.StableEventID(task.ID, agentstream.EventThinkingDelta, params.TurnID, params.Item.ID, text),
			TaskID:    task.ID,
			TurnID:    params.TurnID,
			ItemID:    params.Item.ID,
			Kind:      agentstream.EventThinkingDelta,
			Agent:     "codex",
			CreatedAt: time.Now(),
			Cursor:    notificationCursor(notification.Method, params.ThreadID, params.TurnID, params.Item.ID),
			Text:      text,
		}, true, nil
	case NotifyTurnCompleted:
		var params struct {
			ThreadID string `json:"threadId"`
			Turn     Turn   `json:"turn"`
		}
		if err := json.Unmarshal(notification.Params, &params); err != nil {
			return agentstream.Event{}, false, err
		}
		return agentstream.Event{
			ID:        agentstream.StableEventID(task.ID, agentstream.EventTurnCompleted, params.Turn.ID),
			TaskID:    task.ID,
			TurnID:    params.Turn.ID,
			Kind:      agentstream.EventTurnCompleted,
			Agent:     "codex",
			CreatedAt: time.Now(),
			Cursor:    notificationCursor(notification.Method, params.ThreadID, params.Turn.ID, ""),
		}, true, nil
	case NotifyThreadStatus:
		var params struct {
			ThreadID string `json:"threadId"`
			Status   struct {
				Type string `json:"type"`
			} `json:"status"`
		}
		if err := json.Unmarshal(notification.Params, &params); err != nil {
			return agentstream.Event{}, false, err
		}
		if !strings.EqualFold(strings.TrimSpace(params.Status.Type), "idle") {
			return agentstream.Event{}, false, nil
		}
		return agentstream.Event{
			ID:        agentstream.StableEventID(task.ID, agentstream.EventTurnCompleted, params.ThreadID, "idle"),
			TaskID:    task.ID,
			Kind:      agentstream.EventTurnCompleted,
			Agent:     "codex",
			CreatedAt: time.Now(),
			Cursor:    notificationCursor(notification.Method, params.ThreadID, "", ""),
		}, true, nil
	case NotifyError:
		message := ExtractErrorMessage(notification.Params)
		if message == "" {
			message = ErrorNoDetail
		}
		return agentstream.Event{
			ID:        agentstream.StableEventID(task.ID, agentstream.EventError, message),
			TaskID:    task.ID,
			Kind:      agentstream.EventError,
			Agent:     "codex",
			CreatedAt: time.Now(),
			Error:     message,
		}, true, nil
	default:
		return agentstream.Event{}, false, nil
	}
}

// ErrorNoDetail is the fallback surfaced when codex signals an error but no
// message could be extracted from the notification. Callers can compare against
// it to decide whether to enrich the error with other diagnostics (stderr).
const ErrorNoDetail = "codex reported an error without any details."

// ExtractErrorMessage pulls a human-readable message out of a codex "error"
// notification. Codex has shipped several shapes over time ({"message": …},
// {"error": …}, {"error": {"message": …}}, {"data": …}); this tries each and,
// as a last resort, returns the raw params JSON so a diagnostic is never lost.
func ExtractErrorMessage(params json.RawMessage) string {
	if len(params) == 0 {
		return ""
	}
	var probe struct {
		Message string          `json:"message"`
		Detail  string          `json:"detail"`
		Reason  string          `json:"reason"`
		Data    json.RawMessage `json:"data"`
		Error   json.RawMessage `json:"error"`
	}
	_ = json.Unmarshal(params, &probe)
	if m := firstNonEmpty(probe.Message, probe.Detail, probe.Reason); m != "" {
		return m
	}
	if m := nestedErrorMessage(probe.Error); m != "" {
		return m
	}
	if m := nestedErrorMessage(probe.Data); m != "" {
		return m
	}
	// Last resort: surface the raw params so an unrecognized-but-populated error
	// shape is not lost. Skip it when the payload carries no meaningful value
	// (e.g. {} or {"message":"   "}), which upstream treats as "no detail".
	if hasMeaningfulValue(params) {
		return strings.TrimSpace(string(params))
	}
	return ""
}

// hasMeaningfulValue reports whether a JSON payload carries any value worth
// surfacing: a non-string value, or a string that is not blank once trimmed.
func hasMeaningfulValue(params json.RawMessage) bool {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(params, &fields); err != nil {
		raw := strings.TrimSpace(string(params))
		return raw != "" && raw != "null"
	}
	for _, value := range fields {
		raw := strings.TrimSpace(string(value))
		if raw == "" || raw == "null" || raw == `""` {
			continue
		}
		var text string
		if err := json.Unmarshal(value, &text); err == nil {
			if strings.TrimSpace(text) == "" {
				continue
			}
		}
		return true
	}
	return false
}

// nestedErrorMessage decodes a nested error value that may be a bare string or
// an object carrying message/detail fields.
func nestedErrorMessage(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return strings.TrimSpace(text)
	}
	var obj struct {
		Message string `json:"message"`
		Detail  string `json:"detail"`
		Reason  string `json:"reason"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil {
		if m := firstNonEmpty(obj.Message, obj.Detail, obj.Reason); m != "" {
			return m
		}
	}
	return trimmed
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func notificationCursor(method, threadID, turnID, itemID string) string {
	return fmt.Sprintf("%s:%s:%s:%s", method, threadID, turnID, itemID)
}
