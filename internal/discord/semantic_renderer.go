package discord

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/nashory/agx/internal/agentstream"
)

const (
	semanticMessageBudget       = 1800
	progressPreviewMaxLines     = 3
	progressPreviewMaxLineRunes = 240
	assistantProgressMinRunes   = 80
	assistantSentenceMinRunes   = 24
	assistantProgressForceRunes = 220
	assistantProgressInterval   = 2 * time.Second
	errorSummaryMaxRunes        = 180
)

type RenderActionKind string

const (
	RenderSend           RenderActionKind = "send"
	RenderUpdateProgress RenderActionKind = "update_progress"
	RenderClearProgress  RenderActionKind = "clear_progress"
)

type RenderAction struct {
	Kind         RenderActionKind
	Content      string
	HighPriority bool
	Prompt       *InteractivePrompt
}

type AgentEventSubscriber interface {
	SubscribeAgentEvents(context.Context, TaskSummary) (<-chan agentstream.Event, error)
}

type ProgressMessageSender interface {
	UpdateProgressMessage(ctx context.Context, channelID, content string) error
	ClearProgressMessage(ctx context.Context, channelID string) error
}

type SemanticEventForwarder struct {
	sender   MessageSender
	renderer SemanticRenderer
	turns    map[string]*semanticTurnState
}

type semanticTurnState struct {
	assistantParts        []string
	sentAssistant         bool
	lastAssistantProgress string
}

func NewSemanticEventForwarder(sender MessageSender) *SemanticEventForwarder {
	return &SemanticEventForwarder{sender: sender, renderer: NewSemanticRenderer(), turns: map[string]*semanticTurnState{}}
}

func (f *SemanticEventForwarder) Forward(ctx context.Context, channelID string, events <-chan agentstream.Event) error {
	if f.sender == nil {
		return fmt.Errorf("discord semantic sender is not configured")
	}
	if strings.TrimSpace(channelID) == "" {
		return fmt.Errorf("discord channel id is required")
	}
	if f.turns == nil {
		f.turns = map[string]*semanticTurnState{}
	}
	ticker := time.NewTicker(assistantProgressInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := f.forwardActions(ctx, channelID, f.flushAllAssistantProgress()); err != nil {
				return err
			}
		case event, ok := <-events:
			if !ok {
				if err := f.forwardActions(ctx, channelID, f.flushAllAssistantProgress()); err != nil {
					return err
				}
				return nil
			}
			if err := f.forwardActions(ctx, channelID, f.render(event)); err != nil {
				return err
			}
		}
	}
}

func (f *SemanticEventForwarder) forwardActions(ctx context.Context, channelID string, actions []RenderAction) error {
	for _, action := range actions {
		switch action.Kind {
		case RenderSend:
			if strings.TrimSpace(action.Content) == "" {
				continue
			}
			if action.Prompt != nil {
				if sender, ok := f.sender.(InteractivePromptSender); ok {
					if err := sender.SendInteractivePrompt(ctx, channelID, *action.Prompt); err != nil {
						return err
					}
					continue
				}
			}
			if err := f.sender.SendMessage(ctx, channelID, action.Content); err != nil {
				return err
			}
		case RenderUpdateProgress:
			if strings.TrimSpace(action.Content) == "" {
				continue
			}
			if progress, ok := f.sender.(ProgressMessageSender); ok {
				_ = progress.UpdateProgressMessage(ctx, channelID, action.Content)
			}
		case RenderClearProgress:
			if progress, ok := f.sender.(ProgressMessageSender); ok {
				_ = progress.ClearProgressMessage(ctx, channelID)
			}
		}
	}
	return nil
}

func (f *SemanticEventForwarder) render(event agentstream.Event) []RenderAction {
	key := semanticTurnKey(event)
	state := f.turns[key]
	if state == nil && key != "" {
		state = &semanticTurnState{}
		f.turns[key] = state
	}
	switch event.Kind {
	case agentstream.EventTurnStarted:
		if key != "" {
			f.turns[key] = &semanticTurnState{}
		}
		return f.renderer.Render(event)
	case agentstream.EventAssistantDelta:
		if state != nil && event.Text != "" {
			state.assistantParts = append(state.assistantParts, event.Text)
			if text, ok := state.assistantProgressText(event.Text); ok {
				return []RenderAction{{Kind: RenderUpdateProgress, Content: f.renderer.assistantProgress("✍️ Writing...", text)}}
			}
			return nil
		}
		return f.renderer.Render(event)
	case agentstream.EventAssistantMessage:
		if state != nil {
			state.sentAssistant = true
			state.assistantParts = nil
		}
		return f.renderer.Render(event)
	case agentstream.EventTurnCompleted:
		actions := []RenderAction{{Kind: RenderClearProgress}}
		if state != nil && !state.sentAssistant {
			text := strings.TrimSpace(strings.Join(state.assistantParts, ""))
			actions = append(actions, f.renderer.sendChunks(text, false)...)
		}
		delete(f.turns, key)
		return actions
	case agentstream.EventInterrupted, agentstream.EventError:
		delete(f.turns, key)
		return f.renderer.Render(event)
	default:
		actions := f.flushAssistantProgress(state)
		return append(actions, f.renderer.Render(event)...)
	}
}

func (f *SemanticEventForwarder) flushAssistantProgress(state *semanticTurnState) []RenderAction {
	if state == nil {
		return nil
	}
	text := strings.TrimSpace(strings.Join(state.assistantParts, ""))
	if text == "" || text == state.lastAssistantProgress {
		return nil
	}
	state.lastAssistantProgress = text
	return []RenderAction{{Kind: RenderUpdateProgress, Content: f.renderer.assistantProgress("✍️ Writing...", text)}}
}

func (f *SemanticEventForwarder) flushAllAssistantProgress() []RenderAction {
	if len(f.turns) == 0 {
		return nil
	}
	var actions []RenderAction
	for _, state := range f.turns {
		actions = append(actions, f.flushAssistantProgress(state)...)
	}
	return actions
}

func (s *semanticTurnState) assistantProgressText(delta string) (string, bool) {
	text := strings.TrimSpace(strings.Join(s.assistantParts, ""))
	if text == "" || text == s.lastAssistantProgress {
		return "", false
	}
	if shouldFlushAssistantProgress(text, s.lastAssistantProgress, delta) {
		s.lastAssistantProgress = text
		return text, true
	}
	return "", false
}

func shouldFlushAssistantProgress(text, previous, delta string) bool {
	added := runeLen(text) - runeLen(previous)
	if added >= assistantProgressForceRunes {
		return true
	}
	if strings.Contains(delta, "\n") && added >= assistantSentenceMinRunes {
		return true
	}
	if added >= assistantProgressMinRunes {
		return true
	}
	return added >= assistantSentenceMinRunes && endsSentence(text)
}

func endsSentence(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	for _, suffix := range []string{".", "!", "?", "。", "！", "？", "…"} {
		if strings.HasSuffix(text, suffix) {
			return true
		}
	}
	return false
}

func runeLen(text string) int {
	return len([]rune(text))
}

func semanticTurnKey(event agentstream.Event) string {
	taskID := strings.TrimSpace(event.TaskID)
	if taskID == "" {
		return ""
	}
	turnID := strings.TrimSpace(event.TurnID)
	if turnID == "" {
		return taskID
	}
	return taskID + ":" + turnID
}

type SemanticRenderer struct {
	MaxMessageBytes int
}

func NewSemanticRenderer() SemanticRenderer {
	return SemanticRenderer{MaxMessageBytes: semanticMessageBudget}
}

func toAgentStreamTask(task TaskSummary) agentstream.TaskSummary {
	return agentstream.TaskSummary{
		ID:               task.ID,
		Title:            task.Title,
		ProjectName:      task.ProjectName,
		Agent:            task.Agent,
		Status:           task.Status,
		AgentThreadID:    task.AgentThreadID,
		AgentEventCursor: task.AgentEventCursor,
		StreamKind:       valueOrEmpty(task.AgentStreamKind),
	}
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func (r SemanticRenderer) Render(event agentstream.Event) []RenderAction {
	switch event.Kind {
	case agentstream.EventTurnStarted:
		return []RenderAction{{Kind: RenderUpdateProgress, Content: "⏳ Thinking..."}}
	case agentstream.EventThinkingDelta:
		return []RenderAction{{Kind: RenderUpdateProgress, Content: r.progress("💭 Thinking...", event.Text)}}
	case agentstream.EventAssistantDelta:
		return []RenderAction{{Kind: RenderUpdateProgress, Content: r.assistantProgress("✍️ Writing...", event.Text)}}
	case agentstream.EventAssistantMessage:
		return r.sendChunks(event.Text, false)
	case agentstream.EventCommandStarted:
		if event.Command == nil || strings.TrimSpace(event.Command.Command) == "" {
			return nil
		}
		return []RenderAction{{Kind: RenderUpdateProgress, Content: r.progress("⚙️ Running...", inlineCode(event.Command.Command))}}
	case agentstream.EventCommandOutputDelta:
		if event.Command == nil || strings.TrimSpace(event.Command.Stdout+event.Command.Stderr) == "" {
			return nil
		}
		output := strings.TrimSpace(strings.Join([]string{event.Command.Stdout, event.Command.Stderr}, "\n"))
		return []RenderAction{{Kind: RenderUpdateProgress, Content: r.progress("⚙️ Running...", output)}}
	case agentstream.EventCommandCompleted:
		return r.renderCommandCompleted(event)
	case agentstream.EventFileChanged:
		if event.File == nil || strings.TrimSpace(event.File.Path) == "" {
			return nil
		}
		action := strings.TrimSpace(event.File.Action)
		if action == "" {
			action = "changed"
		}
		return []RenderAction{{Kind: RenderSend, Content: fmt.Sprintf("File %s: `%s`", action, event.File.Path)}}
	case agentstream.EventToolStarted:
		if event.Tool == nil || strings.TrimSpace(event.Tool.Name) == "" {
			return nil
		}
		trace := renderToolTrace(event.Tool)
		return []RenderAction{{Kind: RenderUpdateProgress, Content: r.progress("🔧 Working...", trace)}}
	case agentstream.EventApprovalRequested:
		content := renderApproval(event.Approval)
		return []RenderAction{{Kind: RenderSend, Content: content, HighPriority: true, Prompt: approvalPrompt(event.TaskID, content, event.Approval)}}
	case agentstream.EventQuestionRequested:
		content := renderQuestion(event.Question)
		return []RenderAction{{Kind: RenderSend, Content: content, HighPriority: true, Prompt: questionPrompt(event.TaskID, content, event.Question)}}
	case agentstream.EventTurnCompleted:
		return []RenderAction{{Kind: RenderClearProgress}}
	case agentstream.EventInterrupted:
		return []RenderAction{{Kind: RenderUpdateProgress, Content: "⏹️ Interrupted."}}
	case agentstream.EventError:
		actions := []RenderAction{{Kind: RenderClearProgress}}
		return append(actions, r.errorMessages(event.Error)...)
	default:
		return nil
	}
}

func (r SemanticRenderer) Unsupported(task agentstream.TaskSummary) RenderAction {
	agent := strings.TrimSpace(task.Agent)
	if agent == "" {
		agent = "this agent"
	}
	return RenderAction{
		Kind: RenderSend,
		Content: fmt.Sprintf(
			"%s does not support structured Discord streaming yet.\nOpen the task in AGX Desktop, or use `/logs` for a terminal snapshot.",
			agent,
		),
		HighPriority: true,
	}
}

// errorMessages intentionally keeps Discord terse. Full agent/tool errors are
// already preserved in task logs; sending raw multi-line failures to Discord can
// flood a task channel.
func (r SemanticRenderer) errorMessages(errText string) []RenderAction {
	summary := summarizeAgentError(errText)
	content := "❌ AGX agent error"
	if summary != "" {
		content += ": " + summary
	}
	content += "\nDetails are available in AGX Desktop or `/logs`."
	return []RenderAction{{
		Kind:         RenderSend,
		Content:      content,
		HighPriority: true,
	}}
}

func summarizeAgentError(errText string) string {
	errText = strings.TrimSpace(stripANSI(errText))
	if errText == "" {
		return "the agent did not include details"
	}
	for _, line := range strings.Split(strings.ReplaceAll(errText, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		return summarizeAgentErrorLine(line)
	}
	return "the agent did not include details"
}

func summarizeAgentErrorLine(line string) string {
	switch {
	case strings.Contains(line, "apply_patch verification failed"):
		return "tool error: apply_patch verification failed"
	case strings.Contains(line, "unable to locate image"):
		return "tool error: unable to locate image"
	case strings.Contains(line, "codex_core::tools::router"):
		return "tool error"
	case strings.Contains(line, "BatchLogProcessor.ExportError"),
		strings.Contains(line, "BatchSpanProcessor.Flush.ExportError"),
		strings.Contains(line, "opentelemetry_sdk"):
		return "telemetry export error"
	default:
		return truncateRunesWithEllipsis(line, errorSummaryMaxRunes)
	}
}

func (r SemanticRenderer) sendChunks(text string, highPriority bool) []RenderAction {
	chunks := splitDiscordMarkdown(formatDiscordMarkdown(strings.TrimSpace(text)), r.messageBudget())
	if len(chunks) == 0 {
		return nil
	}
	actions := make([]RenderAction, 0, len(chunks))
	for _, chunk := range chunks {
		actions = append(actions, RenderAction{Kind: RenderSend, Content: chunk, HighPriority: highPriority})
	}
	return actions
}

func (r SemanticRenderer) progress(label, text string) string {
	preview := compactProgressPreview(text, false)
	if preview == "" {
		return label
	}
	return label + "\n" + truncateUTF8(preview, r.messageBudget()-len(label)-2)
}

func (r SemanticRenderer) assistantProgress(label, text string) string {
	preview := compactProgressPreview(text, true)
	if preview == "" {
		return label
	}
	return label + "\n" + truncateUTF8(preview, r.messageBudget()-len(label)-2)
}

func (r SemanticRenderer) renderCommandCompleted(event agentstream.Event) []RenderAction {
	if event.Command == nil {
		return nil
	}
	failed := event.Command.ExitCode != nil && *event.Command.ExitCode != 0
	preview := strings.TrimSpace(strings.Join([]string{event.Command.Stdout, event.Command.Stderr}, "\n"))
	if failed {
		status := fmt.Sprintf("failed with exit code %d", *event.Command.ExitCode)
		if preview == "" {
			return []RenderAction{{Kind: RenderUpdateProgress, Content: "❌ Command " + status + "."}}
		}
		return []RenderAction{{Kind: RenderUpdateProgress, Content: r.progress("❌ Command "+status+".", preview)}}
	}
	if preview != "" {
		return []RenderAction{{Kind: RenderUpdateProgress, Content: r.progress("✅ Done.", preview)}}
	}
	return []RenderAction{{Kind: RenderUpdateProgress, Content: "✅ Done."}}
}

func (r SemanticRenderer) messageBudget() int {
	if r.MaxMessageBytes > 0 && r.MaxMessageBytes < maxDiscordMessage {
		return r.MaxMessageBytes
	}
	return semanticMessageBudget
}

func compactProgressPreview(text string, tail bool) string {
	lines := progressPreviewLines(text)
	if len(lines) == 0 {
		return ""
	}
	if len(lines) > progressPreviewMaxLines {
		if tail {
			keep := progressPreviewMaxLines - 1
			lines = append([]string{"..."}, lines[len(lines)-keep:]...)
		} else {
			lines = append(append([]string{}, lines[:progressPreviewMaxLines-1]...), "...")
		}
	}
	for i, line := range lines {
		if line == "..." {
			continue
		}
		lines[i] = truncateRunesWithEllipsis(line, progressPreviewMaxLineRunes)
	}
	return strings.Join(lines, "\n")
}

func progressPreviewLines(text string) []string {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	if text == "" {
		return nil
	}
	raw := strings.Split(text, "\n")
	lines := make([]string, 0, len(raw))
	for _, line := range raw {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		lines = append(lines, line)
	}
	return lines
}

func truncateRunesWithEllipsis(text string, maxRunes int) string {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) <= maxRunes {
		return string(runes)
	}
	if maxRunes <= 3 {
		return "..."
	}
	return string(runes[:maxRunes-3]) + "..."
}

func formatDiscordMarkdown(text string) string {
	return fenceMarkdownTables(text)
}

func fenceMarkdownTables(text string) string {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines)+4)
	inFence := false
	for i := 0; i < len(lines); {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			out = append(out, line)
			i++
			continue
		}
		if inFence || !isMarkdownTableStart(lines, i) {
			out = append(out, line)
			i++
			continue
		}
		start := i
		end := i + 2
		for end < len(lines) && isMarkdownTableRow(lines[end]) {
			end++
		}
		out = append(out, "```text")
		out = append(out, lines[start:end]...)
		out = append(out, "```")
		i = end
	}
	return strings.Join(out, "\n")
}

func isMarkdownTableStart(lines []string, index int) bool {
	if index+1 >= len(lines) {
		return false
	}
	return isMarkdownTableRow(lines[index]) && isMarkdownTableSeparator(lines[index+1])
}

func isMarkdownTableRow(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || !strings.Contains(trimmed, "|") {
		return false
	}
	return strings.Count(trimmed, "|") >= 2
}

func isMarkdownTableSeparator(line string) bool {
	trimmed := strings.TrimSpace(line)
	if !isMarkdownTableRow(trimmed) {
		return false
	}
	trimmed = strings.Trim(trimmed, "| ")
	cells := strings.Split(trimmed, "|")
	if len(cells) < 2 {
		return false
	}
	for _, cell := range cells {
		cell = strings.TrimSpace(cell)
		if len(cell) < 3 {
			return false
		}
		cell = strings.Trim(cell, ":")
		if cell == "" || strings.Trim(cell, "-") != "" {
			return false
		}
	}
	return true
}

func renderToolTrace(tool *agentstream.ToolEvent) string {
	if tool == nil {
		return ""
	}
	name := strings.TrimSpace(tool.Name)
	if name == "" {
		return ""
	}
	detail := toolTraceDetail(name, tool.Input)
	if detail == "" {
		return inlineCode(name)
	}
	return inlineCode(name) + " " + detail
}

func toolTraceDetail(name, input string) string {
	fields := map[string]any{}
	if err := json.Unmarshal([]byte(input), &fields); err != nil {
		return truncateUTF8(strings.TrimSpace(input), 220)
	}
	lowerName := strings.ToLower(strings.TrimSpace(name))
	for _, key := range []string{"file_path", "path", "url", "pattern", "query"} {
		if value := jsonStringField(fields, key); value != "" {
			return inlineCode(value)
		}
	}
	if lowerName == "bash" || lowerName == "shell" {
		if command := jsonStringField(fields, "command"); command != "" {
			return inlineCode(command)
		}
	}
	return ""
}

func jsonStringField(fields map[string]any, key string) string {
	value, ok := fields[key]
	if !ok {
		return ""
	}
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(text)
}

func inlineCode(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	text = strings.ReplaceAll(text, "`", "'")
	return "`" + truncateUTF8(text, 220) + "`"
}

func renderApproval(approval *agentstream.ApprovalEvent) string {
	if approval == nil {
		return "Agent requested approval."
	}
	lines := []string{"Agent requested approval."}
	if strings.TrimSpace(approval.Prompt) != "" {
		lines = append(lines, approval.Prompt)
	}
	if strings.TrimSpace(approval.Command) != "" {
		lines = append(lines, codeBlock(approval.Command))
	}
	if len(approval.Options) > 0 {
		lines = append(lines, "Options:")
		for _, option := range approval.Options {
			lines = append(lines, "- "+option.Label)
		}
	}
	return strings.Join(lines, "\n")
}

func approvalPrompt(taskID, content string, approval *agentstream.ApprovalEvent) *InteractivePrompt {
	if approval == nil || len(approval.Options) == 0 {
		return nil
	}
	options := make([]InteractiveOption, 0, len(approval.Options))
	for _, option := range approval.Options {
		label := strings.TrimSpace(option.Label)
		if label == "" {
			continue
		}
		options = append(options, InteractiveOption{ID: strings.TrimSpace(option.ID), Label: label})
	}
	if len(options) == 0 {
		return nil
	}
	return &InteractivePrompt{TaskID: taskID, Kind: "approval", Content: content, Options: options}
}

func renderQuestion(question *agentstream.QuestionEvent) string {
	if question == nil {
		return "Agent requested input."
	}
	lines := []string{"Agent requested input."}
	if strings.TrimSpace(question.Prompt) != "" {
		lines = append(lines, question.Prompt)
	}
	if len(question.Options) > 0 {
		lines = append(lines, "Options:")
		for _, option := range question.Options {
			lines = append(lines, "- "+option.Label)
		}
	}
	return strings.Join(lines, "\n")
}

func questionPrompt(taskID, content string, question *agentstream.QuestionEvent) *InteractivePrompt {
	if question == nil || len(question.Options) == 0 {
		return nil
	}
	options := make([]InteractiveOption, 0, len(question.Options))
	for _, option := range question.Options {
		label := strings.TrimSpace(option.Label)
		if label == "" {
			continue
		}
		options = append(options, InteractiveOption{ID: strings.TrimSpace(option.ID), Label: label})
	}
	if len(options) == 0 {
		return nil
	}
	return &InteractivePrompt{TaskID: taskID, Kind: "question", Content: content, Options: options}
}

func splitDiscordMarkdown(text string, max int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if max <= 0 {
		max = semanticMessageBudget
	}
	rawChunks := splitOutput(text, max-16)
	chunks := make([]string, 0, len(rawChunks))
	inFence := false
	fenceLang := ""
	for _, raw := range rawChunks {
		chunk := strings.TrimSpace(raw)
		if chunk == "" {
			continue
		}
		prefix := ""
		suffix := ""
		if inFence {
			prefix = "```" + fenceLang + "\n"
		}
		nextInFence, nextLang := scanFenceState(chunk, inFence, fenceLang)
		if nextInFence {
			suffix = "\n```"
		}
		chunks = append(chunks, prefix+chunk+suffix)
		inFence = nextInFence
		fenceLang = nextLang
	}
	return chunks
}

func scanFenceState(text string, inFence bool, lang string) (bool, string) {
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "```") {
			continue
		}
		if inFence {
			inFence = false
			lang = ""
			continue
		}
		inFence = true
		lang = strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
	}
	return inFence, lang
}
