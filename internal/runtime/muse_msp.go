package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/nashory/agx/internal/agentstream"
	"github.com/nashory/agx/internal/db"
	agxdiscord "github.com/nashory/agx/internal/discord"
	"github.com/nashory/agx/internal/museapp"
)

func taskWithThread(task db.Task, threadID string) db.Task {
	task.AgentThreadID = &threadID
	return task
}

func (s *agentEventService) ensureMuse(ctx context.Context, allMighty bool) (museRuntime, error) {
	s.mu.Lock()
	current := s.muse
	if allMighty {
		current = s.museAllMighty
	}
	if current != nil {
		client := current
		s.mu.Unlock()
		return client, nil
	}
	start := s.startMuse
	s.mu.Unlock()

	client, err := start(ctx, allMighty)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	current = s.muse
	if allMighty {
		current = s.museAllMighty
	}
	if current != nil {
		existing := current
		s.mu.Unlock()
		_ = client.Close()
		return existing, nil
	}
	if allMighty {
		s.museAllMighty = client
	} else {
		s.muse = client
	}
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
		s.rememberThread(task.ID, threadID)
		cursor := ""
		if task.AgentEventCursor != nil {
			cursor = strings.TrimSpace(*task.AgentEventCursor)
		}
		resumed, err := client.SessionResume(ctx, threadID, cursor)
		if err == nil {
			s.mu.Lock()
			if resumed.Session.ActiveTurnID != nil {
				s.activeTurns[task.ID] = strings.TrimSpace(*resumed.Session.ActiveTurnID)
			} else {
				delete(s.activeTurns, task.ID)
			}
			s.mu.Unlock()
			streamKind := museStreamKind
			if err := s.runtime.store.UpdateTaskAgentStream(task.ID, &threadID, task.AgentEventCursor, &streamKind); err != nil {
				return "", err
			}
			s.mu.Lock()
			s.museTaskHosts[task.ID] = client
			s.mu.Unlock()
			return threadID, nil
		}
		if !museapp.IsSessionNotFound(err) {
			s.forgetThreadMapping(task.ID, threadID)
			return "", fmt.Errorf("resume Muse session %s: %w", threadID, err)
		}
		s.forgetThreadMapping(task.ID, threadID)
	}

	threadID = museapp.NewCommandID()
	s.rememberThread(task.ID, threadID)
	started, err := client.SessionStart(ctx, threadID, taskWorkingDir(task, project), task.AllMighty)
	if err != nil {
		s.forgetThreadMapping(task.ID, threadID)
		return "", fmt.Errorf("start Muse session: %w", err)
	}
	if strings.TrimSpace(started.Session.ID) != "" {
		actualID := strings.TrimSpace(started.Session.ID)
		if actualID != threadID {
			s.mu.Lock()
			delete(s.threadToTask, threadID)
			s.threadToTask[actualID] = task.ID
			s.mu.Unlock()
		}
		threadID = actualID
	}
	streamKind := museStreamKind
	if err := s.runtime.store.UpdateTaskAgentStream(task.ID, &threadID, nil, &streamKind); err != nil {
		return "", err
	}
	s.rememberThread(task.ID, threadID)
	s.mu.Lock()
	s.museTaskHosts[task.ID] = client
	s.mu.Unlock()
	return threadID, nil
}

func (s *agentEventService) forgetThreadMapping(taskID, threadID string) {
	s.mu.Lock()
	if s.threadToTask[threadID] == taskID {
		delete(s.threadToTask, threadID)
	}
	s.mu.Unlock()
}

func (s *agentEventService) startMuseMSPTurn(ctx context.Context, task db.Task, project db.Project, message string, promptTokens ...string) error {
	client, err := s.ensureMuse(ctx, task.AllMighty)
	if err != nil {
		return err
	}
	sessionID, err := s.ensureMuseSession(ctx, client, task, project)
	if err != nil {
		return err
	}
	promptToken := ""
	if len(promptTokens) != 0 {
		promptToken = promptTokens[0]
	}
	if resolved, err := s.resolveMusePrompt(ctx, client, task.ID, message, promptToken); resolved || err != nil {
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
		if taskID == "" {
			continue
		}
		if notification.Method == museapp.NotifyViewGap {
			var gap struct {
				After string `json:"after"`
				Next  string `json:"next"`
			}
			if json.Unmarshal(notification.Params, &gap) != nil || gap.After == "" || gap.Next == "" {
				continue
			}
			s.mu.Lock()
			alreadyRecovering := s.museGapActive[sessionID]
			if !alreadyRecovering {
				s.museGapActive[sessionID] = true
			} else {
				s.museGapQueue[sessionID] = append(s.museGapQueue[sessionID], museGapRange{after: gap.After, next: gap.Next})
			}
			s.mu.Unlock()
			if !alreadyRecovering {
				go s.recoverMuseGap(client, taskID, sessionID, gap.After, gap.Next)
			}
			continue
		}
		task, err := s.runtime.store.GetTask(taskID)
		if err != nil {
			continue
		}
		taskLock := s.runtime.taskLock(taskID)
		if notification.Method == museapp.NotifyApprovalRequest || notification.Method == museapp.NotifyApprovalRequested {
			s.rememberMuseApproval(taskID, notification)
		}
		if notification.Method == museapp.NotifyUserInputRequest || notification.Method == museapp.NotifyUserInputRequested {
			s.rememberMuseQuestion(taskID, notification)
		}
		if notification.RequestID != "" {
			if err := client.Respond(notification, map[string]any{}); err != nil {
				logRuntimeOperation("muse_msp_request", "status", "ack_failed", "method", notification.Method, "error", err)
			}
		}
		taskLock.Lock()
		s.rememberMuseItemTurn(taskID, notification)
		summary := agentstream.TaskSummary{ID: task.ID, Agent: task.Agent, AgentThreadID: task.AgentThreadID}
		events, err := museapp.MapNotification(summary, notification)
		if err != nil {
			logRuntimeOperation("muse_msp_event", "status", "map_failed", "method", notification.Method, "error", err)
			taskLock.Unlock()
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
				if current := s.activeTurns[taskID]; current == "" || current == event.TurnID {
					s.activeTurns[taskID] = event.TurnID
				}
				s.mu.Unlock()
			}
			if event.Kind == agentstream.EventTurnCompleted || event.Kind == agentstream.EventInterrupted || event.Kind == agentstream.EventError {
				s.mu.Lock()
				currentTurn := s.activeTurns[taskID]
				isCurrent := event.TurnID == "" || currentTurn == "" || currentTurn == event.TurnID
				if isCurrent {
					delete(s.activeTurns, taskID)
					delete(s.musePrompts, taskID)
				}
				s.mu.Unlock()
				if isCurrent {
					_ = s.runtime.store.UpdateTaskStatus(taskID, db.StatusWaiting)
					s.runtime.emitMetadataEvent(task.ProjectID)
					s.runtime.syncDiscordAsync()
				}
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
		taskLock.Unlock()
	}
}

func (s *agentEventService) recoverMuseGap(client museRuntime, taskID, sessionID, after, next string) {
	for {
		s.recoverMuseGapRange(client, taskID, sessionID, after, next)
		s.mu.Lock()
		queued := s.museGapQueue[sessionID]
		if len(queued) == 0 {
			delete(s.museGapActive, sessionID)
			delete(s.museGapQueue, sessionID)
			s.mu.Unlock()
			return
		}
		after, next = queued[0].after, queued[0].next
		s.museGapQueue[sessionID] = queued[1:]
		s.mu.Unlock()
	}
}

func (s *agentEventService) recoverMuseGapRange(client museRuntime, taskID, sessionID, after, next string) {
	ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
	defer cancel()
	cursor := after
	for {
		page, err := client.ViewPage(ctx, sessionID, cursor, 1000)
		if err != nil {
			if museapp.IsMethodNotFound(err) {
				unsubscribeErr := client.ViewUnsubscribe(ctx, sessionID)
				if unsubscribeErr == nil {
					unsubscribeErr = client.ViewSubscribe(ctx, sessionID, after)
				}
				if unsubscribeErr == nil {
					logRuntimeOperation("muse_msp_gap", "status", "recovered_by_subscribe", "task", taskID)
					return
				}
				if museapp.IsMethodNotFound(unsubscribeErr) {
					if _, resumeErr := client.SessionResume(ctx, sessionID, after); resumeErr == nil {
						s.publish(taskID, agentstream.Event{ID: agentstream.StableEventID(taskID, agentstream.EventError, "muse-gap", after, next), TaskID: taskID, Kind: agentstream.EventError, Agent: "muse", Error: "Muse dropped part of the event stream and this Muse build does not expose its advertised replay API. Live streaming resumed, but some intermediate output may be missing.", CreatedAt: time.Now()})
						logRuntimeOperation("muse_msp_gap", "status", "resume_without_replay", "task", taskID)
						return
					}
				}
			}
			logRuntimeOperation("muse_msp_gap", "status", "page_failed", "task", taskID, "error", err)
			return
		}
		reachedNext := false
		for _, notification := range page.Events {
			var params struct {
				ViewCursor string `json:"viewCursor"`
			}
			_ = json.Unmarshal(notification.Params, &params)
			if params.ViewCursor == next {
				reachedNext = true
				break
			}
			s.replayMuseNotification(taskID, notification)
		}
		if reachedNext {
			break
		}
		if page.NextCursor == nil || strings.TrimSpace(*page.NextCursor) == "" || *page.NextCursor == cursor {
			logRuntimeOperation("muse_msp_gap", "status", "boundary_not_found", "task", taskID)
			return
		}
		cursor = *page.NextCursor
	}
	logRuntimeOperation("muse_msp_gap", "status", "recovered", "task", taskID)
}

func (s *agentEventService) replayMuseNotification(taskID string, notification museapp.Notification) {
	task, err := s.runtime.store.GetTask(taskID)
	if err != nil {
		return
	}
	lock := s.runtime.taskLock(taskID)
	lock.Lock()
	defer lock.Unlock()
	if notification.Method == museapp.NotifyApprovalRequest || notification.Method == museapp.NotifyApprovalRequested {
		s.rememberMuseApproval(taskID, notification)
	}
	if notification.Method == museapp.NotifyUserInputRequest || notification.Method == museapp.NotifyUserInputRequested {
		s.rememberMuseQuestion(taskID, notification)
	}
	s.rememberMuseItemTurn(taskID, notification)
	events, err := museapp.MapNotification(agentstream.TaskSummary{ID: task.ID, Agent: task.Agent, AgentThreadID: task.AgentThreadID}, notification)
	if err != nil {
		return
	}
	for _, event := range events {
		if event.TurnID == "" && event.ItemID != "" {
			s.mu.Lock()
			event.TurnID = s.museItemTurns[taskID+":"+event.ItemID]
			s.mu.Unlock()
		}
		if event.Kind == agentstream.EventTurnStarted && event.TurnID != "" {
			s.mu.Lock()
			if current := s.activeTurns[taskID]; current == "" || current == event.TurnID {
				s.activeTurns[taskID] = event.TurnID
			}
			s.mu.Unlock()
		}
		if event.Kind == agentstream.EventTurnCompleted || event.Kind == agentstream.EventInterrupted || event.Kind == agentstream.EventError {
			s.mu.Lock()
			current := s.activeTurns[taskID]
			isCurrent := event.TurnID == "" || current == "" || current == event.TurnID
			if isCurrent {
				delete(s.activeTurns, taskID)
				delete(s.musePrompts, taskID)
			}
			s.mu.Unlock()
			if isCurrent {
				_ = s.runtime.store.UpdateTaskStatus(taskID, db.StatusWaiting)
				s.runtime.emitMetadataEvent(task.ProjectID)
				s.runtime.syncDiscordAsync()
			}
		}
		s.publish(taskID, event)
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
	activeTaskTurns := map[string]string{}
	allMightyHost := false
	s.mu.Lock()
	if s.muse == client || s.museAllMighty == client {
		if s.muse == client {
			s.muse = nil
		} else {
			s.museAllMighty = nil
			allMightyHost = true
		}
		for taskID, turnID := range s.activeTurns {
			if s.museTaskHosts[taskID] == client {
				activeTaskTurns[taskID] = turnID
			}
		}
	}
	s.mu.Unlock()
	if s.ctx.Err() != nil {
		return
	}
	for taskID, failedTurnID := range activeTaskTurns {
		task, err := s.runtime.store.GetTask(taskID)
		if err != nil || !isMuseTask(task.Agent) || task.AllMighty != allMightyHost {
			continue
		}
		taskLock := s.runtime.taskLock(taskID)
		taskLock.Lock()
		s.mu.Lock()
		turnID := s.activeTurns[taskID]
		if turnID != failedTurnID || s.museTaskHosts[taskID] != client {
			s.mu.Unlock()
			taskLock.Unlock()
			continue
		}
		delete(s.museTaskHosts, taskID)
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
		taskLock.Unlock()
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
	prompt := musePendingPrompt{kind: "approval", sessionID: params.SessionID, id: params.ApprovalID, revision: string(notification.Params), requirement: requirement, choicesByLabel: map[string]string{}}
	for _, choice := range params.Choices {
		prompt.choicesByLabel[strings.ToLower(strings.TrimSpace(choice.Label))] = choice.ID
	}
	s.enqueueMusePrompt(taskID, prompt)
}

func (s *agentEventService) rememberMuseQuestion(taskID string, notification museapp.Notification) {
	var params struct {
		SessionID   string `json:"sessionId"`
		UserInputID string `json:"userInputId"`
		Questions   []struct {
			ID       string `json:"id"`
			Header   string `json:"header"`
			Question string `json:"question"`
			Options  []struct {
				Label string `json:"label"`
			} `json:"options"`
			Selection struct {
				Mode          string `json:"mode"`
				MinSelections int    `json:"minSelections"`
				MaxSelections int    `json:"maxSelections"`
			} `json:"selection"`
		} `json:"questions"`
	}
	if json.Unmarshal(notification.Params, &params) != nil || len(params.Questions) == 0 {
		return
	}
	prompt := musePendingPrompt{kind: "question", sessionID: params.SessionID, id: params.UserInputID, revision: string(notification.Params)}
	for _, question := range params.Questions {
		pending := musePendingQuestion{id: question.ID, prompt: strings.TrimSpace(strings.Join([]string{question.Header, question.Question}, "\n")), choicesByLabel: map[string]string{}, allowFreeText: len(question.Options) == 0, multiple: strings.EqualFold(question.Selection.Mode, "multiple"), minSelections: question.Selection.MinSelections, maxSelections: question.Selection.MaxSelections}
		for _, option := range question.Options {
			pending.choicesByLabel[strings.ToLower(strings.TrimSpace(option.Label))] = option.Label
			pending.options = append(pending.options, agentstream.QuestionOption{ID: option.Label, Label: option.Label})
		}
		prompt.questions = append(prompt.questions, pending)
	}
	prompt.questionID = prompt.questions[0].id
	prompt.choicesByLabel = prompt.questions[0].choicesByLabel
	prompt.allowFreeText = prompt.questions[0].allowFreeText
	s.enqueueMusePrompt(taskID, prompt)
}

func (s *agentEventService) enqueueMusePrompt(taskID string, prompt musePendingPrompt) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for index, existing := range s.musePrompts[taskID] {
		if existing.kind == prompt.kind && existing.id == prompt.id {
			if prompt.kind == "approval" {
				s.musePrompts[taskID][index] = prompt
			}
			return
		}
	}
	s.musePrompts[taskID] = append(s.musePrompts[taskID], prompt)
}

func (s *agentEventService) resolveMusePrompt(ctx context.Context, client museRuntime, taskID, message string, promptTokens ...string) (bool, error) {
	label := strings.ToLower(strings.TrimSpace(message))
	s.mu.Lock()
	prompts := s.musePrompts[taskID]
	ok := len(prompts) != 0
	var prompt musePendingPrompt
	if ok {
		prompt = prompts[0]
	}
	providedToken := ""
	if len(promptTokens) != 0 {
		providedToken = strings.TrimSpace(promptTokens[0])
	}
	identity := prompt.id
	if prompt.kind == "question" {
		identity += ":" + prompt.questionID
	}
	if ok && providedToken != "" && providedToken != agxdiscord.PromptToken(identity) {
		s.mu.Unlock()
		return true, fmt.Errorf("this Discord choice belongs to an expired Muse prompt")
	}
	choice, matches := prompt.choicesByLabel[label]
	if ok && prompt.allowFreeText {
		choice, matches = strings.TrimSpace(message), strings.TrimSpace(message) != ""
	}
	var selected []string
	invalidSelection := false
	if ok && prompt.kind == "question" && prompt.questionIndex < len(prompt.questions) && prompt.questions[prompt.questionIndex].multiple {
		seen := map[string]bool{}
		for _, part := range strings.Split(message, ",") {
			key := strings.ToLower(strings.TrimSpace(part))
			value, found := prompt.choicesByLabel[key]
			if !found {
				invalidSelection = true
				continue
			}
			if !seen[key] {
				seen[key] = true
				selected = append(selected, value)
			}
		}
		matches = len(selected) != 0 && !invalidSelection
		current := prompt.questions[prompt.questionIndex]
		if current.minSelections > 0 && len(selected) < current.minSelections {
			matches = false
		}
		if current.maxSelections > 0 && len(selected) > current.maxSelections {
			matches = false
		}
	}
	s.mu.Unlock()
	if !ok {
		return false, nil
	}
	if !matches {
		return true, fmt.Errorf("answer does not match the pending Muse prompt; for multiple choice, separate labels with commas")
	}
	var err error
	if prompt.kind == "approval" {
		err = client.ApprovalDecide(ctx, prompt.sessionID, prompt.id, prompt.requirement, choice)
	} else {
		prompt.answers = append(prompt.answers, museapp.UserInputAnswer{QuestionID: prompt.questionID, Value: choice, Values: selected, FreeText: prompt.allowFreeText})
		prompt.questionIndex++
		if prompt.questionIndex < len(prompt.questions) {
			next := prompt.questions[prompt.questionIndex]
			prompt.questionID, prompt.choicesByLabel, prompt.allowFreeText = next.id, next.choicesByLabel, next.allowFreeText
			s.mu.Lock()
			current := s.musePrompts[taskID]
			if len(current) == 0 || current[0].kind != prompt.kind || current[0].id != prompt.id || current[0].revision != prompt.revision {
				s.mu.Unlock()
				return true, fmt.Errorf("Muse prompt changed while applying the answer")
			}
			s.musePrompts[taskID][0] = prompt
			s.mu.Unlock()
			s.publish(taskID, agentstream.Event{ID: agentstream.StableEventID(taskID, agentstream.EventQuestionRequested, prompt.id, next.id), TaskID: taskID, Kind: agentstream.EventQuestionRequested, Agent: "muse", CreatedAt: time.Now(), Question: &agentstream.QuestionEvent{ID: prompt.id + ":" + next.id, Prompt: next.prompt, Options: next.options, Multiple: next.multiple}})
			return true, nil
		}
		err = client.UserInputAnswer(ctx, prompt.sessionID, prompt.id, prompt.answers)
	}
	if err == nil {
		s.mu.Lock()
		prompts := s.musePrompts[taskID]
		if len(prompts) == 0 || prompts[0].kind != prompt.kind || prompts[0].id != prompt.id || prompts[0].revision != prompt.revision {
			s.mu.Unlock()
			return true, fmt.Errorf("Muse prompt changed while applying the answer")
		}
		if len(prompts) == 1 {
			delete(s.musePrompts, taskID)
		} else {
			s.musePrompts[taskID] = prompts[1:]
		}
		s.mu.Unlock()
	}
	return true, err
}
