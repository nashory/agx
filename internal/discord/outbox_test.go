package discord

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nashory/agx/internal/config"
	"github.com/nashory/agx/internal/db"
)

func TestDiscordOutboxRetriesDurableSemanticMessage(t *testing.T) {
	store, taskID := openDiscordOutboxTestStore(t)
	bridge := NewBridge(config.DiscordConfig{})
	bridge.SetStore(store)
	actions := []RenderAction{{Kind: RenderSend, Content: "final answer"}}
	if err := bridge.QueueRenderActions(context.Background(), taskID, "channel-1", "event-1", actions); err != nil {
		t.Fatal(err)
	}
	if err := bridge.QueueRenderActions(context.Background(), taskID, "channel-1", "event-1", actions); err != nil {
		t.Fatal(err)
	}
	pending, err := store.ListPendingDiscordDeliveries(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending = %#v, want one deduplicated delivery", pending)
	}

	previousDelay := semanticSendRetryDelay
	semanticSendRetryDelay = time.Millisecond
	t.Cleanup(func() { semanticSendRetryDelay = previousDelay })
	sendErr := errors.New("discord unavailable")
	sender := &flakySemanticSender{failures: semanticSendRetryAttempts, err: sendErr}
	if delivered, err := deliverDiscordOutboxOnce(context.Background(), store, sender); !errors.Is(err, sendErr) || delivered != 0 {
		t.Fatalf("first delivery = (%d, %v), want (0, %v)", delivered, err, sendErr)
	}
	failed, err := store.DiscordDelivery(pending[0].DeliveryKey)
	if err != nil {
		t.Fatal(err)
	}
	if failed.Attempts != 1 || failed.DeliveredAt != nil {
		t.Fatalf("failed delivery = %#v", failed)
	}

	if delivered, err := deliverDiscordOutboxOnce(context.Background(), store, sender); err != nil || delivered != 1 {
		t.Fatalf("retry delivery = (%d, %v), want (1, nil)", delivered, err)
	}
	if len(sender.messages) != 1 || sender.messages[0] != "final answer" {
		t.Fatalf("messages = %#v, want final answer", sender.messages)
	}
}

func TestDiscordOutboxFailureDoesNotBlockOtherChannels(t *testing.T) {
	store, taskID := openDiscordOutboxTestStore(t)
	deliveries := []db.DiscordDelivery{
		{DeliveryKey: "first", TaskID: taskID, ChannelID: "blocked", Kind: db.DiscordDeliveryMessage, Content: "blocked message"},
		{DeliveryKey: "second", TaskID: taskID, ChannelID: "healthy", Kind: db.DiscordDeliveryMessage, Content: "healthy message"},
	}
	if err := store.EnqueueDiscordDeliveries(deliveries); err != nil {
		t.Fatal(err)
	}
	sender := channelSelectiveSender{failedChannel: "blocked", messages: make(chan string, 1)}
	previousDelay := semanticSendRetryDelay
	semanticSendRetryDelay = time.Millisecond
	t.Cleanup(func() { semanticSendRetryDelay = previousDelay })
	if delivered, err := deliverDiscordOutboxOnce(context.Background(), store, sender); err == nil || delivered != 1 {
		t.Fatalf("delivery = (%d, %v), want one success and one error", delivered, err)
	}
	select {
	case message := <-sender.messages:
		if message != "healthy message" {
			t.Fatalf("message = %q, want healthy message", message)
		}
	default:
		t.Fatal("healthy channel was blocked by another channel's failure")
	}
}

func TestDiscordOutboxPreservesChunkOrder(t *testing.T) {
	store, taskID := openDiscordOutboxTestStore(t)
	bridge := NewBridge(config.DiscordConfig{})
	bridge.SetStore(store)
	actions := []RenderAction{
		{Kind: RenderSend, Content: "first chunk"},
		{Kind: RenderSend, Content: "second chunk"},
	}
	if err := bridge.QueueRenderActions(context.Background(), taskID, "channel-1", "event-1", actions); err != nil {
		t.Fatal(err)
	}
	sender := &recordingSemanticSender{}
	if delivered, err := deliverDiscordOutboxOnce(context.Background(), store, sender); err != nil || delivered != 2 {
		t.Fatalf("delivery = (%d, %v), want (2, nil)", delivered, err)
	}
	if len(sender.messages) != 2 || sender.messages[0] != "first chunk" || sender.messages[1] != "second chunk" {
		t.Fatalf("messages = %#v, want chunks in order", sender.messages)
	}
}

type channelSelectiveSender struct {
	failedChannel string
	messages      chan string
}

func (s channelSelectiveSender) SendMessage(ctx context.Context, channelID, content string) error {
	if channelID == s.failedChannel {
		return errors.New("channel unavailable")
	}
	s.messages <- content
	return nil
}

func openDiscordOutboxTestStore(t *testing.T) (*db.Store, string) {
	t.Helper()
	store, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	project, err := store.EnsureProject(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTask(project.ID, "task", nil, "codex", db.StatusActive)
	if err != nil {
		t.Fatal(err)
	}
	return store, task.ID
}
