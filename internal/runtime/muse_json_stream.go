package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nashory/agx/internal/agent"
	"github.com/nashory/agx/internal/agentstream"
	"github.com/nashory/agx/internal/db"
)

type museJSONEnvelope struct {
	Sequence    int64           `json:"sequence"`
	RecordedAt  int64           `json:"recorded_at"`
	PayloadType string          `json:"payload_type"`
	Payload     json.RawMessage `json:"payload"`
}

const museSessionBusyRetryAttempts = 61

var museSessionBusyRetryDelay = 2 * time.Second
var museSessionObserveInterval = 250 * time.Millisecond

type museTurnSessionObserver struct {
	task      db.Task
	turnID    string
	startedAt time.Time
	workspace string
	path      string
	offset    int64
	pending   string
	mu        sync.Mutex
}

func (s *agentEventService) startMuseTurn(ctx context.Context, task db.Task, project db.Project, message string) error {
	if err := s.ensureMuseStreamTask(task); err != nil {
		return err
	}
	s.mu.Lock()
	if s.activeTurns[task.ID] != "" {
		s.museQueues[task.ID] = append(s.museQueues[task.ID], message)
		s.mu.Unlock()
		return nil
	}
	turnID, turnCtx, cancel := s.reserveClaudeTurnLocked(task.ID)
	s.mu.Unlock()

	s.launchMuseTurn(task, project, turnID, turnCtx, cancel, message)
	return nil
}

func (s *agentEventService) launchMuseTurn(task db.Task, project db.Project, turnID string, turnCtx context.Context, cancel context.CancelFunc, message string) {
	if err := s.runtime.store.UpdateTaskStatus(task.ID, db.StatusActive); err == nil {
		s.runtime.emitMetadataEvent(task.ProjectID)
	}
	s.publish(task.ID, agentstream.Event{
		ID:        agentstream.StableEventID(task.ID, agentstream.EventTurnStarted, turnID),
		TaskID:    task.ID,
		TurnID:    turnID,
		Kind:      agentstream.EventTurnStarted,
		Agent:     task.Agent,
		CreatedAt: time.Now(),
	})

	go s.runMuseTurn(turnCtx, cancel, task, project, turnID, message)
}

func (s *agentEventService) runMuseTurn(ctx context.Context, cancel context.CancelFunc, task db.Task, project db.Project, turnID, message string) {
	defer cancel()
	err := s.execMuseStream(ctx, task, project, turnID, message)
	if errors.Is(err, errStructuredFailurePublished) {
		_ = s.runtime.store.UpdateTaskStatus(task.ID, db.StatusWaiting)
		s.runtime.emitMetadataEvent(task.ProjectID)
		s.runtime.syncDiscordAsync()
		s.finishMuseTurn(task, project, turnID, false)
		return
	}
	if err != nil {
		kind := agentstream.EventError
		text := strings.TrimSpace(err.Error())
		if text == "" {
			text = "The Muse process failed without an error message."
		}
		if ctx.Err() != nil {
			kind = agentstream.EventInterrupted
			text = "Interrupted."
		}
		s.publish(task.ID, agentstream.Event{
			ID:        agentstream.StableEventID(task.ID, kind, turnID, text),
			TaskID:    task.ID,
			TurnID:    turnID,
			Kind:      kind,
			Agent:     task.Agent,
			Error:     text,
			CreatedAt: time.Now(),
		})
		_ = s.runtime.store.UpdateTaskStatus(task.ID, db.StatusWaiting)
		s.runtime.emitMetadataEvent(task.ProjectID)
		s.runtime.syncDiscordAsync()
		s.finishMuseTurn(task, project, turnID, false)
		return
	}
	_ = s.runtime.store.UpdateTaskStatus(task.ID, db.StatusWaiting)
	s.runtime.emitMetadataEvent(task.ProjectID)
	s.runtime.syncDiscordAsync()
	s.finishMuseTurn(task, project, turnID, true)
}

func (s *agentEventService) finishMuseTurn(task db.Task, project db.Project, completedTurnID string, startQueued bool) {
	var nextMessage string
	var turnID string
	var turnCtx context.Context
	var cancel context.CancelFunc
	s.mu.Lock()
	if s.activeTurns[task.ID] != completedTurnID {
		s.mu.Unlock()
		return
	}
	delete(s.activeTurns, task.ID)
	delete(s.turnCancels, task.ID)
	if !startQueued {
		delete(s.museQueues, task.ID)
	}
	if startQueued && len(s.museQueues[task.ID]) > 0 {
		nextMessage = mergeQueuedClaudeMessages(s.museQueues[task.ID])
		delete(s.museQueues, task.ID)
		if nextMessage != "" {
			turnID, turnCtx, cancel = s.reserveClaudeTurnLocked(task.ID)
		}
	}
	s.mu.Unlock()
	if nextMessage == "" {
		return
	}
	refreshed, err := s.runtime.store.GetTask(task.ID)
	if err != nil {
		cancel()
		s.mu.Lock()
		delete(s.activeTurns, task.ID)
		delete(s.turnCancels, task.ID)
		s.mu.Unlock()
		return
	}
	s.launchMuseTurn(refreshed, project, turnID, turnCtx, cancel, nextMessage)
}

func (s *agentEventService) execMuseStream(ctx context.Context, task db.Task, project db.Project, turnID, message string) error {
	var err error
	for attempt := 0; attempt < museSessionBusyRetryAttempts; attempt++ {
		err = s.execMuseStreamOnce(ctx, task, project, turnID, message)
		if ctx.Err() != nil || !museSessionAlreadyInUse(err) {
			return err
		}
		if attempt == museSessionBusyRetryAttempts-1 {
			break
		}
		timer := time.NewTimer(museSessionBusyRetryDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return fmt.Errorf("Muse session remained busy after waiting for the previous process: %w", err)
}

func (s *agentEventService) execMuseStreamOnce(ctx context.Context, task db.Task, project db.Project, turnID, message string) error {
	registry := agent.RegistryForProject(project.Path)
	ag, err := registry.Get(task.Agent)
	if err != nil {
		return err
	}
	workingDir := taskWorkingDir(task, project)
	promptFile, err := writeMusePrompt(message)
	if err != nil {
		return err
	}
	defer os.Remove(promptFile)
	cmd := exec.CommandContext(ctx, museExecCommand(ag.Command), museStreamArgs(task, workingDir, promptFile)...)
	cmd.Dir = workingDir
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	observer := newMuseTurnSessionObserver(task, turnID, workingDir, time.Now())
	if err := cmd.Start(); err != nil {
		return err
	}
	observerCtx, stopObserver := context.WithCancel(ctx)
	observerDone := make(chan struct{})
	go func() {
		defer close(observerDone)
		observer.observe(observerCtx, func(event agentstream.Event) {
			s.publish(task.ID, event)
		})
	}()

	terminalSeen := false
	failurePublished := false
	readErr := agentstream.ReadJSONLines(stdout, func(line []byte) error {
		for _, event := range mapMuseJSONLine(task, turnID, line) {
			s.publish(task.ID, event)
			if event.Kind == agentstream.EventTurnCompleted {
				terminalSeen = true
			}
			if event.Kind == agentstream.EventError || event.Kind == agentstream.EventInterrupted {
				terminalSeen = true
				failurePublished = true
			}
			if event.Cursor != "" {
				cursor := event.Cursor
				_ = s.runtime.store.UpdateTaskAgentEventCursor(task.ID, &cursor)
			}
		}
		return nil
	})
	waitErr := cmd.Wait()
	stopObserver()
	<-observerDone
	if readErr != nil {
		return readErr
	}
	if failurePublished {
		return errStructuredFailurePublished
	}
	if waitErr != nil {
		errText := strings.TrimSpace(stderr.String())
		if errText == "" {
			errText = waitErr.Error()
		}
		return fmt.Errorf("Muse stream failed: %s", errText)
	}
	if !terminalSeen {
		return fmt.Errorf("Muse stream ended without a terminal result")
	}
	return nil
}

func newMuseTurnSessionObserver(task db.Task, turnID, workingDir string, startedAt time.Time) *museTurnSessionObserver {
	observer := &museTurnSessionObserver{task: task, turnID: turnID, startedAt: startedAt, workspace: workingDir}
	observer.path = findMuseSessionLogForID(museTaskSessionID(task), workingDir)
	if observer.path != "" {
		if info, err := os.Stat(observer.path); err == nil && !info.IsDir() {
			observer.offset = info.Size()
		}
	}
	return observer
}

func (o *museTurnSessionObserver) observe(ctx context.Context, publish func(agentstream.Event)) {
	if o == nil || publish == nil {
		return
	}
	ticker := time.NewTicker(museSessionObserveInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			for _, event := range o.readEvents() {
				publish(event)
			}
			return
		case <-ticker.C:
			for _, event := range o.readEvents() {
				publish(event)
			}
		}
	}
}

func (o *museTurnSessionObserver) readEvents() []agentstream.Event {
	o.mu.Lock()
	defer o.mu.Unlock()
	if strings.TrimSpace(o.path) == "" {
		o.path = findMuseSessionLogForID(museTaskSessionID(o.task), o.workspace)
		if o.path == "" {
			return nil
		}
	}
	file, err := os.Open(o.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			o.path = ""
			o.offset = 0
			o.pending = ""
		}
		return nil
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil
	}
	if o.offset > info.Size() {
		o.offset = 0
		o.pending = ""
	}
	if _, err := file.Seek(o.offset, 0); err != nil {
		return nil
	}
	data, err := io.ReadAll(file)
	if err != nil || len(data) == 0 {
		return nil
	}
	o.offset += int64(len(data))
	text := o.pending + string(data)
	lines := strings.Split(text, "\n")
	if strings.HasSuffix(text, "\n") {
		o.pending = ""
	} else {
		o.pending = lines[len(lines)-1]
		lines = lines[:len(lines)-1]
	}
	var events []agentstream.Event
	for _, line := range lines {
		var record museSessionRecord
		if json.Unmarshal([]byte(line), &record) != nil || record.RecordedAt < o.startedAt.UnixMicro() {
			continue
		}
		events = append(events, mapMuseTurnSessionProgress(o.task, o.turnID, record)...)
	}
	return events
}

func mapMuseTurnSessionProgress(task db.Task, turnID string, record museSessionRecord) []agentstream.Event {
	if record.PayloadType != "runtime.session" || record.Payload.Kind != "run" || len(record.Payload.Event) == 0 {
		return nil
	}
	var envelope museEventEnvelope
	if json.Unmarshal(record.Payload.Event, &envelope) != nil {
		return nil
	}
	createdAt := museRecordTime(record.RecordedAt, time.Now())
	cursor := fmt.Sprintf("%s:%s:session:%d", museStreamKind, turnID, record.Sequence)
	switch envelope.Kind {
	case "assistant_message_committed":
		var message museAssistantMessageEvent
		if json.Unmarshal(record.Payload.Event, &message) != nil || message.Phase != "commentary" || strings.TrimSpace(message.Text) == "" {
			return nil
		}
		return []agentstream.Event{{
			ID:     agentstream.StableEventID(task.ID, agentstream.EventAssistantMessage, turnID, message.MessageID, message.Text),
			TaskID: task.ID, TurnID: turnID, ItemID: message.MessageID, Kind: agentstream.EventAssistantMessage,
			Agent: task.Agent, CreatedAt: createdAt, Cursor: cursor, Text: message.Text,
		}}
	case "assistant_tool_calls_committed":
		var calls museToolCallsEvent
		if json.Unmarshal(record.Payload.Event, &calls) != nil {
			return nil
		}
		events := make([]agentstream.Event, 0, len(calls.ToolCalls))
		for _, call := range calls.ToolCalls {
			input := museToolInput(call.Args)
			if strings.TrimSpace(input) == "" {
				continue
			}
			name := firstNonEmpty(call.Name, "Muse Code")
			id := callID(call)
			events = append(events, agentstream.Event{
				ID:     agentstream.StableEventID(task.ID, agentstream.EventToolStarted, turnID, id, name),
				TaskID: task.ID, TurnID: turnID, ItemID: id, Kind: agentstream.EventToolStarted,
				Agent: task.Agent, CreatedAt: createdAt, Cursor: cursor, Tool: &agentstream.ToolEvent{ID: id, Name: name, Input: input},
			})
		}
		return events
	default:
		return nil
	}
}

func museTaskSessionID(task db.Task) string {
	if task.AgentThreadID != nil && strings.TrimSpace(*task.AgentThreadID) != "" {
		return strings.TrimSpace(*task.AgentThreadID)
	}
	return strings.TrimSpace(task.ID)
}

func findMuseSessionLogForID(sessionID, workspaceRoot string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ""
	}
	paths, _ := filepath.Glob(filepath.Join(museDataDir(), "sessions", "*", "*", "*", sessionID, "session.jsonl"))
	sort.SliceStable(paths, func(i, j int) bool {
		left, leftErr := os.Stat(paths[i])
		right, rightErr := os.Stat(paths[j])
		if leftErr != nil {
			return false
		}
		if rightErr != nil {
			return true
		}
		return left.ModTime().After(right.ModTime())
	})
	for _, path := range paths {
		if matches, err := museSessionLogMatchesWorkspace(path, canonicalPath(workspaceRoot)); err == nil && matches {
			return path
		}
	}
	return ""
}

func museSessionAlreadyInUse(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "session") && strings.Contains(message, "already in use")
}

func museStreamArgs(task db.Task, workingDir, promptFile string) []string {
	threadID := task.ID
	if task.AgentThreadID != nil && strings.TrimSpace(*task.AgentThreadID) != "" {
		threadID = strings.TrimSpace(*task.AgentThreadID)
	}
	args := []string{"exec", "--json", "--user-input-auto-resolve", "--prompt-file", promptFile, "--workspace", workingDir, "--session-id", threadID}
	if task.AllMighty {
		args = append(args, "--yolo")
	}
	return args
}

func writeMusePrompt(message string) (string, error) {
	file, err := os.CreateTemp("", "agx-muse-prompt-*")
	if err != nil {
		return "", err
	}
	path := file.Name()
	if _, err := file.WriteString(message); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

// Meta's launcher wraps muse.real and can remain alive after a headless child
// exits. Prefer the sibling real binary when the standard command resolves to
// that launcher; custom Muse commands continue to run unchanged.
func museExecCommand(command string) string {
	path, err := exec.LookPath(command)
	if err != nil {
		return command
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		resolved = path
	}
	candidate := filepath.Join(filepath.Dir(resolved), "muse.real")
	if info, err := os.Stat(candidate); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
		return candidate
	}
	return command
}

func mapMuseJSONLine(task db.Task, turnID string, line []byte) []agentstream.Event {
	line = bytes.TrimSpace(line)
	if len(line) == 0 || line[0] != '{' {
		return nil
	}
	var envelope museJSONEnvelope
	if json.Unmarshal(line, &envelope) != nil {
		return nil
	}
	createdAt := museRecordTime(envelope.RecordedAt, time.Now())
	cursor := fmt.Sprintf("%s:%s:%d", museStreamKind, turnID, envelope.Sequence)
	eventID := func(kind agentstream.EventKind, parts ...string) string {
		return agentstream.StableEventID(task.ID, kind, append([]string{turnID, fmt.Sprint(envelope.Sequence)}, parts...)...)
	}

	switch envelope.PayloadType {
	case "run.output.delta":
		var payload struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(envelope.Payload, &payload) != nil || payload.Text == "" {
			return nil
		}
		return []agentstream.Event{{
			ID: eventID(agentstream.EventAssistantDelta), TaskID: task.ID, TurnID: turnID,
			Kind: agentstream.EventAssistantDelta, Agent: task.Agent, CreatedAt: createdAt,
			Cursor: cursor, Text: payload.Text,
		}}
	case "task.lifecycle.side_effect_intent":
		var payload struct {
			Event struct {
				TaskID         string `json:"task_id"`
				Operation      string `json:"operation"`
				IdempotencyKey string `json:"idempotency_key"`
			} `json:"event"`
		}
		if json.Unmarshal(envelope.Payload, &payload) != nil || !strings.HasPrefix(payload.Event.Operation, "tool:") {
			return nil
		}
		name := strings.TrimPrefix(payload.Event.Operation, "tool:")
		return []agentstream.Event{{
			ID: eventID(agentstream.EventToolStarted, payload.Event.IdempotencyKey), TaskID: task.ID, TurnID: turnID,
			ItemID: payload.Event.TaskID, Kind: agentstream.EventToolStarted, Agent: task.Agent, CreatedAt: createdAt,
			Cursor: cursor, Tool: &agentstream.ToolEvent{ID: payload.Event.IdempotencyKey, Name: name},
		}}
	case "task.lifecycle.output":
		var payload struct {
			Event struct {
				TaskID string          `json:"task_id"`
				Chunk  json.RawMessage `json:"chunk"`
			} `json:"event"`
		}
		if json.Unmarshal(envelope.Payload, &payload) != nil {
			return nil
		}
		chunk, ok := parseMuseOutputChunk(payload.Event.Chunk)
		if !ok {
			return nil
		}
		return []agentstream.Event{{
			ID: eventID(agentstream.EventCommandCompleted, chunk.ChunkID), TaskID: task.ID, TurnID: turnID,
			ItemID: firstNonEmpty(chunk.ChunkID, payload.Event.TaskID), Kind: agentstream.EventCommandCompleted,
			Agent: task.Agent, CreatedAt: createdAt, Cursor: cursor,
			Command: &agentstream.CommandEvent{ID: chunk.ChunkID, Command: firstNonEmpty(chunk.Description, chunk.Command), ExitCode: chunk.ExitCode, Stdout: chunk.Output},
		}}
	}

	if !strings.HasPrefix(envelope.PayloadType, "run.terminal.") {
		return nil
	}
	var terminal struct {
		Terminal string  `json:"terminal"`
		Text     string  `json:"text"`
		Reason   *string `json:"reason"`
	}
	if json.Unmarshal(envelope.Payload, &terminal) != nil {
		return nil
	}
	if terminal.Terminal == "completed" {
		events := make([]agentstream.Event, 0, 2)
		if strings.TrimSpace(terminal.Text) != "" {
			events = append(events, agentstream.Event{
				ID: eventID(agentstream.EventAssistantMessage, terminal.Text), TaskID: task.ID, TurnID: turnID,
				Kind: agentstream.EventAssistantMessage, Agent: task.Agent, CreatedAt: createdAt,
				Cursor: cursor + ":message", Text: terminal.Text,
			})
		}
		events = append(events, agentstream.Event{
			ID: eventID(agentstream.EventTurnCompleted), TaskID: task.ID, TurnID: turnID,
			Kind: agentstream.EventTurnCompleted, Agent: task.Agent, CreatedAt: createdAt,
			Cursor: cursor + ":completed",
		})
		return events
	}
	reason := strings.TrimSpace(valueOrDefault(terminal.Reason, terminal.Text))
	if reason == "" {
		reason = "Muse returned " + firstNonEmpty(terminal.Terminal, "an error") + "."
	}
	kind := agentstream.EventError
	if terminal.Terminal == "cancelled" || terminal.Terminal == "interrupted" {
		kind = agentstream.EventInterrupted
	}
	return []agentstream.Event{{
		ID: eventID(kind, reason), TaskID: task.ID, TurnID: turnID, Kind: kind,
		Agent: task.Agent, CreatedAt: createdAt, Cursor: cursor, Error: reason,
	}}
}
