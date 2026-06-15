package db

import "testing"

func TestReleaseCompatibilitySchemaIncludesCurrentState(t *testing.T) {
	store, err := OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	for table, columns := range map[string][]string{
		"projects": {
			"id",
			"name",
			"path",
			"description",
			"default_agent",
			"last_opened",
			"created_at",
		},
		"tasks": {
			"id",
			"project_id",
			"title",
			"description",
			"last_user_prompt",
			"interface",
			"workspace_mode",
			"status",
			"agent",
			"all_mighty",
			"agent_thread_id",
			"agent_event_cursor",
			"agent_stream_kind",
		},
		"discord_task_sync_state": {
			"task_id",
			"status",
			"attempts",
			"discord_channel_id",
			"discord_thread_id",
			"last_success_at",
			"last_failure_at",
			"last_error",
			"retry_after",
		},
	} {
		actual, err := tableColumns(store, table)
		if err != nil {
			t.Fatalf("tableColumns(%s): %v", table, err)
		}
		for _, column := range columns {
			if !actual[column] {
				t.Fatalf("%s.%s missing after migration; columns=%v", table, column, actual)
			}
		}
	}
}

func tableColumns(store *Store, table string) (map[string]bool, error) {
	rows, err := store.db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return nil, err
		}
		columns[name] = true
	}
	return columns, rows.Err()
}
