package discord

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nashory/agx/internal/agentstream"
	"github.com/nashory/agx/internal/config"
	"github.com/nashory/agx/internal/db"
)

func TestShouldStreamTaskOnlyStreamsLiveMappedTasks(t *testing.T) {
	tests := []struct {
		name string
		task TaskSummary
		want bool
	}{
		{
			name: "active mapped legacy task",
			task: TaskSummary{ID: "task-1", ChannelID: "channel-1", Status: "active", Agent: "claude"},
			want: false,
		},
		{
			name: "active mapped structured codex task",
			task: TaskSummary{
				ID:              "task-1",
				ChannelID:       "channel-1",
				Status:          "active",
				Agent:           "codex",
				AgentThreadID:   stringPtr("thread-1"),
				AgentStreamKind: stringPtr("codex-app-server"),
			},
			want: true,
		},
		{
			name: "active mapped unprepared codex task",
			task: TaskSummary{ID: "task-1", ChannelID: "channel-1", Status: "active", Agent: "codex"},
			want: false,
		},
		{
			name: "waiting mapped structured task",
			task: TaskSummary{
				ID:              "task-1",
				ChannelID:       "channel-1",
				Status:          "waiting",
				Agent:           "codex",
				AgentThreadID:   stringPtr("thread-1"),
				AgentStreamKind: stringPtr("codex-app-server"),
			},
			want: true,
		},
		{
			name: "offline mapped task",
			task: TaskSummary{ID: "task-1", ChannelID: "channel-1", Status: "offline"},
			want: false,
		},
		{
			name: "active unmapped task",
			task: TaskSummary{ID: "task-1", Status: "active"},
			want: false,
		},
		{
			name: "missing task id",
			task: TaskSummary{ChannelID: "channel-1", Status: "active"},
			want: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := shouldStreamTask(test.task); got != test.want {
				t.Fatalf("shouldStreamTask(%#v) = %v, want %v", test.task, got, test.want)
			}
		})
	}
}

func TestBridgeStatusMasksBotToken(t *testing.T) {
	bridge := NewBridge(config.DiscordConfig{
		Enabled:        true,
		BotToken:       "abcd-secret-token",
		GuildID:        "guild-1",
		AllowedUserIDs: []string{"user-1"},
	})

	status := bridge.Status()
	if status.MaskedBotToken != "abcd..." {
		t.Fatalf("MaskedBotToken = %q, want abcd...", status.MaskedBotToken)
	}
}

func TestBridgeConfigureSettersStopAndStatusCopy(t *testing.T) {
	store, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	bridge := NewBridge(config.DiscordConfig{
		Enabled:        true,
		BotToken:       "old-token",
		GuildID:        "old-guild",
		AllowedUserIDs: []string{"old-user"},
	})
	events := scriptedAgentEvents{events: make(chan agentstream.Event)}
	bridge.SetStore(store)
	bridge.SetCommandService(nil)
	bridge.SetAgentEventSubscriber(events)
	bridge.Configure(config.DiscordConfig{
		Enabled:        true,
		BotToken:       "new-token",
		GuildID:        "new-guild",
		AllowedUserIDs: []string{"user-1"},
	})

	if bridge.store != store || bridge.service != nil || bridge.events == nil {
		t.Fatal("bridge setters did not store dependencies")
	}
	status := bridge.Status()
	if status.GuildID != "new-guild" || status.MaskedBotToken != "new-..." {
		t.Fatalf("status = %#v, want configured guild and masked token", status)
	}
	status.AllowedUserIDs[0] = "mutated"
	if bridge.Status().AllowedUserIDs[0] != "user-1" {
		t.Fatal("AllowedUserIDs was not defensively copied")
	}
	if err := bridge.Stop(); err != nil {
		t.Fatal(err)
	}
	if bridge.Status().Connected {
		t.Fatal("Connected = true after Stop on idle bridge")
	}
}

func TestBridgeStartValidationRecordsStatusError(t *testing.T) {
	bridge := NewBridge(config.DiscordConfig{
		Enabled:  true,
		BotToken: "token",
		GuildID:  "guild",
	})
	err := bridge.Start(context.Background(), "test")
	if err == nil || !strings.Contains(err.Error(), "allowed Discord user") {
		t.Fatalf("Start() error = %v, want allowlist validation error", err)
	}
	status := bridge.Status()
	if status.Error == "" || !strings.Contains(status.Error, "allowed Discord user") {
		t.Fatalf("status error = %q, want validation error", status.Error)
	}
	if status.Connected {
		t.Fatal("Connected = true after failed Start")
	}
}

func TestBridgeSoftSyncReturnsInProgress(t *testing.T) {
	bridge := NewBridge(config.DiscordConfig{})
	bridge.maintSync <- struct{}{}
	defer func() { <-bridge.maintSync }()

	if err := bridge.SoftSync(context.Background()); !errors.Is(err, ErrSyncInProgress) {
		t.Fatalf("SoftSync() error = %v, want ErrSyncInProgress", err)
	}
}

func TestBridgeSyncTaskChannelReturnsInProgressDuringHardSync(t *testing.T) {
	store, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	bridge := NewBridge(config.DiscordConfig{GuildID: "guild-1"})
	bridge.connected = true
	bridge.bot = &Bot{}
	bridge.store = store
	bridge.hardSync.Lock()
	defer bridge.hardSync.Unlock()

	if err := bridge.SyncTaskChannel(context.Background(), "task-1"); !errors.Is(err, ErrSyncInProgress) {
		t.Fatalf("SyncTaskChannel() error = %v, want ErrSyncInProgress", err)
	}
}

func TestRefreshTaskStreamsStartsMappedStructuredTask(t *testing.T) {
	bridge := NewBridge(config.DiscordConfig{})
	bridge.connected = true
	bridge.bot = &Bot{}
	calls := make(chan struct{}, 1)
	events := make(chan agentstream.Event)
	defer close(events)
	bridge.service = &fakeCommandService{tasks: []TaskSummary{{
		ID:              "task-1",
		ChannelID:       "channel-1",
		Status:          "active",
		Agent:           "codex",
		AgentThreadID:   stringPtr("thread-1"),
		AgentStreamKind: stringPtr("codex-app-server"),
	}}}
	bridge.events = scriptedAgentEvents{events: events, calls: calls}

	bridge.RefreshTaskStreams(context.Background())

	select {
	case <-calls:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for task stream subscription")
	}
	bridge.mu.Lock()
	if _, ok := bridge.streams["task-1"]; !ok {
		t.Fatal("task stream was not registered")
	}
	bridge.cancelTaskStreamsLocked()
	bridge.mu.Unlock()
}

func stringPtr(value string) *string {
	return &value
}

type scriptedAgentEvents struct {
	events chan agentstream.Event
	calls  chan struct{}
}

func (s scriptedAgentEvents) SubscribeAgentEvents(context.Context, TaskSummary) (<-chan agentstream.Event, error) {
	if s.calls != nil {
		s.calls <- struct{}{}
	}
	return s.events, nil
}

type channelMessageSender struct {
	messages chan string
}

func (s channelMessageSender) SendMessage(ctx context.Context, channelID, content string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case s.messages <- content:
		return nil
	}
}

func TestStartTaskStreamForwardsSemanticEvents(t *testing.T) {
	bridge := NewBridge(config.DiscordConfig{})
	bridge.connected = true
	sender := channelMessageSender{messages: make(chan string, 1)}
	events := make(chan agentstream.Event, 1)
	events <- agentstream.Event{TaskID: "task-1", Kind: agentstream.EventAssistantMessage, Text: "done"}
	close(events)

	bridge.startTaskStream(nil, scriptedAgentEvents{events: events}, sender, TaskSummary{
		ID:              "task-1",
		ChannelID:       "channel-1",
		Status:          "active",
		Agent:           "codex",
		AgentThreadID:   stringPtr("thread-1"),
		AgentStreamKind: stringPtr("codex-app-server"),
	})

	select {
	case message := <-sender.messages:
		if message != "done" {
			t.Fatalf("message = %q, want semantic assistant output", message)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for semantic output")
	}
}

func TestStartTaskStreamDoesNotDuplicateSameTaskChannel(t *testing.T) {
	bridge := NewBridge(config.DiscordConfig{})
	bridge.connected = true
	sender := channelMessageSender{messages: make(chan string, 1)}
	events := make(chan agentstream.Event)
	calls := make(chan struct{}, 2)
	task := TaskSummary{
		ID:              "task-1",
		ChannelID:       "channel-1",
		Status:          "active",
		Agent:           "codex",
		AgentThreadID:   stringPtr("thread-1"),
		AgentStreamKind: stringPtr("codex-app-server"),
	}

	bridge.startTaskStream(nil, scriptedAgentEvents{events: events, calls: calls}, sender, task)
	bridge.startTaskStream(nil, scriptedAgentEvents{events: events, calls: calls}, sender, task)

	if got := len(calls); got != 1 {
		t.Fatalf("SubscribeAgentEvents calls = %d, want 1", got)
	}
	bridge.mu.Lock()
	bridge.cancelTaskStreamsLocked()
	bridge.mu.Unlock()
}
