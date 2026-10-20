package runtime

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/nashory/agx/internal/agentstream"
	"github.com/nashory/agx/internal/codexapp"
	"github.com/nashory/agx/internal/db"
	agxdiscord "github.com/nashory/agx/internal/discord"
)

func TestCodexQuestionIsAnsweredThroughPendingPrompt(t *testing.T) {
	service, fake, task := newCodexTurnTestService(t)
	notification := codexapp.Notification{
		Method:    codexapp.NotifyUserInputRequest,
		RequestID: "input-1",
		Params:    json.RawMessage(`{"threadId":"thread-1","turnId":"turn-1","questions":[{"id":"color","header":"Color","question":"Pick one","options":[{"label":"Blue"}],"isOther":true}]}`),
	}
	request, ok := codexapp.ParseUserInputRequest(notification)
	if !ok || !service.agents.rememberCodexQuestion(notification, request) {
		t.Fatal("Codex question was not remembered")
	}
	token := agxdiscord.PromptToken("input-1:color")
	resolved, err := service.agents.resolveCodexPrompt(fake, task.ID, "Blue", token)
	if err != nil || !resolved {
		t.Fatalf("resolveCodexPrompt() = %v, %v", resolved, err)
	}
	answers := <-fake.inputAnswers
	if got := strings.Join(answers["color"], ","); got != "Blue" {
		t.Fatalf("answer = %q, want Blue", got)
	}
}

func TestClaudeQuestionParsingAndSequentialAnswers(t *testing.T) {
	line := []byte(`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"tool-1","name":"AskUserQuestion","input":{"questions":[{"header":"Mode","question":"Choose","options":[{"label":"Fast"}],"multiSelect":false},{"header":"Notes","question":"Anything else?","options":[],"multiSelect":false}]}}]}}`)
	prompt, ok := parseClaudeQuestionLine(line)
	if !ok || len(prompt.questions) != 2 {
		t.Fatalf("parseClaudeQuestionLine() = %#v, %v", prompt, ok)
	}
	service := NewService("test")
	service.agents.claudePrompts["task"] = []claudePendingPrompt{prompt}
	resolved, answer, err := service.agents.resolveClaudePrompt("task", "Fast", agxdiscord.PromptToken("tool-1:q1"))
	if err != nil || !resolved || answer != "" {
		t.Fatalf("first answer = %v, %q, %v", resolved, answer, err)
	}
	resolved, answer, err = service.agents.resolveClaudePrompt("task", "No", agxdiscord.PromptToken("tool-1:q2"))
	if err != nil || !resolved || !strings.Contains(answer, "Fast") || !strings.Contains(answer, "No") {
		t.Fatalf("final answer = %v, %q, %v", resolved, answer, err)
	}
}

func TestClaudeOptionQuestionTreatsOtherTextAsNewInstruction(t *testing.T) {
	service := NewService("test")
	service.agents.claudePrompts["task"] = []claudePendingPrompt{{
		id: "tool-1",
		questions: []pendingQuestion{{
			id: "q1", options: []agentstream.QuestionOption{{ID: "Fix ceiling", Label: "Fix ceiling"}},
			choicesByLabel: map[string]string{"fix ceiling": "Fix ceiling", "fix network": "Fix network"},
			allowFreeText:  true, multiple: true,
		}},
	}}
	resolved, answer, err := service.agents.resolveClaudePrompt("task", "Instead, draft the storyline", "")
	if err != nil || resolved || answer != "" {
		t.Fatalf("resolveClaudePrompt() = %v, %q, %v; want raw instruction", resolved, answer, err)
	}
	if len(service.agents.claudePrompts["task"]) != 0 {
		t.Fatal("superseded Claude prompt was retained")
	}
}

func TestClaudeOptionQuestionAcceptsTypedLabels(t *testing.T) {
	service := NewService("test")
	service.agents.claudePrompts["task"] = []claudePendingPrompt{{
		id: "tool-1",
		questions: []pendingQuestion{{
			id: "q1", options: []agentstream.QuestionOption{{ID: "Fix ceiling", Label: "Fix ceiling"}, {ID: "Fix network", Label: "Fix network"}},
			choicesByLabel: map[string]string{"fix ceiling": "Fix ceiling", "fix network": "Fix network"},
			allowFreeText:  true, multiple: true,
		}},
	}}
	resolved, answer, err := service.agents.resolveClaudePrompt("task", "Fix ceiling, Fix network", "")
	if err != nil || !resolved || !strings.Contains(answer, "Fix ceiling, Fix network") {
		t.Fatalf("resolveClaudePrompt() = %v, %q, %v", resolved, answer, err)
	}
}

func TestClaudeRejectsExpiredPromptButton(t *testing.T) {
	service := NewService("test")
	resolved, answer, err := service.agents.resolveClaudePrompt("task", "Old choice", agxdiscord.PromptToken("old:q1"))
	if !resolved || answer != "" || err == nil {
		t.Fatalf("resolveClaudePrompt() = %v, %q, %v; want expired choice error", resolved, answer, err)
	}
}

func TestClaudeQuestionMarkerParsingAndSuppression(t *testing.T) {
	line := []byte(`{"type":"assistant","message":{"id":"msg-1","content":[{"type":"text","text":"<agx-question>{\"questions\":[{\"header\":\"Color\",\"question\":\"Pick one\",\"options\":[{\"label\":\"Blue\"}],\"multiSelect\":false}]}</agx-question>"}]}}`)
	prompt, ok := parseClaudeQuestionLine(line)
	if !ok || prompt.id != "msg-1" || len(prompt.questions) != 1 || prompt.questions[0].options[0].Label != "Blue" {
		t.Fatalf("parseClaudeQuestionLine() = %#v, %v", prompt, ok)
	}
	if events := mapClaudeStreamLine(testClaudeTask(), "turn", line); len(events) != 0 {
		t.Fatalf("events = %#v, want marker suppressed from chat", events)
	}
}

func TestClaudeQuestionMarkerPreservesSubstantiveAnswer(t *testing.T) {
	line := []byte(`{"type":"assistant","message":{"id":"msg-1","content":[{"type":"text","text":"Here is the complete storyline.\n\n<agx-question>{\"questions\":[{\"header\":\"Next\",\"question\":\"Apply it?\",\"options\":[{\"label\":\"Apply\"}],\"multiSelect\":false}]}</agx-question>"}]}}`)
	prompt, ok := parseClaudeQuestionLine(line)
	if !ok || prompt.questions[0].prompt != "Next\nApply it?" {
		t.Fatalf("parseClaudeQuestionLine() = %#v, %v", prompt, ok)
	}
	events := mapClaudeStreamLine(testClaudeTask(), "turn", line)
	if len(events) != 1 || events[0].Kind != agentstream.EventAssistantMessage || events[0].Text != "Here is the complete storyline." {
		t.Fatalf("events = %#v, want visible answer without marker", events)
	}
}

func TestClaudeQuestionMarkerReleasesActiveTurn(t *testing.T) {
	line := []byte(`{"type":"assistant","message":{"id":"msg-1","content":[{"type":"text","text":"<agx-question>{\"questions\":[{\"header\":\"Mode\",\"question\":\"Choose\",\"options\":[{\"label\":\"Keep\"}],\"multiSelect\":false}]}</agx-question>"}]}}`)
	prompt, ok := parseClaudeQuestionLine(line)
	if !ok || prompt.nativeTool {
		t.Fatalf("parseClaudeQuestionLine() = %#v, %v, want fallback marker", prompt, ok)
	}
	service := NewService("test")
	releases := 0
	if !service.agents.acceptClaudeQuestion("task", "turn", prompt, func() { releases++ }) {
		t.Fatal("fallback question was not accepted")
	}
	if releases != 1 {
		t.Fatalf("turn releases = %d, want 1", releases)
	}
	if service.agents.acceptClaudeQuestion("task", "turn", prompt, func() { releases++ }) {
		t.Fatal("duplicate question was accepted")
	}
	if releases != 1 {
		t.Fatalf("duplicate question released turn; releases = %d", releases)
	}
}

func TestClaudeQuestionToolIsNotRenderedAsGenericTool(t *testing.T) {
	line := []byte(`{"type":"assistant","message":{"id":"msg","content":[{"type":"tool_use","id":"tool-1","name":"AskUserQuestion","input":{"questions":[]}}]}}`)
	if events := mapClaudeStreamLine(testClaudeTask(), "turn", line); len(events) != 0 {
		t.Fatalf("events = %#v, want question tool handled separately", events)
	}
}

func testClaudeTask() db.Task {
	return db.Task{ID: "task", Agent: "claude"}
}
