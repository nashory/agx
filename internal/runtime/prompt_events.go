package runtime

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/nashory/agx/internal/agentstream"
	"github.com/nashory/agx/internal/codexapp"
	agxdiscord "github.com/nashory/agx/internal/discord"
)

const claudeQuestionStart = "<agx-question>"
const claudeQuestionEnd = "</agx-question>"

const claudeQuestionSystemPrompt = `When you need the user to choose or provide information before continuing, do not ask only in prose. End your response with exactly one machine-readable block in this form: <agx-question>{"questions":[{"header":"Short title","question":"Question text","options":[{"label":"Choice","description":"What it means"}],"multiSelect":false}]}</agx-question>. Use 2-4 options when choices are known; use an empty options array for free text. Do not put the block in a Markdown code fence.`

func parseClaudeQuestionLine(line []byte) (claudePendingPrompt, bool) {
	var envelope struct {
		Type    string `json:"type"`
		Message struct {
			ID      string `json:"id"`
			Content []struct {
				Type  string          `json:"type"`
				ID    string          `json:"id"`
				Name  string          `json:"name"`
				Text  string          `json:"text"`
				Input json.RawMessage `json:"input"`
			} `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &envelope) != nil || !strings.EqualFold(envelope.Type, "assistant") {
		return claudePendingPrompt{}, false
	}
	for _, content := range envelope.Message.Content {
		if content.Type == "text" {
			if prompt, ok := parseClaudeQuestionPayload(content.Text, strings.TrimSpace(envelope.Message.ID)); ok {
				return prompt, true
			}
		}
		if content.Type != "tool_use" || !strings.EqualFold(strings.TrimSpace(content.Name), "AskUserQuestion") {
			continue
		}
		prompt, ok := parseClaudeQuestionJSON(content.Input, strings.TrimSpace(content.ID))
		if !ok {
			return claudePendingPrompt{}, false
		}
		prompt.nativeTool = true
		return prompt, true
	}
	return claudePendingPrompt{}, false
}

func parseClaudeQuestionPayload(text, id string) (claudePendingPrompt, bool) {
	_, raw, ok := splitClaudeQuestionPayload(text)
	if !ok {
		return claudePendingPrompt{}, false
	}
	return parseClaudeQuestionJSON([]byte(raw), id)
}

func splitClaudeQuestionPayload(text string) (string, string, bool) {
	start := strings.Index(text, claudeQuestionStart)
	if start < 0 {
		return text, "", false
	}
	payloadStart := start + len(claudeQuestionStart)
	end := strings.Index(text[payloadStart:], claudeQuestionEnd)
	if end < 0 {
		return text, "", false
	}
	payloadEnd := payloadStart + end
	visibleParts := make([]string, 0, 2)
	if prefix := strings.TrimSpace(text[:start]); prefix != "" {
		visibleParts = append(visibleParts, prefix)
	}
	if suffix := strings.TrimSpace(text[payloadEnd+len(claudeQuestionEnd):]); suffix != "" {
		visibleParts = append(visibleParts, suffix)
	}
	visible := strings.Join(visibleParts, "\n\n")
	return visible, text[payloadStart:payloadEnd], true
}

func parseClaudeQuestionJSON(raw []byte, id string) (claudePendingPrompt, bool) {
	var input struct {
		Questions []struct {
			Question    string `json:"question"`
			Header      string `json:"header"`
			MultiSelect bool   `json:"multiSelect"`
			Options     []struct {
				Label       string `json:"label"`
				Description string `json:"description"`
			} `json:"options"`
		} `json:"questions"`
	}
	if json.Unmarshal(raw, &input) != nil || len(input.Questions) == 0 || id == "" {
		return claudePendingPrompt{}, false
	}
	prompt := claudePendingPrompt{id: id}
	for index, question := range input.Questions {
		pending := pendingQuestion{
			id:             fmt.Sprintf("q%d", index+1),
			prompt:         strings.TrimSpace(strings.Join([]string{question.Header, question.Question}, "\n")),
			choicesByLabel: map[string]string{},
			allowFreeText:  true,
			multiple:       question.MultiSelect,
		}
		for _, option := range question.Options {
			label := strings.TrimSpace(option.Label)
			if label == "" {
				continue
			}
			pending.choicesByLabel[strings.ToLower(label)] = label
			pending.options = append(pending.options, agentstream.QuestionOption{ID: label, Label: label})
		}
		if pending.prompt != "" {
			prompt.questions = append(prompt.questions, pending)
		}
	}
	return prompt, len(prompt.questions) > 0
}

func (s *agentEventService) rememberClaudeQuestion(taskID, turnID string, prompt claudePendingPrompt) bool {
	s.mu.Lock()
	for _, existing := range s.claudePrompts[taskID] {
		if existing.id == prompt.id {
			s.mu.Unlock()
			return false
		}
	}
	s.claudePrompts[taskID] = append(s.claudePrompts[taskID], prompt)
	s.mu.Unlock()
	s.publishPendingQuestion(taskID, "claude", turnID, prompt.id, prompt.questions[0])
	return true
}

func (s *agentEventService) acceptClaudeQuestion(taskID, turnID string, prompt claudePendingPrompt, releaseTurn func()) bool {
	if !s.rememberClaudeQuestion(taskID, turnID, prompt) {
		return false
	}
	// Questions emitted through either the native tool or the fallback marker
	// are terminal for this headless turn. Release it immediately so a late
	// Discord choice resumes the agent instead of waiting behind a process
	// whose stdout may still be held open by a background child.
	if releaseTurn != nil {
		releaseTurn()
	}
	return true
}

func (s *agentEventService) resolveClaudePrompt(taskID, message, promptToken string) (bool, string, error) {
	s.mu.Lock()
	prompts := s.claudePrompts[taskID]
	if len(prompts) == 0 {
		s.mu.Unlock()
		if strings.TrimSpace(promptToken) != "" {
			return true, "", fmt.Errorf("this Discord choice belongs to an expired Claude prompt")
		}
		return false, "", nil
	}
	prompt := prompts[0]
	question := prompt.questions[prompt.questionIndex]
	identity := prompt.id + ":" + question.id
	if promptToken != "" && promptToken != agxdiscord.PromptToken(identity) {
		s.mu.Unlock()
		return true, "", fmt.Errorf("this Discord choice belongs to an expired Claude prompt")
	}
	answers, ok := promptAnswer(question, message)
	// A normal Discord message has no prompt token. When buttons are present,
	// only an exact option label should answer the question; any other text is a
	// new instruction or clarification. Drop the stale prompt and let the raw
	// message start a new Claude turn instead of trapping the conversation in
	// the question state. Free-text-only questions still accept normal replies.
	if promptToken == "" && len(question.options) > 0 {
		answers, ok = promptOptionAnswer(question, message)
		if !ok {
			delete(s.claudePrompts, taskID)
			s.mu.Unlock()
			return false, "", nil
		}
	}
	if !ok {
		s.mu.Unlock()
		return true, "", fmt.Errorf("answer does not match the pending Claude prompt")
	}
	prompt.answers = append(prompt.answers, strings.Join(answers, ", "))
	prompt.questionIndex++
	if prompt.questionIndex < len(prompt.questions) {
		next := prompt.questions[prompt.questionIndex]
		s.claudePrompts[taskID][0] = prompt
		s.mu.Unlock()
		s.publishPendingQuestion(taskID, "claude", "", prompt.id, next)
		return true, "", nil
	}
	if len(prompts) == 1 {
		delete(s.claudePrompts, taskID)
	} else {
		s.claudePrompts[taskID] = prompts[1:]
	}
	s.mu.Unlock()
	var lines []string
	for index, answer := range prompt.answers {
		lines = append(lines, fmt.Sprintf("%s: %s", prompt.questions[index].prompt, answer))
	}
	return true, "Here are my answers to your pending questions:\n" + strings.Join(lines, "\n"), nil
}

func promptOptionAnswer(question pendingQuestion, message string) ([]string, bool) {
	question.allowFreeText = false
	return promptAnswer(question, message)
}

func (s *agentEventService) rememberCodexQuestion(notification codexapp.Notification, request codexapp.UserInputRequest) bool {
	s.mu.Lock()
	taskID := s.threadToTask[request.ThreadID]
	if taskID == "" {
		taskID = s.taskIDForNotificationLocked(notification)
	}
	s.mu.Unlock()
	if taskID == "" {
		return false
	}
	prompt := codexPendingPrompt{notification: notification, requestID: notification.RequestID, turnID: request.TurnID, answers: map[string][]string{}}
	for _, question := range request.Questions {
		pending := pendingQuestion{
			id:             strings.TrimSpace(question.ID),
			prompt:         strings.TrimSpace(strings.Join([]string{question.Header, question.Question}, "\n")),
			choicesByLabel: map[string]string{},
			allowFreeText:  question.IsOther || len(question.Options) == 0,
		}
		if pending.id == "" || pending.prompt == "" {
			continue
		}
		for _, option := range question.Options {
			label := strings.TrimSpace(option.Label)
			if label == "" {
				continue
			}
			pending.choicesByLabel[strings.ToLower(label)] = label
			pending.options = append(pending.options, agentstream.QuestionOption{ID: label, Label: label})
		}
		prompt.questions = append(prompt.questions, pending)
	}
	if len(prompt.questions) == 0 {
		return false
	}
	s.mu.Lock()
	for _, existing := range s.codexPrompts[taskID] {
		if existing.requestID == prompt.requestID {
			s.mu.Unlock()
			return true
		}
	}
	s.codexPrompts[taskID] = append(s.codexPrompts[taskID], prompt)
	s.mu.Unlock()
	s.publishPendingQuestion(taskID, "codex", prompt.turnID, prompt.requestID, prompt.questions[0])
	return true
}

func (s *agentEventService) resolveCodexPrompt(client codexRuntime, taskID, message, promptToken string) (bool, error) {
	s.mu.Lock()
	prompts := s.codexPrompts[taskID]
	if len(prompts) == 0 {
		s.mu.Unlock()
		return false, nil
	}
	prompt := prompts[0]
	question := prompt.questions[prompt.questionIndex]
	identity := prompt.requestID + ":" + question.id
	if promptToken != "" && promptToken != agxdiscord.PromptToken(identity) {
		s.mu.Unlock()
		return true, fmt.Errorf("this Discord choice belongs to an expired Codex prompt")
	}
	answer, ok := promptAnswer(question, message)
	if !ok {
		s.mu.Unlock()
		return true, fmt.Errorf("answer does not match the pending Codex prompt")
	}
	prompt.answers[question.id] = answer
	prompt.questionIndex++
	if prompt.questionIndex < len(prompt.questions) {
		next := prompt.questions[prompt.questionIndex]
		s.codexPrompts[taskID][0] = prompt
		s.mu.Unlock()
		s.publishPendingQuestion(taskID, "codex", prompt.turnID, prompt.requestID, next)
		return true, nil
	}
	s.mu.Unlock()
	if err := client.AnswerInputRequest(prompt.notification, prompt.answers); err != nil {
		return true, err
	}
	s.mu.Lock()
	current := s.codexPrompts[taskID]
	if len(current) > 0 && current[0].requestID == prompt.requestID {
		if len(current) == 1 {
			delete(s.codexPrompts, taskID)
		} else {
			s.codexPrompts[taskID] = current[1:]
		}
	}
	s.mu.Unlock()
	return true, nil
}

func promptAnswer(question pendingQuestion, message string) ([]string, bool) {
	trimmed := strings.TrimSpace(message)
	if trimmed == "" {
		return nil, false
	}
	if question.multiple {
		var answers []string
		seen := map[string]bool{}
		for _, part := range strings.Split(trimmed, ",") {
			value, ok := question.choicesByLabel[strings.ToLower(strings.TrimSpace(part))]
			if !ok {
				if question.allowFreeText {
					return []string{trimmed}, true
				}
				return nil, false
			}
			if !seen[value] {
				seen[value] = true
				answers = append(answers, value)
			}
		}
		return answers, len(answers) > 0
	}
	if value, ok := question.choicesByLabel[strings.ToLower(trimmed)]; ok {
		return []string{value}, true
	}
	if question.allowFreeText {
		return []string{trimmed}, true
	}
	return nil, false
}

func (s *agentEventService) publishPendingQuestion(taskID, agent, turnID, promptID string, question pendingQuestion) {
	s.publish(taskID, agentstream.Event{
		ID: agentstream.StableEventID(taskID, agentstream.EventQuestionRequested, promptID, question.id), TaskID: taskID,
		TurnID: turnID, Kind: agentstream.EventQuestionRequested, Agent: agent, CreatedAt: time.Now(),
		Question: &agentstream.QuestionEvent{ID: promptID + ":" + question.id, Prompt: question.prompt, Options: question.options, Multiple: question.multiple},
	})
}
