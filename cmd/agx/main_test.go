package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nashory/agx/internal/config"
	"github.com/nashory/agx/internal/db"
	agxdiscord "github.com/nashory/agx/internal/discord"
	agxruntime "github.com/nashory/agx/internal/runtime"
	"github.com/spf13/cobra"
)

func TestPrintRuntimeServiceStatus(t *testing.T) {
	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)
	printRuntimeServiceStatus(cmd, agxruntime.RuntimeServiceStatus{
		Manager:   "systemd",
		PathLabel: "systemd unit",
		Path:      "/home/agx/.config/systemd/user/dev.agx.runtime.service",
		State:     "missing",
		Detail:    "systemctl not found",
	})
	text := out.String()
	for _, want := range []string{
		"systemd unit: /home/agx/.config/systemd/user/dev.agx.runtime.service (missing)",
		"systemd detail: systemctl not found",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("runtime service status output missing %q:\n%s", want, text)
		}
	}
}

func TestPrintRuntimeServiceStatusWithoutPath(t *testing.T) {
	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)
	printRuntimeServiceStatus(cmd, agxruntime.RuntimeServiceStatus{
		State: "unsupported",
	})
	if got, want := out.String(), "runtime service: unsupported\n"; got != want {
		t.Fatalf("runtime service status output = %q, want %q", got, want)
	}
}

func TestRuntimeBackedInvocationCoversStatefulCommands(t *testing.T) {
	tests := []struct {
		args []string
		want bool
	}{
		{args: []string{"agx", "run"}, want: true},
		{args: []string{"agx", "ps"}, want: true},
		{args: []string{"agx", "logs"}, want: true},
		{args: []string{"agx", "send"}, want: true},
		{args: []string{"agx", "stop"}, want: true},
		{args: []string{"agx", "interrupt"}, want: true},
		{args: []string{"agx", "attach"}, want: true},
		{args: []string{"agx", "discord", "connect"}, want: true},
		{args: []string{"agx", "chat", "sync"}, want: true},
		{args: []string{"agx", "launch"}, want: true},
		{args: []string{"agx", "task", "create"}, want: true},
		{args: []string{"agx", "task", "list"}, want: true},
		{args: []string{"agx", "task", "show"}, want: true},
		{args: []string{"agx", "task", "edit"}, want: true},
		{args: []string{"agx", "task", "delete"}, want: true},
		{args: []string{"agx", "project", "init"}, want: true},
		{args: []string{"agx", "project", "list"}, want: true},
		{args: []string{"agx", "project", "config"}, want: true},
		{args: []string{"agx", "project", "delete"}, want: true},
		{args: []string{"agx", "agent", "list"}, want: true},
		{args: []string{"agx", "doctor"}, want: false},
		{args: []string{"agx", "runtime", "status"}, want: false},
		{args: []string{"agx", "tui"}, want: false},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			if got := isRuntimeBackedInvocation(tt.args); got != tt.want {
				t.Fatalf("isRuntimeBackedInvocation(%v) = %v, want %v", tt.args, got, tt.want)
			}
		})
	}
}

func TestDirectInvocationRouting(t *testing.T) {
	if !isRuntimeInvocation([]string{"agx", "runtime", "status"}) {
		t.Fatal("isRuntimeInvocation(runtime status) = false, want true")
	}
	if isRuntimeInvocation([]string{"agx", "doctor"}) {
		t.Fatal("isRuntimeInvocation(doctor) = true, want false")
	}
	if !isDoctorInvocation([]string{"agx", "doctor"}) {
		t.Fatal("isDoctorInvocation(doctor) = false, want true")
	}
	if isDoctorInvocation([]string{"agx", "runtime"}) {
		t.Fatal("isDoctorInvocation(runtime) = true, want false")
	}
}

func TestVersionString(t *testing.T) {
	oldVersion, oldCommit, oldDate := version, commit, date
	t.Cleanup(func() {
		version, commit, date = oldVersion, oldCommit, oldDate
	})
	version, commit, date = "1.2.3", "abc123", "2026-01-02"
	if got, want := versionString(), "1.2.3 (abc123, 2026-01-02)"; got != want {
		t.Fatalf("versionString() = %q, want %q", got, want)
	}
}

func TestRuntimeCommandTreeAndResetValidation(t *testing.T) {
	runtimeCmd := newRuntimeCmd()
	for _, name := range []string{"start", "status", "stop", "reset", "install-service", "uninstall-service"} {
		if _, _, err := runtimeCmd.Find([]string{name}); err != nil {
			t.Fatalf("runtime command missing %q: %v", name, err)
		}
	}

	rootCmd := &cobra.Command{}
	rootCmd.AddCommand(newRuntimeClientTuiCmd(agxruntime.NewClient()))
	tuiCmd, _, err := rootCmd.Find([]string{"tui", "--once"})
	if err != nil {
		t.Fatalf("tui command missing: %v", err)
	}
	if tuiCmd.Use != "tui" {
		t.Fatalf("tui command Use = %q, want tui", tuiCmd.Use)
	}

	resetCmd := newRuntimeResetCmd()
	var out bytes.Buffer
	resetCmd.SetOut(&out)
	resetCmd.SetErr(&out)
	if err := resetCmd.Execute(); err == nil || !strings.Contains(err.Error(), "--confirm") {
		t.Fatalf("reset without confirm error = %v, want confirmation error", err)
	}

	configDir := t.TempDir()
	t.Setenv("AGX_CONFIG_DIR", configDir)
	dbPath := filepath.Join(configDir, "agx.db")
	if err := os.WriteFile(dbPath, []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
	resetCmd = newRuntimeResetCmd()
	out.Reset()
	resetCmd.SetOut(&out)
	resetCmd.SetErr(&out)
	resetCmd.SetArgs([]string{"--confirm"})
	if err := resetCmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Removed AGX runtime state") {
		t.Fatalf("reset output = %q, want removed state message", out.String())
	}
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Fatalf("runtime database still exists: %v", err)
	}
}

func TestRuntimeClientTaskCreatePassesWorkspaceFlags(t *testing.T) {
	client := &fakeRuntimeTaskCreateClient{
		projects: []agxruntime.Project{{ID: "project-1", Name: "AGX", Path: "/repo/agx"}},
		runTask: func(_ context.Context, projectID, title string, description *string, agentName string, allMighty bool, initialPrompt *string, workspaceMode db.WorkspaceMode) (agxruntime.Task, error) {
			if projectID != "project-1" || title != "ship it" || agentName != "codex" || allMighty {
				t.Fatalf("run task args = (%q, %q, %q, %v), want project/title/codex/allMighty=false", projectID, title, agentName, allMighty)
			}
			if description == nil || *description != "details" {
				t.Fatalf("description = %#v, want details", description)
			}
			if initialPrompt != nil {
				t.Fatalf("initialPrompt = %#v, want nil", initialPrompt)
			}
			if workspaceMode != db.WorkspaceModeProject {
				t.Fatalf("workspaceMode = %q, want project", workspaceMode)
			}
			return agxruntime.Task{ID: "task-12345678", Agent: agentName, Status: db.StatusActive}, nil
		},
	}
	cmd := newRuntimeClientTaskCreateCmd(client)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{
		"--project", "AGX",
		"--agent", "codex",
		"--description", "details",
		"--workspace-mode", "project",
		"--all-mighty=false",
		"ship it",
	})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "task task-123 active (codex)") {
		t.Fatalf("output = %q, want created task summary", out.String())
	}
}

func TestRuntimeClientTaskCreateDiscordPath(t *testing.T) {
	client := &fakeRuntimeTaskCreateClient{
		projects: []agxruntime.Project{{ID: "project-1", Name: "AGX", Path: "/repo/agx"}},
		runDiscordTask: func(_ context.Context, projectID, title string, description *string, agentName string, allMighty bool, workspaceMode db.WorkspaceMode) (agxruntime.Task, error) {
			if projectID != "project-1" || title != "sync me" || agentName != "" || !allMighty || workspaceMode != db.WorkspaceModeWorktree {
				t.Fatalf("discord task args = (%q, %q, %q, %v, %q)", projectID, title, agentName, allMighty, workspaceMode)
			}
			return agxruntime.Task{ID: "task-discord", Agent: "codex", Status: db.StatusActive}, nil
		},
	}
	cmd := newRuntimeClientTaskCreateCmd(client)
	cmd.SetArgs([]string{"--project", "AGX", "--discord", "sync me"})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeClientTaskCreatePreservesRuntimeError(t *testing.T) {
	conflict := &agxruntime.RuntimeError{
		Method:     http.MethodPost,
		Path:       "/v1/tasks",
		Status:     "409 Conflict",
		StatusCode: http.StatusConflict,
		Message:    "another project-mode task is already active for this project: task-1",
		Code:       agxruntime.ErrorCodeConflict,
		Retryable:  true,
	}
	client := &fakeRuntimeTaskCreateClient{
		projects: []agxruntime.Project{{ID: "project-1", Name: "AGX", Path: "/repo/agx"}},
		runTask: func(context.Context, string, string, *string, string, bool, *string, db.WorkspaceMode) (agxruntime.Task, error) {
			return agxruntime.Task{}, conflict
		},
	}
	cmd := newRuntimeClientTaskCreateCmd(client)
	cmd.SetArgs([]string{"--project", "AGX", "--workspace-mode", "project", "second"})

	err := cmd.Execute()
	if !errors.Is(err, conflict) {
		t.Fatalf("Execute() error = %v, want runtime conflict", err)
	}
	var runtimeErr *agxruntime.RuntimeError
	if !errors.As(err, &runtimeErr) || runtimeErr.Code != agxruntime.ErrorCodeConflict || !runtimeErr.Retryable {
		t.Fatalf("runtime error = %#v, want retryable conflict", runtimeErr)
	}
}

func TestRuntimeClientTaskCreateRejectsInvalidWorkspaceMode(t *testing.T) {
	client := &fakeRuntimeTaskCreateClient{
		projects: []agxruntime.Project{{ID: "project-1", Name: "AGX", Path: "/repo/agx"}},
	}
	cmd := newRuntimeClientTaskCreateCmd(client)
	cmd.SetArgs([]string{"--project", "AGX", "--workspace-mode", "spaceship", "ship it"})

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "invalid workspace mode") {
		t.Fatalf("Execute() error = %v, want invalid workspace mode", err)
	}
}

func TestRuntimeClientTaskListFiltersProjectAndStatus(t *testing.T) {
	var gotProjectID string
	var gotStatus string
	client := &fakeRuntimeTaskCreateClient{
		projects: []agxruntime.Project{{ID: "project-1", Name: "AGX", Path: "/repo/agx"}},
		listTasksStatus: func(_ context.Context, projectID, status string) ([]agxruntime.Task, error) {
			gotProjectID = projectID
			gotStatus = status
			return []agxruntime.Task{{ID: "task-abcdef12", Status: db.StatusWaiting, Agent: "codex", Title: "review"}}, nil
		},
	}
	cmd := newRuntimeClientTaskListCmd(client)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--project", "AGX", "--status", string(db.StatusWaiting)})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if gotProjectID != "project-1" || gotStatus != string(db.StatusWaiting) {
		t.Fatalf("ListTasksStatus args = (%q, %q), want project-1/waiting", gotProjectID, gotStatus)
	}
	for _, want := range []string{"ID", "STATUS", "AGENT", "TITLE", "task-abc", "waiting", "codex", "review"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("task list output missing %q:\n%s", want, out.String())
		}
	}
}

func TestRuntimeClientTaskShowAndDeleteResolveTaskPrefix(t *testing.T) {
	description := "details"
	sessionName := "task-window"
	worktreePath := "/repo/agx/.worktrees/task"
	branchName := "agx/task"
	client := &fakeRuntimeTaskCreateClient{
		tasks: []agxruntime.Task{{
			ID:           "task-abcdef12",
			Title:        "ship",
			Status:       db.StatusActive,
			Agent:        "codex",
			Description:  &description,
			SessionName:  &sessionName,
			WorktreePath: &worktreePath,
			BranchName:   &branchName,
		}},
	}
	showCmd := newRuntimeClientTaskShowCmd(client)
	var showOut bytes.Buffer
	showCmd.SetOut(&showOut)
	showCmd.SetErr(&showOut)
	showCmd.SetArgs([]string{"task-abc"})

	if err := showCmd.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ID:          task-abcdef12", "Title:       ship", "Description: details", "Worktree:    /repo/agx/.worktrees/task"} {
		if !strings.Contains(showOut.String(), want) {
			t.Fatalf("task show output missing %q:\n%s", want, showOut.String())
		}
	}

	deleteCmd := newRuntimeClientTaskDeleteCmd(client)
	var deleteOut bytes.Buffer
	deleteCmd.SetOut(&deleteOut)
	deleteCmd.SetErr(&deleteOut)
	deleteCmd.SetArgs([]string{"task-abc"})

	if err := deleteCmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if client.deletedTaskID != "task-abcdef12" {
		t.Fatalf("deleted task id = %q, want resolved full task ID", client.deletedTaskID)
	}
	if !strings.Contains(deleteOut.String(), "deleted task-abc") {
		t.Fatalf("delete output = %q, want short deleted summary", deleteOut.String())
	}
}

func TestRuntimeClientPsListsMonitorTasksAndAllTasks(t *testing.T) {
	client := &fakeRuntimeTaskCreateClient{
		projects: []agxruntime.Project{{ID: "project-1", Name: "AGX", Path: "/repo/agx"}},
		monitorTasks: []agxruntime.MonitorTask{{
			ProjectName: "AGX",
			Task:        agxruntime.Task{ID: "task-monitor", ProjectID: "project-1", Title: "monitor", Agent: "codex", Status: db.StatusActive},
		}},
		tasks: []agxruntime.Task{{ID: "task-all", ProjectID: "project-1", Title: "all", Agent: "codex", Status: db.StatusComplete}},
	}

	monitorCmd := newRuntimeClientPsCmd(client)
	var monitorOut bytes.Buffer
	monitorCmd.SetOut(&monitorOut)
	monitorCmd.SetErr(&monitorOut)
	if err := monitorCmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(monitorOut.String(), "monitor") || !strings.Contains(monitorOut.String(), "AGX") {
		t.Fatalf("ps monitor output = %q, want monitor task with project", monitorOut.String())
	}

	allCmd := newRuntimeClientPsCmd(client)
	var allOut bytes.Buffer
	allCmd.SetOut(&allOut)
	allCmd.SetErr(&allOut)
	allCmd.SetArgs([]string{"--all"})
	if err := allCmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(allOut.String(), "all") || !strings.Contains(allOut.String(), "complete") {
		t.Fatalf("ps --all output = %q, want all task", allOut.String())
	}
}

func TestRuntimeClientChatCommandsUseRuntimeStatus(t *testing.T) {
	var gotToken string
	var gotGuild string
	var gotAllowedUser string
	client := &fakeRuntimeTaskCreateClient{
		discordStatus: agxdiscord.Status{
			Enabled:        true,
			Connected:      true,
			GuildID:        "guild-1",
			GuildName:      "AGX Guild",
			AllowedUserIDs: []string{"user-1"},
		},
		discordConnect: func(_ context.Context, token, guildID, allowedUserID string) (agxdiscord.Status, error) {
			gotToken = token
			gotGuild = guildID
			gotAllowedUser = allowedUserID
			return agxdiscord.Status{GuildID: guildID}, nil
		},
	}

	connectCmd := newRuntimeClientChatConnectCmd(client)
	var connectOut bytes.Buffer
	connectCmd.SetOut(&connectOut)
	connectCmd.SetErr(&connectOut)
	connectCmd.SetArgs([]string{"--token", "token-1", "--guild", "guild-1", "--allow-user", "user-1", "--allow-user", "ignored"})
	if err := connectCmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if gotToken != "token-1" || gotGuild != "guild-1" || gotAllowedUser != "user-1" {
		t.Fatalf("DiscordConnect args = (%q, %q, %q), want first configured user", gotToken, gotGuild, gotAllowedUser)
	}
	if !strings.Contains(connectOut.String(), "discord enabled for guild guild-1") {
		t.Fatalf("connect output = %q", connectOut.String())
	}

	statusCmd := newRuntimeClientChatStatusCmd(client)
	var statusOut bytes.Buffer
	statusCmd.SetOut(&statusOut)
	statusCmd.SetErr(&statusOut)
	if err := statusCmd.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"enabled: true", "connected: true", "guild: guild-1", "guild name: AGX Guild", "allowed user: user-1"} {
		if !strings.Contains(statusOut.String(), want) {
			t.Fatalf("status output missing %q:\n%s", want, statusOut.String())
		}
	}

	syncCmd := newRuntimeClientChatSyncCmd(client)
	var syncOut bytes.Buffer
	syncCmd.SetOut(&syncOut)
	syncCmd.SetErr(&syncOut)
	if err := syncCmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !client.discordSoftSynced || !strings.Contains(syncOut.String(), "discord synced with AGX Guild") {
		t.Fatalf("sync output = %q, synced=%v", syncOut.String(), client.discordSoftSynced)
	}

	disconnectCmd := newRuntimeClientChatDisconnectCmd(client)
	var disconnectOut bytes.Buffer
	disconnectCmd.SetOut(&disconnectOut)
	disconnectCmd.SetErr(&disconnectOut)
	if err := disconnectCmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !client.discordDisconnected || !strings.Contains(disconnectOut.String(), "discord disabled") {
		t.Fatalf("disconnect output = %q, disconnected=%v", disconnectOut.String(), client.discordDisconnected)
	}
}

func TestRuntimeClientDiscordCommandKeepsChatAlias(t *testing.T) {
	client := &fakeRuntimeTaskCreateClient{}
	cmd := newRuntimeClientDiscordCmd(client)
	if cmd.Use != "discord" {
		t.Fatalf("Use = %q, want discord", cmd.Use)
	}
	aliasFound := false
	for _, alias := range cmd.Aliases {
		if alias == "chat" {
			aliasFound = true
			break
		}
	}
	if !aliasFound {
		t.Fatalf("aliases = %#v, want chat alias", cmd.Aliases)
	}
	for _, args := range [][]string{{"connect"}, {"disconnect"}, {"status"}, {"sync"}} {
		if _, _, err := cmd.Find(args); err != nil {
			t.Fatalf("discord command missing %v: %v", args, err)
		}
	}
}

func TestLaunchCommandParsesOptions(t *testing.T) {
	var got launchOptions
	cmd := newLaunchCmdWithRunner(func(ctx context.Context, cmd *cobra.Command, opts launchOptions) error {
		got = opts
		return nil
	})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{
		"--platform", "windows",
		"--discord-token", "token-1",
		"--discord-server-id", "guild-1",
		"--allow-user", "user-1",
		"--skip-discord-sync",
		"--wait", "3s",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if got.Platform != "windows" || got.DiscordToken != "token-1" || got.DiscordGuildID != "guild-1" || got.DiscordAllowedUserID != "user-1" || !got.SkipDiscordSync || got.Wait != 3*time.Second {
		t.Fatalf("launch options = %#v", got)
	}
}

func TestNormalizeLaunchPlatform(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		goos    string
		want    string
		wantErr string
	}{
		{name: "auto linux", goos: "linux", want: "linux"},
		{name: "auto macos", goos: "darwin", want: "macos"},
		{name: "auto windows", goos: "windows", want: "windows"},
		{name: "windows from windows", value: "windows", goos: "windows", want: "windows"},
		{name: "windows from linux rejected", value: "windows", goos: "linux", wantErr: "native Windows"},
		{name: "windows from macos rejected", value: "windows", goos: "darwin", wantErr: "native Windows"},
		{name: "linux typo rejected", value: "lunux", goos: "linux", wantErr: "unsupported launch platform"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeLaunchPlatform(tt.value, tt.goos)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("normalizeLaunchPlatform() error = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("normalizeLaunchPlatform() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNormalizeLaunchWait(t *testing.T) {
	wait, err := normalizeLaunchWait(5 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if wait != 5*time.Second {
		t.Fatalf("normalizeLaunchWait() = %s, want 5s", wait)
	}
	for _, value := range []time.Duration{0, -time.Second} {
		if _, err := normalizeLaunchWait(value); err == nil || !strings.Contains(err.Error(), "--wait") {
			t.Fatalf("normalizeLaunchWait(%s) error = %v, want wait validation error", value, err)
		}
	}
}

func TestLaunchDiscordInputsUseConfigAndEnv(t *testing.T) {
	t.Setenv("DISCORD_BOT_TOKEN", "env-token")
	token, guildID, allowedUserID, err := launchDiscordInputs(launchOptions{}, config.Config{
		Discord: config.DiscordConfig{
			BotToken:       "config-token",
			GuildID:        "guild-1",
			AllowedUserIDs: []string{" ", "user-1"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if token != "env-token" || guildID != "guild-1" || allowedUserID != "user-1" {
		t.Fatalf("launchDiscordInputs() = (%q, %q, %q)", token, guildID, allowedUserID)
	}
}

func TestLaunchDiscordInputsUseConfigTokenWhenEnvMissing(t *testing.T) {
	t.Setenv("DISCORD_BOT_TOKEN", "")
	token, guildID, allowedUserID, err := launchDiscordInputs(launchOptions{}, config.Config{
		Discord: config.DiscordConfig{
			Enabled:        false,
			BotToken:       "config-token",
			GuildID:        "guild-1",
			AllowedUserIDs: []string{"user-1"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if token != "config-token" || guildID != "guild-1" || allowedUserID != "user-1" {
		t.Fatalf("launchDiscordInputs() = (%q, %q, %q)", token, guildID, allowedUserID)
	}
}

func TestLaunchDiscordInputsFlagsOverrideEnvAndConfig(t *testing.T) {
	t.Setenv("DISCORD_BOT_TOKEN", "env-token")
	token, guildID, allowedUserID, err := launchDiscordInputs(launchOptions{
		DiscordToken:         "flag-token",
		DiscordGuildID:       "flag-guild",
		DiscordAllowedUserID: "flag-user",
	}, config.Config{
		Discord: config.DiscordConfig{
			BotToken:       "config-token",
			GuildID:        "config-guild",
			AllowedUserIDs: []string{"config-user"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if token != "flag-token" || guildID != "flag-guild" || allowedUserID != "flag-user" {
		t.Fatalf("launchDiscordInputs() = (%q, %q, %q)", token, guildID, allowedUserID)
	}
}

func TestLaunchDiscordInputsRequireTokenAndIDs(t *testing.T) {
	_, _, _, err := launchDiscordInputs(launchOptions{}, config.Config{})
	if err == nil || !strings.Contains(err.Error(), "bot token") {
		t.Fatalf("launchDiscordInputs() error = %v, want bot token error", err)
	}

	_, _, _, err = launchDiscordInputs(launchOptions{DiscordToken: "token"}, config.Config{})
	if err == nil || !strings.Contains(err.Error(), "guild id") {
		t.Fatalf("launchDiscordInputs() error = %v, want guild id error", err)
	}

	_, _, _, err = launchDiscordInputs(launchOptions{DiscordToken: "token", DiscordGuildID: "guild"}, config.Config{})
	if err == nil || !strings.Contains(err.Error(), "allowed Discord user") {
		t.Fatalf("launchDiscordInputs() error = %v, want allowed user error", err)
	}
}

func TestDoctorCommandPrintsOfflineDiagnostics(t *testing.T) {
	t.Setenv("AGX_CONFIG_DIR", t.TempDir())
	cmd := newDoctorCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{
		"runtime: not running",
		"config dir:",
		"database:",
		"default agent:",
		"discord enabled:",
		"PATH:",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("doctor output missing %q:\n%s", want, text)
		}
	}
}

func TestCompactPath(t *testing.T) {
	short := "/a:/b:/c"
	if got := compactPath(short); got != short {
		t.Fatalf("compactPath(short) = %q, want unchanged", got)
	}
	long := "/1:/2:/3:/4:/5:/6:/7:/8:/9"
	if got, want := compactPath(long), "/1:/2:/3:/4:/5:/6:/7:/8:..."; got != want {
		t.Fatalf("compactPath(long) = %q, want %q", got, want)
	}
}
