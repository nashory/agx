package db

import (
	"database/sql"
	"errors"
	"fmt"

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
