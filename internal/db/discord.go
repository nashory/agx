package db

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

var ErrDiscordMappingNotFound = errors.New("discord mapping not found")

const (
	DiscordAGXControl = "control"
	DiscordAGXProject = "project"
	DiscordAGXTask    = "task"

	DiscordTypeCategory = "category"
	DiscordTypeChannel  = "channel"

	DiscordControlAGXID = "agx-control"
)

func (s *Store) UpsertDiscordMapping(agxType, agxID, discordType, discordID string) (DiscordMapping, error) {
	if err := validateDiscordMapping(agxType, agxID, discordType, discordID); err != nil {
		return DiscordMapping{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return DiscordMapping{}, err
	}
	defer func() {
		_ = tx.Rollback()
	}()
	if _, err := tx.Exec(`
DELETE FROM discord_mappings
WHERE discord_type = ?
  AND discord_id = ?
  AND (agx_type != ? OR agx_id != ?)
`, discordType, discordID, agxType, agxID); err != nil {
		return DiscordMapping{}, err
	}
	id := uuid.NewString()
	if _, err := tx.Exec(`
INSERT INTO discord_mappings (id, agx_type, agx_id, discord_type, discord_id, updated_at)
VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
ON CONFLICT(agx_type, agx_id) DO UPDATE SET
	discord_type = excluded.discord_type,
	discord_id = excluded.discord_id,
	updated_at = CURRENT_TIMESTAMP
`, id, agxType, agxID, discordType, discordID); err != nil {
		return DiscordMapping{}, err
	}
	if err := tx.Commit(); err != nil {
		return DiscordMapping{}, err
	}
	return s.GetDiscordMapping(agxType, agxID)
}

func (s *Store) GetDiscordMapping(agxType, agxID string) (DiscordMapping, error) {
	row := s.db.QueryRow(`
SELECT id, agx_type, agx_id, discord_type, discord_id, created_at, updated_at
FROM discord_mappings
WHERE agx_type = ? AND agx_id = ?
`, agxType, agxID)
	return scanDiscordMapping(row)
}

func (s *Store) GetDiscordMappingByDiscordID(discordID string) (DiscordMapping, error) {
	row := s.db.QueryRow(`
SELECT id, agx_type, agx_id, discord_type, discord_id, created_at, updated_at
FROM discord_mappings
WHERE discord_id = ?
`, discordID)
	return scanDiscordMapping(row)
}

func (s *Store) ListDiscordMappings() ([]DiscordMapping, error) {
	rows, err := s.db.Query(`
SELECT id, agx_type, agx_id, discord_type, discord_id, created_at, updated_at
FROM discord_mappings
ORDER BY created_at ASC, id ASC
`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var mappings []DiscordMapping
	for rows.Next() {
		mapping, err := scanDiscordMapping(rows)
		if err != nil {
			return nil, err
		}
		mappings = append(mappings, mapping)
	}
	return mappings, rows.Err()
}

func (s *Store) DeleteDiscordMapping(agxType, agxID string) error {
	result, err := s.db.Exec(`
DELETE FROM discord_mappings
WHERE agx_type = ? AND agx_id = ?
`, agxType, agxID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("get discord mapping rows affected: %w", err)
	}
	if n == 0 {
		return ErrDiscordMappingNotFound
	}
	return nil
}

func (s *Store) UpsertDiscordTaskSyncPending(taskID string) (DiscordTaskSyncState, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return DiscordTaskSyncState{}, fmt.Errorf("task id is required")
	}
	if _, err := s.db.Exec(`
INSERT INTO discord_task_sync_state (task_id, status, attempts, updated_at)
VALUES (?, ?, 1, CURRENT_TIMESTAMP)
ON CONFLICT(task_id) DO UPDATE SET
	status = excluded.status,
	attempts = discord_task_sync_state.attempts + 1,
	last_error = NULL,
	retry_after = NULL,
	updated_at = CURRENT_TIMESTAMP
`, taskID, string(DiscordTaskSyncPending)); err != nil {
		return DiscordTaskSyncState{}, err
	}
	return s.GetDiscordTaskSyncState(taskID)
}

func (s *Store) MarkDiscordTaskSyncSuccess(taskID, channelID string) (DiscordTaskSyncState, error) {
	taskID = strings.TrimSpace(taskID)
	channelID = strings.TrimSpace(channelID)
	if taskID == "" {
		return DiscordTaskSyncState{}, fmt.Errorf("task id is required")
	}
	if channelID == "" {
		return DiscordTaskSyncState{}, fmt.Errorf("discord channel id is required")
	}
	if _, err := s.db.Exec(`
INSERT INTO discord_task_sync_state (task_id, status, attempts, discord_channel_id, last_success_at, updated_at)
VALUES (?, ?, 0, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
ON CONFLICT(task_id) DO UPDATE SET
	status = excluded.status,
	discord_channel_id = excluded.discord_channel_id,
	last_success_at = CURRENT_TIMESTAMP,
	last_error = NULL,
	retry_after = NULL,
	updated_at = CURRENT_TIMESTAMP
`, taskID, string(DiscordTaskSyncSynced), channelID); err != nil {
		return DiscordTaskSyncState{}, err
	}
	return s.GetDiscordTaskSyncState(taskID)
}

func (s *Store) MarkDiscordTaskSyncFailure(taskID string, syncErr error) (DiscordTaskSyncState, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return DiscordTaskSyncState{}, fmt.Errorf("task id is required")
	}
	message := ""
	if syncErr != nil {
		message = syncErr.Error()
	}
	if _, err := s.db.Exec(`
INSERT INTO discord_task_sync_state (task_id, status, attempts, last_failure_at, last_error, updated_at)
VALUES (?, ?, 1, CURRENT_TIMESTAMP, ?, CURRENT_TIMESTAMP)
ON CONFLICT(task_id) DO UPDATE SET
	status = excluded.status,
	attempts = discord_task_sync_state.attempts + 1,
	last_failure_at = CURRENT_TIMESTAMP,
	last_error = excluded.last_error,
	updated_at = CURRENT_TIMESTAMP
`, taskID, string(DiscordTaskSyncFailed), message); err != nil {
		return DiscordTaskSyncState{}, err
	}
	return s.GetDiscordTaskSyncState(taskID)
}

func (s *Store) GetDiscordTaskSyncState(taskID string) (DiscordTaskSyncState, error) {
	row := s.db.QueryRow(`
SELECT task_id, status, attempts, discord_channel_id, discord_thread_id, last_success_at, last_failure_at, last_error, retry_after, created_at, updated_at
FROM discord_task_sync_state
WHERE task_id = ?
`, taskID)
	return scanDiscordTaskSyncState(row)
}

func (s *Store) backfillDiscordTaskSyncState() error {
	_, err := s.db.Exec(`
INSERT INTO discord_task_sync_state (task_id, status, discord_channel_id, last_success_at, updated_at)
SELECT tasks.id, ?, discord_mappings.discord_id, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
FROM tasks
JOIN discord_mappings
  ON discord_mappings.agx_type = ?
 AND discord_mappings.agx_id = tasks.id
WHERE discord_mappings.discord_type = ?
ON CONFLICT(task_id) DO UPDATE SET
	discord_channel_id = excluded.discord_channel_id,
	last_success_at = COALESCE(discord_task_sync_state.last_success_at, excluded.last_success_at),
	updated_at = CURRENT_TIMESTAMP
`, string(DiscordTaskSyncSynced), DiscordAGXTask, DiscordTypeChannel)
	return err
}

func validateDiscordMapping(agxType, agxID, discordType, discordID string) error {
	if agxID == "" {
		return fmt.Errorf("agx id is required")
	}
	if discordID == "" {
		return fmt.Errorf("discord id is required")
	}
	switch agxType {
	case DiscordAGXControl, DiscordAGXProject, DiscordAGXTask:
	default:
		return fmt.Errorf("invalid agx type %q", agxType)
	}
	switch discordType {
	case DiscordTypeCategory, DiscordTypeChannel:
	default:
		return fmt.Errorf("invalid discord type %q", discordType)
	}
	return nil
}

func scanDiscordMapping(scanner interface {
	Scan(dest ...any) error
}) (DiscordMapping, error) {
	var mapping DiscordMapping
	err := scanner.Scan(&mapping.ID, &mapping.AGXType, &mapping.AGXID, &mapping.DiscordType, &mapping.DiscordID, &mapping.CreatedAt, &mapping.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return DiscordMapping{}, ErrDiscordMappingNotFound
	}
	return mapping, err
}

func scanDiscordTaskSyncState(scanner interface {
	Scan(dest ...any) error
}) (DiscordTaskSyncState, error) {
	var state DiscordTaskSyncState
	var channelID, threadID, lastError sql.NullString
	var lastSuccess, lastFailure, retryAfter sql.NullTime
	err := scanner.Scan(
		&state.TaskID,
		&state.Status,
		&state.Attempts,
		&channelID,
		&threadID,
		&lastSuccess,
		&lastFailure,
		&lastError,
		&retryAfter,
		&state.CreatedAt,
		&state.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return DiscordTaskSyncState{}, ErrDiscordMappingNotFound
	}
	if err != nil {
		return DiscordTaskSyncState{}, err
	}
	if channelID.Valid {
		state.DiscordChannelID = &channelID.String
	}
	if threadID.Valid {
		state.DiscordThreadID = &threadID.String
	}
	if lastSuccess.Valid {
		state.LastSuccessAt = &lastSuccess.Time
	}
	if lastFailure.Valid {
		state.LastFailureAt = &lastFailure.Time
	}
	if lastError.Valid {
		state.LastError = &lastError.String
	}
	if retryAfter.Valid {
		state.RetryAfter = &retryAfter.Time
	}
	return state, nil
}
