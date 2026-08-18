package runtime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nashory/agx/internal/agentstream"
	"github.com/nashory/agx/internal/codexapp"
)

func TestEnrichCodexError(t *testing.T) {
	if got := enrichCodexError("boom", ""); got != "boom" {
		t.Fatalf("no stderr: got %q, want unchanged message", got)
	}
	got := enrichCodexError(codexapp.ErrorNoDetail, "panic: nil map\ngoroutine 1")
	if !strings.Contains(got, "panic: nil map") || strings.Contains(got, codexapp.ErrorNoDetail) {
		t.Fatalf("fallback+stderr: got %q, want stderr replacing the no-detail fallback", got)
	}
	got = enrichCodexError("stream closed", "auth token expired")
	if !strings.Contains(got, "stream closed") || !strings.Contains(got, "auth token expired") {
		t.Fatalf("message+stderr: got %q, want both", got)
	}
	if got := enrichCodexError("dup context", "dup context"); got != "dup context" {
		t.Fatalf("duplicate stderr: got %q, want no duplication", got)
	}
}

func TestAttachAgentErrorDiagnosticWritesJSONL(t *testing.T) {
	service := NewService("test")
	service.paths.ConfigDir = t.TempDir()
	when := time.Date(2026, 8, 10, 14, 53, 0, 0, time.UTC)

	event := service.agents.attachAgentErrorDiagnostic("task-1", agentstream.Event{
		TaskID:    "task-1",
		TurnID:    "turn-1",
		ID:        "event-1",
		Kind:      agentstream.EventError,
		Agent:     "codex",
		CreatedAt: when,
		Error:     "This content was flagged for possible cybersecurity risk.\nRecent codex output: details",
	})
	if event.DiagnosticID == "" {
		t.Fatal("DiagnosticID is empty")
	}

	path := filepath.Join(service.paths.ConfigDir, "logs", "agent-errors", "2026-08-10.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var record agentErrorDiagnosticRecord
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatalf("diagnostic JSON = %q: %v", string(data), err)
	}
	if record.ID != event.DiagnosticID {
		t.Fatalf("record.ID = %q, want %q", record.ID, event.DiagnosticID)
	}
	if record.TaskID != "task-1" || record.TurnID != "turn-1" || record.Agent != "codex" {
		t.Fatalf("record identity fields = %#v", record)
	}
	if !strings.Contains(record.RawError, "cybersecurity risk") {
		t.Fatalf("RawError = %q, want raw classifier message", record.RawError)
	}
	if strings.Contains(record.Summary, "\n") || !strings.Contains(record.Summary, "cybersecurity risk") {
		t.Fatalf("Summary = %q, want first-line classifier summary", record.Summary)
	}
}
