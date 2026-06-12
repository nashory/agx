package db

import (
	"database/sql"
	"strings"
)

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS schema_migrations (
	version INTEGER PRIMARY KEY
);

CREATE TABLE IF NOT EXISTS projects (
	id            TEXT PRIMARY KEY,
	name          TEXT NOT NULL,
	path          TEXT NOT NULL UNIQUE,
	description   TEXT,
	default_agent TEXT,
	last_opened   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS tasks (
	id              TEXT PRIMARY KEY,
	project_id      TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
	title           TEXT NOT NULL,
	description     TEXT,
	last_user_prompt TEXT,
	interface       TEXT NOT NULL DEFAULT 'local' CHECK (interface IN ('local', 'discord')),
	workspace_mode TEXT NOT NULL DEFAULT 'worktree' CHECK (workspace_mode IN ('worktree', 'project')),
	status          TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'waiting', 'complete', 'offline')),
	agent           TEXT NOT NULL,
	all_mighty      INTEGER NOT NULL DEFAULT 0,
	session_name    TEXT,
	worktree_path   TEXT,
	branch_name     TEXT,
	base_branch     TEXT,
	agent_thread_id TEXT,
	agent_event_cursor TEXT,
	agent_stream_kind TEXT,
	created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_tasks_project ON tasks(project_id);
CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status);

CREATE TABLE IF NOT EXISTS task_transcript_messages (
	id                 INTEGER PRIMARY KEY AUTOINCREMENT,
	task_id            TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
	turn_id            TEXT,
	role               TEXT NOT NULL CHECK (role IN ('user', 'assistant', 'system', 'status', 'tool_trace')),
	body               TEXT NOT NULL,
	discord_message_id TEXT,
	created_at         DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at         DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_task_transcript_task_created ON task_transcript_messages(task_id, created_at, id);

CREATE TABLE IF NOT EXISTS task_attachments (
	id                    TEXT PRIMARY KEY,
	task_id               TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
	discord_message_id    TEXT NOT NULL,
	discord_attachment_id TEXT NOT NULL,
	filename              TEXT NOT NULL,
	content_type          TEXT,
	size_bytes            INTEGER NOT NULL,
	local_path            TEXT NOT NULL,
	source_url            TEXT,
	sha256                TEXT,
	created_at            DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	UNIQUE(task_id, discord_message_id, discord_attachment_id)
);

CREATE INDEX IF NOT EXISTS idx_task_attachments_task_id ON task_attachments(task_id);
CREATE INDEX IF NOT EXISTS idx_task_attachments_created_at ON task_attachments(created_at);
CREATE INDEX IF NOT EXISTS idx_task_attachments_discord_msg ON task_attachments(task_id, discord_message_id);

CREATE TABLE IF NOT EXISTS discord_processed_messages (
	task_id            TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
	discord_message_id TEXT NOT NULL,
	status             TEXT NOT NULL CHECK (status IN ('processing', 'delivered', 'failed')),
	error              TEXT,
	created_at         DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at         DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	PRIMARY KEY(task_id, discord_message_id)
);

CREATE TABLE IF NOT EXISTS discord_mappings (
	id            TEXT PRIMARY KEY,
	agx_type      TEXT NOT NULL,
	agx_id        TEXT NOT NULL,
	discord_type  TEXT NOT NULL,
	discord_id    TEXT NOT NULL,
	created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	UNIQUE(agx_type, agx_id),
	UNIQUE(discord_type, discord_id)
);

CREATE INDEX IF NOT EXISTS idx_discord_agx ON discord_mappings(agx_type, agx_id);
CREATE INDEX IF NOT EXISTS idx_discord_id ON discord_mappings(discord_id);

CREATE TABLE IF NOT EXISTS project_access_grants (
	path       TEXT PRIMARY KEY,
	granted_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT OR IGNORE INTO schema_migrations(version) VALUES (1);
`)
	if err != nil {
		return err
	}
	if err := s.ensureColumn("projects", "description", "TEXT"); err != nil {
		return err
	}
	if err := s.ensureColumn("tasks", "base_branch", "TEXT"); err != nil {
		return err
	}
	if err := s.ensureColumn("tasks", "all_mighty", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := s.ensureColumn("tasks", "agent_thread_id", "TEXT"); err != nil {
		return err
	}
	if err := s.ensureColumn("tasks", "agent_event_cursor", "TEXT"); err != nil {
		return err
	}
	if err := s.ensureColumn("tasks", "agent_stream_kind", "TEXT"); err != nil {
		return err
	}
	if err := s.ensureColumn("tasks", "last_user_prompt", "TEXT"); err != nil {
		return err
	}
	if err := s.ensureColumn("tasks", "interface", "TEXT NOT NULL DEFAULT 'local'"); err != nil {
		return err
	}
	if err := s.ensureColumn("tasks", "workspace_mode", "TEXT NOT NULL DEFAULT 'worktree'"); err != nil {
		return err
	}
	if _, err := s.db.Exec(`
UPDATE tasks
SET interface = 'discord'
WHERE id IN (
	SELECT agx_id FROM discord_mappings WHERE agx_type = 'task'
)
`); err != nil {
		return err
	}
	return s.migrateV2TaskStatuses()
}

func (s *Store) ensureColumn(table, column, definition string) error {
	if !validMigrationColumn(table, column, definition) {
		return ErrInvalidMigrationColumn
	}
	rows, err := s.db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return err
		}
		if name == column {
			return rows.Err()
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = s.db.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + column + ` ` + definition)
	return err
}

var ErrInvalidMigrationColumn = errInvalidMigrationColumn{}

type errInvalidMigrationColumn struct{}

func (errInvalidMigrationColumn) Error() string {
	return "invalid migration column"
}

func validMigrationColumn(table, column, definition string) bool {
	switch table {
	case "projects":
		return column == "description" && definition == "TEXT"
	case "tasks":
		return (column == "base_branch" && definition == "TEXT") ||
			(column == "all_mighty" && definition == "INTEGER NOT NULL DEFAULT 0") ||
			(column == "agent_thread_id" && definition == "TEXT") ||
			(column == "agent_event_cursor" && definition == "TEXT") ||
			(column == "agent_stream_kind" && definition == "TEXT") ||
			(column == "last_user_prompt" && definition == "TEXT") ||
			(column == "interface" && definition == "TEXT NOT NULL DEFAULT 'local'") ||
			(column == "workspace_mode" && definition == "TEXT NOT NULL DEFAULT 'worktree'")
	default:
		return false
	}
}

func (s *Store) migrateV2TaskStatuses() error {
	var schema string
	if err := s.db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'tasks'`).Scan(&schema); err != nil {
		return err
	}
	if strings.Contains(schema, "'active'") &&
		strings.Contains(schema, "'waiting'") &&
		strings.Contains(schema, "'complete'") &&
		strings.Contains(schema, "'offline'") &&
		!strings.Contains(schema, "'backlog'") {
		_, err := s.db.Exec(`
UPDATE tasks SET status = 'offline' WHERE status NOT IN ('active', 'waiting', 'complete', 'offline');
`)
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if err := migrateV2TaskStatusesTx(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func migrateV2TaskStatusesTx(tx *sql.Tx) error {
	_, err := tx.Exec(`
DROP TABLE IF EXISTS tasks_v2;

CREATE TABLE tasks_v2 (
	id              TEXT PRIMARY KEY,
	project_id      TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
	title           TEXT NOT NULL,
	description     TEXT,
	last_user_prompt TEXT,
	interface       TEXT NOT NULL DEFAULT 'local' CHECK (interface IN ('local', 'discord')),
	workspace_mode TEXT NOT NULL DEFAULT 'worktree' CHECK (workspace_mode IN ('worktree', 'project')),
	status          TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'waiting', 'complete', 'offline')),
	agent           TEXT NOT NULL,
	all_mighty      INTEGER NOT NULL DEFAULT 0,
	session_name    TEXT,
	worktree_path   TEXT,
	branch_name     TEXT,
	base_branch     TEXT,
	agent_thread_id TEXT,
	agent_event_cursor TEXT,
	agent_stream_kind TEXT,
	created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO tasks_v2 (id, project_id, title, description, last_user_prompt, interface, workspace_mode, status, agent, all_mighty, session_name, worktree_path, branch_name, base_branch, agent_thread_id, agent_event_cursor, agent_stream_kind, created_at, updated_at)
SELECT id,
       project_id,
       title,
       description,
       last_user_prompt,
       COALESCE(interface, 'local'),
       COALESCE(workspace_mode, 'worktree'),
       CASE status
         WHEN 'backlog' THEN 'offline'
         WHEN 'running' THEN 'active'
         WHEN 'review' THEN 'complete'
         WHEN 'active' THEN 'active'
         WHEN 'waiting' THEN 'waiting'
         WHEN 'complete' THEN 'complete'
         WHEN 'offline' THEN 'offline'
         ELSE 'offline'
       END,
       agent,
       all_mighty,
       session_name,
       worktree_path,
       branch_name,
       base_branch,
       agent_thread_id,
       agent_event_cursor,
       agent_stream_kind,
       created_at,
       updated_at
FROM tasks
WHERE status != 'done';

DROP TABLE tasks;
ALTER TABLE tasks_v2 RENAME TO tasks;
CREATE INDEX IF NOT EXISTS idx_tasks_project ON tasks(project_id);
CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status);
`)
	return err
}
