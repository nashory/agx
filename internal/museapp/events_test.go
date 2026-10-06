package museapp

import (
	"encoding/json"
	"testing"

	"github.com/nashory/agx/internal/agentstream"
)

func TestMapAgentMessageDeltaAndCompletion(t *testing.T) {
	task := agentstream.TaskSummary{ID: "task-1", Agent: "muse"}
	delta, err := MapNotification(task, Notification{Method: NotifyItemDelta, Params: json.RawMessage(`{"sessionId":"s","viewCursor":"v:1","itemId":"i","delta":"hello ","field":"text"}`)})
	if err != nil || len(delta) != 1 || delta[0].Kind != agentstream.EventAssistantDelta || delta[0].Text != "hello " {
		t.Fatalf("delta = %#v, err=%v", delta, err)
	}
	completed, err := MapNotification(task, Notification{Method: NotifyItemCompleted, Params: json.RawMessage(`{"sessionId":"s","viewCursor":"v:2","item":{"itemId":"i","kind":"agentMessage","turnId":"turn","revision":2,"status":"completed","text":"hello world"}}`)})
	if err != nil || len(completed) != 1 || completed[0].Kind != agentstream.EventAssistantMessage || completed[0].Text != "hello world" {
		t.Fatalf("completed = %#v, err=%v", completed, err)
	}
}

func TestMapToolLifecycle(t *testing.T) {
	task := agentstream.TaskSummary{ID: "task-1", Agent: "muse"}
	started, _ := MapNotification(task, Notification{Method: NotifyItemStarted, Params: json.RawMessage(`{"viewCursor":"v:1","item":{"itemId":"tool-1","kind":"toolCall","turnId":"turn","revision":1,"status":"inProgress","tool":"bash","args":"{\"command\":\"go test ./...\"}"}}`)})
	if len(started) != 1 || started[0].Tool == nil || started[0].Tool.Name != "bash" {
		t.Fatalf("started = %#v", started)
	}
	output, _ := MapNotification(task, Notification{Method: NotifyItemDelta, Params: json.RawMessage(`{"viewCursor":"v:2","itemId":"tool-1","delta":"ok\n","field":"output"}`)})
	if len(output) != 1 || output[0].Kind != agentstream.EventCommandOutputDelta || output[0].Command.Stdout != "ok\n" {
		t.Fatalf("output = %#v", output)
	}
	completed, _ := MapNotification(task, Notification{Method: NotifyItemCompleted, Params: json.RawMessage(`{"viewCursor":"v:3","item":{"itemId":"tool-1","kind":"toolCall","turnId":"turn","revision":2,"status":"completed","tool":"bash","visibleOutput":"ok\n"}}`)})
	if len(completed) != 1 || completed[0].Command == nil || completed[0].Command.ExitCode == nil || *completed[0].Command.ExitCode != 0 {
		t.Fatalf("completed = %#v", completed)
	}
}

func TestMapApprovalAndQuestion(t *testing.T) {
	task := agentstream.TaskSummary{ID: "task-1", Agent: "muse"}
	approval, err := MapNotification(task, Notification{Method: NotifyApprovalRequested, Params: json.RawMessage(`{"approvalId":"a","turnId":"t","itemId":"i","toolName":"bash","rawArgs":"ls","viewCursor":"v:1","subject":{"kind":"shell","command":"ls"},"availableChoices":[{"choiceId":"yes","label":"Allow","decision":"approved","scope":"once"}]}`)})
	if err != nil || len(approval) != 1 || approval[0].Approval == nil || approval[0].Approval.Options[0].ID != "yes" {
		t.Fatalf("approval = %#v, err=%v", approval, err)
	}
	question, err := MapNotification(task, Notification{Method: NotifyUserInputRequested, Params: json.RawMessage(`{"userInputId":"u","turnId":"t","itemId":"i","viewCursor":"v:2","questions":[{"id":"q","header":"Choice","question":"Pick one","selection":{"mode":"single"},"options":[{"label":"A"},{"label":"B"}]}]}`)})
	if err != nil || len(question) != 1 || question[0].Question == nil || len(question[0].Question.Options) != 2 {
		t.Fatalf("question = %#v, err=%v", question, err)
	}
}

func TestMapReasoningDoesNotExposeRawText(t *testing.T) {
	events, err := MapNotification(agentstream.TaskSummary{ID: "task"}, Notification{Method: NotifyItemCompleted, Params: json.RawMessage(`{"viewCursor":"v:1","item":{"itemId":"r","kind":"reasoning","turnId":"t","status":"completed","text":"private chain of thought"}}`)})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("raw reasoning was mapped: %#v", events)
	}
}
