package db

import (
	"database/sql"
	"fmt"
	"strings"
	"unicode/utf8"
)

const maxTranscriptBodyBytes = 64 * 1024

func (s *Store) AppendTaskTranscriptMessage(taskID, role, body string, turnID, discordMessageID *string) error {
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
	return s.execTaskTranscriptInsert(taskID, role, body, cleanOptionalString(turnID), cleanOptionalString(discordMessageID))
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
SELECT id, task_id, turn_id, role, body, discord_message_id, created_at, updated_at
FROM (
	SELECT id, task_id, turn_id, role, body, discord_message_id, created_at, updated_at
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

func (s *Store) execTaskTranscriptInsert(taskID, role, body string, turnID, discordMessageID *string) error {
	_, err := s.db.Exec(`
INSERT INTO task_transcript_messages (task_id, turn_id, role, body, discord_message_id, updated_at)
VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
`, taskID, turnID, role, body, discordMessageID)
	return err
}

func scanTaskTranscriptMessage(scanner interface {
	Scan(dest ...any) error
}) (TaskTranscriptMessage, error) {
	var message TaskTranscriptMessage
	if err := scanner.Scan(&message.ID, &message.TaskID, &message.TurnID, &message.Role, &message.Body, &message.DiscordMessageID, &message.CreatedAt, &message.UpdatedAt); err != nil {
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
