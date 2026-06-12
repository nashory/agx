package db

import (
	"database/sql"
	"path/filepath"
)

func (s *Store) MarkProjectAccessGranted(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`
INSERT INTO project_access_grants (path)
VALUES (?)
ON CONFLICT(path) DO UPDATE SET updated_at = CURRENT_TIMESTAMP
`, abs)
	return err
}

func (s *Store) HasProjectAccessGrant(path string) (bool, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return false, err
	}
	var exists int
	err = s.db.QueryRow(`SELECT 1 FROM project_access_grants WHERE path = ?`, abs).Scan(&exists)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
