package agentstream

import (
	"fmt"
	"strings"
	"time"
)

type EventKind string

const (
	EventTurnStarted        EventKind = "turn_started"
	EventThinkingDelta      EventKind = "thinking_delta"
	EventAssistantDelta     EventKind = "assistant_delta"
	EventAssistantMessage   EventKind = "assistant_message"
	EventCommandStarted     EventKind = "command_started"
	EventCommandOutputDelta EventKind = "command_output_delta"
	EventCommandCompleted   EventKind = "command_completed"
	EventFileChanged        EventKind = "file_changed"
	EventToolStarted        EventKind = "tool_started"
	EventToolCompleted      EventKind = "tool_completed"
	EventApprovalRequested  EventKind = "approval_requested"
	EventQuestionRequested  EventKind = "question_requested"
	EventTurnCompleted      EventKind = "turn_completed"
	EventInterrupted        EventKind = "interrupted"
	EventError              EventKind = "error"
)

type TaskSummary struct {
	ID               string
	Title            string
	ProjectID        string
	ProjectName      string
	Agent            string
	Status           string
	SessionName      *string
	AgentThreadID    *string
	AgentEventCursor *string
	StreamKind       string
}

type Event struct {
	ID        string
	TaskID    string
	TurnID    string
	ItemID    string
	Kind      EventKind
	Agent     string
	CreatedAt time.Time
	Cursor    string

	Text     string
	Command  *CommandEvent
	File     *FileEvent
	Tool     *ToolEvent
	Approval *ApprovalEvent
	Question *QuestionEvent
	Result   *ResultEvent
	Error    string
}

type CommandEvent struct {
	ID       string
	Command  string
	ExitCode *int
	Stdout   string
	Stderr   string
}

type FileEvent struct {
	Path   string
	Action string
}

type ToolEvent struct {
	ID    string
	Name  string
	Input string
}

type ApprovalEvent struct {
	ID          string
	Prompt      string
	Command     string
	Options     []ApprovalOption
	ExpiresAt   time.Time
	Permissions map[string]string
}

type ApprovalOption struct {
	ID    string
	Label string
}

type QuestionEvent struct {
	ID      string
	Prompt  string
	Options []QuestionOption
}

type QuestionOption struct {
	ID    string
	Label string
}

type ResultEvent struct {
	Duration time.Duration
	Tokens   int
}

type Response struct {
	RequestID string
	OptionID  string
	Text      string
	Allow     bool
}

func StableEventID(taskID string, kind EventKind, parts ...string) string {
	values := make([]string, 0, len(parts)+2)
	values = append(values, strings.TrimSpace(taskID), string(kind))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			values = append(values, trimmed)
		}
	}
	return strings.Join(values, ":")
}

func (e Event) DedupKey() string {
	if strings.TrimSpace(e.Cursor) != "" {
		return strings.TrimSpace(e.Cursor)
	}
	if strings.TrimSpace(e.ID) != "" {
		return strings.TrimSpace(e.ID)
	}
	if strings.TrimSpace(e.TaskID) == "" || e.Kind == "" {
		return ""
	}
	return StableEventID(e.TaskID, e.Kind, e.TurnID, e.ItemID, e.Text, e.Error)
}

func (e Event) Validate() error {
	if strings.TrimSpace(e.TaskID) == "" {
		return fmt.Errorf("agent stream event task id is required")
	}
	if e.Kind == "" {
		return fmt.Errorf("agent stream event kind is required")
	}
	return nil
}

type Deduper struct {
	seen map[string]struct{}
}

func NewDeduper(keys ...string) *Deduper {
	d := &Deduper{seen: map[string]struct{}{}}
	for _, key := range keys {
		if trimmed := strings.TrimSpace(key); trimmed != "" {
			d.seen[trimmed] = struct{}{}
		}
	}
	return d
}

func (d *Deduper) Accept(event Event) bool {
	if d.seen == nil {
		d.seen = map[string]struct{}{}
	}
	key := event.DedupKey()
	if key == "" {
		return true
	}
	if _, ok := d.seen[key]; ok {
		return false
	}
	d.seen[key] = struct{}{}
	return true
}
