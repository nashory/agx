package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/nashory/agx/internal/agentstream"
	"github.com/nashory/agx/internal/codexapp"
	"github.com/nashory/agx/internal/db"
	agxdiscord "github.com/nashory/agx/internal/discord"
)

const tmuxLogStreamKind = "tmux-log"

type discordLogSubscriber struct {
	runtime *Service
}

type runtimeAgentSubscriber struct {
	runtime *Service
}

func (s runtimeAgentSubscriber) SubscribeAgentEvents(ctx context.Context, task agxdiscord.TaskSummary) (<-chan agentstream.Event, error) {
	if s.runtime.agents != nil && isRuntimeStructuredAgentTask(task) {
		return s.runtime.agents.SubscribeAgentEvents(ctx, task)
	}
	return discordLogSubscriber{runtime: s.runtime}.SubscribeAgentEvents(ctx, task)
}

func (s discordLogSubscriber) SubscribeAgentEvents(ctx context.Context, task agxdiscord.TaskSummary) (<-chan agentstream.Event, error) {
	if task.AgentStreamKind == nil || strings.TrimSpace(*task.AgentStreamKind) != tmuxLogStreamKind {
		return nil, agentstream.UnsupportedError{TaskID: task.ID, Agent: task.Agent}
	}
	ch := make(chan agentstream.Event, 32)
	go s.forwardLogs(ctx, task, ch)
	return ch, nil
}

func isRuntimeStructuredAgentTask(task agxdiscord.TaskSummary) bool {
	if task.AgentStreamKind == nil {
		return false
	}
	kind := strings.TrimSpace(*task.AgentStreamKind)
	return kind == claudeStreamKind || kind == codexapp.StreamKind
}

// isStructuredStreamTask reports whether a task is backed by a structured agent
// event stream (codex/claude) rather than a tmux session, so its logs must come
// from the persisted transcript instead of a pane capture.
func isStructuredStreamTask(task db.Task) bool {
	if task.AgentStreamKind == nil {
		return false
	}
	kind := strings.TrimSpace(*task.AgentStreamKind)
	return kind == claudeStreamKind || kind == codexapp.StreamKind
}

// structuredTaskTranscript renders a structured task's recent transcript as a
// plain-text log for `/task logs`, since these tasks have no tmux pane.
func (s *Service) structuredTaskTranscript(task db.Task, limit int) (string, error) {
	if limit <= 0 {
		limit = 100
	}
	messages, err := s.store.ListTaskTranscriptMessages(task.ID, limit)
	if err != nil {
		return "", err
	}
	parts := make([]string, 0, len(messages))
	for _, message := range messages {
		body := strings.TrimSpace(message.Body)
		if body == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("[%s] %s", message.Role, body))
	}
	return strings.Join(parts, "\n\n"), nil
}

func (s discordLogSubscriber) forwardLogs(ctx context.Context, summary agxdiscord.TaskSummary, ch chan<- agentstream.Event) {
	defer close(ch)
	turnID := strings.TrimSpace(summary.ID)
	if summary.AgentThreadID != nil && strings.TrimSpace(*summary.AgentThreadID) != "" {
		turnID = strings.TrimSpace(*summary.AgentThreadID)
	}
	send := func(event agentstream.Event) bool {
		select {
		case <-ctx.Done():
			return false
		case ch <- event:
			return true
		}
	}
	if !send(agentstream.Event{TaskID: summary.ID, TurnID: turnID, Kind: agentstream.EventTurnStarted, Agent: summary.Agent, CreatedAt: time.Now()}) {
		return
	}

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	var previous string
	var museState museLogState
	for {
		task, project, err := s.runtime.taskAndProject(summary.ID)
		if err != nil {
			_ = send(agentstream.Event{TaskID: summary.ID, TurnID: turnID, Kind: agentstream.EventError, Agent: summary.Agent, Error: err.Error(), CreatedAt: time.Now()})
			return
		}
		logs, err := s.runtime.managerForProject(project).GetLogs(task, 2000)
		if err != nil {
			_ = send(agentstream.Event{TaskID: summary.ID, TurnID: turnID, Kind: agentstream.EventError, Agent: summary.Agent, Error: err.Error(), CreatedAt: time.Now()})
			return
		}
		cleaned := agxdiscord.CleanTerminalOutput(logs)
		if isMuseTask(summary.Agent) {
			for _, event := range museLogEvents(summary.ID, turnID, summary.Agent, cleaned, &museState, time.Now()) {
				if !send(event) {
					return
				}
			}
			if !isRuntimeLogStreamLive(task.Status) {
				_ = send(agentstream.Event{TaskID: summary.ID, TurnID: turnID, Kind: agentstream.EventTurnCompleted, Agent: summary.Agent, CreatedAt: time.Now()})
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			continue
		}
		if delta := logDelta(previous, cleaned); delta != "" {
			if !send(agentstream.Event{TaskID: summary.ID, TurnID: turnID, Kind: agentstream.EventAssistantDelta, Agent: summary.Agent, Text: delta, CreatedAt: time.Now()}) {
				return
			}
			previous = cleaned
		}
		if !isRuntimeLogStreamLive(task.Status) {
			_ = send(agentstream.Event{TaskID: summary.ID, TurnID: turnID, Kind: agentstream.EventTurnCompleted, Agent: summary.Agent, CreatedAt: time.Now()})
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

type museLogState struct {
	processedBlocks int
}

type museLogBlock struct {
	text   string
	status bool
}

func museLogEvents(taskID, turnID, agent, logs string, state *museLogState, now time.Time) []agentstream.Event {
	if state == nil {
		state = &museLogState{}
	}
	var events []agentstream.Event
	blocks := museLogBlocks(logs)
	if state.processedBlocks > len(blocks) {
		state.processedBlocks = 0
	}
	for _, block := range blocks[state.processedBlocks:] {
		if block.status {
			events = append(events, agentstream.Event{
				TaskID:    taskID,
				TurnID:    turnID,
				Kind:      agentstream.EventToolStarted,
				Agent:     agent,
				CreatedAt: now,
				Tool:      &agentstream.ToolEvent{Name: "Muse Code", Input: block.text},
			})
			continue
		}
		events = append(events,
			agentstream.Event{TaskID: taskID, TurnID: turnID, Kind: agentstream.EventAssistantMessage, Agent: agent, Text: block.text, CreatedAt: now},
			agentstream.Event{TaskID: taskID, TurnID: turnID, Kind: agentstream.EventTurnCompleted, Agent: agent, CreatedAt: now},
		)
	}
	state.processedBlocks = len(blocks)
	return events
}

func isMuseTask(agent string) bool {
	return strings.EqualFold(strings.TrimSpace(agent), "muse")
}

func latestMuseAssistantMessage(logs string) (string, bool) {
	if !museComposerReady(logs) {
		return "", false
	}
	var latest string
	for _, block := range museLogBlocks(logs) {
		if !block.status {
			latest = block.text
		}
	}
	if latest == "" {
		return "", false
	}
	return latest, true
}

func latestMuseProgress(logs string) (string, bool) {
	var latest string
	for _, block := range museLogBlocks(logs) {
		if block.status {
			latest = block.text
		}
	}
	if latest == "" {
		return "", false
	}
	return latest, true
}

func museLogBlocks(logs string) []museLogBlock {
	lines := strings.Split(logs, "\n")
	var blocks []museLogBlock
	var current []string
	flush := func() {
		if len(current) == 0 {
			return
		}
		block := strings.TrimSpace(strings.Join(current, "\n"))
		current = nil
		if block != "" {
			blocks = append(blocks, museLogBlock{text: block, status: isMuseStatusBlock(block)})
		}
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "◆ ") {
			flush()
			current = append(current, strings.TrimSpace(strings.TrimPrefix(trimmed, "◆ ")))
			continue
		}
		if len(current) == 0 {
			continue
		}
		if trimmed == "" || strings.HasPrefix(trimmed, "── ") || strings.HasPrefix(trimmed, "⟩") {
			flush()
			continue
		}
		current = append(current, trimmed)
	}
	flush()
	return blocks
}

func museComposerReady(logs string) bool {
	return strings.Contains(logs, "── Voice input") || strings.Contains(logs, "how can I help?")
}

func isMuseStatusBlock(block string) bool {
	first, _, _ := strings.Cut(block, "\n")
	first = strings.TrimSpace(first)
	for _, prefix := range []string{
		"Backgrounded",
		"Finished",
		"Ran command",
		"Ran ",
		"Read ",
		"Worked for",
		"Wrote ",
	} {
		if strings.HasPrefix(first, prefix) {
			return true
		}
	}
	return false
}

func logDelta(previous, current string) string {
	previous = strings.TrimSpace(previous)
	current = strings.TrimSpace(current)
	if current == "" || current == previous {
		return ""
	}
	if previous != "" && strings.HasPrefix(current, previous) {
		return strings.TrimSpace(strings.TrimPrefix(current, previous))
	}
	return current
}

func isRuntimeLogStreamLive(status db.TaskStatus) bool {
	switch status {
	case db.StatusActive, db.StatusWaiting:
		return true
	default:
		return false
	}
}
