package db

import (
	"errors"
	"testing"
)

func TestDiscordMappingUpsertAndLookup(t *testing.T) {
	store, err := OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	first, err := store.UpsertDiscordMapping(DiscordAGXTask, "task-1", DiscordTypeChannel, "channel-1")
	if err != nil {
		t.Fatal(err)
	}
	if first.AGXID != "task-1" || first.DiscordID != "channel-1" {
		t.Fatalf("first mapping = %#v", first)
	}

	second, err := store.UpsertDiscordMapping(DiscordAGXTask, "task-1", DiscordTypeChannel, "channel-2")
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID {
		t.Fatalf("upsert changed mapping id: got %q want %q", second.ID, first.ID)
	}
	if second.DiscordID != "channel-2" {
		t.Fatalf("DiscordID = %q, want channel-2", second.DiscordID)
	}

	byDiscord, err := store.GetDiscordMappingByDiscordID("channel-2")
	if err != nil {
		t.Fatal(err)
	}
	if byDiscord.AGXID != "task-1" {
		t.Fatalf("AGXID = %q, want task-1", byDiscord.AGXID)
	}
}

func TestDiscordMappingUpsertReclaimsDiscordID(t *testing.T) {
	store, err := OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	stale, err := store.UpsertDiscordMapping(DiscordAGXProject, "old-project", DiscordTypeCategory, "category-1")
	if err != nil {
		t.Fatal(err)
	}
	current, err := store.UpsertDiscordMapping(DiscordAGXProject, "new-project", DiscordTypeCategory, "category-1")
	if err != nil {
		t.Fatal(err)
	}
	if current.AGXID != "new-project" || current.DiscordID != stale.DiscordID {
		t.Fatalf("current mapping = %#v, want new project to reclaim category-1", current)
	}
	if _, err := store.GetDiscordMapping(DiscordAGXProject, "old-project"); !errors.Is(err, ErrDiscordMappingNotFound) {
		t.Fatalf("old mapping error = %v, want ErrDiscordMappingNotFound", err)
	}
	byDiscord, err := store.GetDiscordMappingByDiscordID("category-1")
	if err != nil {
		t.Fatal(err)
	}
	if byDiscord.AGXID != "new-project" {
		t.Fatalf("AGXID by discord = %q, want new-project", byDiscord.AGXID)
	}
}

func TestDeleteDiscordMapping(t *testing.T) {
	store, err := OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if _, err := store.UpsertDiscordMapping(DiscordAGXProject, "project-1", DiscordTypeCategory, "category-1"); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteDiscordMapping(DiscordAGXProject, "project-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetDiscordMapping(DiscordAGXProject, "project-1"); !errors.Is(err, ErrDiscordMappingNotFound) {
		t.Fatalf("GetDiscordMapping error = %v, want ErrDiscordMappingNotFound", err)
	}
}

func TestDiscordTaskSyncStateTracksAttempts(t *testing.T) {
	store, err := OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	project, err := store.EnsureProject(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTaskRuntimeModeInterface(NewTaskID(), project.ID, "discord task", nil, "codex", false, TaskInterfaceDiscord, StatusWaiting, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	pending, err := store.UpsertDiscordTaskSyncPending(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if pending.Status != DiscordTaskSyncPending || pending.Attempts != 1 {
		t.Fatalf("pending state = %#v, want pending attempt 1", pending)
	}
	failed, err := store.MarkDiscordTaskSyncFailure(task.ID, errors.New("timeout"))
	if err != nil {
		t.Fatal(err)
	}
	if failed.Status != DiscordTaskSyncFailed || failed.Attempts != 1 || failed.LastError == nil || *failed.LastError != "timeout" {
		t.Fatalf("failed state = %#v, want failed attempt 1 with error", failed)
	}
	synced, err := store.MarkDiscordTaskSyncSuccess(task.ID, "channel-1")
	if err != nil {
		t.Fatal(err)
	}
	if synced.Status != DiscordTaskSyncSynced || synced.DiscordChannelID == nil || *synced.DiscordChannelID != "channel-1" || synced.LastError != nil {
		t.Fatalf("synced state = %#v, want channel and cleared error", synced)
	}
}

func TestDiscordTaskSyncStateBackfillsExistingMappings(t *testing.T) {
	store, err := OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	project, err := store.EnsureProject(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTaskRuntimeModeInterface(NewTaskID(), project.ID, "discord task", nil, "codex", false, TaskInterfaceDiscord, StatusWaiting, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertDiscordMapping(DiscordAGXTask, task.ID, DiscordTypeChannel, "channel-1"); err != nil {
		t.Fatal(err)
	}
	if err := store.backfillDiscordTaskSyncState(); err != nil {
		t.Fatal(err)
	}

	state, err := store.GetDiscordTaskSyncState(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != DiscordTaskSyncSynced || state.DiscordChannelID == nil || *state.DiscordChannelID != "channel-1" {
		t.Fatalf("backfilled state = %#v, want synced channel mapping", state)
	}
}

func TestResetAllClearsDiscordMappings(t *testing.T) {
	store, err := OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if _, err := store.UpsertDiscordMapping(DiscordAGXTask, "task-1", DiscordTypeChannel, "channel-1"); err != nil {
		t.Fatal(err)
	}
	if err := store.ResetAll(); err != nil {
		t.Fatal(err)
	}
	mappings, err := store.ListDiscordMappings()
	if err != nil {
		t.Fatal(err)
	}
	if len(mappings) != 0 {
		t.Fatalf("len(mappings) = %d, want 0", len(mappings))
	}
}

func TestDiscordMessageReservation(t *testing.T) {
	store, err := OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	project, err := store.EnsureProject(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTaskRuntimeModeInterface(NewTaskID(), project.ID, "discord task", nil, "claude", false, TaskInterfaceDiscord, StatusActive, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	reserved, err := store.ReserveDiscordMessage(task.ID, "message-1")
	if err != nil {
		t.Fatal(err)
	}
	if !reserved {
		t.Fatal("first ReserveDiscordMessage() = false, want true")
	}
	reserved, err = store.ReserveDiscordMessage(task.ID, "message-1")
	if err != nil {
		t.Fatal(err)
	}
	if reserved {
		t.Fatal("duplicate ReserveDiscordMessage() = true, want false")
	}
	if err := store.MarkDiscordMessageDelivered(task.ID, "message-1"); err != nil {
		t.Fatal(err)
	}
	status, err := store.DiscordMessageStatus(task.ID, "message-1")
	if err != nil {
		t.Fatal(err)
	}
	if status != DiscordMessageDelivered {
		t.Fatalf("status = %q, want delivered", status)
	}
}

func TestDiscordMessageReservationCanBeReleasedForRetry(t *testing.T) {
	store, err := OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	project, err := store.EnsureProject(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTaskRuntimeModeInterface(NewTaskID(), project.ID, "discord task", nil, "claude", false, TaskInterfaceDiscord, StatusActive, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if reserved, err := store.ReserveDiscordMessage(task.ID, "message-1"); err != nil || !reserved {
		t.Fatalf("ReserveDiscordMessage() = %v, %v; want true, nil", reserved, err)
	}
	if err := store.DeleteDiscordMessageReservation(task.ID, "message-1"); err != nil {
		t.Fatal(err)
	}
	if reserved, err := store.ReserveDiscordMessage(task.ID, "message-1"); err != nil || !reserved {
		t.Fatalf("ReserveDiscordMessage() after release = %v, %v; want true, nil", reserved, err)
	}
}

func TestDiscordMessageReservationFailedStatusBlocksDuplicate(t *testing.T) {
	store, err := OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	project, err := store.EnsureProject(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTaskRuntimeModeInterface(NewTaskID(), project.ID, "discord task", nil, "claude", false, TaskInterfaceDiscord, StatusActive, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if reserved, err := store.ReserveDiscordMessage(task.ID, "message-1"); err != nil || !reserved {
		t.Fatalf("ReserveDiscordMessage() = %v, %v; want true, nil", reserved, err)
	}
	if err := store.MarkDiscordMessageFailed(task.ID, "message-1", errors.New("ambiguous delivery")); err != nil {
		t.Fatal(err)
	}
	status, err := store.DiscordMessageStatus(task.ID, "message-1")
	if err != nil {
		t.Fatal(err)
	}
	if status != DiscordMessageFailed {
		t.Fatalf("status = %q, want failed", status)
	}
	if reserved, err := store.ReserveDiscordMessage(task.ID, "message-1"); err != nil || reserved {
		t.Fatalf("ReserveDiscordMessage() after failed = %v, %v; want false, nil", reserved, err)
	}
	if err := store.DeleteDiscordMessageReservation(task.ID, "message-1"); err != nil {
		t.Fatal(err)
	}
	if reserved, err := store.ReserveDiscordMessage(task.ID, "message-1"); err != nil || reserved {
		t.Fatalf("ReserveDiscordMessage() after deleting failed = %v, %v; want false, nil", reserved, err)
	}
}

func TestDiscordMessageReservationResetAndCascade(t *testing.T) {
	store, err := OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	project, err := store.EnsureProject(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTaskRuntimeModeInterface(NewTaskID(), project.ID, "discord task", nil, "claude", false, TaskInterfaceDiscord, StatusActive, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if reserved, err := store.ReserveDiscordMessage(task.ID, "message-1"); err != nil || !reserved {
		t.Fatalf("ReserveDiscordMessage() = %v, %v; want true, nil", reserved, err)
	}
	if err := store.DeleteTask(task.ID); err != nil {
		t.Fatal(err)
	}
	if reserved, err := store.ReserveDiscordMessage(task.ID, "message-1"); err == nil || reserved {
		t.Fatalf("ReserveDiscordMessage() after task delete = %v, %v; want foreign-key error", reserved, err)
	}

	task, err = store.CreateTaskRuntimeModeInterface(NewTaskID(), project.ID, "discord task 2", nil, "claude", false, TaskInterfaceDiscord, StatusActive, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if reserved, err := store.ReserveDiscordMessage(task.ID, "message-2"); err != nil || !reserved {
		t.Fatalf("ReserveDiscordMessage() second = %v, %v; want true, nil", reserved, err)
	}
	if err := store.ResetAll(); err != nil {
		t.Fatal(err)
	}
	project, err = store.EnsureProject(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err = store.CreateTaskRuntimeModeInterface(NewTaskID(), project.ID, "discord task 3", nil, "claude", false, TaskInterfaceDiscord, StatusActive, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if reserved, err := store.ReserveDiscordMessage(task.ID, "message-2"); err != nil || !reserved {
		t.Fatalf("ReserveDiscordMessage() after ResetAll = %v, %v; want true, nil", reserved, err)
	}
}
