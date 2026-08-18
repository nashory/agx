package db

import (
	"database/sql"
	"testing"
)

func TestMigrateV2TaskStatusesFromLegacySchema(t *testing.T) {
	database, err := sql.Open("sqlite", "file:agx-legacy-migration?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = database.Close()
	})
	store := &Store{db: database}
	if _, err := database.Exec(`
CREATE TABLE projects (
	id            TEXT PRIMARY KEY,
	name          TEXT NOT NULL,
	path          TEXT NOT NULL UNIQUE,
	default_agent TEXT,
	last_opened   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE tasks (
	id              TEXT PRIMARY KEY,
	project_id      TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
	title           TEXT NOT NULL,
	description     TEXT,
	status          TEXT NOT NULL DEFAULT 'backlog' CHECK (status IN ('backlog', 'running', 'review', 'done')),
	agent           TEXT NOT NULL,
	session_name    TEXT,
	worktree_path   TEXT,
	branch_name     TEXT,
	created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO projects (id, name, path) VALUES ('project-1', 'project', '/tmp/project');
INSERT INTO tasks (id, project_id, title, status, agent) VALUES
	('task-backlog', 'project-1', 'backlog task', 'backlog', 'claude'),
	('task-running', 'project-1', 'running task', 'running', 'claude'),
	('task-review', 'project-1', 'review task', 'review', 'claude'),
	('task-done', 'project-1', 'done task', 'done', 'claude');
`); err != nil {
		t.Fatal(err)
	}
	if err := store.migrate(); err != nil {
		t.Fatal(err)
	}
	tasks, err := store.ListTasks("project-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(tasks), 3; got != want {
		t.Fatalf("len(tasks) = %d, want %d", got, want)
	}
	statusByID := map[string]TaskStatus{}
	for _, task := range tasks {
		statusByID[task.ID] = task.Status
	}
	for id, want := range map[string]TaskStatus{
		"task-backlog": StatusOffline,
		"task-running": StatusActive,
		"task-review":  StatusComplete,
	} {
		if got := statusByID[id]; got != want {
			t.Fatalf("%s status = %q, want %q", id, got, want)
		}
	}
	if _, ok := statusByID["task-done"]; ok {
		t.Fatal("done task was not deleted during migration")
	}
}

func TestMigrateAddsDiscordDeliveryEventKeys(t *testing.T) {
	database, err := sql.Open("sqlite", "file:agx-event-key-migration?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if _, err := database.Exec(`
CREATE TABLE task_transcript_messages (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	task_id TEXT NOT NULL,
	turn_id TEXT,
	role TEXT NOT NULL,
	body TEXT NOT NULL,
	discord_message_id TEXT,
	created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE discord_outbox (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	delivery_key TEXT NOT NULL UNIQUE,
	task_id TEXT NOT NULL,
	channel_id TEXT NOT NULL,
	kind TEXT NOT NULL,
	content TEXT NOT NULL,
	prompt_json TEXT,
	attempts INTEGER NOT NULL DEFAULT 0,
	last_error TEXT,
	delivered_at DATETIME,
	created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
`); err != nil {
		t.Fatal(err)
	}
	store := &Store{db: database}
	if err := store.migrate(); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"task_transcript_messages", "discord_outbox"} {
		columns, err := tableColumns(store, table)
		if err != nil {
			t.Fatal(err)
		}
		if !columns["event_key"] {
			t.Fatalf("%s.event_key missing after migration", table)
		}
	}
}
