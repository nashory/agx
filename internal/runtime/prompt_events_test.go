package runtime

import (
	"encoding/json"
	"strings"
	"testing"

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

func TestClaudeQuestionToolIsNotRenderedAsGenericTool(t *testing.T) {
	line := []byte(`{"type":"assistant","message":{"id":"msg","content":[{"type":"tool_use","id":"tool-1","name":"AskUserQuestion","input":{"questions":[]}}]}}`)
	if events := mapClaudeStreamLine(testClaudeTask(), "turn", line); len(events) != 0 {
		t.Fatalf("events = %#v, want question tool handled separately", events)
	}
}

func testClaudeTask() db.Task {
	return db.Task{ID: "task", Agent: "claude"}
}
