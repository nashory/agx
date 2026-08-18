package db

import (
	"errors"
	"testing"
)

func TestDiscordOutboxPersistsAndDeduplicatesDeliveries(t *testing.T) {
	store, err := OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	project, err := store.EnsureProject(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTask(project.ID, "task", nil, "codex", StatusActive)
	if err != nil {
		t.Fatal(err)
	}
	delivery := DiscordDelivery{DeliveryKey: "delivery-1", TaskID: task.ID, ChannelID: "channel-1", Kind: DiscordDeliveryMessage, Content: "done"}
	if err := store.EnqueueDiscordDeliveries([]DiscordDelivery{delivery, delivery}); err != nil {
		t.Fatal(err)
	}
	pending, err := store.ListPendingDiscordDeliveries(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].Content != "done" {
		t.Fatalf("pending = %#v, want one delivery", pending)
	}
	sendErr := errors.New("discord unavailable")
	if err := store.MarkDiscordDeliveryFailed(delivery.DeliveryKey, sendErr); err != nil {
		t.Fatal(err)
	}
	failed, err := store.DiscordDelivery(delivery.DeliveryKey)
	if err != nil {
		t.Fatal(err)
	}
	if failed.Attempts != 1 || failed.LastError == nil || *failed.LastError != sendErr.Error() || failed.DeliveredAt != nil {
		t.Fatalf("failed delivery = %#v", failed)
	}
	if err := store.MarkDiscordDeliveryDelivered(delivery.DeliveryKey); err != nil {
		t.Fatal(err)
	}
	delivered, err := store.DiscordDelivery(delivery.DeliveryKey)
	if err != nil {
		t.Fatal(err)
	}
	if delivered.Attempts != 2 || delivered.DeliveredAt == nil || delivered.LastError != nil {
		t.Fatalf("delivered = %#v", delivered)
	}
	pending, err = store.ListPendingDiscordDeliveries(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending = %#v, want none", pending)
	}
}
