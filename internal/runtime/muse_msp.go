package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/nashory/agx/internal/agentstream"
	"github.com/nashory/agx/internal/db"
	"github.com/nashory/agx/internal/museapp"
)

func taskWithThread(task db.Task, threadID string) db.Task {
	task.AgentThreadID = &threadID
	return task
}

func (s *agentEventService) ensureMuse(ctx context.Context) (museRuntime, error) {
	s.mu.Lock()
	if s.muse != nil {
		client := s.muse
		s.mu.Unlock()
		return client, nil
	}
	start := s.startMuse
	s.mu.Unlock()

	client, err := start(ctx)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	if s.muse != nil {
		existing := s.muse
		s.mu.Unlock()
		_ = client.Close()
		return existing, nil
	}
	s.muse = client
	s.mu.Unlock()
	go s.forwardMuseEvents(client)
	return client, nil
}

func (s *agentEventService) ensureMuseSession(ctx context.Context, client museRuntime, task db.Task, project db.Project) (string, error) {
	threadID := ""
	if task.AgentThreadID != nil {
		threadID = strings.TrimSpace(*task.AgentThreadID)
	}
	if threadID != "" {
		cursor := ""
		if task.AgentEventCursor != nil {
			cursor = strings.TrimSpace(*task.AgentEventCursor)
		}
		resumed, err := client.SessionResume(ctx, threadID, cursor)
		if err == nil {
			s.rememberThread(task.ID, threadID)
			if resumed.Session.ActiveTurnID != nil {
				s.mu.Lock()
				s.activeTurns[task.ID] = strings.TrimSpace(*resumed.Session.ActiveTurnID)
				s.mu.Unlock()
			}
			streamKind := museStreamKind
			if err := s.runtime.store.UpdateTaskAgentStream(task.ID, &threadID, task.AgentEventCursor, &streamKind); err != nil {
				return "", err
			}
			return threadID, nil
		}
		if !museapp.IsSessionNotFound(err) {
			return "", fmt.Errorf("resume Muse session %s: %w", threadID, err)
		}
	}

	threadID = museapp.NewCommandID()
	started, err := client.SessionStart(ctx, threadID, taskWorkingDir(task, project), task.AllMighty)
	if err != nil {
		return "", fmt.Errorf("start Muse session: %w", err)
	}
	if strings.TrimSpace(started.Session.ID) != "" {
		threadID = strings.TrimSpace(started.Session.ID)
	}
	streamKind := museStreamKind
	if err := s.runtime.store.UpdateTaskAgentStream(task.ID, &threadID, nil, &streamKind); err != nil {
		return "", err
	}
	s.rememberThread(task.ID, threadID)
	return threadID, nil
}

func (s *agentEventService) startMuseMSPTurn(ctx context.Context, task db.Task, project db.Project, message string) error {
	client, err := s.ensureMuse(ctx)
	if err != nil {
		return err
	}
	sessionID, err := s.ensureMuseSession(ctx, client, task, project)
	if err != nil {
		return err
	}
	if resolved, err := s.resolveMusePrompt(ctx, client, task.ID, message); resolved || err != nil {
		return err
	}

	s.mu.Lock()
	activeTurn := s.activeTurns[task.ID]
	s.mu.Unlock()
	var turn museapp.TurnResponse
	if activeTurn != "" {
		turn, err = client.TurnSteer(ctx, sessionID, activeTurn, message)
	} else {
		turn, err = client.TurnStart(ctx, sessionID, message, "queue")
	}
	if err != nil {
		return err
	}
	if strings.TrimSpace(turn.TurnID) != "" {
		s.mu.Lock()
		s.activeTurns[task.ID] = strings.TrimSpace(turn.TurnID)
		s.mu.Unlock()
	}
	if err := s.runtime.store.UpdateTaskStatus(task.ID, db.StatusActive); err == nil {
		s.runtime.emitMetadataEvent(task.ProjectID)
		s.runtime.syncDiscordAsync()
	}
	return nil
}

func (s *agentEventService) forwardMuseEvents(client museRuntime) {
	defer s.forgetMuseRuntime(client)
	for notification := range client.Events() {
		sessionID := museapp.SessionID(notification)
		s.mu.Lock()
		taskID := s.threadToTask[sessionID]
		s.mu.Unlock()
		if notification.RequestID != "" {
			if err := client.Respond(notification, map[string]any{}); err != nil {
				logRuntimeOperation("muse_msp_request", "status", "ack_failed", "method", notification.Method, "error", err)
			}
		}
		if taskID == "" {
			continue
		}
		task, err := s.runtime.store.GetTask(taskID)
		if err != nil {
			continue
		}
		if notification.Method == museapp.NotifyApprovalRequest || notification.Method == museapp.NotifyApprovalRequested {
			s.rememberMuseApproval(taskID, notification)
		}
		if notification.Method == museapp.NotifyUserInputRequest || notification.Method == museapp.NotifyUserInputRequested {
			s.rememberMuseQuestion(taskID, notification)
		}
		s.rememberMuseItemTurn(taskID, notification)
		summary := agentstream.TaskSummary{ID: task.ID, Agent: task.Agent, AgentThreadID: task.AgentThreadID}
		events, err := museapp.MapNotification(summary, notification)
		if err != nil {
			logRuntimeOperation("muse_msp_event", "status", "map_failed", "method", notification.Method, "error", err)
			continue
		}
		for _, event := range events {
			if event.TurnID == "" && event.ItemID != "" {
				s.mu.Lock()
				event.TurnID = s.museItemTurns[taskID+":"+event.ItemID]
				s.mu.Unlock()
			}
			if event.Cursor != "" {
				cursor := event.Cursor
				_ = s.runtime.store.UpdateTaskAgentEventCursor(taskID, &cursor)
			}
			if event.Kind == agentstream.EventTurnStarted && event.TurnID != "" {
				s.mu.Lock()
				s.activeTurns[taskID] = event.TurnID
				s.mu.Unlock()
			}
			if event.Kind == agentstream.EventTurnCompleted || event.Kind == agentstream.EventInterrupted || event.Kind == agentstream.EventError {
				s.mu.Lock()
				if event.TurnID == "" || s.activeTurns[taskID] == event.TurnID {
					delete(s.activeTurns, taskID)
					delete(s.musePrompts, taskID)
				}
				s.mu.Unlock()
				_ = s.runtime.store.UpdateTaskStatus(taskID, db.StatusWaiting)
				s.runtime.emitMetadataEvent(task.ProjectID)
				s.runtime.syncDiscordAsync()
			}
			s.publish(taskID, event)
		}
		if notification.Method == museapp.NotifyItemCompleted {
			var params struct {
				Item struct {
					ID string `json:"itemId"`
				} `json:"item"`
			}
			if json.Unmarshal(notification.Params, &params) == nil {
				s.mu.Lock()
				delete(s.museItemTurns, taskID+":"+params.Item.ID)
				s.mu.Unlock()
			}
		}
	}
}

func (s *agentEventService) rememberMuseItemTurn(taskID string, notification museapp.Notification) {
	if notification.Method != museapp.NotifyItemStarted && notification.Method != museapp.NotifyItemUpdated {
		return
	}
	var params struct {
		Item struct {
			ID     string  `json:"itemId"`
			TurnID *string `json:"turnId"`
		} `json:"item"`
	}
	if json.Unmarshal(notification.Params, &params) != nil || params.Item.TurnID == nil || strings.TrimSpace(params.Item.ID) == "" {
		return
	}
	s.mu.Lock()
	s.museItemTurns[taskID+":"+params.Item.ID] = strings.TrimSpace(*params.Item.TurnID)
	s.mu.Unlock()
}

func (s *agentEventService) forgetMuseRuntime(client museRuntime) {
	var activeTaskIDs []string
	s.mu.Lock()
	if s.muse == client {
		s.muse = nil
		for taskID := range s.activeTurns {
			activeTaskIDs = append(activeTaskIDs, taskID)
		}
	}
	s.mu.Unlock()
	if s.ctx.Err() != nil {
		return
	}
	for _, taskID := range activeTaskIDs {
		task, err := s.runtime.store.GetTask(taskID)
		if err != nil || !isMuseTask(task.Agent) {
			continue
		}
		s.mu.Lock()
		turnID := s.activeTurns[taskID]
		delete(s.activeTurns, taskID)
		delete(s.musePrompts, taskID)
		s.mu.Unlock()
		message := "Muse MSP host stopped unexpectedly. Send another message to reconnect the session."
		if stderr := strings.TrimSpace(client.RecentStderr()); stderr != "" {
			message += "\n\nRecent Muse output:\n" + stderr
		}
		s.publish(taskID, agentstream.Event{
			ID:     agentstream.StableEventID(taskID, agentstream.EventError, turnID, "msp-host-stopped"),
			TaskID: taskID, TurnID: turnID, Kind: agentstream.EventError, Agent: task.Agent,
			Error: message, CreatedAt: time.Now(),
		})
		_ = s.runtime.store.UpdateTaskStatus(taskID, db.StatusWaiting)
		s.runtime.emitMetadataEvent(task.ProjectID)
	}
	s.runtime.syncDiscordAsync()
}

func (s *agentEventService) rememberMuseApproval(taskID string, notification museapp.Notification) {
	var params struct {
		SessionID     string          `json:"sessionId"`
		ApprovalID    string          `json:"approvalId"`
		RequirementID json.RawMessage `json:"currentRequirementId"`
		Choices       []struct {
			ID    string `json:"choiceId"`
			Label string `json:"label"`
		} `json:"availableChoices"`
	}
	if json.Unmarshal(notification.Params, &params) != nil {
		return
	}
	var requirement any
	_ = json.Unmarshal(params.RequirementID, &requirement)
	prompt := musePendingPrompt{kind: "approval", sessionID: params.SessionID, id: params.ApprovalID, requirement: requirement, choicesByLabel: map[string]string{}}
	for _, choice := range params.Choices {
		prompt.choicesByLabel[strings.ToLower(strings.TrimSpace(choice.Label))] = choice.ID
	}
	s.mu.Lock()
	s.musePrompts[taskID] = prompt
	s.mu.Unlock()
}

func (s *agentEventService) rememberMuseQuestion(taskID string, notification museapp.Notification) {
	var params struct {
		SessionID   string `json:"sessionId"`
		UserInputID string `json:"userInputId"`
		Questions   []struct {
			ID      string `json:"id"`
			Options []struct {
				Label string `json:"label"`
			} `json:"options"`
		} `json:"questions"`
	}
	if json.Unmarshal(notification.Params, &params) != nil || len(params.Questions) == 0 {
		return
	}
	prompt := musePendingPrompt{kind: "question", sessionID: params.SessionID, id: params.UserInputID, questionID: params.Questions[0].ID, choicesByLabel: map[string]string{}, allowFreeText: len(params.Questions[0].Options) == 0}
	for _, option := range params.Questions[0].Options {
		prompt.choicesByLabel[strings.ToLower(strings.TrimSpace(option.Label))] = option.Label
	}
	s.mu.Lock()
	s.musePrompts[taskID] = prompt
	s.mu.Unlock()
}

func (s *agentEventService) resolveMusePrompt(ctx context.Context, client museRuntime, taskID, message string) (bool, error) {
	label := strings.ToLower(strings.TrimSpace(message))
	s.mu.Lock()
	prompt, ok := s.musePrompts[taskID]
	choice, matches := prompt.choicesByLabel[label]
	if ok && prompt.allowFreeText {
		choice, matches = strings.TrimSpace(message), strings.TrimSpace(message) != ""
	}
	s.mu.Unlock()
	if !ok || !matches {
		return false, nil
	}
	var err error
	if prompt.kind == "approval" {
		err = client.ApprovalDecide(ctx, prompt.sessionID, prompt.id, prompt.requirement, choice)
	} else {
		err = client.UserInputAnswer(ctx, prompt.sessionID, prompt.id, prompt.questionID, choice, prompt.allowFreeText)
	}
	if err == nil {
		s.mu.Lock()
		delete(s.musePrompts, taskID)
		s.mu.Unlock()
	}
	return true, err
}
