package discord

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nashory/agx/internal/db"
	"github.com/nashory/agx/internal/display"
)

type fakeSyncClient struct {
	control             string
	category            map[string]string
	text                map[string]string
	topics              map[string]string
	names               map[string]string
	extraChannels       []GuildChannel
	deleted             []string
	ensureCategoryCalls int
	ensureTextCalls     int
	createCategoryCalls int
	createTextCalls     int
	permissionControl   string
	permissionTasks     []string
	ensureTextErr       error
	invalidCategoryIDs  map[string]error
	deleteErrs          map[string]error
}

func newFakeSyncClient() *fakeSyncClient {
	return &fakeSyncClient{
		category: map[string]string{},
		text:     map[string]string{},
		topics:   map[string]string{},
		names:    map[string]string{},
	}
}

func (f *fakeSyncClient) EnsureControlChannel(ctx context.Context, guildID, name string) (string, error) {
	f.control = name
	return "control-1", nil
}

func (f *fakeSyncClient) EnsureCategory(ctx context.Context, guildID, name string) (string, error) {
	f.ensureCategoryCalls++
	if id := f.category[name]; id != "" {
		return id, nil
	}
	id := "category-" + name
	f.category[name] = id
	return id, nil
}

func (f *fakeSyncClient) EnsureTextChannel(ctx context.Context, guildID, categoryID, name, topic string) (string, error) {
	f.ensureTextCalls++
	if f.ensureTextErr != nil {
		return "", f.ensureTextErr
	}
	return f.createTextChannel(categoryID, name, topic, "channel-")
}

func (f *fakeSyncClient) CreateCategory(ctx context.Context, guildID, name string) (string, error) {
	f.createCategoryCalls++
	id := "direct-category-" + name
	f.category[name] = id
	return id, nil
}

func (f *fakeSyncClient) CreateTextChannel(ctx context.Context, guildID, categoryID, name, topic string) (string, error) {
	f.createTextCalls++
	return f.createTextChannel(categoryID, name, topic, "direct-channel-")
}

func (f *fakeSyncClient) createTextChannel(categoryID, name, topic, prefix string) (string, error) {
	if f.invalidCategoryIDs != nil {
		if err := f.invalidCategoryIDs[categoryID]; err != nil {
			return "", err
		}
	}
	key := categoryID + "/" + name
	if id := f.text[key]; id != "" {
		f.topics[id] = topic
		return id, nil
	}
	id := prefix + name
	f.text[key] = id
	f.topics[id] = topic
	f.names[id] = name
	return id, nil
}

func (f *fakeSyncClient) UpdateChannelTopic(ctx context.Context, channelID, topic string) error {
	f.topics[channelID] = topic
	return nil
}

func (f *fakeSyncClient) UpdateTextChannel(ctx context.Context, channelID, name, topic string) error {
	if _, ok := f.names[channelID]; !ok {
		return errors.New("missing channel")
	}
	f.names[channelID] = name
	f.topics[channelID] = topic
	return nil
}

func (f *fakeSyncClient) DeleteChannel(ctx context.Context, channelID string) error {
	if f.deleteErrs != nil {
		if err := f.deleteErrs[channelID]; err != nil {
			return err
		}
	}
	f.deleted = append(f.deleted, channelID)
	for name, id := range f.category {
		if id == channelID {
			delete(f.category, name)
		}
	}
	for key, id := range f.text {
		if id == channelID {
			delete(f.text, key)
		}
	}
	delete(f.topics, channelID)
	delete(f.names, channelID)
	out := f.extraChannels[:0]
	for _, channel := range f.extraChannels {
		if channel.ID != channelID {
			out = append(out, channel)
		}
	}
	f.extraChannels = out
	return nil
}

func (f *fakeSyncClient) ListGuildChannels(ctx context.Context, guildID string) ([]GuildChannel, error) {
	out := make([]GuildChannel, 0, len(f.category)+len(f.text)+len(f.extraChannels))
	for name, id := range f.category {
		out = append(out, GuildChannel{ID: id, Name: name, Type: GuildChannelCategory})
	}
	for key, id := range f.text {
		parentID := ""
		name := key
		if parts := strings.SplitN(key, "/", 2); len(parts) == 2 {
			parentID = parts[0]
			name = parts[1]
		}
		out = append(out, GuildChannel{ID: id, Name: name, ParentID: parentID, Type: GuildChannelText})
	}
	out = append(out, f.extraChannels...)
	return out, nil
}

func (f *fakeSyncClient) ConfigureCommandPermissions(ctx context.Context, guildID, controlChannelID string, taskChannelIDs []string) error {
	f.permissionControl = controlChannelID
	f.permissionTasks = append([]string(nil), taskChannelIDs...)
	return nil
}

func TestSyncActiveTasksCreatesMappings(t *testing.T) {
	store, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	project, err := store.EnsureProjectDetails(t.TempDir(), "My App", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTaskRuntimeModeInterface(db.NewTaskID(), project.ID, "active task", nil, "claude", false, db.TaskInterfaceDiscord, db.StatusActive, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateTaskRuntimeModeInterface(db.NewTaskID(), project.ID, "complete task", nil, "claude", false, db.TaskInterfaceDiscord, db.StatusComplete, nil, nil, nil); err != nil {
		t.Fatal(err)
	}

	client := newFakeSyncClient()
	if err := NewSyncer(store, client, "guild-1").SyncActiveTasks(context.Background()); err != nil {
		t.Fatal(err)
	}
	if client.control != controlChannelName {
		t.Fatalf("control = %q, want %q", client.control, controlChannelName)
	}
	mappings, err := store.ListDiscordMappings()
	if err != nil {
		t.Fatal(err)
	}
	if len(mappings) != 3 {
		t.Fatalf("len(mappings) = %d, want control + project + active task mappings", len(mappings))
	}
	if _, err := store.GetDiscordMapping(db.DiscordAGXControl, db.DiscordControlAGXID); err != nil {
		t.Fatalf("control mapping error = %v", err)
	}
	taskMapping, err := store.GetDiscordMapping(db.DiscordAGXTask, task.ID)
	if err != nil {
		t.Fatalf("task mapping error = %v", err)
	}
	topic := client.topics[taskMapping.DiscordID]
	for _, expected := range []string{"Project: My App", "Task: active task", "Agent: claude", "Mode: standard", "Workspace: worktree", task.ID} {
		if !strings.Contains(topic, expected) {
			t.Fatalf("topic = %q, missing %q", topic, expected)
		}
	}
	if strings.Contains(topic, "Status:") {
		t.Fatalf("topic = %q, should not include status", topic)
	}
}

func TestSyncActiveTasksConfiguresCommandPermissions(t *testing.T) {
	store, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	project, err := store.EnsureProjectDetails(t.TempDir(), "My App", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.CreateTaskRuntimeModeInterface(db.NewTaskID(), project.ID, "first task", nil, "claude", false, db.TaskInterfaceDiscord, db.StatusActive, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateTaskRuntimeModeInterface(db.NewTaskID(), project.ID, "second task", nil, "claude", false, db.TaskInterfaceDiscord, db.StatusWaiting, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	client := newFakeSyncClient()
	if err := NewSyncer(store, client, "guild-1").SyncActiveTasks(context.Background()); err != nil {
		t.Fatal(err)
	}
	if client.permissionControl != "control-1" {
		t.Fatalf("permission control = %q, want control-1", client.permissionControl)
	}
	if len(client.permissionTasks) != 2 {
		t.Fatalf("permission task channels = %#v, want two mirrored task channels", client.permissionTasks)
	}
	for _, task := range []db.Task{first, second} {
		mapping, err := store.GetDiscordMapping(db.DiscordAGXTask, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !containsString(client.permissionTasks, mapping.DiscordID) {
			t.Fatalf("permission task channels = %#v, missing %s", client.permissionTasks, mapping.DiscordID)
		}
	}
}

func TestSyncActiveTasksIgnoresLocalTasks(t *testing.T) {
	store, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	project, err := store.EnsureProjectDetails(t.TempDir(), "My App", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	localTask, err := store.CreateTask(project.ID, "local task", nil, "claude", db.StatusActive)
	if err != nil {
		t.Fatal(err)
	}

	client := newFakeSyncClient()
	if err := NewSyncer(store, client, "guild-1").SyncActiveTasks(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetDiscordMapping(db.DiscordAGXTask, localTask.ID); !errors.Is(err, db.ErrDiscordMappingNotFound) {
		t.Fatalf("local task mapping error = %v, want ErrDiscordMappingNotFound", err)
	}
	if len(client.names) != 0 {
		t.Fatalf("created task channels = %v, want none", client.names)
	}
	if len(client.category) != 0 {
		t.Fatalf("created categories = %v, want none for local-only project", client.category)
	}
}

func TestSyncActiveTasksRenamesMappedTaskChannel(t *testing.T) {
	store, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	project, err := store.EnsureProjectDetails(t.TempDir(), "My App", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTaskRuntimeModeInterface(db.NewTaskID(), project.ID, "old task", nil, "claude", false, db.TaskInterfaceDiscord, db.StatusActive, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	client := newFakeSyncClient()
	syncer := NewSyncer(store, client, "guild-1")
	if err := syncer.SyncActiveTasks(context.Background()); err != nil {
		t.Fatal(err)
	}
	mapping, err := store.GetDiscordMapping(db.DiscordAGXTask, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	title := "new task"
	if err := store.UpdateTask(task.ID, &title, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := syncer.SyncActiveTasks(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, want := client.names[mapping.DiscordID], "new task-"+display.ShortID(task.ID); got != want {
		t.Fatalf("channel name = %q, want %q", got, want)
	}
	if topic := client.topics[mapping.DiscordID]; !strings.Contains(topic, "Task: new task") {
		t.Fatalf("topic = %q, want renamed task", topic)
	}
}

func TestSyncActiveTasksRecreatesMissingMappedTaskChannel(t *testing.T) {
	store, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	project, err := store.EnsureProjectDetails(t.TempDir(), "My App", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTaskRuntimeModeInterface(db.NewTaskID(), project.ID, "active task", nil, "claude", false, db.TaskInterfaceDiscord, db.StatusActive, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertDiscordMapping(db.DiscordAGXTask, task.ID, db.DiscordTypeChannel, "missing-channel"); err != nil {
		t.Fatal(err)
	}

	client := newFakeSyncClient()
	if err := NewSyncer(store, client, "guild-1").SyncActiveTasks(context.Background()); err != nil {
		t.Fatal(err)
	}
	mapping, err := store.GetDiscordMapping(db.DiscordAGXTask, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if mapping.DiscordID == "missing-channel" {
		t.Fatalf("mapping DiscordID = %q, want replacement channel", mapping.DiscordID)
	}
	if client.names[mapping.DiscordID] != TaskChannelName(task) {
		t.Fatalf("created channel name = %q, want %q", client.names[mapping.DiscordID], TaskChannelName(task))
	}
}

func TestSyncTaskChannelCreatesOnlyRequestedTaskChannel(t *testing.T) {
	store, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	project, err := store.EnsureProjectDetails(t.TempDir(), "My App", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTaskRuntimeModeInterface(db.NewTaskID(), project.ID, "active task", nil, "claude", false, db.TaskInterfaceDiscord, db.StatusActive, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateTaskRuntimeModeInterface(db.NewTaskID(), project.ID, "other task", nil, "claude", false, db.TaskInterfaceDiscord, db.StatusActive, nil, nil, nil); err != nil {
		t.Fatal(err)
	}

	client := newFakeSyncClient()
	if err := NewSyncer(store, client, "guild-1").SyncTaskChannel(context.Background(), task.ID); err != nil {
		t.Fatal(err)
	}
	if client.ensureTextCalls != 1 {
		t.Fatalf("ensure text calls = %d, want requested task channel only", client.ensureTextCalls)
	}
	if _, err := store.GetDiscordMapping(db.DiscordAGXTask, task.ID); err != nil {
		t.Fatalf("requested task mapping error = %v", err)
	}
	state, err := store.GetDiscordTaskSyncState(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != db.DiscordTaskSyncSynced || state.DiscordChannelID == nil {
		t.Fatalf("sync state = %#v, want synced channel", state)
	}
	if client.control != controlChannelName {
		t.Fatalf("control = %q, want %q", client.control, controlChannelName)
	}
	if len(client.names) != 1 {
		t.Fatalf("created channels = %#v, want requested task channel only", client.names)
	}
}

func TestSyncTaskChannelFastSkipsControlAndPermissions(t *testing.T) {
	store, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	project, err := store.EnsureProjectDetails(t.TempDir(), "My App", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTaskRuntimeModeInterface(db.NewTaskID(), project.ID, "active task", nil, "claude", false, db.TaskInterfaceDiscord, db.StatusActive, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	client := newFakeSyncClient()
	if err := NewSyncer(store, client, "guild-1").SyncTaskChannelFast(context.Background(), task.ID); err != nil {
		t.Fatal(err)
	}
	if client.control != "" {
		t.Fatalf("control = %q, want no control channel work on fast path", client.control)
	}
	if client.permissionControl != "" || len(client.permissionTasks) != 0 {
		t.Fatalf("permissions = %q %#v, want deferred permission refresh", client.permissionControl, client.permissionTasks)
	}
	if client.ensureTextCalls != 1 {
		t.Fatalf("ensure text calls = %d, want one task channel", client.ensureTextCalls)
	}
	if _, err := store.GetDiscordMapping(db.DiscordAGXTask, task.ID); err != nil {
		t.Fatalf("requested task mapping error = %v", err)
	}
}

func TestSyncTaskChannelFastReusesProjectMappingAndRepairsStaleCategory(t *testing.T) {
	store, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	project, err := store.EnsureProjectDetails(t.TempDir(), "My App", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTaskRuntimeModeInterface(db.NewTaskID(), project.ID, "active task", nil, "claude", false, db.TaskInterfaceDiscord, db.StatusActive, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertDiscordMapping(db.DiscordAGXProject, project.ID, db.DiscordTypeCategory, "stale-category"); err != nil {
		t.Fatal(err)
	}

	client := newFakeSyncClient()
	client.invalidCategoryIDs = map[string]error{"stale-category": errors.New("unknown parent")}
	if err := NewSyncer(store, client, "guild-1").SyncTaskChannelFast(context.Background(), task.ID); err != nil {
		t.Fatal(err)
	}
	mapping, err := store.GetDiscordMapping(db.DiscordAGXProject, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if mapping.DiscordID == "stale-category" {
		t.Fatalf("project category mapping = %q, want repaired category", mapping.DiscordID)
	}
	taskMapping, err := store.GetDiscordMapping(db.DiscordAGXTask, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if client.names[taskMapping.DiscordID] != TaskChannelName(task) {
		t.Fatalf("task channel name = %q, want %q", client.names[taskMapping.DiscordID], TaskChannelName(task))
	}
}

func TestRefreshCommandPermissionsUsesMappedTaskChannels(t *testing.T) {
	store, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	project, err := store.EnsureProjectDetails(t.TempDir(), "My App", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.CreateTaskRuntimeModeInterface(db.NewTaskID(), project.ID, "first", nil, "claude", false, db.TaskInterfaceDiscord, db.StatusActive, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateTaskRuntimeModeInterface(db.NewTaskID(), project.ID, "second", nil, "claude", false, db.TaskInterfaceDiscord, db.StatusWaiting, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertDiscordMapping(db.DiscordAGXTask, first.ID, db.DiscordTypeChannel, "channel-first"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertDiscordMapping(db.DiscordAGXTask, second.ID, db.DiscordTypeChannel, "channel-second"); err != nil {
		t.Fatal(err)
	}

	client := newFakeSyncClient()
	if err := NewSyncer(store, client, "guild-1").RefreshCommandPermissions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if client.permissionControl != "control-1" {
		t.Fatalf("permission control = %q, want control-1", client.permissionControl)
	}
	for _, channelID := range []string{"channel-first", "channel-second"} {
		if !containsString(client.permissionTasks, channelID) {
			t.Fatalf("permission task channels = %#v, missing %s", client.permissionTasks, channelID)
		}
	}
}

func TestSyncTaskChannelRecordsFailureState(t *testing.T) {
	store, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	project, err := store.EnsureProjectDetails(t.TempDir(), "My App", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTaskRuntimeModeInterface(db.NewTaskID(), project.ID, "active task", nil, "claude", false, db.TaskInterfaceDiscord, db.StatusActive, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	client := newFakeSyncClient()
	client.ensureTextErr = errors.New("discord timeout")

	err = NewSyncer(store, client, "guild-1").SyncTaskChannel(context.Background(), task.ID)
	if err == nil || !strings.Contains(err.Error(), "discord timeout") {
		t.Fatalf("SyncTaskChannel error = %v, want discord timeout", err)
	}
	state, err := store.GetDiscordTaskSyncState(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != db.DiscordTaskSyncFailed || state.Attempts != 1 || state.LastError == nil || !strings.Contains(*state.LastError, "discord timeout") {
		t.Fatalf("sync state = %#v, want failed timeout state", state)
	}
}

func TestSyncTaskChannelRetriesAfterFailureAndReusesSingleChannel(t *testing.T) {
	store, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	project, err := store.EnsureProjectDetails(t.TempDir(), "My App", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTaskRuntimeModeInterface(db.NewTaskID(), project.ID, "active task", nil, "claude", false, db.TaskInterfaceDiscord, db.StatusActive, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	client := newFakeSyncClient()
	client.ensureTextErr = errors.New("discord timeout")
	syncer := NewSyncer(store, client, "guild-1")

	if err := syncer.SyncTaskChannel(context.Background(), task.ID); err == nil || !strings.Contains(err.Error(), "discord timeout") {
		t.Fatalf("first SyncTaskChannel error = %v, want discord timeout", err)
	}
	failed, err := store.GetDiscordTaskSyncState(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if failed.Status != db.DiscordTaskSyncFailed || failed.Attempts != 1 {
		t.Fatalf("failed sync state = %#v, want failed attempt 1", failed)
	}

	client.ensureTextErr = nil
	if err := syncer.SyncTaskChannel(context.Background(), task.ID); err != nil {
		t.Fatal(err)
	}
	synced, err := store.GetDiscordTaskSyncState(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if synced.Status != db.DiscordTaskSyncSynced || synced.Attempts != 2 || synced.LastError != nil || synced.DiscordChannelID == nil {
		t.Fatalf("synced state = %#v, want recovered sync with cleared error and two attempts", synced)
	}
	if client.ensureTextCalls != 2 {
		t.Fatalf("ensure text calls = %d, want one failed call and one retry", client.ensureTextCalls)
	}
	if len(client.names) != 1 {
		t.Fatalf("created channels = %#v, want exactly one recovered task channel", client.names)
	}
	mapping, err := store.GetDiscordMapping(db.DiscordAGXTask, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if mapping.DiscordID != *synced.DiscordChannelID {
		t.Fatalf("mapping channel = %q, sync state channel = %q", mapping.DiscordID, *synced.DiscordChannelID)
	}
}

func TestSyncTaskChannelDeletesChannelForUnmirroredTask(t *testing.T) {
	store, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	project, err := store.EnsureProjectDetails(t.TempDir(), "My App", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTaskRuntimeModeInterface(db.NewTaskID(), project.ID, "complete task", nil, "claude", false, db.TaskInterfaceDiscord, db.StatusComplete, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertDiscordMapping(db.DiscordAGXTask, task.ID, db.DiscordTypeChannel, "channel-complete"); err != nil {
		t.Fatal(err)
	}

	client := newFakeSyncClient()
	if err := NewSyncer(store, client, "guild-1").SyncTaskChannel(context.Background(), task.ID); err != nil {
		t.Fatal(err)
	}
	if len(client.deleted) != 1 || client.deleted[0] != "channel-complete" {
		t.Fatalf("deleted = %#v, want completed task channel deletion", client.deleted)
	}
	if _, err := store.GetDiscordMapping(db.DiscordAGXTask, task.ID); !errors.Is(err, db.ErrDiscordMappingNotFound) {
		t.Fatalf("task mapping error = %v, want ErrDiscordMappingNotFound", err)
	}
}

func TestRebuildSyncCreatesChannelsDirectly(t *testing.T) {
	store, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	project, err := store.EnsureProjectDetails(t.TempDir(), "My App", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateTaskRuntimeModeInterface(db.NewTaskID(), project.ID, "active task", nil, "claude", false, db.TaskInterfaceDiscord, db.StatusActive, nil, nil, nil); err != nil {
		t.Fatal(err)
	}

	client := newFakeSyncClient()
	if err := NewRebuildSyncer(store, client, "guild-1").SyncActiveTasks(context.Background()); err != nil {
		t.Fatal(err)
	}
	if client.ensureCategoryCalls != 0 || client.ensureTextCalls != 0 {
		t.Fatalf("ensure calls = category %d text %d, want direct create path", client.ensureCategoryCalls, client.ensureTextCalls)
	}
	if client.createCategoryCalls != 1 || client.createTextCalls != 1 {
		t.Fatalf("create calls = category %d text %d, want 1 each", client.createCategoryCalls, client.createTextCalls)
	}
}

func TestSyncActiveTasksLeavesUnmirroredTaskChannels(t *testing.T) {
	store, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	project, err := store.EnsureProjectDetails(t.TempDir(), "My App", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTask(project.ID, "complete task", nil, "claude", db.StatusComplete)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertDiscordMapping(db.DiscordAGXTask, task.ID, db.DiscordTypeChannel, "channel-stale"); err != nil {
		t.Fatal(err)
	}

	client := newFakeSyncClient()
	if err := NewSyncer(store, client, "guild-1").SyncActiveTasks(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(client.deleted) != 0 {
		t.Fatalf("deleted = %#v, want soft sync to leave stale task channel", client.deleted)
	}
	if _, err := store.GetDiscordMapping(db.DiscordAGXTask, task.ID); err != nil {
		t.Fatalf("task mapping error = %v, want stale mapping kept", err)
	}
}

func TestSyncActiveTasksWithCleanupDeletesUnmirroredTaskChannels(t *testing.T) {
	store, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	project, err := store.EnsureProjectDetails(t.TempDir(), "My App", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTask(project.ID, "complete task", nil, "claude", db.StatusComplete)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertDiscordMapping(db.DiscordAGXTask, task.ID, db.DiscordTypeChannel, "channel-stale"); err != nil {
		t.Fatal(err)
	}

	client := newFakeSyncClient()
	if err := NewSyncer(store, client, "guild-1").SyncActiveTasksWithCleanup(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if len(client.deleted) != 1 || client.deleted[0] != "channel-stale" {
		t.Fatalf("deleted = %#v, want stale task channel deletion", client.deleted)
	}
	if _, err := store.GetDiscordMapping(db.DiscordAGXTask, task.ID); !errors.Is(err, db.ErrDiscordMappingNotFound) {
		t.Fatalf("task mapping error = %v, want ErrDiscordMappingNotFound", err)
	}
}

func TestSyncActiveTasksWithCleanupPreservesMappingWhenDeleteFails(t *testing.T) {
	store, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	project, err := store.EnsureProjectDetails(t.TempDir(), "My App", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTask(project.ID, "complete task", nil, "claude", db.StatusComplete)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertDiscordMapping(db.DiscordAGXTask, task.ID, db.DiscordTypeChannel, "channel-stale"); err != nil {
		t.Fatal(err)
	}
	deleteErr := errors.New("discord permission denied")
	client := newFakeSyncClient()
	client.deleteErrs = map[string]error{"channel-stale": deleteErr}

	err = NewSyncer(store, client, "guild-1").SyncActiveTasksWithCleanup(context.Background(), true)
	if !errors.Is(err, deleteErr) {
		t.Fatalf("cleanup error = %v, want delete error", err)
	}
	if _, err := store.GetDiscordMapping(db.DiscordAGXTask, task.ID); err != nil {
		t.Fatalf("task mapping error = %v, want mapping preserved after delete failure", err)
	}
	if len(client.deleted) != 0 {
		t.Fatalf("deleted = %#v, want failed delete to stop before recording success", client.deleted)
	}
}

func TestDeleteTaskChannelWithFallbackDeletesCurrentChannelWithoutMapping(t *testing.T) {
	store, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	client := newFakeSyncClient()
	if err := NewSyncer(store, client, "guild-1").DeleteTaskChannelWithFallback(context.Background(), "missing-task", "channel-current"); err != nil {
		t.Fatal(err)
	}
	if len(client.deleted) != 1 || client.deleted[0] != "channel-current" {
		t.Fatalf("deleted = %#v, want channel-current", client.deleted)
	}
}

func TestDeleteTaskChannelWithFallbackPrefersCurrentChannelOverStaleMapping(t *testing.T) {
	store, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	project, err := store.EnsureProjectDetails(t.TempDir(), "My App", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTaskRuntimeModeInterface(db.NewTaskID(), project.ID, "active task", nil, "claude", false, db.TaskInterfaceDiscord, db.StatusActive, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertDiscordMapping(db.DiscordAGXTask, task.ID, db.DiscordTypeChannel, "channel-stale"); err != nil {
		t.Fatal(err)
	}

	client := newFakeSyncClient()
	if err := NewSyncer(store, client, "guild-1").DeleteTaskChannelWithFallback(context.Background(), task.ID, "channel-current"); err != nil {
		t.Fatal(err)
	}
	if len(client.deleted) != 2 || client.deleted[0] != "channel-current" || client.deleted[1] != "channel-stale" {
		t.Fatalf("deleted = %#v, want current channel first and stale mapped channel second", client.deleted)
	}
	if _, err := store.GetDiscordMapping(db.DiscordAGXTask, task.ID); !errors.Is(err, db.ErrDiscordMappingNotFound) {
		t.Fatalf("task mapping error = %v, want ErrDiscordMappingNotFound", err)
	}
}

func TestSyncActiveTasksWithCleanupDeletesUnmappedGuildChannels(t *testing.T) {
	store, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	project, err := store.EnsureProjectDetails(t.TempDir(), "My App", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTaskRuntimeModeInterface(db.NewTaskID(), project.ID, "active task", nil, "claude", false, db.TaskInterfaceDiscord, db.StatusActive, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	client := newFakeSyncClient()
	client.extraChannels = []GuildChannel{
		{ID: "orphan-text", Name: "old-task", ParentID: "orphan-category", Type: GuildChannelText},
		{ID: "orphan-category", Name: "Old Project", Type: GuildChannelCategory},
	}
	if _, err := store.UpsertDiscordMapping(db.DiscordAGXProject, "deleted-project", db.DiscordTypeCategory, "orphan-category"); err != nil {
		t.Fatal(err)
	}
	if err := NewSyncer(store, client, "guild-1").SyncActiveTasksWithCleanup(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if got, want := client.deleted, []string{"orphan-text", "orphan-category"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("deleted = %#v, want %#v", got, want)
	}
	if _, err := store.GetDiscordMapping(db.DiscordAGXProject, "deleted-project"); !errors.Is(err, db.ErrDiscordMappingNotFound) {
		t.Fatalf("project mapping error = %v, want ErrDiscordMappingNotFound", err)
	}
	if _, err := store.GetDiscordMapping(db.DiscordAGXTask, task.ID); err != nil {
		t.Fatalf("live task mapping error = %v", err)
	}
}

func TestFormatNames(t *testing.T) {
	if got := FormatCategoryName("My App"); got != "📁 My App" {
		t.Fatalf("FormatCategoryName = %q", got)
	}
	task := db.Task{ID: "abcdef123456", Title: "Coding Machine"}
	if got := SanitizeTextChannelName(TaskChannelName(task)); got != "coding-machine-abcdef12" {
		t.Fatalf("TaskChannelName = %q", got)
	}
	if got := SanitizeTextChannelName("Hello World!!"); got != "hello-world" {
		t.Fatalf("SanitizeTextChannelName = %q", got)
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
