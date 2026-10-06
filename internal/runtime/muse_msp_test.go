package runtime

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/nashory/agx/internal/db"
	agxdiscord "github.com/nashory/agx/internal/discord"
	"github.com/nashory/agx/internal/museapp"
)

type fakeMuseRuntime struct {
	events          chan museapp.Notification
	sessionID       string
	workspace       string
	startedText     string
	steeredText     string
	interruptedTurn string
	approvalChoice  string
	questionLabel   string
	questionAnswers []museapp.UserInputAnswer
}

func newFakeMuseRuntime() *fakeMuseRuntime {
	return &fakeMuseRuntime{events: make(chan museapp.Notification, 16)}
}

func (f *fakeMuseRuntime) Initialize(context.Context) (museapp.InitializeResponse, error) {
	return museapp.InitializeResponse{}, nil
}
func (f *fakeMuseRuntime) SessionStart(_ context.Context, sessionID, workspace string, _ bool) (museapp.SessionResponse, error) {
	f.sessionID, f.workspace = sessionID, workspace
	return museapp.SessionResponse{Session: museapp.Session{ID: sessionID}}, nil
}
func (f *fakeMuseRuntime) SessionResume(context.Context, string, string) (museapp.SessionResponse, error) {
	return museapp.SessionResponse{}, &museapp.CallError{Code: -32020, Message: "session not found"}
}
func (f *fakeMuseRuntime) ViewPage(context.Context, string, string, int) (museapp.ViewPageResponse, error) {
	return museapp.ViewPageResponse{}, nil
}
func (f *fakeMuseRuntime) ViewSubscribe(context.Context, string, string) error { return nil }
func (f *fakeMuseRuntime) ViewUnsubscribe(context.Context, string) error       { return nil }
func (f *fakeMuseRuntime) TurnStart(_ context.Context, _ string, text, _ string) (museapp.TurnResponse, error) {
	f.startedText = text
	return museapp.TurnResponse{TurnID: museapp.NewCommandID(), Status: "accepted"}, nil
}
func (f *fakeMuseRuntime) TurnSteer(_ context.Context, _, _ string, text string) (museapp.TurnResponse, error) {
	f.steeredText = text
	return museapp.TurnResponse{TurnID: museapp.NewCommandID()}, nil
}
func (f *fakeMuseRuntime) TurnInterrupt(_ context.Context, _, turnID string) error {
	f.interruptedTurn = turnID
	return nil
}
func (f *fakeMuseRuntime) ApprovalDecide(_ context.Context, _, _ string, _ any, choice string) error {
	f.approvalChoice = choice
	return nil
}
func (f *fakeMuseRuntime) UserInputAnswer(_ context.Context, _, _ string, answers []museapp.UserInputAnswer) error {
	f.questionAnswers = answers
	if len(answers) != 0 {
		f.questionLabel = answers[len(answers)-1].Value
	}
	return nil
}
func (f *fakeMuseRuntime) Respond(museapp.Notification, any) error { return nil }
func (f *fakeMuseRuntime) Events() <-chan museapp.Notification     { return f.events }
func (f *fakeMuseRuntime) RecentStderr() string                    { return "" }
func (f *fakeMuseRuntime) Close() error                            { return nil }

func TestMuseMSPStartsSessionAndTurn(t *testing.T) {
	store, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	project, err := store.EnsureProject(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTaskRuntimeModeInterface(db.NewTaskID(), project.ID, "muse", nil, "muse", true, db.TaskInterfaceDiscord, db.StatusWaiting, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService("test")
	service.store = store
	fake := newFakeMuseRuntime()
	startedAllMighty := false
	service.agents.startMuse = func(_ context.Context, allMighty bool) (museRuntime, error) {
		startedAllMighty = allMighty
		return fake, nil
	}
	t.Cleanup(func() { _ = service.agents.Close() })

	if err := service.agents.startMuseMSPTurn(context.Background(), task, project, "hello"); err != nil {
		t.Fatal(err)
	}
	if fake.startedText != "hello" || fake.sessionID == "" || fake.workspace == "" {
		t.Fatalf("fake = %#v", fake)
	}
	if !startedAllMighty {
		t.Fatal("all-mighty task used the sandboxed Muse host")
	}
	updated, err := store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.AgentStreamKind == nil || *updated.AgentStreamKind != museapp.StreamKind || updated.AgentThreadID == nil {
		t.Fatalf("updated task = %#v", updated)
	}
}

func TestMuseMSPResolvesPendingApproval(t *testing.T) {
	service := NewService("test")
	t.Cleanup(func() { _ = service.agents.Close() })
	fake := newFakeMuseRuntime()
	notification := museapp.Notification{Method: museapp.NotifyApprovalRequested, Params: json.RawMessage(`{"sessionId":"session","approvalId":"approval","currentRequirementId":{"approvalId":"approval","sourceIndex":0},"availableChoices":[{"choiceId":"allow-once","label":"Allow once"}]}`)}
	service.agents.rememberMuseApproval("task", notification)
	resolved, err := service.agents.resolveMusePrompt(context.Background(), fake, "task", "Allow once")
	if err != nil || !resolved || fake.approvalChoice != "allow-once" {
		t.Fatalf("resolved=%v choice=%q err=%v", resolved, fake.approvalChoice, err)
	}
}

func TestMuseMSPCollectsEveryUserInputAnswer(t *testing.T) {
	service := NewService("test")
	t.Cleanup(func() { _ = service.agents.Close() })
	fake := newFakeMuseRuntime()
	notification := museapp.Notification{Method: museapp.NotifyUserInputRequested, Params: json.RawMessage(`{"sessionId":"session","userInputId":"input","questions":[{"id":"q1","header":"First","question":"Pick one","options":[{"label":"A"}]},{"id":"q2","header":"Second","question":"Explain","options":[]}]}`)}
	service.agents.rememberMuseQuestion("task", notification)
	if resolved, err := service.agents.resolveMusePrompt(context.Background(), fake, "task", "A"); err != nil || !resolved {
		t.Fatalf("first answer resolved=%v err=%v", resolved, err)
	}
	if len(fake.questionAnswers) != 0 {
		t.Fatalf("answers sent too early: %#v", fake.questionAnswers)
	}
	if resolved, err := service.agents.resolveMusePrompt(context.Background(), fake, "task", "details"); err != nil || !resolved {
		t.Fatalf("second answer resolved=%v err=%v", resolved, err)
	}
	if len(fake.questionAnswers) != 2 || fake.questionAnswers[0].QuestionID != "q1" || fake.questionAnswers[1].QuestionID != "q2" {
		t.Fatalf("answers = %#v", fake.questionAnswers)
	}
}

func TestMuseMSPQueuesPendingApprovals(t *testing.T) {
	service := NewService("test")
	t.Cleanup(func() { _ = service.agents.Close() })
	fake := newFakeMuseRuntime()
	service.agents.rememberMuseApproval("task", museapp.Notification{Method: museapp.NotifyApprovalRequested, Params: json.RawMessage(`{"sessionId":"session","approvalId":"first","currentRequirementId":1,"availableChoices":[{"choiceId":"first-yes","label":"Allow first"}]}`)})
	service.agents.rememberMuseApproval("task", museapp.Notification{Method: museapp.NotifyApprovalRequested, Params: json.RawMessage(`{"sessionId":"session","approvalId":"second","currentRequirementId":2,"availableChoices":[{"choiceId":"second-yes","label":"Allow second"}]}`)})
	if resolved, err := service.agents.resolveMusePrompt(context.Background(), fake, "task", "Allow first"); err != nil || !resolved || fake.approvalChoice != "first-yes" {
		t.Fatalf("first approval resolved=%v choice=%q err=%v", resolved, fake.approvalChoice, err)
	}
	if resolved, err := service.agents.resolveMusePrompt(context.Background(), fake, "task", "Allow second"); err != nil || !resolved || fake.approvalChoice != "second-yes" {
		t.Fatalf("second approval resolved=%v choice=%q err=%v", resolved, fake.approvalChoice, err)
	}
}

func TestMuseMSPRejectsStalePromptToken(t *testing.T) {
	service := NewService("test")
	t.Cleanup(func() { _ = service.agents.Close() })
	fake := newFakeMuseRuntime()
	first := museapp.Notification{Method: museapp.NotifyApprovalRequested, Params: json.RawMessage(`{"sessionId":"session","approvalId":"first","currentRequirementId":1,"availableChoices":[{"choiceId":"first-yes","label":"Allow"}]}`)}
	second := museapp.Notification{Method: museapp.NotifyApprovalRequested, Params: json.RawMessage(`{"sessionId":"session","approvalId":"second","currentRequirementId":2,"availableChoices":[{"choiceId":"second-yes","label":"Allow"}]}`)}
	service.agents.rememberMuseApproval("task", first)
	service.agents.rememberMuseApproval("task", second)
	firstToken := agxdiscord.PromptToken("first")
	if _, err := service.agents.resolveMusePrompt(context.Background(), fake, "task", "Allow", firstToken); err != nil {
		t.Fatal(err)
	}
	fake.approvalChoice = ""
	if resolved, err := service.agents.resolveMusePrompt(context.Background(), fake, "task", "Allow", firstToken); !resolved || err == nil {
		t.Fatalf("stale choice resolved=%v err=%v", resolved, err)
	}
	if fake.approvalChoice != "" {
		t.Fatalf("stale choice reached Muse: %q", fake.approvalChoice)
	}
}

func TestMuseMSPValidatesMultipleSelectionBounds(t *testing.T) {
	service := NewService("test")
	t.Cleanup(func() { _ = service.agents.Close() })
	fake := newFakeMuseRuntime()
	service.agents.rememberMuseQuestion("task", museapp.Notification{Method: museapp.NotifyUserInputRequested, Params: json.RawMessage(`{"sessionId":"session","userInputId":"input","questions":[{"id":"q","question":"Pick two","selection":{"mode":"multiple","minSelections":2,"maxSelections":2},"options":[{"label":"A"},{"label":"B"},{"label":"C"}]}]}`)})
	if resolved, err := service.agents.resolveMusePrompt(context.Background(), fake, "task", "A"); !resolved || err == nil {
		t.Fatalf("single answer resolved=%v err=%v", resolved, err)
	}
	if resolved, err := service.agents.resolveMusePrompt(context.Background(), fake, "task", "A, A"); !resolved || err == nil {
		t.Fatalf("duplicate answer resolved=%v err=%v", resolved, err)
	}
	if resolved, err := service.agents.resolveMusePrompt(context.Background(), fake, "task", "A, B, unknown"); !resolved || err == nil {
		t.Fatalf("unknown answer resolved=%v err=%v", resolved, err)
	}
	if resolved, err := service.agents.resolveMusePrompt(context.Background(), fake, "task", "A, B"); err != nil || !resolved {
		t.Fatalf("multiple answer resolved=%v err=%v", resolved, err)
	}
	if len(fake.questionAnswers) != 1 || len(fake.questionAnswers[0].Values) != 2 {
		t.Fatalf("answers = %#v", fake.questionAnswers)
	}
}

func TestMuseMSPIgnoresStaleTurnCompletionForTaskStatus(t *testing.T) {
	store, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	project, err := store.EnsureProject(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTaskRuntimeModeInterface(db.NewTaskID(), project.ID, "muse", nil, "muse", false, db.TaskInterfaceDiscord, db.StatusActive, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService("test")
	service.store = store
	t.Cleanup(func() { _ = service.agents.Close() })
	fake := newFakeMuseRuntime()
	service.agents.rememberThread(task.ID, "session")
	service.agents.mu.Lock()
	service.agents.activeTurns[task.ID] = "new-turn"
	service.agents.mu.Unlock()
	go service.agents.forwardMuseEvents(fake)
	fake.events <- museapp.Notification{Method: museapp.NotifyTurnCompleted, Params: json.RawMessage(`{"sessionId":"session","turnId":"old-turn","terminal":"completed","viewCursor":"v:1"}`)}
	deadline := time.Now().Add(time.Second)
	for {
		updatedCursor, err := store.GetTask(task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if updatedCursor.AgentEventCursor != nil && *updatedCursor.AgentEventCursor == "v:1" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for stale completion to be processed")
		}
		time.Sleep(time.Millisecond)
	}
	updated, err := store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != db.StatusActive {
		t.Fatalf("status = %q, want active", updated.Status)
	}
}
