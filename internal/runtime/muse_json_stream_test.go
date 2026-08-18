package runtime

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/nashory/agx/internal/agentstream"
	"github.com/nashory/agx/internal/db"
)

func TestMuseStreamArgsUsesPersistentSession(t *testing.T) {
	threadID := "session-1"
	task := db.Task{ID: "task-1", Agent: "muse", AllMighty: true, AgentThreadID: &threadID}
	got := museStreamArgs(task, "/workspace", "hello")
	want := []string{"exec", "--json", "--workspace", "/workspace", "--session-id", "session-1", "--yolo", "hello"}
	if !slices.Equal(got, want) {
		t.Fatalf("museStreamArgs() = %#v, want %#v", got, want)
	}
}

func TestMuseExecCommandLeavesCustomCommandAlone(t *testing.T) {
	command := filepath.Join(t.TempDir(), "custom-muse")
	if err := os.WriteFile(command, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := museExecCommand(command); got != command {
		t.Fatalf("museExecCommand() = %q, want custom command %q", got, command)
	}
}

func TestMapMuseJSONLineMapsLiveEvents(t *testing.T) {
	task := db.Task{ID: "task-1", Agent: "muse"}

	delta := mapMuseJSONLine(task, "turn-1", []byte(`{"sequence":10,"recorded_at":1000000,"payload_type":"run.output.delta","payload":{"text":"hello "}}`))
	if len(delta) != 1 || delta[0].Kind != agentstream.EventAssistantDelta || delta[0].Text != "hello " {
		t.Fatalf("delta events = %#v", delta)
	}

	tool := mapMuseJSONLine(task, "turn-1", []byte(`{"sequence":11,"payload_type":"task.lifecycle.side_effect_intent","payload":{"event":{"task_id":"tool-task","operation":"tool:bash","idempotency_key":"tool:call-1"}}}`))
	if len(tool) != 1 || tool[0].Kind != agentstream.EventToolStarted || tool[0].Tool == nil || tool[0].Tool.Name != "bash" {
		t.Fatalf("tool events = %#v", tool)
	}

	output := mapMuseJSONLine(task, "turn-1", []byte(`{"sequence":12,"payload_type":"task.lifecycle.output","payload":{"event":{"task_id":"tool-task","chunk":"{\"chunk_id\":\"exec-1\",\"command\":\"pwd\",\"description\":\"Run pwd\",\"exit_code\":0,\"output\":\"/workspace\\n\"}"}}}`))
	if len(output) != 1 || output[0].Kind != agentstream.EventCommandCompleted || output[0].Command == nil || output[0].Command.Command != "Run pwd" || output[0].Command.Stdout != "/workspace\n" {
		t.Fatalf("output events = %#v", output)
	}

	terminal := mapMuseJSONLine(task, "turn-1", []byte(`{"sequence":13,"payload_type":"run.terminal.completed","payload":{"terminal":"completed","text":"hello world","reason":null}}`))
	if len(terminal) != 2 || terminal[0].Kind != agentstream.EventAssistantMessage || terminal[0].Text != "hello world" || terminal[1].Kind != agentstream.EventTurnCompleted {
		t.Fatalf("terminal events = %#v", terminal)
	}
}

func TestMuseIsStructuredRuntimeAgent(t *testing.T) {
	if !isStructuredAgentName("muse") {
		t.Fatal("Muse should use the structured runtime")
	}
	kind := museStreamKind
	task := db.Task{AgentStreamKind: &kind}
	if !isRuntimeStructuredDBTask(task) || !isStructuredStreamTask(task) {
		t.Fatal("Muse JSONL task should be recognized as structured")
	}
}
