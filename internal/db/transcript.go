package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const maxTranscriptBodyBytes = 64 * 1024

func (s *Store) AppendTaskTranscriptMessage(taskID, role, body string, turnID, discordMessageID *string) error {
	return s.appendTaskTranscriptMessage(taskID, role, body, turnID, discordMessageID, nil)
}

func (s *Store) AppendTaskTranscriptEventMessage(taskID, role, body string, turnID *string, eventKey string) error {
	eventKey = strings.TrimSpace(eventKey)
	if eventKey == "" {
		return s.AppendTaskTranscriptMessage(taskID, role, body, turnID, nil)
	}
	return s.appendTaskTranscriptMessage(taskID, role, body, turnID, nil, &eventKey)
}

func (s *Store) appendTaskTranscriptMessage(taskID, role, body string, turnID, discordMessageID, eventKey *string) error {
	taskID = strings.TrimSpace(taskID)
	role = strings.TrimSpace(role)
	body = strings.TrimSpace(body)
	if taskID == "" {
		return fmt.Errorf("task id is required")
	}
	if !isValidTranscriptRole(role) {
		return fmt.Errorf("invalid transcript role %q", role)
	}
	if body == "" {
		return nil
	}
	body = truncateBytes(body, maxTranscriptBodyBytes)
	return s.execTaskTranscriptInsert(taskID, role, body, cleanOptionalString(turnID), cleanOptionalString(discordMessageID), cleanOptionalString(eventKey))
}

func (s *Store) ListTaskTranscriptMessages(taskID string, limit int) ([]TaskTranscriptMessage, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil, fmt.Errorf("task id is required")
	}
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := s.db.Query(`
SELECT id, task_id, turn_id, role, body, discord_message_id, event_key, created_at, updated_at
FROM (
	SELECT id, task_id, turn_id, role, body, discord_message_id, event_key, created_at, updated_at
	FROM task_transcript_messages
	WHERE task_id = ?
	ORDER BY created_at DESC, id DESC
	LIMIT ?
)
ORDER BY created_at ASC, id ASC
`, taskID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var messages []TaskTranscriptMessage
	for rows.Next() {
		message, err := scanTaskTranscriptMessage(rows)
		if err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	return messages, rows.Err()
}

func (s *Store) ListUnqueuedAssistantTranscriptMessages(taskID string, notBefore time.Time, limit int) ([]TaskTranscriptMessage, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil, fmt.Errorf("task id is required")
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.Query(`
SELECT id, task_id, turn_id, role, body, discord_message_id, event_key, created_at, updated_at
FROM (
	SELECT t.id, t.task_id, t.turn_id, t.role, t.body, t.discord_message_id, t.event_key, t.created_at, t.updated_at
	FROM task_transcript_messages t
	WHERE t.task_id = ?
	  AND t.role = 'assistant'
	  AND t.event_key IS NOT NULL
	  AND TRIM(t.event_key) != ''
	  AND t.created_at >= ?
	  AND NOT EXISTS (
		SELECT 1
		FROM discord_outbox o
		WHERE o.task_id = t.task_id AND o.event_key = t.event_key
	  )
	ORDER BY t.created_at DESC, t.id DESC
	LIMIT ?
)
ORDER BY created_at ASC, id ASC
`, taskID, notBefore.UTC(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var messages []TaskTranscriptMessage
	for rows.Next() {
		message, err := scanTaskTranscriptMessage(rows)
		if err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	return messages, rows.Err()
}

func (s *Store) execTaskTranscriptInsert(taskID, role, body string, turnID, discordMessageID, eventKey *string) error {
	query := `
INSERT INTO task_transcript_messages (task_id, turn_id, role, body, discord_message_id, event_key, updated_at)
VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
`
	if eventKey != nil {
		query += ` ON CONFLICT DO NOTHING`
	}
	_, err := s.db.Exec(query, taskID, turnID, role, body, discordMessageID, eventKey)
	return err
}

func scanTaskTranscriptMessage(scanner interface {
	Scan(dest ...any) error
}) (TaskTranscriptMessage, error) {
	var message TaskTranscriptMessage
	if err := scanner.Scan(&message.ID, &message.TaskID, &message.TurnID, &message.Role, &message.Body, &message.DiscordMessageID, &message.EventKey, &message.CreatedAt, &message.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			return TaskTranscriptMessage{}, err
		}
		return TaskTranscriptMessage{}, err
	}
	return message, nil
}

func isValidTranscriptRole(role string) bool {
	switch role {
	case "user", "assistant", "system", "status", "tool_trace":
		return true
	default:
		return false
	}
}

func cleanOptionalString(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func truncateBytes(value string, max int) string {
	if len(value) <= max {
		return value
	}
	cut := value[:max]
	for !utf8.ValidString(cut) && len(cut) > 0 {
		cut = cut[:len(cut)-1]
	}
	return strings.TrimSpace(cut)
}
