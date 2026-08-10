package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nashory/agx/internal/agentstream"
	"github.com/nashory/agx/internal/db"
)

type agentErrorDiagnosticRecord struct {
	ID               string    `json:"id"`
	Time             time.Time `json:"time"`
	TaskID           string    `json:"task_id"`
	TurnID           string    `json:"turn_id,omitempty"`
	EventID          string    `json:"event_id,omitempty"`
	Agent            string    `json:"agent,omitempty"`
	StreamKind       string    `json:"stream_kind,omitempty"`
	ThreadID         string    `json:"thread_id,omitempty"`
	Cursor           string    `json:"cursor,omitempty"`
	DiscordChannelID string    `json:"discord_channel_id,omitempty"`
	Summary          string    `json:"summary"`
	RawError         string    `json:"raw_error,omitempty"`
	Text             string    `json:"text,omitempty"`
}

func (s *agentEventService) attachAgentErrorDiagnostic(taskID string, event agentstream.Event) agentstream.Event {
	record := s.agentErrorDiagnosticRecord(taskID, event)
	if record.ID == "" {
		return event
	}
	event.DiagnosticID = record.ID
	if err := s.writeAgentErrorDiagnostic(record); err != nil {
		logRuntimeOperation("agent_error_diagnostic",
			"status", "failed",
			"task", shortDiagnosticID(taskID),
			"diagnostic_id", record.ID,
			"error", err,
		)
	}
	return event
}

func (s *agentEventService) agentErrorDiagnosticRecord(taskID string, event agentstream.Event) agentErrorDiagnosticRecord {
	if taskID = strings.TrimSpace(taskID); taskID == "" {
		taskID = strings.TrimSpace(event.TaskID)
	}
	if taskID == "" {
		return agentErrorDiagnosticRecord{}
	}
	when := event.CreatedAt
	if when.IsZero() {
		when = time.Now()
	}
	when = when.UTC()
	record := agentErrorDiagnosticRecord{
		ID:       strings.TrimSpace(event.DiagnosticID),
		Time:     when,
		TaskID:   taskID,
		TurnID:   strings.TrimSpace(event.TurnID),
		EventID:  strings.TrimSpace(event.ID),
		Agent:    strings.TrimSpace(event.Agent),
		Cursor:   strings.TrimSpace(event.Cursor),
		Summary:  summarizeAgentDiagnosticError(event),
		RawError: strings.TrimSpace(event.Error),
		Text:     strings.TrimSpace(event.Text),
	}
	if record.ID == "" {
		record.ID = agentErrorDiagnosticID(record, when)
	}
	if s == nil || s.runtime == nil || s.runtime.store == nil {
		return record
	}
	task, err := s.runtime.store.GetTask(taskID)
	if err == nil {
		record.Agent = firstNonEmptyDiagnosticValue(record.Agent, task.Agent)
		record.StreamKind = diagnosticStringValue(task.AgentStreamKind)
		record.ThreadID = diagnosticStringValue(task.AgentThreadID)
	}
	if mapping, err := s.runtime.store.GetDiscordMapping(db.DiscordAGXTask, taskID); err == nil {
		record.DiscordChannelID = mapping.DiscordID
	}
	return record
}

func (s *agentEventService) writeAgentErrorDiagnostic(record agentErrorDiagnosticRecord) error {
	if s == nil || s.runtime == nil {
		return nil
	}
	configDir := strings.TrimSpace(s.runtime.paths.ConfigDir)
	if configDir == "" {
		return nil
	}
	dir := filepath.Join(configDir, "logs", "agent-errors")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(dir, record.Time.Format("2006-01-02")+".jsonl")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(record)
}

func agentErrorDiagnosticID(record agentErrorDiagnosticRecord, when time.Time) string {
	hash := sha256.Sum256([]byte(strings.Join([]string{
		record.TaskID,
		record.TurnID,
		record.EventID,
		record.RawError,
		record.Text,
		when.Format(time.RFC3339Nano),
	}, "\x00")))
	return "err_" + when.Format("20060102_150405") + "_" + hex.EncodeToString(hash[:4])
}

func summarizeAgentDiagnosticError(event agentstream.Event) string {
	for _, value := range []string{event.Error, event.Text} {
		for _, line := range strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			return truncateDiagnosticSummary(line)
		}
	}
	return "agent error without details"
}

func truncateDiagnosticSummary(value string) string {
	const maxRunes = 240
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	return string(runes[:maxRunes-3]) + "..."
}

func firstNonEmptyDiagnosticValue(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func diagnosticStringValue(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}
