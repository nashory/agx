package runtime

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/nashory/agx/internal/db"
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
func (f *fakeMuseRuntime) UserInputAnswer(_ context.Context, _, _, _, label string, _ bool) error {
	f.questionLabel = label
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
	service.agents.startMuse = func(context.Context) (museRuntime, error) { return fake, nil }
	t.Cleanup(func() { _ = service.agents.Close() })

	if err := service.agents.startMuseMSPTurn(context.Background(), task, project, "hello"); err != nil {
		t.Fatal(err)
	}
	if fake.startedText != "hello" || fake.sessionID == "" || fake.workspace == "" {
		t.Fatalf("fake = %#v", fake)
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
