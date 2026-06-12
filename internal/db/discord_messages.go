package db

import (
	"fmt"
	"strings"
)

const (
	DiscordMessageProcessing = "processing"
	DiscordMessageDelivered  = "delivered"
	DiscordMessageFailed     = "failed"
)

// ReserveDiscordMessage records that a Discord message is being processed for a
// task. It returns false when the message was already seen, allowing callers to
// skip duplicate gateway deliveries before they reach an agent.
func (s *Store) ReserveDiscordMessage(taskID, discordMessageID string) (bool, error) {
	taskID = strings.TrimSpace(taskID)
	discordMessageID = strings.TrimSpace(discordMessageID)
	if taskID == "" {
		return false, fmt.Errorf("task id is required")
	}
	if discordMessageID == "" {
		return false, fmt.Errorf("discord message id is required")
	}
	result, err := s.db.Exec(`
INSERT INTO discord_processed_messages (task_id, discord_message_id, status, updated_at)
VALUES (?, ?, ?, CURRENT_TIMESTAMP)
ON CONFLICT(task_id, discord_message_id) DO NOTHING
`, taskID, discordMessageID, DiscordMessageProcessing)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("get discord message rows affected: %w", err)
	}
	return n > 0, nil
}

func (s *Store) MarkDiscordMessageDelivered(taskID, discordMessageID string) error {
	return s.updateDiscordMessageStatus(taskID, discordMessageID, DiscordMessageDelivered, "")
}

func (s *Store) MarkDiscordMessageFailed(taskID, discordMessageID string, cause error) error {
	message := ""
	if cause != nil {
		message = cause.Error()
	}
	return s.updateDiscordMessageStatus(taskID, discordMessageID, DiscordMessageFailed, message)
}

func (s *Store) DeleteDiscordMessageReservation(taskID, discordMessageID string) error {
	taskID = strings.TrimSpace(taskID)
	discordMessageID = strings.TrimSpace(discordMessageID)
	if taskID == "" {
		return fmt.Errorf("task id is required")
	}
	if discordMessageID == "" {
		return fmt.Errorf("discord message id is required")
	}
	_, err := s.db.Exec(`
DELETE FROM discord_processed_messages
WHERE task_id = ? AND discord_message_id = ? AND status = ?
`, taskID, discordMessageID, DiscordMessageProcessing)
	return err
}

func (s *Store) DiscordMessageStatus(taskID, discordMessageID string) (string, error) {
	taskID = strings.TrimSpace(taskID)
	discordMessageID = strings.TrimSpace(discordMessageID)
	if taskID == "" {
		return "", fmt.Errorf("task id is required")
	}
	if discordMessageID == "" {
		return "", fmt.Errorf("discord message id is required")
	}
	var status string
	err := s.db.QueryRow(`
SELECT status FROM discord_processed_messages
WHERE task_id = ? AND discord_message_id = ?
`, taskID, discordMessageID).Scan(&status)
	return status, err
}

func (s *Store) updateDiscordMessageStatus(taskID, discordMessageID, status, message string) error {
	taskID = strings.TrimSpace(taskID)
	discordMessageID = strings.TrimSpace(discordMessageID)
	if taskID == "" {
		return fmt.Errorf("task id is required")
	}
	if discordMessageID == "" {
		return fmt.Errorf("discord message id is required")
	}
	_, err := s.db.Exec(`
UPDATE discord_processed_messages
SET status = ?, error = ?, updated_at = CURRENT_TIMESTAMP
WHERE task_id = ? AND discord_message_id = ?
`, status, cleanNullableString(message), taskID, discordMessageID)
	return err
}
