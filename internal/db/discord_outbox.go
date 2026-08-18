package db

import (
	"fmt"
	"strings"
)

const (
	DiscordDeliveryMessage     = "message"
	DiscordDeliveryInteractive = "interactive"
)

func (s *Store) EnqueueDiscordDeliveries(deliveries []DiscordDelivery) error {
	if len(deliveries) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, delivery := range deliveries {
		delivery.DeliveryKey = strings.TrimSpace(delivery.DeliveryKey)
		delivery.TaskID = strings.TrimSpace(delivery.TaskID)
		delivery.ChannelID = strings.TrimSpace(delivery.ChannelID)
		delivery.Kind = strings.TrimSpace(delivery.Kind)
		if delivery.DeliveryKey == "" || delivery.TaskID == "" || delivery.ChannelID == "" {
			return fmt.Errorf("discord delivery key, task id, and channel id are required")
		}
		if delivery.Kind != DiscordDeliveryMessage && delivery.Kind != DiscordDeliveryInteractive {
			return fmt.Errorf("invalid discord delivery kind %q", delivery.Kind)
		}
		if strings.TrimSpace(delivery.Content) == "" {
			return fmt.Errorf("discord delivery content is required")
		}
		if _, err := tx.Exec(`
INSERT INTO discord_outbox (delivery_key, task_id, channel_id, kind, content, prompt_json, event_key, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
ON CONFLICT(delivery_key) DO NOTHING
`, delivery.DeliveryKey, delivery.TaskID, delivery.ChannelID, delivery.Kind, delivery.Content, cleanOptionalString(delivery.PromptJSON), cleanNullableString(delivery.EventKey)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) ListPendingDiscordDeliveries(limit int) ([]DiscordDelivery, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.Query(`
SELECT id, delivery_key, task_id, channel_id, kind, content, prompt_json, COALESCE(event_key, ''), attempts, last_error, delivered_at, created_at, updated_at
FROM discord_outbox
WHERE delivered_at IS NULL
ORDER BY id ASC
LIMIT ?
`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var deliveries []DiscordDelivery
	for rows.Next() {
		var delivery DiscordDelivery
		if err := rows.Scan(&delivery.ID, &delivery.DeliveryKey, &delivery.TaskID, &delivery.ChannelID, &delivery.Kind, &delivery.Content, &delivery.PromptJSON, &delivery.EventKey, &delivery.Attempts, &delivery.LastError, &delivery.DeliveredAt, &delivery.CreatedAt, &delivery.UpdatedAt); err != nil {
			return nil, err
		}
		deliveries = append(deliveries, delivery)
	}
	return deliveries, rows.Err()
}

func (s *Store) MarkDiscordDeliveryDelivered(deliveryKey string) error {
	deliveryKey = strings.TrimSpace(deliveryKey)
	if deliveryKey == "" {
		return fmt.Errorf("discord delivery key is required")
	}
	_, err := s.db.Exec(`
UPDATE discord_outbox
SET attempts = attempts + 1, last_error = NULL, delivered_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
WHERE delivery_key = ? AND delivered_at IS NULL
`, deliveryKey)
	return err
}

func (s *Store) MarkDiscordDeliveryFailed(deliveryKey string, cause error) error {
	deliveryKey = strings.TrimSpace(deliveryKey)
	if deliveryKey == "" {
		return fmt.Errorf("discord delivery key is required")
	}
	message := ""
	if cause != nil {
		message = cause.Error()
	}
	_, err := s.db.Exec(`
UPDATE discord_outbox
SET attempts = attempts + 1, last_error = ?, updated_at = CURRENT_TIMESTAMP
WHERE delivery_key = ? AND delivered_at IS NULL
`, cleanNullableString(message), deliveryKey)
	return err
}

func (s *Store) DiscordDelivery(deliveryKey string) (DiscordDelivery, error) {
	deliveryKey = strings.TrimSpace(deliveryKey)
	if deliveryKey == "" {
		return DiscordDelivery{}, fmt.Errorf("discord delivery key is required")
	}
	var delivery DiscordDelivery
	err := s.db.QueryRow(`
SELECT id, delivery_key, task_id, channel_id, kind, content, prompt_json, COALESCE(event_key, ''), attempts, last_error, delivered_at, created_at, updated_at
FROM discord_outbox
WHERE delivery_key = ?
`, deliveryKey).Scan(&delivery.ID, &delivery.DeliveryKey, &delivery.TaskID, &delivery.ChannelID, &delivery.Kind, &delivery.Content, &delivery.PromptJSON, &delivery.EventKey, &delivery.Attempts, &delivery.LastError, &delivery.DeliveredAt, &delivery.CreatedAt, &delivery.UpdatedAt)
	return delivery, err
}
