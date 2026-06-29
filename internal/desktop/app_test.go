package desktop

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/nashory/agx/internal/config"
	"github.com/nashory/agx/internal/db"
	agxdiscord "github.com/nashory/agx/internal/discord"
	agxruntime "github.com/nashory/agx/internal/runtime"
	"github.com/nashory/agx/internal/session"
)

func TestExecutableSiblingCLIRequiresExecutableAgx(t *testing.T) {
	dir := t.TempDir()
	desktopPath := filepath.Join(dir, "AGXDesktop")
	if err := os.WriteFile(desktopPath, []byte("desktop"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, ok := executableSiblingCLI(desktopPath); ok {
		t.Fatal("executableSiblingCLI found missing agx sibling")
	}
	agxPath := filepath.Join(dir, "agx")
	if err := os.WriteFile(agxPath, []byte("cli"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := executableSiblingCLI(desktopPath); ok {
		t.Fatal("executableSiblingCLI found non-executable agx sibling")
	}
	if runtime.GOOS == "windows" {
		t.Skip("Unix executable bits are not meaningful on Windows")
	}
	if err := os.Chmod(agxPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, ok := executableSiblingCLI(desktopPath); !ok || got != agxPath {
		t.Fatalf("executableSiblingCLI = %q, %t; want %q, true", got, ok, agxPath)
	}
}

func TestProjectAndTaskDTOs(t *testing.T) {
	app, project := newTestApp(t)

	if _, err := app.store.CreateTask(project.ID, "offline task", nil, "claude", db.StatusOffline); err != nil {
		t.Fatal(err)
	}
	if _, err := app.store.CreateTask(project.ID, "active task", nil, "claude", db.StatusActive); err != nil {
		t.Fatal(err)
	}
	if _, err := app.store.CreateTask(project.ID, "complete task", nil, "claude", db.StatusComplete); err != nil {
		t.Fatal(err)
	}

	projects, err := app.ListProjects()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(projects), 1; got != want {
		t.Fatalf("len(projects) = %d, want %d", got, want)
	}
	if got, want := projects[0].TaskCount, 3; got != want {
		t.Fatalf("TaskCount = %d, want %d", got, want)
	}
	if got, want := projects[0].ActiveCount, 1; got != want {
		t.Fatalf("ActiveCount = %d, want %d", got, want)
	}
	if got, want := projects[0].CompleteCount, 1; got != want {
		t.Fatalf("CompleteCount = %d, want %d", got, want)
	}
	if got, want := projects[0].OfflineCount, 1; got != want {
		t.Fatalf("OfflineCount = %d, want %d", got, want)
	}

	tasks, err := app.ListTasks(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(tasks), 3; got != want {
		t.Fatalf("len(tasks) = %d, want %d", got, want)
	}
	var offlineStatus string
	for _, task := range tasks {
		if task.Status == string(db.StatusOffline) {
			offlineStatus = task.Status
		}
	}
	if offlineStatus != string(db.StatusOffline) {
		t.Fatalf("offline task status = %q, want offline", offlineStatus)
	}
}

func TestNewAppDoesNotOpenRuntimeDatabase(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("AGX_CONFIG_DIR", configDir)
	app, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	if app.store != nil {
		t.Fatal("NewApp opened a desktop store; runtime should own the database")
	}
	if app.tmux != nil {
		t.Fatal("NewApp created a tmux controller; runtime should own tmux")
	}
	if app.agentEvents != nil {
		t.Fatal("NewApp created agent event service; runtime should own agent streams")
	}
	if err := app.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(configDir, "agx.db")); !os.IsNotExist(err) {
		t.Fatalf("agx.db exists after NewApp: %v", err)
	}
}

func TestRuntimeConfigUsesRuntimeClient(t *testing.T) {
	app := NewAppWithStore(nil)
	client := &fakeRuntimeClient{
		configFunc: func(context.Context) (agxruntime.RuntimeConfig, error) {
			return agxruntime.RuntimeConfig{
				DefaultAgent: "gemini",
				VoiceSTT:     agxruntime.VoiceSTTConfig{Mode: config.VoiceSTTEnabled, Language: "ko", Timeout: "90s"},
			}, nil
		},
	}
	withFakeRuntimeClient(t, client)

	cfg, err := app.RuntimeConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultAgent != "gemini" {
		t.Fatalf("DefaultAgent = %q, want gemini", cfg.DefaultAgent)
	}
	if cfg.VoiceSTT.Mode != config.VoiceSTTEnabled || cfg.VoiceSTT.Language != "ko" || cfg.VoiceSTT.Timeout != "90s" {
		t.Fatalf("VoiceSTT = %#v, want runtime config", cfg.VoiceSTT)
	}
}

func TestUpdateDefaultAgentUsesRuntimeClient(t *testing.T) {
	app := NewAppWithStore(nil)
	var gotAgent string
	client := &fakeRuntimeClient{
		updateDefaultAgentFunc: func(_ context.Context, agentName string) (agxruntime.RuntimeConfig, error) {
			gotAgent = agentName
			return agxruntime.RuntimeConfig{DefaultAgent: agentName}, nil
		},
	}
	withFakeRuntimeClient(t, client)

	cfg, err := app.UpdateDefaultAgent("codex")
	if err != nil {
		t.Fatal(err)
	}
	if gotAgent != "codex" || cfg.DefaultAgent != "codex" {
		t.Fatalf("UpdateDefaultAgent = (%q, %#v), want codex", gotAgent, cfg)
	}
}

func TestUpdateVoiceSTTUsesRuntimeClient(t *testing.T) {
	app := NewAppWithStore(nil)
	var got agxruntime.VoiceSTTConfig
	client := &fakeRuntimeClient{
		updateVoiceSTTFunc: func(_ context.Context, voice agxruntime.VoiceSTTConfig) (agxruntime.RuntimeConfig, error) {
			got = voice
			return agxruntime.RuntimeConfig{DefaultAgent: "codex", VoiceSTT: agxruntime.VoiceSTTConfig{Mode: config.VoiceSTTEnabled, Language: "ko", Timeout: "90s"}}, nil
		},
	}
	withFakeRuntimeClient(t, client)

	cfg, err := app.UpdateVoiceSTT("enabled", "ffmpeg", "whisper-cli", "model.bin", "ko", "90s")
	if err != nil {
		t.Fatal(err)
	}
	if got.Mode != config.VoiceSTTEnabled || got.FFmpegPath != "ffmpeg" || got.WhisperPath != "whisper-cli" || got.ModelPath != "model.bin" || got.Language != "ko" || got.Timeout != "90s" {
		t.Fatalf("UpdateVoiceSTT args = %#v, want provided config", got)
	}
	if cfg.VoiceSTT.Mode != config.VoiceSTTEnabled || cfg.VoiceSTT.Language != "ko" || cfg.VoiceSTT.Timeout != "90s" {
		t.Fatalf("UpdateVoiceSTT result = %#v, want runtime config", cfg)
	}
}

func TestDiscordConnectTrimsInputsAndReturnsStatusOnError(t *testing.T) {
	app := NewAppWithStore(nil)
	var token, guildID, allowedUserID string
	client := &fakeRuntimeClient{
		discordStatusFunc: func(context.Context) (agxdiscord.Status, error) {
			return agxdiscord.Status{Enabled: false, Connected: false, Error: "missing token"}, nil
		},
		discordConnectFunc: func(_ context.Context, nextToken, nextGuildID, nextAllowedUserID string) (agxdiscord.Status, error) {
			token = nextToken
			guildID = nextGuildID
			allowedUserID = nextAllowedUserID
			return agxdiscord.Status{}, errors.New("discord bot token is required")
		},
	}
	withFakeRuntimeClient(t, client)

	status, err := app.DiscordConnect(" token ", " guild ", " user ")
	if err == nil {
		t.Fatal("DiscordConnect() error = nil, want runtime error")
	}
	if token != "token" || guildID != "guild" || allowedUserID != "user" {
		t.Fatalf("connect args = (%q, %q, %q), want trimmed values", token, guildID, allowedUserID)
	}
	if status.Connected || status.Error != "missing token" {
		t.Fatalf("status = %#v, want fallback DiscordStatus on connect error", status)
	}
}

func TestCreateTaskNoPromptPassesWorkspaceModeToRuntime(t *testing.T) {
	app := NewAppWithStore(nil)
	var gotInitialPrompt *string
	var gotWorkspaceMode db.WorkspaceMode
	client := &fakeRuntimeClient{
		runNewTaskWithInitialPromptWorkspaceFunc: func(_ context.Context, projectID, title string, description *string, agentName string, allMighty bool, initialPrompt *string, workspaceMode db.WorkspaceMode) (agxruntime.Task, error) {
			gotInitialPrompt = initialPrompt
			gotWorkspaceMode = workspaceMode
			return agxruntime.Task{
				ID:            "task-1",
				ProjectID:     projectID,
				Title:         title,
				Agent:         agentName,
				AllMighty:     allMighty,
				WorkspaceMode: string(workspaceMode),
				Status:        db.StatusOffline,
				CreatedAt:     time.Now(),
				UpdatedAt:     time.Now(),
			}, nil
		},
	}
	withFakeRuntimeClient(t, client)

	task, err := app.CreateTaskNoPrompt("project-1", "  task title  ", "codex", true, string(db.WorkspaceModeProject))
	if err != nil {
		t.Fatal(err)
	}
	if gotInitialPrompt == nil || *gotInitialPrompt != "" {
		t.Fatalf("initial prompt = %#v, want explicit empty prompt", gotInitialPrompt)
	}
	if gotWorkspaceMode != db.WorkspaceModeProject || task.WorkspaceMode != string(db.WorkspaceModeProject) {
		t.Fatalf("workspace mode = (%q, %q), want project", gotWorkspaceMode, task.WorkspaceMode)
	}
}

func TestDiscordTaskSyncTrimsTaskID(t *testing.T) {
	app := NewAppWithStore(nil)
	var gotTaskID string
	client := &fakeRuntimeClient{
		discordTaskSyncFunc: func(_ context.Context, taskID string) (agxdiscord.Status, error) {
			gotTaskID = taskID
			return agxdiscord.Status{Enabled: true, Connected: true, GuildID: "guild"}, nil
		},
	}
	withFakeRuntimeClient(t, client)

	status, err := app.DiscordTaskSync(" task-1 ")
	if err != nil {
		t.Fatal(err)
	}
	if gotTaskID != "task-1" {
		t.Fatalf("taskID = %q, want trimmed task-1", gotTaskID)
	}
	if !status.Connected || status.GuildID != "guild" {
		t.Fatalf("status = %#v, want synced Discord status", status)
	}
}

func TestCreateTaskUsesProjectDefaultAgent(t *testing.T) {
	app, project := newTestApp(t)
	agentName := "codex"
	if err := app.store.UpdateProjectDefaultAgent(project.ID, &agentName); err != nil {
		t.Fatal(err)
	}

	task, err := app.CreateTask(project.ID, "new task", "details", "", false, string(db.WorkspaceModeWorktree))
	if err != nil {
		t.Fatal(err)
	}
	if task.Agent != "codex" {
		t.Fatalf("Agent = %q, want codex", task.Agent)
	}
	if task.Description == nil || *task.Description != "details" {
		t.Fatalf("Description = %#v, want details", task.Description)
	}
}

func TestDetectAndStoreStatusDoesNotClearSessionOnOfflineRefresh(t *testing.T) {
	app, project := newTestApp(t)
	sessionName := "task-missing"
	task, err := app.store.CreateTaskWithSession("task-missing-window", project.ID, "missing window", nil, "claude", db.StatusActive, &sessionName)
	if err != nil {
		t.Fatal(err)
	}

	status := app.detectAndStoreStatus(task)
	if status != db.StatusOffline {
		t.Fatalf("status = %s, want offline", status)
	}
	refreshed, err := app.store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.SessionName == nil || *refreshed.SessionName != sessionName {
		t.Fatalf("SessionName = %#v, want preserved %q", refreshed.SessionName, sessionName)
	}
	if refreshed.Status != db.StatusOffline {
		t.Fatalf("Status = %s, want offline", refreshed.Status)
	}
}

func TestListMonitorTasksIncludesSessionTasks(t *testing.T) {
	app, project := newTestApp(t)
	sessionName := "task-live"
	if _, err := app.store.CreateTaskWithSession("task-live-id", project.ID, "live task", nil, "claude", db.StatusActive, &sessionName); err != nil {
		t.Fatal(err)
	}
	if _, err := app.store.CreateTask(project.ID, "offline task", nil, "claude", db.StatusOffline); err != nil {
		t.Fatal(err)
	}

	tasks, err := app.ListMonitorTasks()
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("len(tasks) = %d, want 1", len(tasks))
	}
	if tasks[0].ID != "task-live-id" || tasks[0].SessionName == nil || *tasks[0].SessionName != sessionName {
		t.Fatalf("monitor task = %#v, want live task with session", tasks[0])
	}
	if tasks[0].ProjectName != project.Name || tasks[0].ProjectPath != project.Path {
		t.Fatalf("project fields = (%q, %q), want (%q, %q)", tasks[0].ProjectName, tasks[0].ProjectPath, project.Name, project.Path)
	}
}

func TestListMonitorTasksIncludesStructuredRuntimeTasks(t *testing.T) {
	app, project := newTestApp(t)
	task, err := app.store.CreateTask(project.ID, "structured task", nil, "codex", db.StatusActive)
	if err != nil {
		t.Fatal(err)
	}
	threadID := "thread-1"
	streamKind := "codex-app-server"
	if err := app.store.UpdateTaskAgentStream(task.ID, &threadID, nil, &streamKind); err != nil {
		t.Fatal(err)
	}

	tasks, err := app.ListMonitorTasks()
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("len(tasks) = %d, want 1", len(tasks))
	}
	if tasks[0].ID != task.ID || tasks[0].AgentStreamKind == nil || *tasks[0].AgentStreamKind != streamKind {
		t.Fatalf("monitor task = %#v, want structured task", tasks[0])
	}
	status, err := app.GetTaskStatus(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if status != string(db.StatusActive) {
		t.Fatalf("status = %q, want active", status)
	}
}

func TestDiscordStatusDefaultsDisabled(t *testing.T) {
	app, _ := newTestApp(t)

	status := app.DiscordStatus()
	if status.Enabled || status.Connected {
		t.Fatalf("DiscordStatus = %#v, want disabled and disconnected", status)
	}
}

func TestDiscordConnectRequiresAllowlistBeforeStarting(t *testing.T) {
	app, _ := newTestApp(t)

	status, err := app.DiscordConnect("token", "guild", "")
	if err == nil {
		t.Fatal("DiscordConnect() error = nil, want allowlist validation error")
	}
	if status.Connected {
		t.Fatalf("Connected = true after failed connect")
	}
}

func TestRegisterProject(t *testing.T) {
	t.Setenv("AGX_DESKTOP_DIRECT_TEST", "1")
	requireDesktopDirectMode(t)
	store, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	root := t.TempDir()
	initDesktopGitRepo(t, root)
	app := NewAppWithStore(store)

	project, err := app.RegisterProject(root, "AGX Test", "local test project")
	if err != nil {
		t.Fatal(err)
	}
	if project.Name != "AGX Test" {
		t.Fatalf("Name = %q, want AGX Test", project.Name)
	}
	if project.Description == nil || *project.Description != "local test project" {
		t.Fatalf("Description = %#v, want local test project", project.Description)
	}

	project, err = app.RegisterProject(root, "Renamed", "")
	if err != nil {
		t.Fatal(err)
	}
	if project.Name != "Renamed" {
		t.Fatalf("Name after re-register = %q, want Renamed", project.Name)
	}
	if project.Description != nil {
		t.Fatalf("Description after re-register = %#v, want nil", project.Description)
	}
}

func TestRegisterProjectAllowsRepositoryWithoutWorktreeHead(t *testing.T) {
	t.Setenv("AGX_DESKTOP_DIRECT_TEST", "1")
	requireDesktopDirectMode(t)
	store, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	root := t.TempDir()
	runCommand(t, root, "git", "init", "-q")
	app := NewAppWithStore(store)

	project, err := app.RegisterProject(root, "No Head", "")
	if err != nil {
		t.Fatal(err)
	}
	if project.AccessGranted {
		t.Fatal("AccessGranted = true, want false until access is granted")
	}
	if project.AccessError == nil || !strings.Contains(*project.AccessError, "Grant access") {
		t.Fatalf("AccessError = %#v, want grant access prompt", project.AccessError)
	}
	if _, err := app.GrantProjectAccess(project.ID); err == nil || !strings.Contains(err.Error(), "git repository has no commits") {
		t.Fatalf("GrantProjectAccess error = %v, want no commits guidance", err)
	}
}

func TestProjectAccessGrantPersistsAcrossReRegistration(t *testing.T) {
	app, project := newTestApp(t)
	first, err := app.GetProject(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if first.AccessGranted {
		t.Fatal("AccessGranted = true before explicit grant")
	}
	granted, err := app.GrantProjectAccess(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !granted.AccessGranted {
		t.Fatalf("AccessGranted = false after grant: %#v", granted.AccessError)
	}
	if err := app.DeleteProject(project.ID); err != nil {
		t.Fatal(err)
	}
	recreated, err := app.RegisterProject(project.Path, project.Name, "")
	if err != nil {
		t.Fatal(err)
	}
	if !recreated.AccessGranted {
		t.Fatalf("AccessGranted = false after re-registering granted path: %#v", recreated.AccessError)
	}
}

func TestListDirectoriesSortsChildDirectories(t *testing.T) {
	app, _ := newTestApp(t)
	root := t.TempDir()
	for _, name := range []string{"zeta", "Alpha", "middle"} {
		if err := os.Mkdir(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("ignored"), 0o644); err != nil {
		t.Fatal(err)
	}

	entries, err := app.ListDirectories(root)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name)
		if !strings.HasPrefix(entry.Path, root) {
			t.Fatalf("entry path = %q, want under %q", entry.Path, root)
		}
	}
	if got, want := strings.Join(names, ","), "Alpha,middle,zeta"; got != want {
		t.Fatalf("directories = %q, want %q", got, want)
	}
}

func TestValidateProjectDirectoryReturnsRegisteredCandidate(t *testing.T) {
	app, project := newTestApp(t)
	writeFile(t, project.Path, "cmd/app/main.go", "package main\n")
	runCommand(t, project.Path, "git", "add", "cmd/app/main.go")
	description := "registered description"
	if err := app.store.UpdateProjectDetails(project.ID, "Registered Name", &description); err != nil {
		t.Fatal(err)
	}

	candidate, err := app.ValidateProjectDirectory(filepath.Join(project.Path, "cmd"))
	if err != nil {
		t.Fatal(err)
	}
	if !candidate.IsRegistered {
		t.Fatal("IsRegistered = false, want true")
	}
	if candidate.Name != "Registered Name" || candidate.Description != description {
		t.Fatalf("candidate = %#v, want registered project metadata", candidate)
	}
	if !sameProjectPath(candidate.Path, project.Path) {
		t.Fatalf("Path = %q, want %q", candidate.Path, project.Path)
	}
	if len(candidate.Languages) == 0 || candidate.Languages[0].Name != "Go" {
		t.Fatalf("Languages = %#v, want Go detected", candidate.Languages)
	}
}

func TestUpdateProjectAndTaskMetadata(t *testing.T) {
	app, project := newTestApp(t)
	updatedProject, err := app.UpdateProject(project.ID, "Renamed Project", "")
	if err != nil {
		t.Fatal(err)
	}
	if updatedProject.Name != "Renamed Project" {
		t.Fatalf("project name = %q, want Renamed Project", updatedProject.Name)
	}
	if updatedProject.Description != nil {
		t.Fatalf("project description = %#v, want nil", updatedProject.Description)
	}

	task, err := app.store.CreateTask(project.ID, "old title", nil, "claude", db.StatusOffline)
	if err != nil {
		t.Fatal(err)
	}
	updatedTask, err := app.UpdateTaskTitle(task.ID, "  new title  ")
	if err != nil {
		t.Fatal(err)
	}
	if updatedTask.Title != "new title" {
		t.Fatalf("task title = %q, want new title", updatedTask.Title)
	}
	if _, err := app.UpdateTaskTitle(task.ID, " "); err == nil {
		t.Fatal("UpdateTaskTitle blank error = nil, want error")
	}
}

func TestTaskTranscriptAndInputMetadata(t *testing.T) {
	app, project := newTestApp(t)
	task, err := app.store.CreateTask(project.ID, "transcript task", nil, "claude", db.StatusOffline)
	if err != nil {
		t.Fatal(err)
	}
	turnID := "turn-1"
	if err := app.store.AppendTaskTranscriptMessage(task.ID, "assistant", "hello", &turnID, nil); err != nil {
		t.Fatal(err)
	}
	messages, err := app.ListTaskTranscript(task.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].Body != "hello" || messages[0].TurnID == nil || *messages[0].TurnID != turnID {
		t.Fatalf("messages = %#v, want stored transcript message", messages)
	}

	if err := app.RecordTaskInput(task.ID, "next prompt"); err != nil {
		t.Fatal(err)
	}
	refreshed, err := app.store.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.LastUserPrompt == nil || *refreshed.LastUserPrompt != "next prompt" {
		t.Fatalf("LastUserPrompt = %#v, want next prompt", refreshed.LastUserPrompt)
	}
}

func TestListDirectorySortsAndHidesInternalEntries(t *testing.T) {
	app, project := newTestApp(t)
	writeFile(t, project.Path, "z.go", "package z\n")
	writeFile(t, project.Path, "a.go", "package a\n")
	writeFile(t, project.Path, ".hidden", "secret\n")
	writeFile(t, project.Path, ".gitignore", "ignored.txt\n")
	writeFile(t, project.Path, "ignored.txt", "ignored\n")
	if err := os.Mkdir(filepath.Join(project.Path, "src"), 0o755); err != nil {
		t.Fatal(err)
	}

	entries, err := app.ListDirectory(project.ID, ".", false)
	if err != nil {
		t.Fatal(err)
	}
	names := entryNames(entries)
	want := "src,a.go,README.md,z.go"
	if got := strings.Join(names, ","); got != want {
		t.Fatalf("entries = %s, want %s", got, want)
	}
}

func TestListDirectoryCanShowHiddenButStillSkipsIgnored(t *testing.T) {
	app, project := newTestApp(t)
	writeFile(t, project.Path, ".visible-hidden", "hidden\n")
	writeFile(t, project.Path, ".gitignore", "ignored.txt\n")
	writeFile(t, project.Path, "ignored.txt", "ignored\n")

	entries, err := app.ListDirectory(project.ID, ".", true)
	if err != nil {
		t.Fatal(err)
	}
	names := strings.Join(entryNames(entries), ",")
	if !strings.Contains(names, ".visible-hidden") {
		t.Fatalf("entries = %q, want hidden file when showHidden=true", names)
	}
	if strings.Contains(names, "ignored.txt") {
		t.Fatalf("entries = %q, want ignored file skipped", names)
	}
}

func TestReadFileRejectsPathTraversal(t *testing.T) {
	app, project := newTestApp(t)
	writeFile(t, project.Path, "ok.txt", "ok")

	if _, err := app.ReadFile(project.ID, "../outside.txt"); err == nil {
		t.Fatal("ReadFile traversal error = nil, want error")
	}
}

func TestReadFileRejectsSymlinkEscape(t *testing.T) {
	app, project := newTestApp(t)
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(project.Path, "outside-link.txt")); err != nil {
		t.Fatal(err)
	}

	if _, err := app.ReadFile(project.ID, "outside-link.txt"); err == nil {
		t.Fatal("ReadFile symlink escape error = nil, want error")
	}
}

func TestReadFileRejectsLargeAndBinaryFiles(t *testing.T) {
	app, project := newTestApp(t)
	if err := os.WriteFile(filepath.Join(project.Path, "large.txt"), make([]byte, maxReadFileBytes+1), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := app.ReadFile(project.ID, "large.txt"); err == nil {
		t.Fatal("ReadFile large file error = nil, want error")
	}

	if err := os.WriteFile(filepath.Join(project.Path, "binary.dat"), []byte{'a', 0, 'b'}, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := app.ReadFile(project.ID, "binary.dat"); err == nil {
		t.Fatal("ReadFile binary file error = nil, want error")
	}
}

func TestTaskFileAPIsUseWorktreeRoot(t *testing.T) {
	app, project := newTestApp(t)
	writeFile(t, project.Path, "workspace/original.txt", "project root\n")
	worktree := t.TempDir()
	initDesktopGitRepo(t, worktree)
	writeFile(t, worktree, "workspace/task-only.txt", "task worktree\n")
	task, err := app.store.CreateTaskRuntime("task-worktree", project.ID, "worktree task", nil, "codex", db.StatusActive, nil, &worktree, nil)
	if err != nil {
		t.Fatal(err)
	}

	entries, err := app.ListTaskDirectory(task.ID, "workspace", false)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(entryNames(entries), ","), "task-only.txt"; got != want {
		t.Fatalf("task directory entries = %q, want %q", got, want)
	}

	contents, err := app.ReadTaskFile(task.ID, "workspace/task-only.txt")
	if err != nil {
		t.Fatal(err)
	}
	if contents != "task worktree\n" {
		t.Fatalf("task file contents = %q, want task worktree contents", contents)
	}

	matches, err := app.SearchTaskFiles(task.ID, "task-only", 10)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(matches, ","), "workspace/task-only.txt"; got != want {
		t.Fatalf("task search matches = %q, want %q", got, want)
	}

	prompt, err := app.ComposeTaskPromptWithFiles(task.ID, "use this context", []string{"workspace/task-only.txt"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "task worktree") || strings.Contains(prompt, "project root") {
		t.Fatalf("task prompt = %q, want task worktree context only", prompt)
	}
}

func TestTaskFileAPIsFailWhenStoredWorktreeIsMissing(t *testing.T) {
	app, project := newTestApp(t)
	writeFile(t, project.Path, "workspace/original.txt", "project root\n")
	missingWorktree := filepath.Join(t.TempDir(), "missing-task-worktree")
	task, err := app.store.CreateTaskRuntime("task-missing-worktree", project.ID, "missing worktree", nil, "codex", db.StatusWaiting, nil, &missingWorktree, nil)
	if err != nil {
		t.Fatal(err)
	}

	checks := map[string]error{}
	_, checks["ListTaskDirectory"] = app.ListTaskDirectory(task.ID, "workspace", false)
	_, checks["ReadTaskFile"] = app.ReadTaskFile(task.ID, "workspace/original.txt")
	_, checks["SearchTaskFiles"] = app.SearchTaskFiles(task.ID, "original", 10)
	_, checks["ComposeTaskPromptWithFiles"] = app.ComposeTaskPromptWithFiles(task.ID, "use this context", []string{"workspace/original.txt"}, true)
	for name, err := range checks {
		if err == nil || !strings.Contains(err.Error(), "task worktree is unavailable") || !strings.Contains(err.Error(), missingWorktree) {
			t.Fatalf("%s error = %v, want missing task worktree path", name, err)
		}
	}
}

func TestSearchFilesAndComposePrompt(t *testing.T) {
	app, project := newTestApp(t)
	writeFile(t, project.Path, "src/auth.go", "package src\n")
	writeFile(t, project.Path, "src/http.go", "package src\n")
	writeFile(t, project.Path, ".gitignore", "ignored_auth.go\n")
	writeFile(t, project.Path, "ignored_auth.go", "package ignored\n")
	writeFile(t, project.Path, ".agx/secret.txt", "secret\n")

	matches, err := app.SearchFiles(project.ID, "auth", 10)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(matches, ","), "src/auth.go"; got != want {
		t.Fatalf("matches = %q, want %q", got, want)
	}

	prompt := app.ComposePrompt("implement auth", []string{"src/auth.go", "src/auth.go", "src/http.go"})
	if !strings.Contains(prompt, "src/auth.go, src/http.go") {
		t.Fatalf("prompt = %q, want context paths", prompt)
	}

	prompt, err = app.ComposePromptWithFiles(project.ID, "implement auth", []string{"src/auth.go"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "--- src/auth.go ---") || !strings.Contains(prompt, "package src") {
		t.Fatalf("prompt with files = %q, want file contents", prompt)
	}

	writeFile(t, project.Path, "src/ranged.go", "line one\nline two\nline three\nline four\n")
	prompt, err = app.ComposePromptWithFiles(project.ID, "implement auth", []string{"src/ranged.go:L2-L3"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "--- src/ranged.go:L2-L3 ---") || !strings.Contains(prompt, "line two\nline three") || strings.Contains(prompt, "line one") || strings.Contains(prompt, "line four") {
		t.Fatalf("prompt with line range = %q, want selected lines only", prompt)
	}

	prompt, err = app.ComposePromptWithFiles(project.ID, "implement auth", []string{"src"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "--- src/auth.go ---") || !strings.Contains(prompt, "--- src/http.go ---") {
		t.Fatalf("prompt with directory = %q, want directory file contents", prompt)
	}
	if strings.Contains(prompt, "ignored_auth.go") || strings.Contains(prompt, ".agx/secret.txt") {
		t.Fatalf("prompt with directory = %q, want ignored/internal files skipped", prompt)
	}
}

func TestComposePromptWithFilesSkipsUnreadableDirectoryEntries(t *testing.T) {
	app, project := newTestApp(t)
	writeFile(t, project.Path, "src/text.go", "package src\n")
	if err := os.WriteFile(filepath.Join(project.Path, "src", "binary.go"), []byte{'p', 0, 'q'}, 0o644); err != nil {
		t.Fatal(err)
	}
	runCommand(t, project.Path, "git", "add", "src/text.go", "src/binary.go")

	prompt, err := app.ComposePromptWithFiles(project.ID, "use context", []string{"src"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "--- src/text.go ---") || !strings.Contains(prompt, "package src") {
		t.Fatalf("prompt = %q, want readable file contents", prompt)
	}
	if !strings.Contains(prompt, "--- src/binary.go ---") || !strings.Contains(prompt, "[skipped: file appears to be binary") {
		t.Fatalf("prompt = %q, want skipped binary marker", prompt)
	}
}

func TestSearchFilesUsesDefaultLimitAndRanking(t *testing.T) {
	app, project := newTestApp(t)
	for _, path := range []string{"src/auth.go", "src/auth_test.go", "docs/auth.md"} {
		writeFile(t, project.Path, path, "content\n")
	}
	runCommand(t, project.Path, "git", "add", "src/auth.go", "src/auth_test.go", "docs/auth.md")

	matches, err := app.SearchFiles(project.ID, "auth", 2)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(matches), 2; got != want {
		t.Fatalf("len(matches) = %d, want %d", got, want)
	}
	if matches[0] != "src/auth.go" {
		t.Fatalf("first match = %q, want best-ranked path src/auth.go", matches[0])
	}
	matches, err = app.SearchFiles(project.ID, "   ", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("blank query matches = %v, want none", matches)
	}
}

func TestCleanContextPaths(t *testing.T) {
	got := cleanContextPaths([]string{" src/auth.go ", "src/auth.go", "", "src/http.go"})
	want := []string{"src/auth.go", "src/http.go"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("cleanContextPaths() = %v, want %v", got, want)
	}
}

func TestReadAppendedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stream.log")
	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	data, offset, err := readAppendedFile(path, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if data != "hello" || offset != 5 {
		t.Fatalf("readAppendedFile initial = (%q, %d), want (hello, 5)", data, offset)
	}
	if err := os.WriteFile(path, []byte("hello world"), 0o600); err != nil {
		t.Fatal(err)
	}
	data, offset, err = readAppendedFile(path, offset, 0)
	if err != nil {
		t.Fatal(err)
	}
	if data != " world" || offset != 11 {
		t.Fatalf("readAppendedFile append = (%q, %d), want ( world, 11)", data, offset)
	}
	if err := os.WriteFile(path, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	data, offset, err = readAppendedFile(path, offset, 0)
	if err != nil {
		t.Fatal(err)
	}
	if data != "new" || offset != 3 {
		t.Fatalf("readAppendedFile truncated = (%q, %d), want (new, 3)", data, offset)
	}
}

func TestReadAppendedFileCompactsLargeStream(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stream.log")
	if err := os.WriteFile(path, []byte("0123456789"), 0o600); err != nil {
		t.Fatal(err)
	}
	data, offset, err := readAppendedFile(path, 10, 8)
	if err != nil {
		t.Fatal(err)
	}
	if data != "6789" || offset != 4 {
		t.Fatalf("readAppendedFile compacted = (%q, %d), want (6789, 4)", data, offset)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 4 {
		t.Fatalf("compacted size = %d, want 4", info.Size())
	}
}

func TestFuzzyScore(t *testing.T) {
	if _, ok := fuzzyScore("src/middleware/auth.go", "sma"); !ok {
		t.Fatal("fuzzyScore did not match ordered acronym")
	}
	if _, ok := fuzzyScore("src/middleware/auth.go", "zzz"); ok {
		t.Fatal("fuzzyScore matched missing query")
	}
}

func TestStreamPathAndCleanupTaskStreams(t *testing.T) {
	app, project := newTestApp(t)
	task, err := app.store.CreateTask(project.ID, "stream task", nil, "claude", db.StatusOffline)
	if err != nil {
		t.Fatal(err)
	}
	streamPath, err := app.streamPath("task/with spaces")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(streamPath, "task-with-spaces.log") {
		t.Fatalf("stream path = %q, want sanitized file name", streamPath)
	}
	actualTaskStream, err := app.streamPath(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(actualTaskStream, []byte("logs"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := app.cleanupTaskStreams([]db.Task{task}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(actualTaskStream); !os.IsNotExist(err) {
		t.Fatalf("stream file still exists: %v", err)
	}
}

func TestStopLogStreamCancelsOnlyTargetStream(t *testing.T) {
	app, _ := newTestApp(t)
	firstCtx, firstCancel := context.WithCancel(context.Background())
	secondCtx, secondCancel := context.WithCancel(context.Background())
	t.Cleanup(secondCancel)
	app.streams["task-1"] = firstCancel
	app.streams["task-2"] = secondCancel

	app.StopLogStream("task-1")

	select {
	case <-firstCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("task-1 stream was not canceled")
	}
	select {
	case <-secondCtx.Done():
		t.Fatal("task-2 stream was canceled")
	default:
	}
	if _, ok := app.streams["task-1"]; ok {
		t.Fatal("task-1 stream remained in map")
	}
	if _, ok := app.streams["task-2"]; !ok {
		t.Fatal("task-2 stream was removed")
	}
}

func TestStatusDTOs(t *testing.T) {
	app, _ := newTestApp(t)
	started := time.Unix(123, 0).UTC()
	runtimeStatus := runtimeStatusDTO(agxruntime.Status{
		Running:       true,
		PID:           42,
		Version:       "test-version",
		StartedAt:     started,
		UptimeSeconds: 9,
		ConfigDir:     "/tmp/agx",
		SocketPath:    "/tmp/agx.sock",
		LockPath:      "/tmp/agx.lock",
		Recovery:      session.RecoveryResult{Offline: 1, Cleared: 2, Orphans: 3},
	})
	if !runtimeStatus.Running || runtimeStatus.PID != 42 || runtimeStatus.StartedAt == nil || !runtimeStatus.StartedAt.Equal(started) {
		t.Fatalf("runtime status = %#v, want mapped runtime fields", runtimeStatus)
	}
	if runtimeStatus.Recovery.Orphans != 3 {
		t.Fatalf("runtime recovery = %#v, want mapped recovery result", runtimeStatus.Recovery)
	}

	now := time.Now()
	app.discordSyncJob = DiscordSyncJob{Running: true, Kind: "hard", Stage: "syncing", StartedAt: &now}
	discordStatus := app.discordStatusDTO(agxdiscord.Status{
		Enabled:        true,
		Connected:      true,
		GuildID:        "guild",
		GuildName:      "Guild",
		AllowedUserIDs: []string{"u1", "u2"},
		MaskedBotToken: "tok...",
		UptimeSeconds:  11,
		LockedBy:       "other",
	})
	if !discordStatus.Enabled || !discordStatus.Connected || discordStatus.Sync.Stage != "syncing" {
		t.Fatalf("discord status = %#v, want mapped status and sync job", discordStatus)
	}
	runtimeSync := app.discordStatusDTO(agxdiscord.Status{
		Sync: agxdiscord.SyncStatusSummary{Running: true, Kind: "hard", Stage: "runtime hard sync"},
	})
	if !runtimeSync.Sync.Running || runtimeSync.Sync.Stage != "runtime hard sync" {
		t.Fatalf("runtime sync status = %#v, want runtime sync job", runtimeSync.Sync)
	}
	discordStatus.AllowedUserIDs[0] = "mutated"
	if app.discordStatusDTO(agxdiscord.Status{AllowedUserIDs: []string{"u1"}}).AllowedUserIDs[0] != "u1" {
		t.Fatal("AllowedUserIDs was not defensively copied")
	}
}

func TestPathAndRepositoryHelpers(t *testing.T) {
	if got, want := appleScriptString(`a"b\c`), `a\"b\\c`; got != want {
		t.Fatalf("appleScriptString() = %q, want %q", got, want)
	}
	if got, want := shellQuote("a'b"), `'a'"'"'b'`; got != want {
		t.Fatalf("shellQuote() = %q, want %q", got, want)
	}
	if !shouldSkipRepoScanDir("node_modules") || !shouldSkipRepoScanDir(".cache") || shouldSkipRepoScanDir("src") {
		t.Fatal("shouldSkipRepoScanDir returned unexpected values")
	}
	if repoScanDepth("/tmp/root", "/tmp/root/a/b/c") != 3 {
		t.Fatal("repoScanDepth did not count relative path elements")
	}
	root := t.TempDir()
	initDesktopGitRepo(t, root)
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if !hasGitMetadata(root) {
		t.Fatal("hasGitMetadata(root) = false, want true")
	}
	same, err := sameGitRepository(root, nested)
	if err != nil {
		t.Fatal(err)
	}
	if !same {
		t.Fatal("sameGitRepository(root, nested) = false, want true")
	}
}

func TestLanguageForPath(t *testing.T) {
	cases := map[string]string{
		"Dockerfile": "Dockerfile",
		"Makefile":   "Makefile",
		"main.go":    "Go",
		"view.tsx":   "TypeScript",
		"script.py":  "Python",
		"Cargo.toml": "Config",
		"README.md":  "Markdown",
		"asset.png":  "",
	}
	for path, want := range cases {
		if got := languageForPath(path); got != want {
			t.Fatalf("languageForPath(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestShouldRetireCompletedShell(t *testing.T) {
	task := db.Task{Status: db.StatusComplete}
	snapshot := stateSnapshot{output: "done\n$ "}

	if !shouldRetireCompletedShell(task, snapshot, db.StatusComplete, "done\n$ exit\n") {
		t.Fatal("shouldRetireCompletedShell() = false, want true for changed completed shell output")
	}
	if shouldRetireCompletedShell(task, stateSnapshot{}, db.StatusComplete, "done\n$ ") {
		t.Fatal("shouldRetireCompletedShell() = true, want false without prior shell snapshot")
	}
	if shouldRetireCompletedShell(db.Task{Status: db.StatusActive}, snapshot, db.StatusComplete, "done\n$ exit\n") {
		t.Fatal("shouldRetireCompletedShell() = true, want false before task is already complete")
	}
}

func newTestApp(t *testing.T) (*App, db.Project) {
	t.Helper()
	t.Setenv("AGX_CONFIG_DIR", t.TempDir())
	t.Setenv("AGX_DESKTOP_DIRECT_TEST", "1")
	prependFakeAgentCLIs(t, "claude", "codex")
	requireDesktopDirectMode(t)
	store, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	root := t.TempDir()
	initDesktopGitRepo(t, root)
	project, err := store.EnsureProject(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	return NewAppWithStore(store), project
}

func prependFakeAgentCLIs(t *testing.T, names ...string) {
	t.Helper()
	binDir := t.TempDir()
	for _, name := range names {
		path := filepath.Join(binDir, name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\necho agx fake agent \"$@\"\nsleep 30\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func requireDesktopDirectMode(t *testing.T) {
	t.Helper()
	if !desktopDirectModeEnabled() {
		t.Skip("desktop direct mode is disabled for this build")
	}
}

func initDesktopGitRepo(t *testing.T, root string) {
	t.Helper()
	runCommand(t, root, "git", "init", "-q")
	runCommand(t, root, "git", "config", "user.email", "agx@example.com")
	runCommand(t, root, "git", "config", "user.name", "AGX Test")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runCommand(t, root, "git", "add", "README.md")
	runCommand(t, root, "git", "commit", "-q", "-m", "initial")
}

func runCommand(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v failed: %v\n%s", name, args, err, out)
	}
}

func writeFile(t *testing.T, root, rel, contents string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func entryNames(entries []FileEntry) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	return names
}

type fakeRuntimeClient struct {
	configFunc                               func(context.Context) (agxruntime.RuntimeConfig, error)
	updateDefaultAgentFunc                   func(context.Context, string) (agxruntime.RuntimeConfig, error)
	updateVoiceSTTFunc                       func(context.Context, agxruntime.VoiceSTTConfig) (agxruntime.RuntimeConfig, error)
	discordStatusFunc                        func(context.Context) (agxdiscord.Status, error)
	discordConnectFunc                       func(context.Context, string, string, string) (agxdiscord.Status, error)
	discordTaskSyncFunc                      func(context.Context, string) (agxdiscord.Status, error)
	runNewTaskWithInitialPromptWorkspaceFunc func(context.Context, string, string, *string, string, bool, *string, db.WorkspaceMode) (agxruntime.Task, error)
}

func withFakeRuntimeClient(t *testing.T, client runtimeClient) {
	t.Helper()
	previous := newRuntimeClient
	newRuntimeClient = func() runtimeClient { return client }
	t.Cleanup(func() { newRuntimeClient = previous })
}

func (f *fakeRuntimeClient) Status(context.Context) (agxruntime.Status, error) {
	return agxruntime.Status{}, nil
}

func (f *fakeRuntimeClient) Shutdown(context.Context) error {
	return nil
}

func (f *fakeRuntimeClient) Config(ctx context.Context) (agxruntime.RuntimeConfig, error) {
	if f.configFunc != nil {
		return f.configFunc(ctx)
	}
	return agxruntime.RuntimeConfig{}, nil
}

func (f *fakeRuntimeClient) UpdateDefaultAgent(ctx context.Context, agentName string) (agxruntime.RuntimeConfig, error) {
	if f.updateDefaultAgentFunc != nil {
		return f.updateDefaultAgentFunc(ctx, agentName)
	}
	return agxruntime.RuntimeConfig{}, nil
}

func (f *fakeRuntimeClient) UpdateVoiceSTT(ctx context.Context, voice agxruntime.VoiceSTTConfig) (agxruntime.RuntimeConfig, error) {
	if f.updateVoiceSTTFunc != nil {
		return f.updateVoiceSTTFunc(ctx, voice)
	}
	return agxruntime.RuntimeConfig{}, nil
}

func (f *fakeRuntimeClient) Events(context.Context) (<-chan agxruntime.Event, error) {
	return make(chan agxruntime.Event), nil
}

func (f *fakeRuntimeClient) ListAgents(context.Context, string) ([]agxruntime.Agent, error) {
	return nil, nil
}

func (f *fakeRuntimeClient) ListProjects(context.Context) ([]agxruntime.Project, error) {
	return nil, nil
}

func (f *fakeRuntimeClient) CreateProject(context.Context, string, string, *string, *string) (agxruntime.Project, error) {
	return agxruntime.Project{}, nil
}

func (f *fakeRuntimeClient) GetProject(context.Context, string) (agxruntime.Project, error) {
	return agxruntime.Project{}, nil
}

func (f *fakeRuntimeClient) UpdateProjectDetails(context.Context, string, string, *string) (agxruntime.Project, error) {
	return agxruntime.Project{}, nil
}

func (f *fakeRuntimeClient) GrantProjectAccess(context.Context, string) (agxruntime.Project, error) {
	return agxruntime.Project{}, nil
}

func (f *fakeRuntimeClient) DeleteProject(context.Context, string) error {
	return nil
}

func (f *fakeRuntimeClient) ListTasks(context.Context, string) ([]agxruntime.Task, error) {
	return nil, nil
}

func (f *fakeRuntimeClient) MonitorTasks(context.Context) ([]agxruntime.MonitorTask, error) {
	return nil, nil
}

func (f *fakeRuntimeClient) RunNewTaskWithInitialPromptWorkspace(ctx context.Context, projectID, title string, description *string, agentName string, allMighty bool, initialPrompt *string, workspaceMode db.WorkspaceMode) (agxruntime.Task, error) {
	if f.runNewTaskWithInitialPromptWorkspaceFunc != nil {
		return f.runNewTaskWithInitialPromptWorkspaceFunc(ctx, projectID, title, description, agentName, allMighty, initialPrompt, workspaceMode)
	}
	return agxruntime.Task{}, nil
}

func (f *fakeRuntimeClient) RunNewDiscordTaskWithWorkspace(context.Context, string, string, *string, string, bool, db.WorkspaceMode) (agxruntime.Task, error) {
	return agxruntime.Task{}, nil
}

func (f *fakeRuntimeClient) GetTask(context.Context, string) (agxruntime.Task, error) {
	return agxruntime.Task{}, nil
}

func (f *fakeRuntimeClient) UpdateTaskTitle(context.Context, string, string) (agxruntime.Task, error) {
	return agxruntime.Task{}, nil
}

func (f *fakeRuntimeClient) RunTask(context.Context, string) (agxruntime.Task, error) {
	return agxruntime.Task{}, nil
}

func (f *fakeRuntimeClient) StopTask(context.Context, string) (agxruntime.Task, error) {
	return agxruntime.Task{}, nil
}

func (f *fakeRuntimeClient) DeleteTask(context.Context, string) error {
	return nil
}

func (f *fakeRuntimeClient) SendTaskMessage(context.Context, string, string) (agxruntime.Task, error) {
	return agxruntime.Task{}, nil
}

func (f *fakeRuntimeClient) RecordTaskInput(context.Context, string, string) (agxruntime.Task, error) {
	return agxruntime.Task{}, nil
}

func (f *fakeRuntimeClient) SendTaskInput(context.Context, string, string) error {
	return nil
}

func (f *fakeRuntimeClient) ResizeTaskTerminal(context.Context, string, int, int) error {
	return nil
}

func (f *fakeRuntimeClient) TaskLogs(context.Context, string, int) (string, error) {
	return "", nil
}

func (f *fakeRuntimeClient) TaskLogStream(context.Context, string, int) (<-chan agxruntime.TaskLogEvent, error) {
	return make(chan agxruntime.TaskLogEvent), nil
}

func (f *fakeRuntimeClient) TaskTranscript(context.Context, string, int) ([]agxruntime.TaskTranscriptMessage, error) {
	return nil, nil
}

func (f *fakeRuntimeClient) DiscordStatus(ctx context.Context) (agxdiscord.Status, error) {
	if f.discordStatusFunc != nil {
		return f.discordStatusFunc(ctx)
	}
	return agxdiscord.Status{}, nil
}

func (f *fakeRuntimeClient) DiscordConnect(ctx context.Context, token, guildID, allowedUserID string) (agxdiscord.Status, error) {
	if f.discordConnectFunc != nil {
		return f.discordConnectFunc(ctx, token, guildID, allowedUserID)
	}
	return agxdiscord.Status{}, nil
}

func (f *fakeRuntimeClient) DiscordDisconnect(context.Context) (agxdiscord.Status, error) {
	return agxdiscord.Status{}, nil
}

func (f *fakeRuntimeClient) DiscordSoftSync(context.Context) (agxdiscord.Status, error) {
	return agxdiscord.Status{}, nil
}

func (f *fakeRuntimeClient) DiscordHardSync(context.Context) (agxdiscord.Status, error) {
	return agxdiscord.Status{}, nil
}

func (f *fakeRuntimeClient) DiscordTaskSync(ctx context.Context, taskID string) (agxdiscord.Status, error) {
	if f.discordTaskSyncFunc != nil {
		return f.discordTaskSyncFunc(ctx, taskID)
	}
	return agxdiscord.Status{}, nil
}

func (f *fakeRuntimeClient) DiscordInviteURL(context.Context, string) (string, error) {
	return "", nil
}
