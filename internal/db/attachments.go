package db

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

var ErrTaskAttachmentNotFound = errors.New("task attachment not found")

// CreateTaskAttachment inserts attachment metadata, or returns the existing row
// for the same Discord message and attachment. This makes Discord gateway
// redelivery safe to process repeatedly.
func (s *Store) CreateTaskAttachment(attachment TaskAttachment) (TaskAttachment, error) {
	attachment.TaskID = strings.TrimSpace(attachment.TaskID)
	attachment.DiscordMessageID = strings.TrimSpace(attachment.DiscordMessageID)
	attachment.DiscordAttachmentID = strings.TrimSpace(attachment.DiscordAttachmentID)
	attachment.Filename = strings.TrimSpace(attachment.Filename)
	attachment.ContentType = strings.TrimSpace(attachment.ContentType)
	attachment.LocalPath = strings.TrimSpace(attachment.LocalPath)
	attachment.SourceURL = strings.TrimSpace(attachment.SourceURL)
	attachment.SHA256 = strings.TrimSpace(attachment.SHA256)
	if attachment.TaskID == "" {
		return TaskAttachment{}, fmt.Errorf("task id is required")
	}
	if attachment.DiscordMessageID == "" {
		return TaskAttachment{}, fmt.Errorf("discord message id is required")
	}
	if attachment.DiscordAttachmentID == "" {
		return TaskAttachment{}, fmt.Errorf("discord attachment id is required")
	}
	if attachment.Filename == "" {
		return TaskAttachment{}, fmt.Errorf("filename is required")
	}
	if attachment.SizeBytes < 0 {
		return TaskAttachment{}, fmt.Errorf("size bytes must be non-negative")
	}
	if attachment.LocalPath == "" {
		return TaskAttachment{}, fmt.Errorf("local path is required")
	}
	id := strings.TrimSpace(attachment.ID)
	if id == "" {
		id = uuid.NewString()
	}
	_, err := s.db.Exec(`
INSERT INTO task_attachments (
	id, task_id, discord_message_id, discord_attachment_id, filename,
	content_type, size_bytes, local_path, source_url, sha256
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(task_id, discord_message_id, discord_attachment_id) DO NOTHING
`, id, attachment.TaskID, attachment.DiscordMessageID, attachment.DiscordAttachmentID, attachment.Filename, cleanNullableString(attachment.ContentType), attachment.SizeBytes, attachment.LocalPath, cleanNullableString(attachment.SourceURL), cleanNullableString(attachment.SHA256))
	if err != nil {
		return TaskAttachment{}, err
	}
	return s.GetTaskAttachmentByDiscord(attachment.TaskID, attachment.DiscordMessageID, attachment.DiscordAttachmentID)
}

func (s *Store) GetTaskAttachmentByDiscord(taskID, discordMessageID, discordAttachmentID string) (TaskAttachment, error) {
	row := s.db.QueryRow(`
SELECT id, task_id, discord_message_id, discord_attachment_id, filename, content_type, size_bytes, local_path, source_url, sha256, created_at
FROM task_attachments
WHERE task_id = ? AND discord_message_id = ? AND discord_attachment_id = ?
`, strings.TrimSpace(taskID), strings.TrimSpace(discordMessageID), strings.TrimSpace(discordAttachmentID))
	return scanTaskAttachment(row)
}

func (s *Store) ListTaskAttachments(taskID string) ([]TaskAttachment, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil, fmt.Errorf("task id is required")
	}
	rows, err := s.db.Query(`
SELECT id, task_id, discord_message_id, discord_attachment_id, filename, content_type, size_bytes, local_path, source_url, sha256, created_at
FROM task_attachments
WHERE task_id = ?
ORDER BY created_at ASC, id ASC
`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTaskAttachments(rows)
}

func (s *Store) ListAllTaskAttachments() ([]TaskAttachment, error) {
	rows, err := s.db.Query(`
SELECT id, task_id, discord_message_id, discord_attachment_id, filename, content_type, size_bytes, local_path, source_url, sha256, created_at
FROM task_attachments
ORDER BY created_at ASC, id ASC
`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTaskAttachments(rows)
}

func (s *Store) ListPrunableTaskAttachments(createdBefore time.Time) ([]TaskAttachment, error) {
	rows, err := s.db.Query(`
SELECT a.id, a.task_id, a.discord_message_id, a.discord_attachment_id, a.filename, a.content_type, a.size_bytes, a.local_path, a.source_url, a.sha256, a.created_at
FROM task_attachments a
JOIN tasks t ON t.id = a.task_id
WHERE t.status IN ('complete', 'offline') AND a.created_at < ?
ORDER BY a.created_at ASC, a.id ASC
`, createdBefore.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTaskAttachments(rows)
}

func (s *Store) DeleteTaskAttachment(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("attachment id is required")
	}
	result, err := s.db.Exec(`DELETE FROM task_attachments WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("get attachment rows affected: %w", err)
	}
	if n == 0 {
		return ErrTaskAttachmentNotFound
	}
	return nil
}

func scanTaskAttachment(scanner interface {
	Scan(dest ...any) error
}) (TaskAttachment, error) {
	var attachment TaskAttachment
	var contentType, sourceURL, sha256 sql.NullString
	err := scanner.Scan(
		&attachment.ID,
		&attachment.TaskID,
		&attachment.DiscordMessageID,
		&attachment.DiscordAttachmentID,
		&attachment.Filename,
		&contentType,
		&attachment.SizeBytes,
		&attachment.LocalPath,
		&sourceURL,
		&sha256,
		&attachment.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return TaskAttachment{}, ErrTaskAttachmentNotFound
	}
	if err != nil {
		return TaskAttachment{}, err
	}
	attachment.ContentType = contentType.String
	attachment.SourceURL = sourceURL.String
	attachment.SHA256 = sha256.String
	return attachment, nil
}

func scanTaskAttachments(rows *sql.Rows) ([]TaskAttachment, error) {
	var attachments []TaskAttachment
	for rows.Next() {
		attachment, err := scanTaskAttachment(rows)
		if err != nil {
			return nil, err
		}
		attachments = append(attachments, attachment)
	}
	return attachments, rows.Err()
}

func cleanNullableString(value string) any {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return value
}
