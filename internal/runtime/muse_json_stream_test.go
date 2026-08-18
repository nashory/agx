package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nashory/agx/internal/agentstream"
	"github.com/nashory/agx/internal/db"
)

func TestMuseStreamArgsUsesPersistentSession(t *testing.T) {
	threadID := "session-1"
	task := db.Task{ID: "task-1", Agent: "muse", AllMighty: true, AgentThreadID: &threadID}
	got := museStreamArgs(task, "/workspace", "/tmp/prompt")
	want := []string{"exec", "--json", "--user-input-auto-resolve", "--prompt-file", "/tmp/prompt", "--workspace", "/workspace", "--session-id", "session-1", "--yolo"}
	if !slices.Equal(got, want) {
		t.Fatalf("museStreamArgs() = %#v, want %#v", got, want)
	}
}

func TestWriteMusePromptUsesPrivateFile(t *testing.T) {
	path, err := writeMusePrompt("private prompt")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })
	assertPromptFileIsPrivate(t, path)
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "private prompt" {
		t.Fatalf("prompt content = %q", content)
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

func TestStructuredTurnCleanupDoesNotClearReplacementTurn(t *testing.T) {
	service := NewService("test")
	t.Cleanup(func() { _ = service.agents.Close() })
	task := db.Task{ID: "task-1"}
	_, replacementCancel := context.WithCancel(context.Background())
	t.Cleanup(replacementCancel)
	service.agents.activeTurns[task.ID] = "replacement-turn"
	service.agents.turnCancels[task.ID] = replacementCancel

	service.agents.finishClaudeTurn(task, db.Project{}, "old-turn", false)
	service.agents.finishMuseTurn(task, db.Project{}, "old-turn", false)

	if got := service.agents.activeTurns[task.ID]; got != "replacement-turn" {
		t.Fatalf("active turn = %q, want replacement-turn", got)
	}
	if service.agents.turnCancels[task.ID] == nil {
		t.Fatal("replacement turn cancel was cleared")
	}
}

func TestMuseStreamPublishesTerminalFailureOnce(t *testing.T) {
	posix := "#!/bin/sh\nprintf '%s\\n' '{\"sequence\":1,\"payload_type\":\"run.terminal.failed\",\"payload\":{\"terminal\":\"failed\",\"text\":\"\",\"reason\":\"boom\"}}'\nexit 1\n"
	batch := batchLines(
		"@echo off",
		`echo {"sequence":1,"payload_type":"run.terminal.failed","payload":{"terminal":"failed","text":"","reason":"boom"}}`,
		"exit /b 1",
	)
	writeStubCommandOnPath(t, "muse", posix, batch)

	store, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	project, err := store.EnsureProject(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTaskRuntimeModeInterface(db.NewTaskID(), project.ID, "muse", nil, "muse", true, db.TaskInterfaceDiscord, db.StatusActive, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService("test")
	service.store = store
	service.paths.ConfigDir = t.TempDir()
	t.Cleanup(func() { _ = service.agents.Close() })

	err = service.agents.execMuseStream(context.Background(), task, project, "turn-1", "hello")
	if !errors.Is(err, errStructuredFailurePublished) {
		t.Fatalf("execMuseStream() error = %v, want published failure sentinel", err)
	}
	messages, err := store.ListTaskTranscriptMessages(task.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].Role != "status" || !strings.Contains(messages[0].Body, "boom") {
		t.Fatalf("messages = %#v, want one persisted failure", messages)
	}
}

func TestMuseStreamWaitsForBusySession(t *testing.T) {
	posix := `#!/bin/sh
count=0
if [ -f "$AGX_MUSE_ATTEMPTS" ]; then
  count=$(sed -n '1p' "$AGX_MUSE_ATTEMPTS")
fi
count=$((count + 1))
printf '%s\n' "$count" > "$AGX_MUSE_ATTEMPTS"
if [ "$count" -eq 1 ]; then
  printf '%s\n' 'session test-session is already in use' >&2
  exit 1
fi
printf '%s\n' '{"sequence":1,"payload_type":"run.terminal.completed","payload":{"terminal":"completed","text":"done","reason":null}}'
`
	batch := batchLines(
		"@echo off",
		"setlocal enabledelayedexpansion",
		`set "count=0"`,
		`if exist "%AGX_MUSE_ATTEMPTS%" set /p count=<"%AGX_MUSE_ATTEMPTS%"`,
		"set /a count=count+1",
		`> "%AGX_MUSE_ATTEMPTS%" echo !count!`,
		"if !count! equ 1 (",
		"echo session test-session is already in use 1>&2",
		"exit /b 1",
		")",
		`echo {"sequence":1,"payload_type":"run.terminal.completed","payload":{"terminal":"completed","text":"done","reason":null}}`,
	)
	commandDir := writeStubCommandOnPath(t, "muse", posix, batch)
	attemptFile := filepath.Join(commandDir, "attempts")
	t.Setenv("AGX_MUSE_ATTEMPTS", attemptFile)
	previousDelay := museSessionBusyRetryDelay
	museSessionBusyRetryDelay = time.Millisecond
	t.Cleanup(func() { museSessionBusyRetryDelay = previousDelay })

	store, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	project, err := store.EnsureProject(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTaskRuntimeModeInterface(db.NewTaskID(), project.ID, "muse", nil, "muse", true, db.TaskInterfaceDiscord, db.StatusActive, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService("test")
	service.store = store
	t.Cleanup(func() { _ = service.agents.Close() })

	if err := service.agents.execMuseStream(context.Background(), task, project, "turn-1", "hello"); err != nil {
		t.Fatal(err)
	}
	attempts, err := os.ReadFile(attemptFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(attempts)) != "2" {
		t.Fatalf("attempts = %q, want 2", attempts)
	}
	messages, err := store.ListTaskTranscriptMessages(task.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].Role != "assistant" || messages[0].Body != "done" || messages[0].EventKey == nil || *messages[0].EventKey == "" {
		t.Fatalf("messages = %#v, want recovered assistant response", messages)
	}
}

func TestMuseSessionAlreadyInUse(t *testing.T) {
	if !museSessionAlreadyInUse(errors.New("session abc is already in use")) {
		t.Fatal("expected busy session error to be detected")
	}
	if museSessionAlreadyInUse(errors.New("provider unavailable")) {
		t.Fatal("unrelated error was detected as a busy session")
	}
}

func TestClaudeStreamRequiresTerminalResult(t *testing.T) {
	writeStubCommandOnPath(t, "claude", stubExitZeroPosix, stubExitZeroBatch)

	store, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	project, err := store.EnsureProject(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	task := db.Task{ID: db.NewTaskID(), ProjectID: project.ID, Agent: "claude"}
	service := NewService("test")
	service.store = store
	t.Cleanup(func() { _ = service.agents.Close() })

	err = service.agents.execClaudeStreamOnce(context.Background(), task, project, "turn-1", "hello")
	if err == nil || !strings.Contains(err.Error(), "without a terminal result") {
		t.Fatalf("execClaudeStreamOnce() error = %v, want missing terminal error", err)
	}
}

func TestClaudeStreamReadsPromptFromStdin(t *testing.T) {
	posix := `#!/bin/sh
printf '%s' "$*" > "$AGX_CLAUDE_ARGS"
IFS= read -r prompt
printf '%s' "$prompt" > "$AGX_CLAUDE_STDIN"
printf '%s\n' '{"type":"result","subtype":"success","is_error":false,"session_id":"session-1"}'
`
	batch := batchLines(
		"@echo off",
		"setlocal enabledelayedexpansion",
		`> "%AGX_CLAUDE_ARGS%" echo %*`,
		"set /p prompt=",
		`> "%AGX_CLAUDE_STDIN%" <nul set /p "=!prompt!"`,
		`echo {"type":"result","subtype":"success","is_error":false,"session_id":"session-1"}`,
	)
	commandDir := writeStubCommandOnPath(t, "claude", posix, batch)
	argsFile := filepath.Join(commandDir, "args")
	stdinFile := filepath.Join(commandDir, "stdin")
	t.Setenv("AGX_CLAUDE_ARGS", argsFile)
	t.Setenv("AGX_CLAUDE_STDIN", stdinFile)
	store, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	project, err := store.EnsureProject(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	task := db.Task{ID: db.NewTaskID(), ProjectID: project.ID, Agent: "claude"}
	service := NewService("test")
	service.store = store
	t.Cleanup(func() { _ = service.agents.Close() })

	const prompt = "private-prompt-value"
	if err := service.agents.execClaudeStreamOnce(context.Background(), task, project, "turn-1", prompt); err != nil {
		t.Fatal(err)
	}
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := os.ReadFile(stdinFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(args), prompt) {
		t.Fatalf("Claude argv exposed prompt: %q", args)
	}
	if string(stdin) != prompt {
		t.Fatalf("Claude stdin = %q, want prompt", stdin)
	}
}
