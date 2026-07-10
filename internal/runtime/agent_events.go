package runtime

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/nashory/agx/internal/agent"
	"github.com/nashory/agx/internal/agentstream"
	"github.com/nashory/agx/internal/codexapp"
	"github.com/nashory/agx/internal/db"
	agxdiscord "github.com/nashory/agx/internal/discord"
)

type codexRuntime interface {
	Initialize(context.Context) (codexapp.InitializeResponse, error)
	ThreadStart(context.Context, string, bool) (codexapp.ThreadStartResponse, error)
	ThreadResume(context.Context, string) (codexapp.ThreadStartResponse, error)
	TurnStart(context.Context, string, string, string, bool) (codexapp.TurnStartResponse, error)
	TurnSteer(context.Context, string, string, string) (codexapp.TurnSteerResponse, error)
	TurnInterrupt(context.Context, string, string) error
	Events() <-chan codexapp.Notification
	ApproveRequest(codexapp.Notification, codexapp.ReviewDecision) error
	RecentStderr() string
	Close() error
}

const claudeStreamKind = "claude-stream-json"
const agentEventSubscriberBuffer = 256

type agentEventService struct {
	runtime *Service
	ctx     context.Context
	cancel  context.CancelFunc

	mu           sync.Mutex
	codex        codexRuntime
	startCodex   func(context.Context) (codexRuntime, error)
	subscribers  map[string]map[chan agentstream.Event]struct{}
	threadToTask map[string]string
	activeTurns  map[string]string
	turnCancels  map[string]context.CancelFunc
	claudeQueues map[string][]string
}

func newAgentEventService(runtime *Service) *agentEventService {
	ctx, cancel := context.WithCancel(context.Background())
	service := &agentEventService{
		runtime:      runtime,
		ctx:          ctx,
		cancel:       cancel,
		subscribers:  map[string]map[chan agentstream.Event]struct{}{},
		threadToTask: map[string]string{},
		activeTurns:  map[string]string{},
		turnCancels:  map[string]context.CancelFunc{},
		claudeQueues: map[string][]string{},
	}
	service.startCodex = func(ctx context.Context) (codexRuntime, error) {
		client, err := codexapp.Start(service.ctx, codexapp.Options{})
		if err != nil {
			return nil, err
		}
		if _, err := client.Initialize(ctx); err != nil {
			_ = client.Close()
			return nil, err
		}
		return client, nil
	}
	return service
}

func (s *agentEventService) Close() error {
	s.cancel()
	s.mu.Lock()
	codex := s.codex
	s.codex = nil
	for taskID, subscribers := range s.subscribers {
		for ch := range subscribers {
			close(ch)
		}
		delete(s.subscribers, taskID)
	}
	s.threadToTask = map[string]string{}
	s.activeTurns = map[string]string{}
	s.claudeQueues = map[string][]string{}
	for _, cancel := range s.turnCancels {
		cancel()
	}
	s.turnCancels = map[string]context.CancelFunc{}
	s.mu.Unlock()
	if codex != nil {
		return codex.Close()
	}
	return nil
}

func (s *agentEventService) SubscribeAgentEvents(ctx context.Context, task agxdiscord.TaskSummary) (<-chan agentstream.Event, error) {
	if !isCodexTask(task.Agent) && !isClaudeTask(task.Agent) {
		return nil, agentstream.UnsupportedError{TaskID: task.ID, Agent: task.Agent}
	}
	if !isStructuredTask(task) {
		return nil, agentstream.UnsupportedError{TaskID: task.ID, Agent: task.Agent}
	}
	if task.AgentThreadID == nil || strings.TrimSpace(*task.AgentThreadID) == "" || task.AgentStreamKind == nil || strings.TrimSpace(*task.AgentStreamKind) == "" {
		return nil, agentstream.UnsupportedError{TaskID: task.ID, Agent: task.Agent}
	}
	if isCodexTask(task.Agent) {
		if _, err := s.ensureCodex(ctx); err != nil {
			return nil, err
		}
	}
	if isClaudeTask(task.Agent) && strings.TrimSpace(*task.AgentStreamKind) != claudeStreamKind {
		return nil, agentstream.UnsupportedError{TaskID: task.ID, Agent: task.Agent}
	}
	ch := make(chan agentstream.Event, agentEventSubscriberBuffer)
	s.mu.Lock()
	if s.subscribers[task.ID] == nil {
		s.subscribers[task.ID] = map[chan agentstream.Event]struct{}{}
	}
	s.subscribers[task.ID][ch] = struct{}{}
	s.threadToTask[*task.AgentThreadID] = task.ID
	s.mu.Unlock()
	go func() {
		<-ctx.Done()
		s.removeSubscriber(task.ID, ch)
	}()
	return ch, nil
}

func (s *agentEventService) SendTaskMessage(ctx context.Context, task db.Task, project db.Project, message string) error {
	lock := s.runtime.taskLock(task.ID)
	lock.Lock()
	defer lock.Unlock()
	if !isCodexTask(task.Agent) && !isClaudeTask(task.Agent) {
		return agentstream.UnsupportedError{TaskID: task.ID, Agent: task.Agent}
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return nil
	}
	if isAgentContextClearCommand(message) {
		return s.clearTaskContext(ctx, task, project)
	}
	if isClaudeTask(task.Agent) {
		return s.startClaudeTurn(ctx, task, project, message)
	}
	client, err := s.ensureCodex(ctx)
	if err != nil {
		return err
	}
	threadID, err := s.ensureCodexThread(ctx, client, task, project)
	if err != nil {
		return err
	}
	s.mu.Lock()
	activeTurn := s.activeTurns[task.ID]
	s.mu.Unlock()
	if activeTurn != "" {
		_, err := client.TurnSteer(ctx, threadID, activeTurn, message)
		return err
	}
	turn, err := client.TurnStart(ctx, threadID, message, taskWorkingDir(task, project), task.AllMighty)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.activeTurns[task.ID] = turn.Turn.ID
	s.mu.Unlock()
	if err := s.runtime.store.UpdateTaskStatus(task.ID, db.StatusActive); err == nil {
		s.runtime.emitMetadataEvent(task.ProjectID)
		s.runtime.syncDiscordAsync()
	}
	return nil
}

func (s *agentEventService) clearTaskContext(ctx context.Context, task db.Task, project db.Project) error {
	if isClaudeTask(task.Agent) {
		return s.clearClaudeTaskContext(task)
	}
	if !isCodexTask(task.Agent) {
		return agentstream.UnsupportedError{TaskID: task.ID, Agent: task.Agent}
	}
	client, err := s.ensureCodex(ctx)
	if err != nil {
		return err
	}
	var activeTurn string
	var activeThread string
	if task.AgentThreadID != nil {
		activeThread = strings.TrimSpace(*task.AgentThreadID)
	}
	s.mu.Lock()
	activeTurn = s.activeTurns[task.ID]
	delete(s.activeTurns, task.ID)
	for threadID, taskID := range s.threadToTask {
		if taskID == task.ID {
			delete(s.threadToTask, threadID)
		}
	}
	s.mu.Unlock()
	if activeTurn != "" && activeThread != "" {
		_ = client.TurnInterrupt(ctx, activeThread, activeTurn)
	}
	thread, err := client.ThreadStart(ctx, taskWorkingDir(task, project), task.AllMighty)
	if err != nil {
		return err
	}
	threadID := strings.TrimSpace(thread.Thread.ID)
	if threadID == "" {
		return fmt.Errorf("Codex returned an empty thread id")
	}
	streamKind := codexapp.StreamKind
	if err := s.runtime.store.UpdateTaskAgentStream(task.ID, &threadID, nil, &streamKind); err != nil {
		return err
	}
	_ = s.runtime.store.UpdateTaskStatus(task.ID, db.StatusWaiting)
	s.rememberThread(task.ID, threadID)
	s.recordContextCleared(task)
	s.runtime.emitMetadataEvent(task.ProjectID)
	s.runtime.syncDiscordAsync()
	return nil
}

func (s *agentEventService) clearClaudeTaskContext(task db.Task) error {
	s.mu.Lock()
	cancel := s.turnCancels[task.ID]
	delete(s.activeTurns, task.ID)
	delete(s.turnCancels, task.ID)
	delete(s.claudeQueues, task.ID)
	for threadID, taskID := range s.threadToTask {
		if taskID == task.ID {
			delete(s.threadToTask, threadID)
		}
	}
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	threadID := db.NewTaskID()
	streamKind := claudeStreamKind
	if err := s.runtime.store.UpdateTaskAgentStream(task.ID, &threadID, nil, &streamKind); err != nil {
		return err
	}
	_ = s.runtime.store.UpdateTaskStatus(task.ID, db.StatusWaiting)
	s.recordContextCleared(task)
	s.runtime.emitMetadataEvent(task.ProjectID)
	s.runtime.syncDiscordAsync()
	return nil
}

func (s *agentEventService) recordContextCleared(task db.Task) {
	_ = s.runtime.store.AppendTaskTranscriptMessage(task.ID, "status", "Context cleared.", nil, nil)
}

func (s *agentEventService) PrepareTask(ctx context.Context, task db.Task, project db.Project) error {
	if isClaudeTask(task.Agent) {
		return s.ensureClaudeStreamTask(task)
	}
	if !isCodexTask(task.Agent) {
		return agentstream.UnsupportedError{TaskID: task.ID, Agent: task.Agent}
	}
	client, err := s.ensureCodex(ctx)
	if err != nil {
		return err
	}
	_, err = s.ensureCodexThread(ctx, client, task, project)
	return err
}

func (s *agentEventService) InterruptTask(ctx context.Context, task db.Task) error {
	if isClaudeTask(task.Agent) {
		s.mu.Lock()
		cancel := s.turnCancels[task.ID]
		delete(s.claudeQueues, task.ID)
		s.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		return nil
	}
	if !isCodexTask(task.Agent) {
		return agentstream.UnsupportedError{TaskID: task.ID, Agent: task.Agent}
	}
	s.mu.Lock()
	activeTurn := s.activeTurns[task.ID]
	s.mu.Unlock()
	if activeTurn == "" {
		return nil
	}
	if task.AgentThreadID == nil || strings.TrimSpace(*task.AgentThreadID) == "" {
		return fmt.Errorf("task %s has no Codex thread", task.ID)
	}
	client, err := s.ensureCodex(ctx)
	if err != nil {
		return err
	}
	return client.TurnInterrupt(ctx, *task.AgentThreadID, activeTurn)
}

func (s *agentEventService) ensureCodex(ctx context.Context) (codexRuntime, error) {
	s.mu.Lock()
	if s.codex != nil {
		client := s.codex
		s.mu.Unlock()
		return client, nil
	}
	start := s.startCodex
	s.mu.Unlock()

	client, err := start(ctx)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	if s.codex != nil {
		existing := s.codex
		s.mu.Unlock()
		_ = client.Close()
		return existing, nil
	}
	s.codex = client
	s.mu.Unlock()
	go s.forwardCodexEvents(client)
	return client, nil
}

func (s *agentEventService) ensureCodexThread(ctx context.Context, client codexRuntime, task db.Task, project db.Project) (string, error) {
	if task.AgentThreadID != nil && strings.TrimSpace(*task.AgentThreadID) != "" {
		threadID := strings.TrimSpace(*task.AgentThreadID)
		if _, err := client.ThreadResume(ctx, threadID); err == nil {
			streamKind := codexapp.StreamKind
			if task.AgentStreamKind == nil || strings.TrimSpace(*task.AgentStreamKind) == "" {
				if err := s.runtime.store.UpdateTaskAgentStream(task.ID, &threadID, task.AgentEventCursor, &streamKind); err != nil {
					return "", err
				}
			}
			s.rememberThread(task.ID, threadID)
			return threadID, nil
		}
	}
	thread, err := client.ThreadStart(ctx, taskWorkingDir(task, project), task.AllMighty)
	if err != nil {
		return "", err
	}
	threadID := thread.Thread.ID
	streamKind := codexapp.StreamKind
	if err := s.runtime.store.UpdateTaskAgentStream(task.ID, &threadID, task.AgentEventCursor, &streamKind); err != nil {
		return "", err
	}
	s.rememberThread(task.ID, threadID)
	return threadID, nil
}

func (s *agentEventService) StopTask(ctx context.Context, task db.Task) error {
	if task.AgentStreamKind == nil || *task.AgentStreamKind == "" {
		return agentstream.UnsupportedError{TaskID: task.ID, Agent: task.Agent}
	}
	if isClaudeTask(task.Agent) {
		_ = s.InterruptTask(ctx, task)
		return s.runtime.store.UpdateTaskAgentStream(task.ID, nil, task.AgentEventCursor, nil)
	}
	if err := s.InterruptTask(ctx, task); err != nil {
		return err
	}
	return s.runtime.store.UpdateTaskAgentStream(task.ID, nil, task.AgentEventCursor, nil)
}

func (s *agentEventService) ensureClaudeStreamTask(task db.Task) error {
	threadID := claudeThreadID(task)
	if threadID == task.ID && task.AgentEventCursor != nil && strings.TrimSpace(*task.AgentEventCursor) != "" {
		threadID = strings.TrimSpace(*task.AgentEventCursor)
	}
	streamKind := claudeStreamKind
	if err := s.runtime.store.UpdateTaskAgentStream(task.ID, &threadID, task.AgentEventCursor, &streamKind); err != nil {
		return err
	}
	return nil
}

func (s *agentEventService) startClaudeTurn(ctx context.Context, task db.Task, project db.Project, message string) error {
	if err := s.ensureClaudeStreamTask(task); err != nil {
		return err
	}
	s.mu.Lock()
	if s.activeTurns[task.ID] != "" {
		s.claudeQueues[task.ID] = append(s.claudeQueues[task.ID], message)
		s.mu.Unlock()
		return nil
	}
	turnID, turnCtx, cancel := s.reserveClaudeTurnLocked(task.ID)
	s.mu.Unlock()

	s.launchClaudeTurn(task, project, turnID, turnCtx, cancel, message)
	return nil
}

func (s *agentEventService) reserveClaudeTurnLocked(taskID string) (string, context.Context, context.CancelFunc) {
	turnID := fmt.Sprintf("%s:%d", taskID, time.Now().UnixNano())
	turnCtx, cancel := context.WithCancel(s.ctx)
	s.activeTurns[taskID] = turnID
	s.turnCancels[taskID] = cancel
	return turnID, turnCtx, cancel
}

func (s *agentEventService) launchClaudeTurn(task db.Task, project db.Project, turnID string, turnCtx context.Context, cancel context.CancelFunc, message string) {
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

	go s.runClaudeTurn(turnCtx, cancel, task, project, turnID, message)
}

func (s *agentEventService) runClaudeTurn(ctx context.Context, cancel context.CancelFunc, task db.Task, project db.Project, turnID, message string) {
	defer cancel()
	err := s.execClaudeStream(ctx, task, project, turnID, message)
	if err != nil {
		kind := agentstream.EventError
		text := strings.TrimSpace(err.Error())
		if text == "" {
			text = "The agent process failed without an error message."
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
		s.finishClaudeTurn(task, project, false)
		return
	}
	_ = s.runtime.store.UpdateTaskStatus(task.ID, db.StatusWaiting)
	s.runtime.emitMetadataEvent(task.ProjectID)
	s.runtime.syncDiscordAsync()
	s.finishClaudeTurn(task, project, true)
}

func (s *agentEventService) finishClaudeTurn(task db.Task, project db.Project, startQueued bool) {
	var nextMessage string
	var turnID string
	var turnCtx context.Context
	var cancel context.CancelFunc
	s.mu.Lock()
	delete(s.activeTurns, task.ID)
	delete(s.turnCancels, task.ID)
	if !startQueued {
		delete(s.claudeQueues, task.ID)
	}
	if startQueued && len(s.claudeQueues[task.ID]) > 0 {
		nextMessage = mergeQueuedClaudeMessages(s.claudeQueues[task.ID])
		delete(s.claudeQueues, task.ID)
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
	s.launchClaudeTurn(refreshed, project, turnID, turnCtx, cancel, nextMessage)
}

func mergeQueuedClaudeMessages(messages []string) string {
	cleaned := make([]string, 0, len(messages))
	for _, message := range messages {
		if trimmed := strings.TrimSpace(message); trimmed != "" {
			cleaned = append(cleaned, trimmed)
		}
	}
	return strings.Join(cleaned, "\n\n")
}

func (s *agentEventService) execClaudeStream(ctx context.Context, task db.Task, project db.Project, turnID, message string) error {
	if err := s.execClaudeStreamOnce(ctx, task, project, turnID, message); err != nil {
		if ctx.Err() == nil && claudeSessionAlreadyInUse(err) {
			threadID := claudeThreadID(task)
			cursor := threadID
			streamKind := claudeStreamKind
			_ = s.runtime.store.UpdateTaskAgentStream(task.ID, &threadID, &cursor, &streamKind)
			task.AgentThreadID = &threadID
			task.AgentEventCursor = &cursor
			task.AgentStreamKind = &streamKind
			return s.execClaudeStreamOnce(ctx, task, project, turnID, message)
		}
		return err
	}
	return nil
}

func (s *agentEventService) execClaudeStreamOnce(ctx context.Context, task db.Task, project db.Project, turnID, message string) error {
	registry := agent.RegistryForProject(project.Path)
	ag, err := registry.Get(task.Agent)
	if err != nil {
		return err
	}
	args := claudeStreamArgs(task, message)
	cmd := exec.CommandContext(ctx, ag.Command, args...)
	cmd.Dir = taskWorkingDir(task, project)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		event, ok := mapClaudeStreamLine(task, turnID, scanner.Bytes())
		if !ok {
			continue
		}
		s.publish(task.ID, event)
		if event.Cursor != "" {
			cursor := event.Cursor
			threadID := cursor
			streamKind := claudeStreamKind
			_ = s.runtime.store.UpdateTaskAgentStream(task.ID, &threadID, &cursor, &streamKind)
		}
	}
	scanErr := scanner.Err()
	waitErr := cmd.Wait()
	if scanErr != nil {
		return scanErr
	}
	if waitErr != nil {
		errText := strings.TrimSpace(stderr.String())
		if errText == "" {
			errText = waitErr.Error()
		}
		return fmt.Errorf("Claude stream failed: %s", errText)
	}
	return nil
}

func claudeStreamArgs(task db.Task, message string) []string {
	args := []string{"--print", "--verbose", "--output-format", "stream-json"}
	threadID := claudeThreadID(task)
	if task.AgentEventCursor != nil && strings.TrimSpace(*task.AgentEventCursor) != "" {
		args = append(args, "--resume", threadID)
	} else {
		args = append(args, "--session-id", threadID)
	}
	if task.AllMighty {
		args = append(args, "--permission-mode", "bypassPermissions")
		args = append(args, agent.SandboxDisableArgs()...)
		args = append(args, "--dangerously-skip-permissions")
	}
	return append(args, message)
}

func claudeThreadID(task db.Task) string {
	if task.AgentThreadID != nil && strings.TrimSpace(*task.AgentThreadID) != "" {
		return strings.TrimSpace(*task.AgentThreadID)
	}
	return task.ID
}

func claudeSessionAlreadyInUse(err error) bool {
	return strings.Contains(strings.ToLower(err.Error()), "session id") &&
		strings.Contains(strings.ToLower(err.Error()), "already in use")
}

func (s *agentEventService) rememberThread(taskID, threadID string) {
	if threadID == "" {
		return
	}
	s.mu.Lock()
	s.threadToTask[threadID] = taskID
	s.mu.Unlock()
}

func taskWorkingDir(task db.Task, project db.Project) string {
	if task.WorktreePath != nil && strings.TrimSpace(*task.WorktreePath) != "" {
		return strings.TrimSpace(*task.WorktreePath)
	}
	return project.Path
}

func (s *agentEventService) forgetRuntime(client codexRuntime) {
	s.mu.Lock()
	if s.codex == client {
		s.codex = nil
		s.activeTurns = map[string]string{}
	}
	s.mu.Unlock()
}

func (s *agentEventService) forwardCodexEvents(client codexRuntime) {
	defer s.forgetRuntime(client)
	for notification := range client.Events() {
		if codexapp.IsApprovalRequest(notification) {
			s.answerCodexApproval(client, notification)
			continue
		}
		s.mu.Lock()
		taskID := s.taskIDForNotificationLocked(notification)
		s.mu.Unlock()
		if taskID == "" {
			continue
		}
		task, err := s.runtime.store.GetTask(taskID)
		if err != nil {
			continue
		}
		summary := agentstream.TaskSummary{ID: task.ID, Agent: task.Agent, AgentThreadID: task.AgentThreadID}
		event, ok, err := codexapp.MapNotification(summary, notification)
		if err != nil || !ok {
			continue
		}
		if event.Kind == agentstream.EventError {
			event.Error = enrichCodexError(event.Error, client.RecentStderr())
		}
		if event.Kind == agentstream.EventTurnStarted && event.TurnID != "" {
			s.mu.Lock()
			s.activeTurns[taskID] = event.TurnID
			s.mu.Unlock()
		}
		turnCompleted := event.Kind == agentstream.EventTurnCompleted
		if turnCompleted {
			s.mu.Lock()
			if event.TurnID == "" {
				event.TurnID = s.activeTurns[taskID]
			}
			delete(s.activeTurns, taskID)
			s.mu.Unlock()
		}
		if event.Cursor != "" {
			cursor := event.Cursor
			_ = s.runtime.store.UpdateTaskAgentEventCursor(taskID, &cursor)
		}
		s.publish(taskID, event)
		if turnCompleted {
			_ = s.runtime.store.UpdateTaskStatus(taskID, db.StatusWaiting)
			s.runtime.emitMetadataEvent(task.ProjectID)
			s.runtime.syncDiscordAsync()
		}
	}
}

// enrichCodexError appends recent app-server stderr to a codex error so the
// operator sees the actual failure. Codex frequently emits an "error"
// notification with no usable message while the real cause (a crash, auth
// failure, or config error) is only on stderr, which is otherwise discarded.
func enrichCodexError(message, stderr string) string {
	message = strings.TrimSpace(message)
	stderr = strings.TrimSpace(stderr)
	if stderr == "" {
		return message
	}
	if message == "" || message == codexapp.ErrorNoDetail {
		return "codex error. Recent codex output:\n" + stderr
	}
	if strings.Contains(message, stderr) {
		return message
	}
	return message + "\n\nRecent codex output:\n" + stderr
}

// answerCodexApproval responds to a Codex approval request so the blocked turn
// can proceed instead of hanging on "Thinking". All-mighty tasks (the only kind
// AGX starts non-interactively over Discord) are auto-approved; anything else is
// denied, since there is no interactive approver and silently approving would
// defeat a non-all-mighty task's intent. Either decision unblocks the turn.
func (s *agentEventService) answerCodexApproval(client codexRuntime, notification codexapp.Notification) {
	decision := codexapp.DecisionApproved
	s.mu.Lock()
	taskID := s.taskIDForNotificationLocked(notification)
	s.mu.Unlock()
	if taskID != "" {
		if task, err := s.runtime.store.GetTask(taskID); err == nil && !task.AllMighty {
			decision = codexapp.DecisionDenied
		}
	}
	if err := client.ApproveRequest(notification, decision); err != nil {
		logRuntimeOperation("codex_approval",
			"status", "failed",
			"method", notification.Method,
			"decision", string(decision),
			"error", err,
		)
	}
}

func (s *agentEventService) taskIDForNotificationLocked(notification codexapp.Notification) string {
	threadID := notificationThreadID(notification)
	if threadID == "" {
		return ""
	}
	return s.threadToTask[threadID]
}

func (s *agentEventService) publish(taskID string, event agentstream.Event) {
	s.persistTranscriptEvent(taskID, event)
	s.mu.Lock()
	subscribers := s.subscribers[taskID]
	for ch := range subscribers {
		publishAgentEventToSubscriber(ch, event)
	}
	s.mu.Unlock()
}

func publishAgentEventToSubscriber(ch chan agentstream.Event, event agentstream.Event) {
	select {
	case ch <- event:
		return
	default:
	}
	if !isCriticalAgentEvent(event.Kind) {
		return
	}
	select {
	case <-ch:
	default:
	}
	select {
	case ch <- event:
	default:
	}
}

func isCriticalAgentEvent(kind agentstream.EventKind) bool {
	switch kind {
	case agentstream.EventAssistantMessage,
		agentstream.EventApprovalRequested,
		agentstream.EventQuestionRequested,
		agentstream.EventTurnCompleted,
		agentstream.EventInterrupted,
		agentstream.EventError:
		return true
	default:
		return false
	}
}

func (s *agentEventService) persistTranscriptEvent(taskID string, event agentstream.Event) {
	role := ""
	body := ""
	switch event.Kind {
	case agentstream.EventAssistantMessage:
		role = "assistant"
		body = event.Text
	case agentstream.EventError:
		role = "status"
		if strings.TrimSpace(event.Error) != "" {
			body = "Error: " + event.Error
		} else {
			body = event.Text
		}
	case agentstream.EventInterrupted:
		role = "status"
		if strings.TrimSpace(event.Error) != "" {
			body = event.Error
		} else {
			body = "Interrupted."
		}
	}
	if role == "" || strings.TrimSpace(body) == "" {
		return
	}
	var turnID *string
	if strings.TrimSpace(event.TurnID) != "" {
		value := event.TurnID
		turnID = &value
	}
	_ = s.runtime.store.AppendTaskTranscriptMessage(taskID, role, body, turnID, nil)
}

func (s *agentEventService) removeSubscriber(taskID string, ch chan agentstream.Event) {
	removed := false
	s.mu.Lock()
	if subscribers := s.subscribers[taskID]; subscribers != nil {
		if _, ok := subscribers[ch]; ok {
			delete(subscribers, ch)
			removed = true
		}
		if len(subscribers) == 0 {
			delete(s.subscribers, taskID)
		}
	}
	s.mu.Unlock()
	if removed {
		close(ch)
	}
}

func (s *agentEventService) forgetTask(taskID string) {
	var cancels []context.CancelFunc
	var subscribers []chan agentstream.Event
	s.mu.Lock()
	for ch := range s.subscribers[taskID] {
		subscribers = append(subscribers, ch)
	}
	delete(s.subscribers, taskID)
	for threadID, mappedTaskID := range s.threadToTask {
		if mappedTaskID == taskID {
			delete(s.threadToTask, threadID)
		}
	}
	delete(s.activeTurns, taskID)
	delete(s.claudeQueues, taskID)
	if cancel := s.turnCancels[taskID]; cancel != nil {
		cancels = append(cancels, cancel)
	}
	delete(s.turnCancels, taskID)
	s.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	for _, ch := range subscribers {
		close(ch)
	}
}

func notificationThreadID(notification codexapp.Notification) string {
	var params struct {
		ThreadID string `json:"threadId"`
	}
	_ = json.Unmarshal(notification.Params, &params)
	return params.ThreadID
}

func isCodexTask(agent string) bool {
	return strings.EqualFold(strings.TrimSpace(agent), "codex")
}

func isAgentContextClearCommand(message string) bool {
	return strings.EqualFold(strings.TrimSpace(message), "/clear")
}

func isClaudeTask(agent string) bool {
	return strings.EqualFold(strings.TrimSpace(agent), "claude")
}

func isStructuredTask(task agxdiscord.TaskSummary) bool {
	return task.AgentThreadID != nil &&
		strings.TrimSpace(*task.AgentThreadID) != "" &&
		task.AgentStreamKind != nil &&
		strings.TrimSpace(*task.AgentStreamKind) != ""
}

func mapClaudeStreamLine(task db.Task, turnID string, line []byte) (agentstream.Event, bool) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 || line[0] != '{' {
		return agentstream.Event{}, false
	}
	var envelope struct {
		Type      string          `json:"type"`
		Subtype   string          `json:"subtype"`
		Message   json.RawMessage `json:"message"`
		IsError   bool            `json:"is_error"`
		Result    string          `json:"result"`
		SessionID string          `json:"session_id"`
		Duration  int64           `json:"duration_ms"`
		Usage     struct {
			InputTokens              int `json:"input_tokens"`
			OutputTokens             int `json:"output_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(line, &envelope); err != nil {
		return agentstream.Event{}, false
	}
	now := time.Now()
	switch envelope.Type {
	case "assistant":
		return mapClaudeAssistantMessage(task, turnID, envelope.Message, now)
	case "result":
		tokens := envelope.Usage.InputTokens + envelope.Usage.OutputTokens + envelope.Usage.CacheCreationInputTokens + envelope.Usage.CacheReadInputTokens
		event := agentstream.Event{
			ID:        agentstream.StableEventID(task.ID, agentstream.EventTurnCompleted, turnID, envelope.SessionID),
			TaskID:    task.ID,
			TurnID:    turnID,
			Kind:      agentstream.EventTurnCompleted,
			Agent:     task.Agent,
			CreatedAt: now,
			Cursor:    strings.TrimSpace(envelope.SessionID),
			Result: &agentstream.ResultEvent{
				Duration: time.Duration(envelope.Duration) * time.Millisecond,
				Tokens:   tokens,
			},
		}
		if envelope.IsError {
			event.Kind = agentstream.EventError
			event.ID = agentstream.StableEventID(task.ID, agentstream.EventError, turnID, envelope.Result)
			event.Error = strings.TrimSpace(envelope.Result)
			if event.Error == "" {
				event.Error = "Claude returned an error."
			}
		}
		return event, true
	default:
		return agentstream.Event{}, false
	}
}

func mapClaudeAssistantMessage(task db.Task, turnID string, raw json.RawMessage, createdAt time.Time) (agentstream.Event, bool) {
	var message struct {
		ID      string `json:"id"`
		Content []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
	}
	if len(raw) == 0 || string(raw) == "null" {
		return agentstream.Event{}, false
	}
	if err := json.Unmarshal(raw, &message); err != nil {
		return agentstream.Event{}, false
	}
	var textParts []string
	for _, content := range message.Content {
		if content.Type == "text" && strings.TrimSpace(content.Text) != "" {
			textParts = append(textParts, strings.TrimSpace(content.Text))
		}
	}
	if len(textParts) > 0 {
		text := strings.Join(textParts, "\n\n")
		return agentstream.Event{
			ID:        agentstream.StableEventID(task.ID, agentstream.EventAssistantMessage, turnID, message.ID, text),
			TaskID:    task.ID,
			TurnID:    turnID,
			ItemID:    message.ID,
			Kind:      agentstream.EventAssistantMessage,
			Agent:     task.Agent,
			Text:      text,
			CreatedAt: createdAt,
		}, true
	}
	for _, content := range message.Content {
		if content.Type != "tool_use" || strings.TrimSpace(content.Name) == "" {
			continue
		}
		toolID := strings.TrimSpace(content.ID)
		input := strings.TrimSpace(string(content.Input))
		return agentstream.Event{
			ID:        agentstream.StableEventID(task.ID, agentstream.EventToolStarted, turnID, toolID, content.Name),
			TaskID:    task.ID,
			TurnID:    turnID,
			ItemID:    toolID,
			Kind:      agentstream.EventToolStarted,
			Agent:     task.Agent,
			CreatedAt: createdAt,
			Tool: &agentstream.ToolEvent{
				ID:    toolID,
				Name:  content.Name,
				Input: input,
			},
		}, true
	}
	return agentstream.Event{}, false
}
