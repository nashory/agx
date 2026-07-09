package discord

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/nashory/agx/internal/agentstream"
	"github.com/nashory/agx/internal/config"
)

type fakeCommandService struct {
	sentText                string
	sendResult              SendTaskMessageResult
	interrupted             string
	deleted                 string
	killedChannel           string
	softSynced              bool
	hardSynced              bool
	hardPreserve            string
	syncStatus              SyncStatusSummary
	control                 map[string]bool
	tasks                   []TaskSummary
	projects                []ProjectSummary
	createdTask             TaskSummary
	deletedTask             TaskSummary
	createdProject          ProjectSummary
	deletedProject          ProjectSummary
	logs                    string
	channel                 map[string]string
	createProjectPath       string
	createProjectName       string
	createProjectAgent      string
	deleteProjectRef        string
	createTaskProject       string
	createTaskTitle         string
	createTaskPrompt        string
	createTaskAgent         string
	createTaskWorkspaceMode string
	createTaskAllMighty     bool
	deleteTaskRef           string
	sendErr                 error
	interruptErr            error
	killErr                 error
	createProjectErr        error
	deleteProjectErr        error
	createTaskErr           error
	deleteTaskErr           error
}

func (f *fakeCommandService) ListTasks(context.Context) ([]TaskSummary, error) {
	return f.tasks, nil
}

func (f *fakeCommandService) ListProjects(context.Context) ([]ProjectSummary, error) {
	return f.projects, nil
}

func (f *fakeCommandService) CreateProject(ctx context.Context, path, name, defaultAgent string) (ProjectSummary, error) {
	f.createProjectPath = path
	f.createProjectName = name
	f.createProjectAgent = defaultAgent
	if f.createProjectErr != nil {
		return ProjectSummary{}, f.createProjectErr
	}
	if f.createdProject.ID != "" || f.createdProject.Name != "" || f.createdProject.Path != "" {
		return f.createdProject, nil
	}
	if name == "" {
		name = filepath.Base(path)
	}
	return ProjectSummary{ID: "project-12345678", Name: name, Path: path}, nil
}

func (f *fakeCommandService) DeleteProject(ctx context.Context, projectRef string) (ProjectSummary, error) {
	f.deleteProjectRef = projectRef
	if f.deleteProjectErr != nil {
		return ProjectSummary{}, f.deleteProjectErr
	}
	if f.deletedProject.ID != "" || f.deletedProject.Name != "" || f.deletedProject.Path != "" {
		return f.deletedProject, nil
	}
	return ProjectSummary{ID: "project-12345678", Name: projectRef}, nil
}

func (f *fakeCommandService) CreateTask(ctx context.Context, projectRef, title, prompt, agentName, workspaceMode string, allMighty bool) (TaskSummary, error) {
	f.createTaskProject = projectRef
	f.createTaskTitle = title
	f.createTaskPrompt = prompt
	f.createTaskAgent = agentName
	f.createTaskWorkspaceMode = workspaceMode
	f.createTaskAllMighty = allMighty
	if f.createTaskErr != nil {
		return TaskSummary{}, f.createTaskErr
	}
	if f.createdTask.ID != "" || f.createdTask.Title != "" {
		return f.createdTask, nil
	}
	return TaskSummary{ID: "task-12345678", Title: title, ProjectName: projectRef}, nil
}

func (f *fakeCommandService) DeleteTask(ctx context.Context, taskRef string) (TaskSummary, error) {
	f.deleteTaskRef = taskRef
	if f.deleteTaskErr != nil {
		return TaskSummary{}, f.deleteTaskErr
	}
	if f.deletedTask.ID != "" || f.deletedTask.Title != "" {
		return f.deletedTask, nil
	}
	return TaskSummary{ID: taskRef}, nil
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
	return f.killErr
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
		assertRequiredOptionsComeFirst(t, command.Name, command.Options)
	}
}

func assertRequiredOptionsComeFirst(t *testing.T, commandPath string, options []*discordgo.ApplicationCommandOption) {
	t.Helper()
	seenOptional := false
	for _, option := range options {
		if option.Type == discordgo.ApplicationCommandOptionSubCommand || option.Type == discordgo.ApplicationCommandOptionSubCommandGroup {
			assertRequiredOptionsComeFirst(t, commandPath+" "+option.Name, option.Options)
			continue
		}
		if !option.Required {
			seenOptional = true
			continue
		}
		if seenOptional {
			t.Fatalf("command %s has required option %s after optional option", commandPath, option.Name)
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
	if !hasSubcommand(commands["project"], "create") || !hasSubcommand(commands["project"], "delete") {
		t.Fatal("ApplicationCommands missing project create/delete subcommands")
	}
	if !hasSubcommand(commands["task"], "create") || !hasSubcommand(commands["task"], "delete") {
		t.Fatal("ApplicationCommands missing task create/delete subcommands")
	}
}

func hasSubcommand(command *discordgo.ApplicationCommand, name string) bool {
	if command == nil {
		return false
	}
	for _, option := range command.Options {
		if option.Type == discordgo.ApplicationCommandOptionSubCommand && option.Name == name {
			return true
		}
	}
	return false
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

func TestCommandRouterProjectCreateRegistersProject(t *testing.T) {
	service := &fakeCommandService{
		control:        map[string]bool{"control-1": true},
		createdProject: ProjectSummary{ID: "project-12345678", Name: "agx", Path: "/repos/agx"},
	}
	router := NewCommandRouter(config.DiscordConfig{GuildID: "guild-1", AllowedUserIDs: []string{"user"}}, service)

	response, err := router.Execute(context.Background(), CommandInput{
		Name:       "project",
		Subcommand: "create",
		GuildID:    "guild-1",
		ChannelID:  "control-1",
		UserID:     "user",
		Options: map[string]string{
			"path":  "/repos/agx",
			"name":  "agx",
			"agent": "codex",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if service.createProjectPath != "/repos/agx" || service.createProjectName != "agx" || service.createProjectAgent != "codex" {
		t.Fatalf("create project args = path %q name %q agent %q", service.createProjectPath, service.createProjectName, service.createProjectAgent)
	}
	if !strings.Contains(response.Content, "registered") || !strings.Contains(response.Content, "`agx`") || !strings.Contains(response.Content, "`/repos/agx`") {
		t.Fatalf("response = %q, want project registered summary", response.Content)
	}
}

func TestCommandRouterProjectDeleteDeletesProject(t *testing.T) {
	service := &fakeCommandService{
		control:        map[string]bool{"control-1": true},
		deletedProject: ProjectSummary{ID: "project-12345678", Name: "agx", Path: "/repos/agx"},
	}
	router := NewCommandRouter(config.DiscordConfig{GuildID: "guild-1", AllowedUserIDs: []string{"user"}}, service)

	response, err := router.Execute(context.Background(), CommandInput{
		Name:       "project",
		Subcommand: "delete",
		GuildID:    "guild-1",
		ChannelID:  "control-1",
		UserID:     "user",
		Options:    map[string]string{"project": "agx"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if service.deleteProjectRef != "agx" {
		t.Fatalf("deleteProjectRef = %q, want agx", service.deleteProjectRef)
	}
	if !strings.Contains(response.Content, "Project `agx` deleted") {
		t.Fatalf("response = %q, want project deleted summary", response.Content)
	}
}

func TestCommandRouterTaskCreateCreatesDiscordTask(t *testing.T) {
	service := &fakeCommandService{
		control:     map[string]bool{"control-1": true},
		createdTask: TaskSummary{ID: "task-12345678", Title: "ship windows controls", ProjectName: "agx", ChannelID: "discord-channel"},
	}
	router := NewCommandRouter(config.DiscordConfig{GuildID: "guild-1", AllowedUserIDs: []string{"user"}}, service)

	response, err := router.Execute(context.Background(), CommandInput{
		Name:       "task",
		Subcommand: "create",
		GuildID:    "guild-1",
		ChannelID:  "control-1",
		UserID:     "user",
		Options: map[string]string{
			"project":        "agx",
			"title":          "ship windows controls",
			"prompt":         "Read the context first",
			"agent":          "codex",
			"workspace-mode": "project",
			"all-mighty":     "false",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if service.createTaskProject != "agx" ||
		service.createTaskTitle != "ship windows controls" ||
		service.createTaskPrompt != "Read the context first" ||
		service.createTaskAgent != "codex" ||
		service.createTaskWorkspaceMode != "project" ||
		service.createTaskAllMighty {
		t.Fatalf("create task args = %#v", service)
	}
	for _, expected := range []string{"Task `task-123` created", "`agx`", "<#discord-channel>"} {
		if !strings.Contains(response.Content, expected) {
			t.Fatalf("response = %q, missing %q", response.Content, expected)
		}
	}
}

func TestCommandRouterTaskCreateDefaultsAllMighty(t *testing.T) {
	service := &fakeCommandService{control: map[string]bool{"control-1": true}}
	router := NewCommandRouter(config.DiscordConfig{GuildID: "guild-1", AllowedUserIDs: []string{"user"}}, service)

	_, err := router.Execute(context.Background(), CommandInput{
		Name:       "task",
		Subcommand: "create",
		GuildID:    "guild-1",
		ChannelID:  "control-1",
		UserID:     "user",
		Options:    map[string]string{"project": "agx", "title": "main"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !service.createTaskAllMighty {
		t.Fatal("createTaskAllMighty = false, want default true")
	}
}

func TestCommandRouterTaskDeleteDeletesTask(t *testing.T) {
	service := &fakeCommandService{
		control:     map[string]bool{"control-1": true},
		deletedTask: TaskSummary{ID: "task-12345678", Title: "old task", ProjectName: "agx"},
	}
	router := NewCommandRouter(config.DiscordConfig{GuildID: "guild-1", AllowedUserIDs: []string{"user"}}, service)

	response, err := router.Execute(context.Background(), CommandInput{
		Name:       "task",
		Subcommand: "delete",
		GuildID:    "guild-1",
		ChannelID:  "control-1",
		UserID:     "user",
		Options:    map[string]string{"task": "task-1234"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if service.deleteTaskRef != "task-1234" {
		t.Fatalf("deleteTaskRef = %q, want task-1234", service.deleteTaskRef)
	}
	if !strings.Contains(response.Content, "Task `task-123` deleted") {
		t.Fatalf("response = %q, want task deleted summary", response.Content)
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

func TestCommandRouterPlainMessageRejectsUnauthorizedUserBeforeResolvingTask(t *testing.T) {
	service := &fakeCommandService{channel: map[string]string{"channel-1": "task-1"}}
	router := NewCommandRouter(config.DiscordConfig{AllowedUserIDs: []string{"allowed"}}, service)

	response, err := router.HandlePlainMessage(context.Background(), CommandInput{GuildID: "", ChannelID: "channel-1", UserID: "blocked"}, "ship it")
	if err != nil {
		t.Fatal(err)
	}
	if !response.Ephemeral || !strings.Contains(response.Content, "not allowed") {
		t.Fatalf("response = %#v, want ephemeral unauthorized response", response)
	}
	if service.sentText != "" {
		t.Fatalf("sent text = %q, want no task message for unauthorized user", service.sentText)
	}
}

func TestCommandRouterPlainMessageDoesNotAckStructuredTask(t *testing.T) {
	service := &fakeCommandService{
		channel: map[string]string{"channel-1": "task-1"},
		tasks: []TaskSummary{{
			ID:              "task-1",
			Status:          "active",
			Agent:           "codex",
			AgentThreadID:   stringPtr("thread-1"),
			AgentStreamKind: stringPtr("codex-app-server"),
		}},
	}
	router := NewCommandRouter(config.DiscordConfig{AllowedUserIDs: []string{"user"}}, service)

	response, err := router.HandlePlainMessage(context.Background(), CommandInput{ChannelID: "channel-1", UserID: "user"}, "ship it")
	if err != nil {
		t.Fatal(err)
	}
	if service.sentText != "ship it" {
		t.Fatalf("sentText = %q, want ship it", service.sentText)
	}
	if strings.TrimSpace(response.Content) != "" {
		t.Fatalf("response.Content = %q, want no fallback ack for structured task", response.Content)
	}
}

func TestCommandRouterPlainMessageAcksLegacyTask(t *testing.T) {
	service := &fakeCommandService{
		channel: map[string]string{"channel-1": "task-1"},
		tasks: []TaskSummary{{
			ID:     "task-1",
			Status: "active",
			Agent:  "codex",
		}},
	}
	router := NewCommandRouter(config.DiscordConfig{AllowedUserIDs: []string{"user"}}, service)

	response, err := router.HandlePlainMessage(context.Background(), CommandInput{ChannelID: "channel-1", UserID: "user"}, "ship it")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(response.Content, "Message sent to codex") || !strings.Contains(response.Content, "AGX Desktop") {
		t.Fatalf("response.Content = %q, want legacy fallback ack", response.Content)
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

func TestCommandRouterPlainKillReportsPartialCleanupFailure(t *testing.T) {
	service := &fakeCommandService{
		channel: map[string]string{"channel-1": "task-1"},
		killErr: partialSuccessTestError{message: "task task-1 deleted, but cleanup failed: remove task worktree"},
	}
	router := NewCommandRouter(config.DiscordConfig{AllowedUserIDs: []string{"user"}}, service)

	response, err := router.HandlePlainMessage(context.Background(), CommandInput{ChannelID: "channel-1", UserID: "user"}, "/kill")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(response.Content, "deleted, but cleanup failed") {
		t.Fatalf("response = %q, want partial cleanup warning", response.Content)
	}
	if service.deleted != "task-1" || service.killedChannel != "channel-1" {
		t.Fatalf("deleted = %q channel = %q, want task-1/channel-1", service.deleted, service.killedChannel)
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

type partialSuccessTestError struct {
	message string
}

func (e partialSuccessTestError) Error() string {
	return e.message
}

func (e partialSuccessTestError) PartialSuccess() bool {
	return true
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
