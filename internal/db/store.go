package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"

	_ "modernc.org/sqlite"

	"github.com/nashory/agx/internal/config"
)

// Store wraps the AGX SQLite database. The connection limit is intentionally one
// because AGX performs small serialized state transitions and uses SQLite WAL
// for safe cross-process reads.
type Store struct {
	db *sql.DB
}

var memoryDBCounter uint64

// Open opens the default runtime database path and applies migrations.
func Open() (*Store, error) {
	return OpenPath(DefaultDBPath())
}

// OpenPath opens or creates an AGX database at path and applies migrations.
func OpenPath(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}
	if err := os.Chmod(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("chmod db dir: %w", err)
	}
	database, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	database.SetMaxOpenConns(1)
	store := &Store{db: database}
	if err := store.init(); err != nil {
		_ = database.Close()
		return nil, err
	}
	return store, nil
}

// OpenMemory returns an isolated in-memory store for tests.
func OpenMemory() (*Store, error) {
	id := atomic.AddUint64(&memoryDBCounter, 1)
	database, err := sql.Open("sqlite", fmt.Sprintf("file:agx-test-%d?mode=memory&cache=shared", id))
	if err != nil {
		return nil, err
	}
	database.SetMaxOpenConns(1)
	store := &Store{db: database}
	if err := store.init(); err != nil {
		_ = database.Close()
		return nil, err
	}
	return store, nil
}

// DefaultDBPath returns the runtime database path under the AGX config
// directory.
func DefaultDBPath() string {
	return filepath.Join(config.ConfigDir(), "agx.db")
}

// Close closes the underlying database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// ResetAll deletes user-visible runtime state while preserving the schema. It is
// used by reset flows that need a clean local playground without deleting the
// database file itself.
func (s *Store) ResetAll() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM project_access_grants; DELETE FROM discord_processed_messages; DELETE FROM discord_mappings; DELETE FROM task_attachments; DELETE FROM tasks; DELETE FROM projects;`); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if _, err := s.db.Exec(`VACUUM;`); err != nil {
		return err
	}
	return nil
}

func (s *Store) init() error {
	if _, err := s.db.Exec(`PRAGMA busy_timeout = 5000; PRAGMA journal_mode = WAL; PRAGMA foreign_keys = ON;`); err != nil {
		return err
	}
	return s.migrate()
}
