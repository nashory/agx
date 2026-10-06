package museapp

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/nashory/agx/internal/agentstream"
)

const (
	NotifyItemStarted        = "item/started"
	NotifyItemDelta          = "item/delta"
	NotifyItemUpdated        = "item/updated"
	NotifyItemCompleted      = "item/completed"
	NotifyTurnStarted        = "turn/started"
	NotifyTurnCompleted      = "turn/completed"
	NotifyApprovalRequest    = "approval/request"
	NotifyApprovalRequested  = "approval/requested"
	NotifyUserInputRequest   = "userInput/request"
	NotifyUserInputRequested = "userInput/requested"
	NotifyViewGap            = "view/gap"
)

type Item struct {
	ID            string   `json:"itemId"`
	Kind          string   `json:"kind"`
	TurnID        *string  `json:"turnId"`
	Status        string   `json:"status"`
	Text          string   `json:"text"`
	Summary       []string `json:"summary"`
	Tool          string   `json:"tool"`
	Args          string   `json:"args"`
	VisibleOutput string   `json:"visibleOutput"`
	FailureReason string   `json:"failureReason"`
	FallbackText  string   `json:"fallbackText"`
	StatusLine    string   `json:"statusLine"`
}

type itemParams struct {
	SessionID  string `json:"sessionId"`
	ViewCursor string `json:"viewCursor"`
	Item       Item   `json:"item"`
}

func SessionID(notification Notification) string {
	var params struct {
		SessionID string `json:"sessionId"`
	}
	_ = json.Unmarshal(notification.Params, &params)
	return strings.TrimSpace(params.SessionID)
}

func MapNotification(task agentstream.TaskSummary, notification Notification) ([]agentstream.Event, error) {
	now := time.Now()
	base := func(kind agentstream.EventKind, turnID, itemID, cursor string) agentstream.Event {
		return agentstream.Event{
			ID: agentstream.StableEventID(task.ID, kind, turnID, itemID, cursor), TaskID: task.ID,
			TurnID: turnID, ItemID: itemID, Kind: kind, Agent: "muse", CreatedAt: now, Cursor: cursor,
		}
	}
	switch notification.Method {
	case NotifyTurnStarted:
		var params struct {
			TurnID     string `json:"turnId"`
			ViewCursor string `json:"viewCursor"`
		}
		if err := json.Unmarshal(notification.Params, &params); err != nil {
			return nil, err
		}
		event := base(agentstream.EventTurnStarted, params.TurnID, "", params.ViewCursor)
		return []agentstream.Event{event}, nil
	case NotifyTurnCompleted:
		var params struct {
			TurnID     string `json:"turnId"`
			Terminal   string `json:"terminal"`
			Reason     string `json:"reason"`
			ViewCursor string `json:"viewCursor"`
			DurationMS int64  `json:"durationMs"`
			Usage      struct {
				InputTokens  int `json:"inputTokens"`
				OutputTokens int `json:"outputTokens"`
			} `json:"usage"`
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(notification.Params, &params); err != nil {
			return nil, err
		}
		kind := agentstream.EventTurnCompleted
		message := first(params.Error.Message, params.Reason)
		switch strings.ToLower(params.Terminal) {
		case "cancelled", "canceled", "interrupted":
			kind = agentstream.EventInterrupted
		case "failed":
			kind = agentstream.EventError
		}
		event := base(kind, params.TurnID, "", params.ViewCursor)
		event.Error, event.Text = message, message
		event.Result = &agentstream.ResultEvent{Duration: time.Duration(params.DurationMS) * time.Millisecond, Tokens: params.Usage.InputTokens + params.Usage.OutputTokens}
		return []agentstream.Event{event}, nil
	case NotifyItemStarted, NotifyItemUpdated, NotifyItemCompleted:
		var params itemParams
		if err := json.Unmarshal(notification.Params, &params); err != nil {
			return nil, err
		}
		return mapItem(base, notification.Method, params), nil
	case NotifyItemDelta:
		var params struct {
			ViewCursor string `json:"viewCursor"`
			ItemID     string `json:"itemId"`
			Delta      string `json:"delta"`
			Field      string `json:"field"`
		}
		if err := json.Unmarshal(notification.Params, &params); err != nil {
			return nil, err
		}
		kind := agentstream.EventAssistantDelta
		if strings.HasPrefix(params.Field, "summary.") {
			kind = agentstream.EventThinkingDelta
		} else if params.Field == "output" {
			kind = agentstream.EventCommandOutputDelta
		}
		event := base(kind, "", params.ItemID, params.ViewCursor)
		event.Text = params.Delta
		if kind == agentstream.EventCommandOutputDelta {
			event.Command = &agentstream.CommandEvent{ID: params.ItemID, Stdout: params.Delta}
		}
		return []agentstream.Event{event}, nil
	case NotifyApprovalRequest, NotifyApprovalRequested:
		return mapApproval(task, notification, now)
	case NotifyUserInputRequest, NotifyUserInputRequested:
		return mapUserInput(task, notification, now)
	default:
		return nil, nil
	}
}

func mapItem(base func(agentstream.EventKind, string, string, string) agentstream.Event, method string, params itemParams) []agentstream.Event {
	item := params.Item
	turnID := ""
	if item.TurnID != nil {
		turnID = *item.TurnID
	}
	switch item.Kind {
	case "agentMessage":
		if method != NotifyItemCompleted || strings.TrimSpace(item.Text) == "" {
			return nil
		}
		event := base(agentstream.EventAssistantMessage, turnID, item.ID, params.ViewCursor)
		event.Text = item.Text
		return []agentstream.Event{event}
	case "reasoning":
		text := strings.TrimSpace(strings.Join(item.Summary, "\n"))
		if text == "" {
			return nil
		}
		event := base(agentstream.EventThinkingDelta, turnID, item.ID, params.ViewCursor)
		event.Text = text
		return []agentstream.Event{event}
	case "toolCall", "userShell":
		if method == NotifyItemStarted {
			event := base(agentstream.EventToolStarted, turnID, item.ID, params.ViewCursor)
			event.Tool = &agentstream.ToolEvent{ID: item.ID, Name: first(item.Tool, item.Kind), Input: item.Args}
			return []agentstream.Event{event}
		}
		if method == NotifyItemUpdated {
			text := first(item.StatusLine, item.VisibleOutput)
			if text == "" {
				return nil
			}
			event := base(agentstream.EventCommandOutputDelta, turnID, item.ID, params.ViewCursor)
			event.Command = &agentstream.CommandEvent{ID: item.ID, Command: item.Tool, Stdout: text}
			return []agentstream.Event{event}
		}
		exitCode, stderr := 0, ""
		if !strings.EqualFold(item.Status, "completed") {
			exitCode, stderr = 1, item.FailureReason
		}
		event := base(agentstream.EventCommandCompleted, turnID, item.ID, params.ViewCursor)
		event.Command = &agentstream.CommandEvent{ID: item.ID, Command: first(item.Tool, item.Args), ExitCode: &exitCode, Stdout: item.VisibleOutput, Stderr: stderr}
		return []agentstream.Event{event}
	default:
		if method == NotifyItemStarted && first(item.FallbackText) != "" {
			event := base(agentstream.EventToolStarted, turnID, item.ID, params.ViewCursor)
			event.Tool = &agentstream.ToolEvent{ID: item.ID, Name: item.Kind, Input: item.FallbackText}
			return []agentstream.Event{event}
		}
		return nil
	}
}

func mapApproval(task agentstream.TaskSummary, notification Notification, now time.Time) ([]agentstream.Event, error) {
	var params struct {
		ApprovalID string                                               `json:"approvalId"`
		TurnID     string                                               `json:"turnId"`
		ItemID     string                                               `json:"itemId"`
		ToolName   string                                               `json:"toolName"`
		RawArgs    string                                               `json:"rawArgs"`
		ViewCursor string                                               `json:"viewCursor"`
		Subject    struct{ Kind, Command, Path, Host, ToolName string } `json:"subject"`
		Choices    []struct {
			ID    string `json:"choiceId"`
			Label string `json:"label"`
		} `json:"availableChoices"`
	}
	if err := json.Unmarshal(notification.Params, &params); err != nil {
		return nil, err
	}
	options := make([]agentstream.ApprovalOption, 0, len(params.Choices))
	for _, choice := range params.Choices {
		options = append(options, agentstream.ApprovalOption{ID: choice.ID, Label: choice.Label})
	}
	prompt := first(params.Subject.Command, params.Subject.Path, params.Subject.Host, params.Subject.ToolName, params.ToolName)
	event := agentstream.Event{ID: agentstream.StableEventID(task.ID, agentstream.EventApprovalRequested, params.ApprovalID), TaskID: task.ID, TurnID: params.TurnID, ItemID: params.ItemID, Kind: agentstream.EventApprovalRequested, Agent: "muse", CreatedAt: now, Cursor: params.ViewCursor, Approval: &agentstream.ApprovalEvent{ID: params.ApprovalID, Prompt: prompt, Command: params.RawArgs, Options: options}}
	return []agentstream.Event{event}, nil
}

func mapUserInput(task agentstream.TaskSummary, notification Notification, now time.Time) ([]agentstream.Event, error) {
	var params struct {
		UserInputID string `json:"userInputId"`
		TurnID      string `json:"turnId"`
		ItemID      string `json:"itemId"`
		ViewCursor  string `json:"viewCursor"`
		Questions   []struct {
			ID       string `json:"id"`
			Header   string `json:"header"`
			Question string `json:"question"`
			Options  []struct {
				Label string `json:"label"`
			} `json:"options"`
			Selection struct {
				Mode string `json:"mode"`
			} `json:"selection"`
		} `json:"questions"`
	}
	if err := json.Unmarshal(notification.Params, &params); err != nil {
		return nil, err
	}
	if len(params.Questions) == 0 {
		return nil, nil
	}
	question := params.Questions[0]
	options := make([]agentstream.QuestionOption, 0, len(question.Options))
	for _, option := range question.Options {
		options = append(options, agentstream.QuestionOption{ID: option.Label, Label: option.Label})
	}
	event := agentstream.Event{ID: agentstream.StableEventID(task.ID, agentstream.EventQuestionRequested, params.UserInputID), TaskID: task.ID, TurnID: params.TurnID, ItemID: params.ItemID, Kind: agentstream.EventQuestionRequested, Agent: "muse", CreatedAt: now, Cursor: params.ViewCursor, Question: &agentstream.QuestionEvent{ID: params.UserInputID + ":" + question.ID, Prompt: strings.TrimSpace(strings.Join([]string{question.Header, question.Question}, "\n")), Options: options, Multiple: strings.EqualFold(question.Selection.Mode, "multiple")}}
	return []agentstream.Event{event}, nil
}

func first(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
