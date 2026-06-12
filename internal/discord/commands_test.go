package discord

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/nashory/agx/internal/agentstream"
	"github.com/nashory/agx/internal/config"
)

type fakeCommandService struct {
	sentText      string
	sendResult    SendTaskMessageResult
	interrupted   string
	deleted       string
	killedChannel string
	softSynced    bool
	hardSynced    bool
	hardPreserve  string
	syncStatus    SyncStatusSummary
	control       map[string]bool
	tasks         []TaskSummary
	projects      []ProjectSummary
	logs          string
	channel       map[string]string
	sendErr       error
	interruptErr  error
}

func (f *fakeCommandService) ListTasks(context.Context) ([]TaskSummary, error) {
	return f.tasks, nil
}

func (f *fakeCommandService) ListProjects(context.Context) ([]ProjectSummary, error) {
	return f.projects, nil
}

func (f *fakeCommandService) IsControlChannel(ctx context.Context, channelID string) (bool, error) {
	if f.control == nil {
		return true, nil
	}
	return f.control[channelID], nil
}

func (f *fakeCommandService) SoftSync(context.Context) error {
	f.softSynced = true
	return nil
}

func (f *fakeCommandService) HardSync(ctx context.Context, preserveControlChannelID string) error {
	f.hardSynced = true
	f.hardPreserve = preserveControlChannelID
	return nil
}

func (f *fakeCommandService) SyncStatus(ctx context.Context) SyncStatusSummary {
	return f.syncStatus
}

func (f *fakeCommandService) ResolveTaskByChannel(ctx context.Context, channelID string) (string, error) {
	return f.channel[channelID], nil
}

func (f *fakeCommandService) GetTask(ctx context.Context, taskID string) (TaskSummary, error) {
	for _, task := range f.tasks {
		if task.ID == taskID {
			return task, nil
		}
	}
	return TaskSummary{ID: taskID, Title: "task title", Status: "active", Agent: "claude"}, nil
}

func (f *fakeCommandService) InterruptTask(ctx context.Context, taskID string) error {
	f.interrupted = taskID
	return f.interruptErr
}

func (f *fakeCommandService) KillTask(ctx context.Context, taskID, channelID string) error {
	f.deleted = taskID
	f.killedChannel = channelID
	return nil
}

func (f *fakeCommandService) TaskLogs(ctx context.Context, taskID string, lines int) (string, error) {
	return f.logs, nil
}

func (f *fakeCommandService) SendTaskMessage(ctx context.Context, taskID string, message IncomingTaskMessage) (SendTaskMessageResult, error) {
	f.sentText = message.Text
	return f.sendResult, f.sendErr
}

func TestApplicationCommandsRequiredOptionsComeFirst(t *testing.T) {
	for _, command := range ApplicationCommands() {
		seenOptional := false
		for _, option := range command.Options {
			if !option.Required {
				seenOptional = true
				continue
			}
			if seenOptional {
				t.Fatalf("command %s has required option %s after optional option", command.Name, option.Name)
			}
		}
	}
}

func TestApplicationCommandsExposeOnlySafeTaskControls(t *testing.T) {
	commands := map[string]*discordgo.ApplicationCommand{}
	for _, command := range ApplicationCommands() {
		commands[command.Name] = command
	}
	for _, removed := range []string{"stop", "restart", "delete"} {
		if commands[removed] != nil {
			t.Fatalf("ApplicationCommands contains %q, want Discord task controls limited to interrupt and kill", removed)
		}
	}
	interrupt := commands["interrupt"]
	if interrupt == nil {
		t.Fatal("ApplicationCommands missing interrupt")
	}
	if len(interrupt.Options) != 0 {
		t.Fatalf("interrupt options = %#v, want no task argument in task channels", interrupt.Options)
	}
	if commands["clear"] == nil {
		t.Fatal("ApplicationCommands missing clear")
	}
	if commands["kill"] == nil {
		t.Fatal("ApplicationCommands missing kill")
	}
}

func TestCommandRouterRejectsUnauthorizedUser(t *testing.T) {
	router := NewCommandRouter(config.DiscordConfig{AllowedUserIDs: []string{"allowed"}}, &fakeCommandService{})

	response, err := router.Execute(context.Background(), CommandInput{Name: "ps", UserID: "denied"})
	if err != nil {
		t.Fatal(err)
	}
	if !response.Ephemeral || !strings.Contains(response.Content, "not allowed") {
		t.Fatalf("response = %#v, want ephemeral denial", response)
	}
}

func TestCommandRouterRejectsWrongGuild(t *testing.T) {
	router := NewCommandRouter(config.DiscordConfig{GuildID: "guild-1", AllowedUserIDs: []string{"user"}}, &fakeCommandService{})

	response, err := router.Execute(context.Background(), CommandInput{Name: "ps", GuildID: "guild-2", UserID: "user"})
	if err != nil {
		t.Fatal(err)
	}
	if !response.Ephemeral || !strings.Contains(response.Content, "not allowed") {
		t.Fatalf("response = %#v, want guild-scoped denial", response)
	}
}

func TestCommandRouterRequiresControlChannelForSlashCommands(t *testing.T) {
	service := &fakeCommandService{control: map[string]bool{"control-1": true}}
	router := NewCommandRouter(config.DiscordConfig{GuildID: "guild-1", AllowedUserIDs: []string{"user"}}, service)

	response, err := router.Execute(context.Background(), CommandInput{
		Name:      "ps",
		GuildID:   "guild-1",
		ChannelID: "task-channel",
		UserID:    "user",
		Options:   map[string]string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !response.Ephemeral || !strings.Contains(response.Content, "#agx-control") {
		t.Fatalf("response = %#v, want ephemeral control-channel guidance", response)
	}
}

func TestCommandRouterAllowsNamedControlChannelWhenMappingIsMissing(t *testing.T) {
	service := &fakeCommandService{control: map[string]bool{}}
	router := NewCommandRouter(config.DiscordConfig{GuildID: "guild-1", AllowedUserIDs: []string{"user"}}, service)

	_, err := router.Execute(context.Background(), CommandInput{
		Name:        "soft-sync",
		GuildID:     "guild-1",
		ChannelID:   "new-control",
		ChannelName: "agx-control",
		UserID:      "user",
		Options:     map[string]string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !service.softSynced {
		t.Fatal("soft sync was not called")
	}
}

func TestCommandRouterRunsSyncCommandsInControlChannel(t *testing.T) {
	service := &fakeCommandService{control: map[string]bool{"control-1": true}}
	router := NewCommandRouter(config.DiscordConfig{GuildID: "guild-1", AllowedUserIDs: []string{"user"}}, service)

	if _, err := router.Execute(context.Background(), CommandInput{
		Name:      "soft-sync",
		GuildID:   "guild-1",
		ChannelID: "control-1",
		UserID:    "user",
		Options:   map[string]string{},
	}); err != nil {
		t.Fatal(err)
	}
	if !service.softSynced {
		t.Fatal("soft sync was not called")
	}

	response, err := router.Execute(context.Background(), CommandInput{
		Name:      "hard-sync",
		GuildID:   "guild-1",
		ChannelID: "control-1",
		UserID:    "user",
		Options:   map[string]string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(response.Content, "Hard sync started") {
		t.Fatalf("hard sync response = %q, want started message", response.Content)
	}
	if !service.hardSynced {
		t.Fatal("hard sync was not called")
	}
	if service.hardPreserve != "control-1" {
		t.Fatalf("hardPreserve = %q, want control-1", service.hardPreserve)
	}
}

func TestCommandRouterTaskListShowsCurrentTasks(t *testing.T) {
	sessionName := "tmux-window"
	service := &fakeCommandService{
		control: map[string]bool{"control-1": true},
		tasks: []TaskSummary{{
			ID:          "task-12345678",
			Title:       "Coding Machine",
			ProjectName: "agx",
			Status:      "active",
			Agent:       "codex",
			AllMighty:   true,
			SessionName: &sessionName,
			ChannelID:   "task-channel",
		}},
	}
	router := NewCommandRouter(config.DiscordConfig{GuildID: "guild-1", AllowedUserIDs: []string{"user"}}, service)

	response, err := router.Execute(context.Background(), CommandInput{
		Name:       "task",
		Subcommand: "list",
		GuildID:    "guild-1",
		ChannelID:  "control-1",
		UserID:     "user",
		Options:    map[string]string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"AGX tasks:", "task-123", "active", "codex", "all-mighty", "live", "Coding Machine", "agx", "<#task-channel>"} {
		if !strings.Contains(response.Content, expected) {
			t.Fatalf("task list = %q, missing %q", response.Content, expected)
		}
	}
}

func TestCommandRouterBlocksManagementCommandsInTaskChannels(t *testing.T) {
	service := &fakeCommandService{
		control: map[string]bool{"control-1": true},
		channel: map[string]string{"task-channel": "task-1"},
	}
	router := NewCommandRouter(config.DiscordConfig{GuildID: "guild-1", AllowedUserIDs: []string{"user"}}, service)

	response, err := router.Execute(context.Background(), CommandInput{
		Name:      "ps",
		GuildID:   "guild-1",
		ChannelID: "task-channel",
		UserID:    "user",
		Options:   map[string]string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !response.Ephemeral || !strings.Contains(response.Content, "#agx-control") {
		t.Fatalf("response = %#v, want ephemeral control-channel guidance", response)
	}
}

func TestCommandRouterHeartbeatReportsTaskHealthInTaskChannel(t *testing.T) {
	threadID := "thread-1"
	streamKind := "claude-json-stream"
	sessionName := "tmux-window"
	service := &fakeCommandService{
		control: map[string]bool{"control-1": true},
		channel: map[string]string{"task-channel": "task-1"},
		tasks: []TaskSummary{{
			ID:              "task-1",
			Title:           "Coding Machine",
			ProjectName:     "yoyohani.ch",
			Status:          "active",
			Agent:           "claude",
			AllMighty:       true,
			SessionName:     &sessionName,
			ChannelID:       "task-channel",
			AgentThreadID:   &threadID,
			AgentStreamKind: &streamKind,
		}},
	}
	router := NewCommandRouter(config.DiscordConfig{GuildID: "guild-1", AllowedUserIDs: []string{"user"}}, service)

	response, err := router.Execute(context.Background(), CommandInput{
		Name:      "heartbeat",
		GuildID:   "guild-1",
		ChannelID: "task-channel",
		UserID:    "user",
		Options:   map[string]string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"AGX heartbeat", "Coding Machine", "yoyohani.ch", "all-mighty", "live"} {
		if !strings.Contains(response.Content, expected) {
			t.Fatalf("heartbeat = %q, missing %q", response.Content, expected)
		}
	}
}

func TestCommandRouterHeartbeatReportsBackendHealthInControlChannel(t *testing.T) {
	threadID := "thread-1"
	service := &fakeCommandService{
		control:  map[string]bool{"control-1": true},
		projects: []ProjectSummary{{ID: "project-1", Name: "agx", Path: "/repo"}},
		syncStatus: SyncStatusSummary{
			Running: true,
			Kind:    "hard",
			Stage:   "Starting hard sync",
		},
		tasks: []TaskSummary{
			{ID: "task-1", Title: "Coding Machine", ProjectName: "agx", Status: "active", Agent: "codex", AgentThreadID: &threadID, AgentStreamKind: &threadID},
			{ID: "task-2", Title: "Reviewer", ProjectName: "agx", Status: "offline", Agent: "claude"},
		},
	}
	router := NewCommandRouter(config.DiscordConfig{GuildID: "guild-1", AllowedUserIDs: []string{"user"}}, service)

	response, err := router.Execute(context.Background(), CommandInput{
		Name:      "heartbeat",
		GuildID:   "guild-1",
		ChannelID: "control-1",
		UserID:    "user",
		Options:   map[string]string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"AGX backend heartbeat", "Status: ok", "Projects: 1", "Tasks: 2", "Live agent sessions: 1", "Sync: running hard", "offline tasks: 1"} {
		if !strings.Contains(response.Content, expected) {
			t.Fatalf("heartbeat = %q, missing %q", response.Content, expected)
		}
	}
}

func TestCommandRouterHeartbeatRejectsUnlinkedChannel(t *testing.T) {
	service := &fakeCommandService{control: map[string]bool{"control-1": true}, channel: map[string]string{}}
	router := NewCommandRouter(config.DiscordConfig{GuildID: "guild-1", AllowedUserIDs: []string{"user"}}, service)

	response, err := router.Execute(context.Background(), CommandInput{
		Name:      "heartbeat",
		GuildID:   "guild-1",
		ChannelID: "random",
		UserID:    "user",
		Options:   map[string]string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !response.Ephemeral || !strings.Contains(response.Content, "task channel") || !strings.Contains(response.Content, "#agx-control") {
		t.Fatalf("response = %#v, want heartbeat channel guidance", response)
	}
}

func TestCommandRouterRequiresTaskIDInControlChannel(t *testing.T) {
	service := &fakeCommandService{control: map[string]bool{"control-1": true}}
	router := NewCommandRouter(config.DiscordConfig{GuildID: "guild-1", AllowedUserIDs: []string{"user"}}, service)

	_, err := router.Execute(context.Background(), CommandInput{
		Name:        "status",
		GuildID:     "guild-1",
		ChannelID:   "control-1",
		ChannelName: "agx-control",
		UserID:      "user",
		Options:     map[string]string{},
	})
	if err == nil || !strings.Contains(err.Error(), "task id is required") {
		t.Fatalf("err = %v, want task id required", err)
	}
}

func TestCommandRouterLogsUsesChannelTask(t *testing.T) {
	service := &fakeCommandService{logs: "hello", channel: map[string]string{"channel-1": "task-1"}}
	router := NewCommandRouter(config.DiscordConfig{AllowedUserIDs: []string{"user"}}, service)

	response, err := router.Execute(context.Background(), CommandInput{
		Name:      "logs",
		ChannelID: "channel-1",
		UserID:    "user",
		Options:   map[string]string{"lines": "20"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.Content != "```\nhello\n```" {
		t.Fatalf("response = %q", response.Content)
	}
}

func TestCommandRouterLogsCleansTerminalNoise(t *testing.T) {
	service := &fakeCommandService{
		logs:    "Using AI Gateway (Vertex upstream)\nactual log\n>> accept edits on (shift+tab to cycle)",
		channel: map[string]string{"channel-1": "task-1"},
	}
	router := NewCommandRouter(config.DiscordConfig{AllowedUserIDs: []string{"user"}}, service)

	response, err := router.Execute(context.Background(), CommandInput{
		Name:      "logs",
		ChannelID: "channel-1",
		UserID:    "user",
		Options:   map[string]string{"lines": "20"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(response.Content, "Using AI Gateway") || strings.Contains(response.Content, "accept edits") {
		t.Fatalf("response still contains terminal noise: %q", response.Content)
	}
	if !strings.Contains(response.Content, "actual log") {
		t.Fatalf("response dropped useful log: %q", response.Content)
	}
}

func TestCommandRouterInterruptCommandInterruptsTask(t *testing.T) {
	service := &fakeCommandService{
		control: map[string]bool{"task-channel": false},
		channel: map[string]string{"task-channel": "task-1"},
	}
	router := NewCommandRouter(config.DiscordConfig{GuildID: "guild-1", AllowedUserIDs: []string{"user"}}, service)

	response, err := router.Execute(context.Background(), CommandInput{
		Name:      "interrupt",
		GuildID:   "guild-1",
		ChannelID: "task-channel",
		UserID:    "user",
		Options:   map[string]string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if service.interrupted != "task-1" {
		t.Fatalf("interrupted = %q, want task-1", service.interrupted)
	}
	if !strings.Contains(response.Content, "interrupted") {
		t.Fatalf("response = %q", response.Content)
	}
}

func TestCommandRouterInterruptRejectedInControlChannel(t *testing.T) {
	service := &fakeCommandService{control: map[string]bool{"control-1": true}}
	router := NewCommandRouter(config.DiscordConfig{GuildID: "guild-1", AllowedUserIDs: []string{"user"}}, service)

	response, err := router.Execute(context.Background(), CommandInput{
		Name:      "interrupt",
		GuildID:   "guild-1",
		ChannelID: "control-1",
		UserID:    "user",
		Options:   map[string]string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !response.Ephemeral || !strings.Contains(response.Content, "task channel") {
		t.Fatalf("response = %#v, want task-channel guidance", response)
	}
	if service.interrupted != "" {
		t.Fatalf("interrupted = %q, want no interrupt", service.interrupted)
	}
}

func TestCommandRouterKillDeletesCurrentTaskChannelTask(t *testing.T) {
	service := &fakeCommandService{
		control: map[string]bool{"control-1": true},
		channel: map[string]string{"task-channel": "task-1"},
	}
	router := NewCommandRouter(config.DiscordConfig{GuildID: "guild-1", AllowedUserIDs: []string{"user"}}, service)

	response, err := router.Execute(context.Background(), CommandInput{
		Name:      "kill",
		GuildID:   "guild-1",
		ChannelID: "task-channel",
		UserID:    "user",
		Options:   map[string]string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if service.deleted != "task-1" {
		t.Fatalf("deleted = %q, want task-1", service.deleted)
	}
	if !strings.Contains(response.Content, "deleting this task channel") {
		t.Fatalf("response = %q, want channel deletion note", response.Content)
	}
}

func TestCommandRouterKillRejectedInControlChannel(t *testing.T) {
	service := &fakeCommandService{control: map[string]bool{"control-1": true}}
	router := NewCommandRouter(config.DiscordConfig{GuildID: "guild-1", AllowedUserIDs: []string{"user"}}, service)

	response, err := router.Execute(context.Background(), CommandInput{
		Name:      "kill",
		GuildID:   "guild-1",
		ChannelID: "control-1",
		UserID:    "user",
		Options:   map[string]string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !response.Ephemeral || !strings.Contains(response.Content, "task channel") {
		t.Fatalf("response = %#v, want task-channel guidance", response)
	}
	if service.deleted != "" {
		t.Fatalf("deleted = %q, want no deletion", service.deleted)
	}
}

func TestCommandRouterClearSendsContextClearCommand(t *testing.T) {
	service := &fakeCommandService{
		control: map[string]bool{"task-channel": false},
		channel: map[string]string{"task-channel": "task-1"},
		tasks:   []TaskSummary{{ID: "task-1", Agent: "codex", AgentThreadID: stringPtr("thread-1"), AgentStreamKind: stringPtr("codex-app-server")}},
	}
	router := NewCommandRouter(config.DiscordConfig{GuildID: "guild-1", AllowedUserIDs: []string{"user"}}, service)

	response, err := router.Execute(context.Background(), CommandInput{
		Name:      "clear",
		GuildID:   "guild-1",
		ChannelID: "task-channel",
		UserID:    "user",
		Options:   map[string]string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if service.sentText != "/clear" {
		t.Fatalf("sentText = %q, want /clear", service.sentText)
	}
	if !strings.Contains(response.Content, "context cleared") {
		t.Fatalf("response = %q, want context cleared", response.Content)
	}
}

func TestCommandRouterPlainMessageSendsToTask(t *testing.T) {
	service := &fakeCommandService{channel: map[string]string{"channel-1": "task-1"}}
	router := NewCommandRouter(config.DiscordConfig{AllowedUserIDs: []string{"user"}}, service)

	_, err := router.HandlePlainMessage(context.Background(), CommandInput{ChannelID: "channel-1", UserID: "user"}, "ship it")
	if err != nil {
		t.Fatal(err)
	}
	if service.sentText != "ship it" {
		t.Fatalf("sentText = %q, want ship it", service.sentText)
	}
}

func TestCommandRouterComponentChoiceSendsSelectedLabelToTask(t *testing.T) {
	service := &fakeCommandService{channel: map[string]string{"channel-1": "task-1"}}
	router := NewCommandRouter(config.DiscordConfig{AllowedUserIDs: []string{"user"}}, service)

	_, err := router.HandleComponentChoice(context.Background(), CommandInput{ChannelID: "channel-1", UserID: "user"}, "task-1", "Build-time embed")
	if err != nil {
		t.Fatal(err)
	}
	if service.sentText != "Build-time embed" {
		t.Fatalf("sentText = %q, want selected label", service.sentText)
	}
}

func TestCommandRouterComponentChoiceRejectsWrongTaskChannel(t *testing.T) {
	service := &fakeCommandService{channel: map[string]string{"channel-1": "task-2"}}
	router := NewCommandRouter(config.DiscordConfig{AllowedUserIDs: []string{"user"}}, service)

	if _, err := router.HandleComponentChoice(context.Background(), CommandInput{ChannelID: "channel-1", UserID: "user"}, "task-1", "Build-time embed"); err == nil {
		t.Fatal("HandleComponentChoice error = nil, want wrong task error")
	}
	if service.sentText != "" {
		t.Fatalf("sentText = %q, want no send", service.sentText)
	}
}

func TestCommandRouterPlainKillDeletesTask(t *testing.T) {
	service := &fakeCommandService{channel: map[string]string{"channel-1": "task-1"}}
	router := NewCommandRouter(config.DiscordConfig{AllowedUserIDs: []string{"user"}}, service)

	response, err := router.HandlePlainMessage(context.Background(), CommandInput{ChannelID: "channel-1", UserID: "user"}, "/kill")
	if err != nil {
		t.Fatal(err)
	}
	if service.deleted != "task-1" {
		t.Fatalf("deleted = %q, want task-1", service.deleted)
	}
	if service.killedChannel != "channel-1" {
		t.Fatalf("killedChannel = %q, want channel-1", service.killedChannel)
	}
	if service.sentText != "" {
		t.Fatalf("sentText = %q, want no agent message", service.sentText)
	}
	if !strings.Contains(response.Content, "deleting this task channel") {
		t.Fatalf("response = %q, want channel deletion note", response.Content)
	}
}

func TestCommandRouterSlashKillPassesCurrentChannel(t *testing.T) {
	service := &fakeCommandService{
		channel: map[string]string{"channel-1": "task-1"},
		control: map[string]bool{"channel-1": false},
	}
	router := NewCommandRouter(config.DiscordConfig{GuildID: "guild-1", AllowedUserIDs: []string{"user"}}, service)

	response, err := router.Execute(context.Background(), CommandInput{
		Name:      "kill",
		GuildID:   "guild-1",
		ChannelID: "channel-1",
		UserID:    "user",
		Options:   map[string]string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if service.deleted != "task-1" || service.killedChannel != "channel-1" {
		t.Fatalf("deleted = %q channel = %q, want task-1/channel-1", service.deleted, service.killedChannel)
	}
	if !strings.Contains(response.Content, "deleting this task channel") {
		t.Fatalf("response = %q, want channel deletion note", response.Content)
	}
}

func TestCommandRouterPlainMessageReportsUnlinkedChannel(t *testing.T) {
	service := &fakeCommandService{channel: map[string]string{"other-channel": "task-1"}}
	router := NewCommandRouter(config.DiscordConfig{AllowedUserIDs: []string{"user"}}, service)

	_, err := router.HandlePlainMessage(context.Background(), CommandInput{ChannelID: "channel-1", UserID: "user"}, "ignore me")
	if !errors.Is(err, ErrChannelNotLinked) {
		t.Fatalf("err = %v, want ErrChannelNotLinked", err)
	}
	if service.sentText != "" {
		t.Fatalf("sentText = %q, want no message sent", service.sentText)
	}
}

func TestCommandRouterPlainMessageReportsUnsupportedAgent(t *testing.T) {
	service := &fakeCommandService{
		channel: map[string]string{"channel-1": "task-1"},
		sendErr: agentstream.UnsupportedError{TaskID: "task-1", Agent: "claude"},
	}
	router := NewCommandRouter(config.DiscordConfig{AllowedUserIDs: []string{"user"}}, service)

	response, err := router.HandlePlainMessage(context.Background(), CommandInput{ChannelID: "channel-1", UserID: "user"}, "ship it")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(response.Content, "structured Discord streaming") || !strings.Contains(response.Content, "/logs") {
		t.Fatalf("response = %q, want unsupported guidance", response.Content)
	}
}

func TestCommandRouterPlainMessageReportsInactiveTask(t *testing.T) {
	service := &fakeCommandService{
		channel: map[string]string{"channel-1": "task-1"},
		sendErr: TaskNotLiveError{TaskID: "task-1", Status: "complete", Agent: "claude"},
	}
	router := NewCommandRouter(config.DiscordConfig{AllowedUserIDs: []string{"user"}}, service)

	response, err := router.HandlePlainMessage(context.Background(), CommandInput{ChannelID: "channel-1", UserID: "user"}, "ship it")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(response.Content, "appears to have stopped") || !strings.Contains(response.Content, "complete") || !strings.Contains(response.Content, "AGX Desktop") || strings.Contains(response.Content, "/restart") {
		t.Fatalf("response = %q, want inactive task guidance", response.Content)
	}
}

func TestCommandRouterReactionInterruptsTask(t *testing.T) {
	service := &fakeCommandService{channel: map[string]string{"channel-1": "task-1"}}
	router := NewCommandRouter(config.DiscordConfig{AllowedUserIDs: []string{"user"}}, service)

	response, err := router.HandleReaction(context.Background(), CommandInput{ChannelID: "channel-1", UserID: "user"}, "🛑")
	if err != nil {
		t.Fatal(err)
	}
	if service.interrupted != "task-1" {
		t.Fatalf("interrupted = %q, want task-1", service.interrupted)
	}
	if !strings.Contains(response.Content, "Interrupted task") {
		t.Fatalf("response = %q", response.Content)
	}
}

func TestCommandRouterIgnoresNonInterruptReaction(t *testing.T) {
	service := &fakeCommandService{channel: map[string]string{"channel-1": "task-1"}}
	router := NewCommandRouter(config.DiscordConfig{AllowedUserIDs: []string{"user"}}, service)

	response, err := router.HandleReaction(context.Background(), CommandInput{ChannelID: "channel-1", UserID: "user"}, "✅")
	if err != nil {
		t.Fatal(err)
	}
	if service.interrupted != "" {
		t.Fatalf("interrupted = %q, want no interrupt", service.interrupted)
	}
	if response.Content != "" {
		t.Fatalf("response = %q, want no response", response.Content)
	}
}

func TestCommandInputFromInteraction(t *testing.T) {
	input := CommandInputFromInteraction(&discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			Type:      discordgo.InteractionApplicationCommand,
			GuildID:   "guild-1",
			ChannelID: "channel-1",
			Member: &discordgo.Member{
				User: &discordgo.User{ID: "user-1"},
			},
			Data: discordgo.ApplicationCommandInteractionData{
				Name: "logs",
				Options: []*discordgo.ApplicationCommandInteractionDataOption{
					{Name: "task", Type: discordgo.ApplicationCommandOptionString, Value: "task-1"},
					{Name: "lines", Type: discordgo.ApplicationCommandOptionInteger, Value: float64(20)},
				},
			},
		},
	})

	if input.Name != "logs" || input.GuildID != "guild-1" || input.ChannelID != "channel-1" || input.UserID != "user-1" {
		t.Fatalf("input = %#v", input)
	}
	if input.Options["task"] != "task-1" || input.Options["lines"] != "20" {
		t.Fatalf("options = %#v", input.Options)
	}
}
