package runtime

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nashory/agx/internal/agentstream"
	"github.com/nashory/agx/internal/db"
)

const museSessionLogStreamKind = "muse-session-log"
const maxMuseSessionLogCandidates = 200
const maxMuseSessionLogHeaderLines = 300

type museSessionLogState struct {
	path        string
	offset      int64
	pending     string
	turnID      string
	initialized bool
}

type museSessionRecord struct {
	Sequence    int64       `json:"sequence"`
	RecordedAt  int64       `json:"recorded_at"`
	PayloadType string      `json:"payload_type"`
	Payload     musePayload `json:"payload"`
}

type musePayload struct {
	Kind    string          `json:"kind"`
	RunID   string          `json:"run_id"`
	Event   json.RawMessage `json:"event"`
	Record  json.RawMessage `json:"record"`
	Outcome struct {
		Kind  string `json:"kind"`
		RunID string `json:"run_id"`
	} `json:"outcome"`
}

type museSessionRootRecord struct {
	CWD           string `json:"cwd"`
	WorkspaceRoot string `json:"workspace_root"`
}

type museEventEnvelope struct {
	Kind string `json:"kind"`
}

type museAssistantMessageEvent struct {
	MessageID string `json:"message_id"`
	Text      string `json:"text"`
	Phase     string `json:"phase"`
}

type museToolCallsEvent struct {
	MessageID string         `json:"message_id"`
	ToolCalls []museToolCall `json:"tool_calls"`
}

type museToolCall struct {
	ID     string          `json:"id"`
	CallID string          `json:"call_id"`
	Name   string          `json:"name"`
	Args   json.RawMessage `json:"args"`
}

type museToolArgs struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

type museTaskOutputEvent struct {
	TaskID string          `json:"task_id"`
	Chunk  json.RawMessage `json:"chunk"`
}

type museOutputChunk struct {
	ChunkID        string `json:"chunk_id"`
	Command        string `json:"command"`
	Description    string `json:"description"`
	ExitCode       *int   `json:"exit_code"`
	TerminalStatus string `json:"terminal_status"`
	Output         string `json:"output"`
}

type museTerminalEvent struct {
	Terminal string `json:"terminal"`
	Reason   string `json:"reason"`
}

func (s *Service) museSessionLogEvents(task db.Task, project db.Project, state *museSessionLogState, now time.Time) ([]agentstream.Event, bool) {
	if state == nil {
		state = &museSessionLogState{}
	}
	path, err := findMuseSessionLogPath(taskWorkingDir(task, project))
	if err != nil {
		return nil, false
	}
	if path == "" {
		return nil, false
	}
	if strings.TrimSpace(state.path) == "" {
		state.path = path
	} else if canonicalPath(path) != canonicalPath(state.path) {
		// A restarted Muse process writes to a new session directory while the
		// previous JSONL remains on disk. Switch immediately and read the new
		// file from its beginning so a fast first response cannot be skipped.
		state.path = path
		state.offset = 0
		state.pending = ""
		state.turnID = ""
		state.initialized = true
	}
	events, err := readMuseSessionLogEvents(task, state, now)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			state.path = ""
			state.offset = 0
			state.pending = ""
			state.initialized = false
			return nil, false
		}
		return nil, false
	}
	return events, true
}

func findMuseSessionLogPath(workspaceRoot string) (string, error) {
	workspaceRoot = canonicalPath(workspaceRoot)
	if workspaceRoot == "" {
		return "", nil
	}
	path, err := findIndexedMuseSessionLogPath(workspaceRoot)
	if err != nil || path != "" {
		return path, err
	}
	return findRecentMuseSessionLogPath(workspaceRoot)
}

func findIndexedMuseSessionLogPath(workspaceRoot string) (string, error) {
	indexPath := filepath.Join(museDataDir(), "session-index.db")
	if _, err := os.Stat(indexPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	database, err := sql.Open("sqlite", "file:"+filepath.ToSlash(indexPath)+"?mode=ro")
	if err != nil {
		return "", err
	}
	defer database.Close()

	rows, err := database.Query(`SELECT workspace_root, session_log_path, updated_at_us FROM sessions WHERE session_log_path IS NOT NULL ORDER BY updated_at_us DESC LIMIT 200`)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	type candidate struct {
		path      string
		updatedAt int64
	}
	var candidates []candidate
	for rows.Next() {
		var root, path string
		var updatedAt sql.NullInt64
		if err := rows.Scan(&root, &path, &updatedAt); err != nil {
			return "", err
		}
		if canonicalPath(root) != workspaceRoot {
			continue
		}
		if strings.TrimSpace(path) == "" {
			continue
		}
		candidates = append(candidates, candidate{path: path, updatedAt: updatedAt.Int64})
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].updatedAt > candidates[j].updatedAt
	})
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate.path); err == nil && !info.IsDir() {
			return candidate.path, nil
		}
	}
	return "", nil
}

func findRecentMuseSessionLogPath(workspaceRoot string) (string, error) {
	paths, err := filepath.Glob(filepath.Join(museDataDir(), "sessions", "*", "*", "*", "*", "session.jsonl"))
	if err != nil {
		return "", err
	}
	type candidate struct {
		path    string
		modTime time.Time
	}
	candidates := make([]candidate, 0, len(paths))
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		candidates = append(candidates, candidate{path: path, modTime: info.ModTime()})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].modTime.After(candidates[j].modTime)
	})
	if len(candidates) > maxMuseSessionLogCandidates {
		candidates = candidates[:maxMuseSessionLogCandidates]
	}
	for _, candidate := range candidates {
		matches, err := museSessionLogMatchesWorkspace(candidate.path, workspaceRoot)
		if err != nil {
			continue
		}
		if matches {
			return candidate.path, nil
		}
	}
	return "", nil
}

func museSessionLogMatchesWorkspace(path, workspaceRoot string) (bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lines := 0
	for scanner.Scan() {
		lines++
		if lines > maxMuseSessionLogHeaderLines {
			break
		}
		var record museSessionRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			continue
		}
		if record.PayloadType != "runtime.session.metadata" && record.PayloadType != "runtime.session.route_facts" {
			continue
		}
		var rootRecord museSessionRootRecord
		if len(record.Payload.Record) > 0 && json.Unmarshal(record.Payload.Record, &rootRecord) == nil {
			if museSessionRecordMatchesWorkspace(rootRecord, workspaceRoot) {
				return true, nil
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return false, err
	}
	return false, nil
}

func museSessionRecordMatchesWorkspace(record museSessionRootRecord, workspaceRoot string) bool {
	return canonicalPath(record.WorkspaceRoot) == workspaceRoot || canonicalPath(record.CWD) == workspaceRoot
}

func museDataDir() string {
	if dataHome := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); dataHome != "" {
		return filepath.Join(dataHome, "muse")
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return filepath.Join(".local", "share", "muse")
	}
	return filepath.Join(home, ".local", "share", "muse")
}

func canonicalPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if evaluated, err := filepath.EvalSymlinks(path); err == nil {
		path = evaluated
	}
	return filepath.Clean(path)
}

func readMuseSessionLogEvents(task db.Task, state *museSessionLogState, now time.Time) ([]agentstream.Event, error) {
	file, err := os.Open(state.path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if state.offset > info.Size() {
		state.offset = 0
		state.pending = ""
		state.initialized = false
	}
	if !state.initialized {
		state.offset = info.Size()
		state.initialized = true
		return nil, nil
	}
	if _, err := file.Seek(state.offset, io.SeekStart); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, nil
	}
	state.offset += int64(len(data))

	text := state.pending + string(data)
	lines := strings.Split(text, "\n")
	if !strings.HasSuffix(text, "\n") {
		state.pending = lines[len(lines)-1]
		lines = lines[:len(lines)-1]
	} else {
		state.pending = ""
	}

	var events []agentstream.Event
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var record museSessionRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			continue
		}
		events = append(events, mapMuseSessionRecord(task, &state.turnID, record, now)...)
	}
	return events, nil
}

func mapMuseSessionRecord(task db.Task, activeTurnID *string, record museSessionRecord, now time.Time) []agentstream.Event {
	createdAt := museRecordTime(record.RecordedAt, now)
	cursor := fmt.Sprintf("%s:%d", museSessionLogStreamKind, record.Sequence)
	turnID := strings.TrimSpace(valueOrDefault(activeTurnID, task.ID))

	if record.PayloadType == "runtime.user_intent.materialized" {
		if record.Payload.Outcome.Kind == "top_level_turn_started" && strings.TrimSpace(record.Payload.Outcome.RunID) != "" {
			*activeTurnID = strings.TrimSpace(record.Payload.Outcome.RunID)
			return []agentstream.Event{{
				ID:        agentstream.StableEventID(task.ID, agentstream.EventTurnStarted, *activeTurnID),
				TaskID:    task.ID,
				TurnID:    *activeTurnID,
				Kind:      agentstream.EventTurnStarted,
				Agent:     task.Agent,
				CreatedAt: createdAt,
				Cursor:    cursor,
			}}
		}
		return nil
	}
	if record.PayloadType != "runtime.session" || len(record.Payload.Event) == 0 {
		return nil
	}

	var envelope museEventEnvelope
	if err := json.Unmarshal(record.Payload.Event, &envelope); err != nil || envelope.Kind == "" {
		return nil
	}
	if strings.TrimSpace(record.Payload.RunID) != "" {
		turnID = strings.TrimSpace(record.Payload.RunID)
		*activeTurnID = turnID
	}

	switch envelope.Kind {
	case "reasoning_committed":
		return []agentstream.Event{{
			ID:        agentstream.StableEventID(task.ID, agentstream.EventThinkingDelta, turnID, fmt.Sprint(record.Sequence)),
			TaskID:    task.ID,
			TurnID:    turnID,
			Kind:      agentstream.EventThinkingDelta,
			Agent:     task.Agent,
			CreatedAt: createdAt,
			Cursor:    cursor,
		}}
	case "assistant_message_committed":
		var event museAssistantMessageEvent
		if json.Unmarshal(record.Payload.Event, &event) != nil || strings.TrimSpace(event.Text) == "" {
			return nil
		}
		return []agentstream.Event{{
			ID:        agentstream.StableEventID(task.ID, agentstream.EventAssistantMessage, turnID, event.MessageID, event.Text),
			TaskID:    task.ID,
			TurnID:    turnID,
			ItemID:    event.MessageID,
			Kind:      agentstream.EventAssistantMessage,
			Agent:     task.Agent,
			CreatedAt: createdAt,
			Cursor:    cursor,
			Text:      event.Text,
		}}
	case "assistant_tool_calls_committed":
		var event museToolCallsEvent
		if json.Unmarshal(record.Payload.Event, &event) != nil {
			return nil
		}
		events := make([]agentstream.Event, 0, len(event.ToolCalls))
		for _, call := range event.ToolCalls {
			name := strings.TrimSpace(call.Name)
			if name == "" {
				name = "Muse Code"
			}
			events = append(events, agentstream.Event{
				ID:        agentstream.StableEventID(task.ID, agentstream.EventToolStarted, turnID, callID(call), name),
				TaskID:    task.ID,
				TurnID:    turnID,
				ItemID:    callID(call),
				Kind:      agentstream.EventToolStarted,
				Agent:     task.Agent,
				CreatedAt: createdAt,
				Cursor:    cursor,
				Tool:      &agentstream.ToolEvent{ID: callID(call), Name: name, Input: museToolInput(call.Args)},
			})
		}
		return events
	case "output":
		var event museTaskOutputEvent
		if json.Unmarshal(record.Payload.Event, &event) != nil {
			return nil
		}
		chunk, ok := parseMuseOutputChunk(event.Chunk)
		if !ok || strings.TrimSpace(chunk.Output) == "" {
			return nil
		}
		return []agentstream.Event{{
			ID:        agentstream.StableEventID(task.ID, agentstream.EventCommandCompleted, turnID, chunk.ChunkID, chunk.Command, chunk.Output),
			TaskID:    task.ID,
			TurnID:    turnID,
			ItemID:    chunk.ChunkID,
			Kind:      agentstream.EventCommandCompleted,
			Agent:     task.Agent,
			CreatedAt: createdAt,
			Cursor:    cursor,
			Command:   &agentstream.CommandEvent{ID: chunk.ChunkID, Command: firstNonEmpty(chunk.Description, chunk.Command), ExitCode: chunk.ExitCode, Stdout: chunk.Output},
		}}
	case "terminal":
		var event museTerminalEvent
		if json.Unmarshal(record.Payload.Event, &event) != nil {
			return nil
		}
		if event.Terminal == "completed" {
			return []agentstream.Event{{
				ID:        agentstream.StableEventID(task.ID, agentstream.EventTurnCompleted, turnID, fmt.Sprint(record.Sequence)),
				TaskID:    task.ID,
				TurnID:    turnID,
				Kind:      agentstream.EventTurnCompleted,
				Agent:     task.Agent,
				CreatedAt: createdAt,
				Cursor:    cursor,
			}}
		}
		if strings.TrimSpace(event.Reason) != "" {
			return []agentstream.Event{{
				ID:        agentstream.StableEventID(task.ID, agentstream.EventError, turnID, event.Reason),
				TaskID:    task.ID,
				TurnID:    turnID,
				Kind:      agentstream.EventError,
				Agent:     task.Agent,
				CreatedAt: createdAt,
				Cursor:    cursor,
				Error:     event.Reason,
			}}
		}
	}
	return nil
}

func museRecordTime(recordedAt int64, fallback time.Time) time.Time {
	if recordedAt <= 0 {
		return fallback
	}
	return time.UnixMicro(recordedAt)
}

func valueOrDefault(value *string, fallback string) string {
	if value == nil || strings.TrimSpace(*value) == "" {
		return fallback
	}
	return strings.TrimSpace(*value)
}

func callID(call museToolCall) string {
	if strings.TrimSpace(call.CallID) != "" {
		return strings.TrimSpace(call.CallID)
	}
	return strings.TrimSpace(call.ID)
}

func museToolInput(raw json.RawMessage) string {
	raw = bytesTrim(raw)
	if len(raw) == 0 {
		return ""
	}
	var encoded string
	if json.Unmarshal(raw, &encoded) == nil {
		raw = []byte(encoded)
	}
	var args museToolArgs
	if json.Unmarshal(raw, &args) == nil {
		return strings.TrimSpace(strings.Join([]string{args.Description, args.Command}, "\n"))
	}
	return string(raw)
}

func parseMuseOutputChunk(raw json.RawMessage) (museOutputChunk, bool) {
	raw = bytesTrim(raw)
	if len(raw) == 0 {
		return museOutputChunk{}, false
	}
	var encoded string
	if json.Unmarshal(raw, &encoded) == nil {
		raw = []byte(encoded)
	}
	var chunk museOutputChunk
	if err := json.Unmarshal(raw, &chunk); err != nil {
		return museOutputChunk{}, false
	}
	return chunk, true
}

func bytesTrim(raw json.RawMessage) json.RawMessage {
	return json.RawMessage(strings.TrimSpace(string(raw)))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
