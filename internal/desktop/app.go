package desktop

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nashory/agx/internal/agent"
	"github.com/nashory/agx/internal/config"
	"github.com/nashory/agx/internal/db"
	agxdiscord "github.com/nashory/agx/internal/discord"
	"github.com/nashory/agx/internal/display"
	agxruntime "github.com/nashory/agx/internal/runtime"
	"github.com/nashory/agx/internal/session"
	"github.com/nashory/agx/internal/tmux"
	"github.com/nashory/agx/internal/worktree"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// App is the Wails-facing desktop application service. In normal builds it
// delegates stateful work to the runtime daemon; in direct test mode it owns a
// local store/tmux controller so core behavior can be tested without Wails.
type App struct {
	store      *db.Store
	tmux       *tmux.Controller
	ctx        context.Context
	directMode bool

	recovery session.RecoveryResult

	registryMu sync.Mutex
	registries map[string]registryCacheEntry

	streamMu sync.Mutex
	streams  map[string]context.CancelFunc

	metadataMu             sync.Mutex
	metadataCancel         context.CancelFunc
	discordSyncMu          sync.Mutex
	discordSyncJob         DiscordSyncJob
	discordSoftSyncRunning bool
	discordSoftSyncPending bool
	agentEvents            *agentEventService

	stateMu sync.Mutex
	states  map[string]stateSnapshot
	locks   map[string]*sync.Mutex
}

type stateSnapshot struct {
	output       string
	lastActivity time.Time
}

type registryCacheEntry struct {
	registry *agent.Registry
	stamp    string
}

// Project is the desktop DTO for a registered repository, including aggregate
// task counts and local access diagnostics for UI rendering.
type Project struct {
	ID            string         `json:"id"`
	Name          string         `json:"name"`
	Path          string         `json:"path"`
	Description   *string        `json:"description,omitempty"`
	DefaultAgent  *string        `json:"defaultAgent,omitempty"`
	AccessGranted bool           `json:"accessGranted"`
	AccessError   *string        `json:"accessError,omitempty"`
	Languages     []LanguageStat `json:"languages,omitempty"`
	TaskCount     int            `json:"taskCount"`
	ActiveCount   int            `json:"activeCount"`
	WaitingCount  int            `json:"waitingCount"`
	CompleteCount int            `json:"completeCount"`
	OfflineCount  int            `json:"offlineCount"`
	LastOpened    time.Time      `json:"lastOpened"`
	CreatedAt     time.Time      `json:"createdAt"`
}

// Task is the desktop DTO for a task. It intentionally mirrors runtime fields so
// the UI can show both legacy tmux tasks and structured agent stream tasks.
type Task struct {
	ID               string    `json:"id"`
	ProjectID        string    `json:"projectId"`
	Title            string    `json:"title"`
	Description      *string   `json:"description,omitempty"`
	LastUserPrompt   *string   `json:"lastUserPrompt,omitempty"`
	Interface        string    `json:"interface"`
	Status           string    `json:"status"`
	Agent            string    `json:"agent"`
	AllMighty        bool      `json:"allMighty"`
	WorkspaceMode    string    `json:"workspaceMode"`
	SessionName      *string   `json:"sessionName,omitempty"`
	WorktreePath     *string   `json:"worktreePath,omitempty"`
	BranchName       *string   `json:"branchName,omitempty"`
	AgentThreadID    *string   `json:"agentThreadId,omitempty"`
	AgentEventCursor *string   `json:"agentEventCursor,omitempty"`
	AgentStreamKind  *string   `json:"agentStreamKind,omitempty"`
	CreatedAt        time.Time `json:"createdAt"`
	UpdatedAt        time.Time `json:"updatedAt"`
}

// MonitorTask augments a task with project context for monitor views.
type MonitorTask struct {
	Task
	ProjectName string `json:"projectName"`
	ProjectPath string `json:"projectPath"`
}

// TaskTranscriptMessage is a desktop-safe transcript entry for structured task
// conversations.
type TaskTranscriptMessage struct {
	ID        int64     `json:"id"`
	TaskID    string    `json:"taskId"`
	TurnID    *string   `json:"turnId,omitempty"`
	Role      string    `json:"role"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Agent describes an available or configured coding agent for the desktop UI.
type Agent struct {
	Name        string `json:"name"`
	Command     string `json:"command"`
	Description string `json:"description"`
	Available   bool   `json:"available"`
}

// LanguageStat summarizes a project's visible source files for project cards.
type LanguageStat struct {
	Name       string  `json:"name"`
	Files      int     `json:"files"`
	Percentage float64 `json:"percentage"`
}

// ProjectCandidate is a discovered git repository that can be registered.
type ProjectCandidate struct {
	Name         string         `json:"name"`
	Path         string         `json:"path"`
	Description  string         `json:"description,omitempty"`
	Languages    []LanguageStat `json:"languages,omitempty"`
	IsRegistered bool           `json:"isRegistered"`
}

// FileEntry describes one project-file browser entry.
type FileEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	IsDir bool   `json:"isDir"`
	Size  int64  `json:"size,omitempty"`
}

// DirectoryEntry describes one filesystem directory picker entry.
type DirectoryEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// LogEvent is emitted to the UI while streaming task terminal output.
type LogEvent struct {
	TaskID string `json:"taskId"`
	Data   string `json:"data,omitempty"`
	Reset  bool   `json:"reset"`
	Error  string `json:"error,omitempty"`
}

// MetadataEvent tells the UI that project/task metadata should be refreshed.
type MetadataEvent struct {
	ProjectID string `json:"projectId,omitempty"`
}

// DiscordStatusInfo is the desktop-facing Discord bridge status plus any
// long-running sync job state.
type DiscordStatusInfo struct {
	Enabled        bool           `json:"enabled"`
	Connected      bool           `json:"connected"`
	GuildID        string         `json:"guildId,omitempty"`
	GuildName      string         `json:"guildName,omitempty"`
	AllowedUserIDs []string       `json:"allowedUserIds,omitempty"`
	MaskedBotToken string         `json:"maskedBotToken,omitempty"`
	UptimeSeconds  int64          `json:"uptimeSeconds"`
	Error          string         `json:"error,omitempty"`
	LockedBy       string         `json:"lockedBy,omitempty"`
	Sync           DiscordSyncJob `json:"sync"`
}

// DiscordSyncJob tracks a background hard sync initiated from the desktop UI.
type DiscordSyncJob struct {
	Running     bool       `json:"running"`
	Kind        string     `json:"kind,omitempty"`
	Stage       string     `json:"stage,omitempty"`
	StartedAt   *time.Time `json:"startedAt,omitempty"`
	CompletedAt *time.Time `json:"completedAt,omitempty"`
	Error       string     `json:"error,omitempty"`
}

// RuntimeStatusInfo is the desktop-facing runtime health snapshot.
type RuntimeStatusInfo struct {
	Running       bool                   `json:"running"`
	PID           int                    `json:"pid,omitempty"`
	Version       string                 `json:"version,omitempty"`
	StartedAt     *time.Time             `json:"startedAt,omitempty"`
	UptimeSeconds int64                  `json:"uptimeSeconds"`
	ConfigDir     string                 `json:"configDir,omitempty"`
	SocketPath    string                 `json:"socketPath"`
	LockPath      string                 `json:"lockPath"`
	Recovery      session.RecoveryResult `json:"recovery"`
	Error         string                 `json:"error,omitempty"`
}

// RuntimeConfigInfo exposes non-secret global runtime configuration to Desktop.
type RuntimeConfigInfo struct {
	DefaultAgent string `json:"defaultAgent"`
}

const maxReadFileBytes = 1 << 20
const maxStreamLogBytes int64 = 1 << 20
const maxContextFiles = 50
const maxContextBytes = 256 * 1024
const maxProjectCandidates = 24
const discordConnectTimeout = 30 * time.Second
const discordHardSyncTimeout = 10 * time.Minute
const runtimeClientTimeout = 30 * time.Second

var streamFileMu sync.Mutex

// NewApp constructs the desktop app without opening the runtime database. The
// runtime daemon owns durable state in normal desktop operation.
func NewApp() (*App, error) {
	return NewAppWithStore(nil), nil
}

// NewAppWithStore constructs an App around an existing store for direct-mode
// tests and migration helpers.
func NewAppWithStore(store *db.Store) *App {
	directMode := desktopDirectModeEnabled()
	app := &App{
		store:      store,
		directMode: directMode,
		registries: map[string]registryCacheEntry{},
		streams:    map[string]context.CancelFunc{},
		states:     map[string]stateSnapshot{},
		locks:      map[string]*sync.Mutex{},
	}
	if directMode {
		app.tmux = tmux.NewController()
		app.agentEvents = newAgentEventService(app)
	}
	return app
}

// Startup records the Wails context and starts background event forwarding from
// the runtime daemon.
func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx
	a.startMetadataWatcher(ctx)
	go a.forwardRuntimeEvents(ctx)
}

func (a *App) Close() error {
	var errs []error
	a.metadataMu.Lock()
	if a.metadataCancel != nil {
		a.metadataCancel()
		a.metadataCancel = nil
	}
	a.metadataMu.Unlock()
	a.stopAllLogStreams()
	if a.agentEvents != nil {
		if err := a.agentEvents.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if a.store == nil {
		return errors.Join(errs...)
	}
	if a.directMode {
		tasks, err := a.allTasks()
		if err != nil {
			errs = append(errs, err)
		} else if err := a.cleanupTaskStreams(tasks); err != nil {
			errs = append(errs, err)
		}
	}
	if err := a.store.Close(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func (a *App) ListProjects() ([]Project, error) {
	if a.directMode {
		return a.directListProjects()
	}
	ctx, cancel := a.runtimeRequestContext(runtimeClientTimeout)
	defer cancel()
	client := agxruntime.NewClient()
	projects, err := client.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Project, 0, len(projects))
	for _, project := range projects {
		tasks, err := client.ListTasks(ctx, project.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, runtimeProjectDTO(project, tasks))
	}
	return out, nil
}

func (a *App) SelectProjectDirectory(defaultDirectory string) (string, error) {
	defaultDirectory = strings.TrimSpace(defaultDirectory)
	if defaultDirectory == "" {
		defaultDirectory, _ = os.UserHomeDir()
	}
	return a.selectDirectory("Open Git Project", defaultDirectory)
}

func (a *App) selectDirectory(title, defaultDirectory string) (string, error) {
	if runtime.GOOS == "darwin" {
		return selectDirectoryWithAppleScript(a.ctx, title, defaultDirectory)
	}
	if a.ctx == nil {
		return "", fmt.Errorf("desktop runtime is not ready")
	}
	return wailsruntime.OpenDirectoryDialog(a.ctx, wailsruntime.OpenDialogOptions{
		Title:                title,
		DefaultDirectory:     defaultDirectory,
		CanCreateDirectories: false,
		ResolvesAliases:      true,
	})
}

func selectDirectoryWithAppleScript(ctx context.Context, title, defaultDirectory string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if title == "" {
		title = "Select Folder"
	}
	if info, err := os.Stat(defaultDirectory); err != nil || !info.IsDir() {
		defaultDirectory, _ = os.UserHomeDir()
	}
	script := fmt.Sprintf(
		`set defaultFolder to POSIX file "%s" as alias
set selectedFolder to choose folder with prompt "%s" default location defaultFolder
POSIX path of selectedFolder`,
		appleScriptString(defaultDirectory),
		appleScriptString(title),
	)
	cmd := exec.CommandContext(ctx, "osascript", "-e", script)
	output, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(output))
	if err != nil {
		if strings.Contains(strings.ToLower(text), "user canceled") {
			return "", nil
		}
		return "", fmt.Errorf("open directory picker: %s: %w", text, err)
	}
	return strings.TrimRight(text, "/"), nil
}

func appleScriptString(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return value
}

func (a *App) HomeDirectory() (string, error) {
	return os.UserHomeDir()
}

func (a *App) ListDirectories(path string) ([]DirectoryEntry, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		path = home
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("path is not a directory: %s", abs)
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return nil, err
	}
	dirs := make([]DirectoryEntry, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == "." || name == ".." {
			continue
		}
		dirs = append(dirs, DirectoryEntry{
			Name: name,
			Path: filepath.Join(abs, name),
		})
	}
	sort.Slice(dirs, func(i, j int) bool {
		return strings.ToLower(dirs[i].Name) < strings.ToLower(dirs[j].Name)
	})
	return dirs, nil
}

func (a *App) ValidateProjectDirectory(path string) (ProjectCandidate, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return ProjectCandidate{}, fmt.Errorf("project path is required")
	}
	info, err := os.Stat(path)
	if err != nil {
		return ProjectCandidate{}, err
	}
	if !info.IsDir() {
		return ProjectCandidate{}, fmt.Errorf("project path is not a directory: %s", path)
	}
	if err := ensureGitRepository(path); err != nil {
		return ProjectCandidate{}, fmt.Errorf("project path is not a git repository: %s", path)
	}
	return a.projectCandidate(path)
}

func (a *App) GrantProjectAccess(projectID string) (Project, error) {
	if a.directMode {
		return a.directGrantProjectAccess(projectID)
	}
	ctx, cancel := a.runtimeRequestContext(runtimeClientTimeout)
	defer cancel()
	client := agxruntime.NewClient()
	project, err := client.GrantProjectAccess(ctx, projectID)
	if err != nil {
		refreshed, getErr := client.GetProject(ctx, projectID)
		if getErr != nil {
			return Project{}, err
		}
		if repairErr := a.ensureProjectWriteAccess(refreshed.Path); repairErr != nil {
			return Project{}, fmt.Errorf("%w; desktop repair failed: %v", err, repairErr)
		}
		project, err = client.GrantProjectAccess(ctx, projectID)
		if err != nil {
			return Project{}, err
		}
	}
	tasks, err := client.ListTasks(ctx, project.ID)
	if err != nil {
		return Project{}, err
	}
	a.emitMetadataEvent(projectID)
	return runtimeProjectDTO(project, tasks), nil
}

func (a *App) ListProjectCandidates(limit int) ([]ProjectCandidate, error) {
	if limit <= 0 || limit > maxProjectCandidates {
		limit = maxProjectCandidates
	}
	paths, err := discoverGitRepositories(limit * 3)
	if err != nil {
		return nil, err
	}
	candidates := make([]ProjectCandidate, 0, len(paths))
	for _, path := range paths {
		candidate, err := a.projectCandidate(path)
		if err != nil {
			continue
		}
		if candidate.IsRegistered {
			continue
		}
		candidates = append(candidates, candidate)
		if len(candidates) >= limit {
			break
		}
	}
	return candidates, nil
}

func (a *App) RegisterProject(path, name, description string) (Project, error) {
	if a.directMode {
		return a.directRegisterProject(path, name, description)
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return Project{}, fmt.Errorf("project path is required")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return Project{}, fmt.Errorf("project name is required")
	}
	info, err := os.Stat(path)
	if err != nil {
		return Project{}, err
	}
	if !info.IsDir() {
		return Project{}, fmt.Errorf("project path is not a directory: %s", path)
	}
	if err := ensureGitRepository(path); err != nil {
		return Project{}, fmt.Errorf("project path is not a git repository: %s", path)
	}
	ctx, cancel := a.runtimeRequestContext(runtimeClientTimeout)
	defer cancel()
	client := agxruntime.NewClient()
	project, err := client.CreateProject(ctx, path, name, display.PtrString(description), nil)
	if err != nil {
		return Project{}, err
	}
	a.emitMetadataEvent(project.ID)
	return runtimeProjectDTO(project, nil), nil
}

func (a *App) projectCandidate(path string) (ProjectCandidate, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return ProjectCandidate{}, err
	}
	root, err := gitRoot(abs)
	if err != nil {
		return ProjectCandidate{}, err
	}
	candidate := ProjectCandidate{
		Name:      filepath.Base(root),
		Path:      root,
		Languages: languageStats(root),
	}
	if registered, ok := a.registeredProjectByPath(root); ok {
		candidate.Name = registered.Name
		candidate.IsRegistered = true
		if registered.Description != nil {
			candidate.Description = *registered.Description
		}
	}
	return candidate, nil
}

func (a *App) registeredProjectByPath(path string) (Project, bool) {
	if a.directMode && a.store != nil {
		project, err := a.store.GetProjectByPath(path)
		if err == nil {
			return a.projectDTO(project, nil), true
		}
		projects, err := a.store.ListProjects()
		if err != nil {
			return Project{}, false
		}
		for _, project := range projects {
			if sameProjectPath(project.Path, path) {
				return a.projectDTO(project, nil), true
			}
		}
		return Project{}, false
	}
	ctx, cancel := a.runtimeRequestContext(runtimeClientTimeout)
	defer cancel()
	projects, err := agxruntime.NewClient().ListProjects(ctx)
	if err != nil {
		return Project{}, false
	}
	for _, project := range projects {
		if sameProjectPath(project.Path, path) {
			return runtimeProjectDTO(project, nil), true
		}
	}
	return Project{}, false
}

func (a *App) UpdateProject(projectID, name, description string) (Project, error) {
	if a.directMode {
		return a.directUpdateProject(projectID, name, description)
	}
	ctx, cancel := a.runtimeRequestContext(runtimeClientTimeout)
	defer cancel()
	client := agxruntime.NewClient()
	project, err := client.UpdateProjectDetails(ctx, projectID, name, display.PtrString(description))
	if err != nil {
		return Project{}, err
	}
	tasks, err := client.ListTasks(ctx, project.ID)
	if err != nil {
		return Project{}, err
	}
	a.emitMetadataEvent(project.ID)
	return runtimeProjectDTO(project, tasks), nil
}

func (a *App) GetProject(id string) (Project, error) {
	if a.directMode {
		return a.directGetProject(id)
	}
	ctx, cancel := a.runtimeRequestContext(runtimeClientTimeout)
	defer cancel()
	client := agxruntime.NewClient()
	project, err := client.GetProject(ctx, id)
	if err != nil {
		return Project{}, err
	}
	tasks, err := client.ListTasks(ctx, project.ID)
	if err != nil {
		return Project{}, err
	}
	return runtimeProjectDTO(project, tasks), nil
}

func (a *App) DeleteProject(projectID string) error {
	if a.directMode {
		return a.directDeleteProject(projectID)
	}
	ctx, cancel := a.runtimeRequestContext(runtimeClientTimeout)
	defer cancel()
	if err := agxruntime.NewClient().DeleteProject(ctx, projectID); err != nil {
		return err
	}
	a.emitMetadataEvent(projectID)
	return nil
}

func (a *App) directDeleteProject(projectID string) error {
	project, err := a.store.GetProject(projectID)
	if err != nil {
		return err
	}
	tasks, err := a.store.ListTasks(project.ID, nil)
	if err != nil {
		return err
	}
	var errs []error
	if err := a.managerForProject(project).StopProject(project); err != nil {
		errs = append(errs, err)
	}
	if err := a.store.DeleteProject(project.ID); err != nil {
		errs = append(errs, err)
	}
	if err := a.cleanupTaskStreams(tasks); err != nil {
		errs = append(errs, err)
	}
	a.emitMetadataEvent(project.ID)
	return errors.Join(errs...)
}

func (a *App) ResetDatabase() error {
	if !a.directMode {
		ctx, cancel := a.runtimeRequestContext(15 * time.Second)
		defer cancel()
		if _, err := agxruntime.ResetState(ctx, agxruntime.ResetOptions{}); err != nil {
			return err
		}
		a.stopAllLogStreams()
		a.stateMu.Lock()
		a.states = map[string]stateSnapshot{}
		a.locks = map[string]*sync.Mutex{}
		a.stateMu.Unlock()
		a.emitMetadataEvent("")
		return nil
	}
	projects, err := a.store.ListProjects()
	if err != nil {
		return err
	}
	tasks, err := a.allTasks()
	if err != nil {
		return err
	}
	var errs []error
	for _, project := range projects {
		if err := a.managerForProject(project).StopProject(project); err != nil {
			errs = append(errs, fmt.Errorf("stop project %s: %w", project.Name, err))
		}
	}
	if err := a.cleanupTaskStreams(tasks); err != nil {
		errs = append(errs, err)
	}
	a.stopAllLogStreams()
	if a.tmux.HasServer() {
		if err := a.tmux.KillServer(); err != nil {
			errs = append(errs, fmt.Errorf("kill tmux server: %w", err))
		}
	}
	if err := a.store.ResetAll(); err != nil {
		errs = append(errs, err)
	}
	a.stateMu.Lock()
	a.states = map[string]stateSnapshot{}
	a.locks = map[string]*sync.Mutex{}
	a.stateMu.Unlock()
	a.emitMetadataEvent("")
	return errors.Join(errs...)
}

func (a *App) RuntimeStatus() RuntimeStatusInfo {
	paths := agxruntime.DefaultPaths()
	ctx, cancel := a.runtimeRequestContext(3 * time.Second)
	defer cancel()
	status, err := agxruntime.NewClient().Status(ctx)
	if err != nil {
		return RuntimeStatusInfo{
			Running:    false,
			SocketPath: paths.Socket,
			LockPath:   paths.Lock,
			Error:      err.Error(),
		}
	}
	return runtimeStatusDTO(status)
}

func (a *App) RuntimeConfig() (RuntimeConfigInfo, error) {
	if !a.directMode {
		ctx, cancel := a.runtimeRequestContext(runtimeClientTimeout)
		defer cancel()
		cfg, err := agxruntime.NewClient().Config(ctx)
		if err != nil {
			return RuntimeConfigInfo{}, err
		}
		return RuntimeConfigInfo{DefaultAgent: cfg.DefaultAgent}, nil
	}
	cfg, warnings := config.LoadGlobal()
	if len(warnings) > 0 {
		return RuntimeConfigInfo{}, warnings[0]
	}
	return RuntimeConfigInfo{DefaultAgent: cfg.DefaultAgent}, nil
}

func (a *App) UpdateDefaultAgent(agentName string) (RuntimeConfigInfo, error) {
	if !a.directMode {
		ctx, cancel := a.runtimeRequestContext(runtimeClientTimeout)
		defer cancel()
		cfg, err := agxruntime.NewClient().UpdateDefaultAgent(ctx, agentName)
		if err != nil {
			return RuntimeConfigInfo{}, err
		}
		a.emitMetadataEvent("")
		return RuntimeConfigInfo{DefaultAgent: cfg.DefaultAgent}, nil
	}
	agentName = strings.TrimSpace(agentName)
	if agentName == "" {
		agentName = config.DefaultAgent
	}
	if _, err := agent.RegistryForProject("").Get(agentName); err != nil {
		return RuntimeConfigInfo{}, err
	}
	if err := config.SaveDefaultAgent(agentName); err != nil {
		return RuntimeConfigInfo{}, err
	}
	a.emitMetadataEvent("")
	return RuntimeConfigInfo{DefaultAgent: agentName}, nil
}

func (a *App) RuntimeStart() (RuntimeStatusInfo, error) {
	if status := a.RuntimeStatus(); status.Running {
		return status, nil
	}
	agxPath, err := runtimeCLIPath()
	if err != nil {
		return a.RuntimeStatus(), err
	}
	stdoutPath, stderrPath := agxruntime.RuntimeLogPaths()
	if err := os.MkdirAll(filepath.Dir(stdoutPath), 0o700); err != nil {
		return a.RuntimeStatus(), err
	}
	stdout, err := os.OpenFile(stdoutPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return a.RuntimeStatus(), err
	}
	stderr, err := os.OpenFile(stderrPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		_ = stdout.Close()
		return a.RuntimeStatus(), err
	}
	cmd := exec.Command(agxPath, "runtime", "start")
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Env = os.Environ()
	if err := cmd.Start(); err != nil {
		_ = stdout.Close()
		_ = stderr.Close()
		return a.RuntimeStatus(), err
	}
	go func() {
		_ = cmd.Wait()
		_ = stdout.Close()
		_ = stderr.Close()
	}()
	return a.waitForRuntimeStart(5 * time.Second)
}

func (a *App) RuntimeInstallService() (RuntimeStatusInfo, error) {
	agxPath, err := runtimeCLIPath()
	if err != nil {
		return a.RuntimeStatus(), err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, agxPath, "runtime", "install-service")
	cmd.Env = os.Environ()
	if output, err := cmd.CombinedOutput(); err != nil {
		return a.RuntimeStatus(), fmt.Errorf("install runtime service: %s: %w", strings.TrimSpace(string(output)), err)
	}
	return a.waitForRuntimeStart(10 * time.Second)
}

func runtimeCLIPath() (string, error) {
	executable, err := os.Executable()
	if err == nil {
		if resolved, resolveErr := filepath.EvalSymlinks(executable); resolveErr == nil {
			executable = resolved
		}
		if path, ok := executableSiblingCLI(executable); ok {
			return path, nil
		}
	}
	path, err := exec.LookPath("agx")
	if err != nil {
		return "", fmt.Errorf("find bundled agx CLI or agx on PATH: %w", err)
	}
	return path, nil
}

func executableSiblingCLI(executable string) (string, bool) {
	candidate := filepath.Join(filepath.Dir(executable), "agx")
	info, err := os.Stat(candidate)
	if err != nil || info.IsDir() || info.Mode().Perm()&0o111 == 0 {
		return "", false
	}
	return candidate, true
}

func (a *App) RuntimeStop() (RuntimeStatusInfo, error) {
	ctx, cancel := a.runtimeRequestContext(3 * time.Second)
	defer cancel()
	if err := agxruntime.NewClient().Shutdown(ctx); err != nil {
		return a.RuntimeStatus(), err
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		status := a.RuntimeStatus()
		if !status.Running {
			return status, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return a.RuntimeStatus(), nil
}

func (a *App) waitForRuntimeStart(timeout time.Duration) (RuntimeStatusInfo, error) {
	deadline := time.Now().Add(timeout)
	var status RuntimeStatusInfo
	for time.Now().Before(deadline) {
		status = a.RuntimeStatus()
		if status.Running {
			return status, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	if status.Error != "" {
		return status, errors.New(status.Error)
	}
	return status, fmt.Errorf("runtime did not start within %s", timeout)
}

func (a *App) DiscordStatus() DiscordStatusInfo {
	ctx, cancel := a.runtimeRequestContext(runtimeClientTimeout)
	defer cancel()
	status, err := agxruntime.NewClient().DiscordStatus(ctx)
	if err != nil {
		return a.discordStatusDTO(agxdiscord.Status{
			Error: err.Error(),
		})
	}
	return a.discordStatusDTO(status)
}

func (a *App) DiscordConnect(token, guildID, allowedUserID string) (DiscordStatusInfo, error) {
	ctx, cancel := a.runtimeRequestContext(discordConnectTimeout)
	defer cancel()
	status, err := agxruntime.NewClient().DiscordConnect(ctx, strings.TrimSpace(token), strings.TrimSpace(guildID), strings.TrimSpace(allowedUserID))
	if err != nil {
		a.emitDiscordStatusEvent()
		return a.DiscordStatus(), err
	}
	a.emitDiscordStatusEvent()
	return a.discordStatusDTO(status), nil
}

func (a *App) OpenDiscordInvite(token string) error {
	ctx, cancel := a.runtimeRequestContext(discordConnectTimeout)
	defer cancel()
	inviteURL, err := agxruntime.NewClient().DiscordInviteURL(ctx, token)
	if err != nil {
		return err
	}
	if a.ctx == nil {
		return fmt.Errorf("desktop runtime is not ready")
	}
	wailsruntime.BrowserOpenURL(a.ctx, inviteURL)
	return nil
}

func (a *App) DiscordSync() (DiscordStatusInfo, error) {
	return a.DiscordSoftSync()
}

func (a *App) DiscordSoftSync() (DiscordStatusInfo, error) {
	ctx, cancel := a.runtimeRequestContext(discordConnectTimeout)
	defer cancel()
	status, err := agxruntime.NewClient().DiscordSoftSync(ctx)
	if err != nil {
		a.emitDiscordStatusEvent()
		return a.DiscordStatus(), err
	}
	a.emitDiscordStatusEvent()
	return a.discordStatusDTO(status), nil
}

func (a *App) DiscordResetManagedChannels() (DiscordStatusInfo, error) {
	return a.DiscordHardSync()
}

func (a *App) DiscordHardSync() (DiscordStatusInfo, error) {
	if !a.directMode {
		ctx, cancel := a.runtimeRequestContext(discordConnectTimeout)
		defer cancel()
		status, err := agxruntime.NewClient().DiscordHardSync(ctx)
		if err != nil {
			a.emitDiscordStatusEvent()
			return a.DiscordStatus(), err
		}
		a.emitDiscordStatusEvent()
		return a.discordStatusDTO(status), nil
	}
	return a.beginDiscordHardSync("")
}

func (a *App) DiscordTaskSync(taskID string) (DiscordStatusInfo, error) {
	ctx, cancel := a.runtimeRequestContext(discordConnectTimeout)
	defer cancel()
	status, err := agxruntime.NewClient().DiscordTaskSync(ctx, strings.TrimSpace(taskID))
	if err != nil {
		a.emitDiscordStatusEvent()
		return a.DiscordStatus(), err
	}
	a.emitDiscordStatusEvent()
	return a.discordStatusDTO(status), nil
}

func (a *App) beginDiscordHardSync(preserveControlChannelID string) (DiscordStatusInfo, error) {
	now := time.Now()
	a.discordSyncMu.Lock()
	if a.discordSyncJob.Running {
		a.discordSyncMu.Unlock()
		return a.DiscordStatus(), nil
	}
	a.discordSyncJob = DiscordSyncJob{
		Running:   true,
		Kind:      "hard",
		Stage:     "Starting hard sync",
		StartedAt: &now,
	}
	a.discordSyncMu.Unlock()
	a.emitDiscordStatusEvent()

	go func() {
		_, err := a.discordHardSyncBlocking(context.Background(), preserveControlChannelID)
		completed := time.Now()
		a.discordSyncMu.Lock()
		a.discordSyncJob.Running = false
		a.discordSyncJob.CompletedAt = &completed
		if err != nil {
			a.discordSyncJob.Stage = "Hard sync failed"
			a.discordSyncJob.Error = err.Error()
		} else {
			a.discordSyncJob.Stage = "Hard sync completed"
			a.discordSyncJob.Error = ""
		}
		a.discordSyncMu.Unlock()
		a.emitDiscordStatusEvent()
	}()

	return a.DiscordStatus(), nil
}

func (a *App) discordHardSyncBlocking(baseCtx context.Context, preserveControlChannelID string) (DiscordStatusInfo, error) {
	ctx := baseCtx
	if ctx == nil {
		ctx = context.Background()
	}
	if a.ctx != nil {
		ctx = context.WithoutCancel(a.ctx)
	}
	ctx, cancel := context.WithTimeout(ctx, discordHardSyncTimeout)
	defer cancel()
	status, err := agxruntime.NewClient().DiscordHardSync(ctx)
	if err != nil {
		a.emitDiscordStatusEvent()
		return a.DiscordStatus(), err
	}
	a.emitDiscordStatusEvent()
	return a.discordStatusDTO(status), nil
}

func (a *App) DiscordDisconnect() (DiscordStatusInfo, error) {
	ctx, cancel := a.runtimeRequestContext(discordConnectTimeout)
	defer cancel()
	status, err := agxruntime.NewClient().DiscordDisconnect(ctx)
	if err != nil {
		a.emitDiscordStatusEvent()
		return a.DiscordStatus(), err
	}
	a.emitDiscordStatusEvent()
	return a.discordStatusDTO(status), nil
}

func (a *App) runtimeRequestContext(timeout time.Duration) (context.Context, context.CancelFunc) {
	base := context.Background()
	if a.ctx != nil {
		base = a.ctx
	}
	return context.WithTimeout(base, timeout)
}

func (a *App) ListTasks(projectID string) ([]Task, error) {
	if a.directMode {
		return a.directListTasks(projectID)
	}
	ctx, cancel := a.runtimeRequestContext(runtimeClientTimeout)
	defer cancel()
	tasks, err := agxruntime.NewClient().ListTasks(ctx, projectID)
	if err != nil {
		return nil, err
	}
	out := make([]Task, 0, len(tasks))
	for _, task := range tasks {
		out = append(out, runtimeTaskDTO(task))
	}
	return out, nil
}

func (a *App) directListTasks(projectID string) ([]Task, error) {
	tasks, err := a.store.ListTasks(projectID, nil)
	if err != nil {
		return nil, err
	}
	tasks = a.refreshTaskStatuses(tasks)
	out := make([]Task, 0, len(tasks))
	for _, task := range tasks {
		out = append(out, a.taskDTO(task))
	}
	return out, nil
}

func (a *App) ListMonitorTasks() ([]MonitorTask, error) {
	if a.directMode {
		return a.directListMonitorTasks()
	}
	ctx, cancel := a.runtimeRequestContext(runtimeClientTimeout)
	defer cancel()
	tasks, err := agxruntime.NewClient().MonitorTasks(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]MonitorTask, 0, len(tasks))
	for _, task := range tasks {
		out = append(out, MonitorTask{
			Task:        runtimeTaskDTO(task.Task),
			ProjectName: task.ProjectName,
			ProjectPath: task.ProjectPath,
		})
	}
	return out, nil
}

func (a *App) directListMonitorTasks() ([]MonitorTask, error) {
	projects, err := a.store.ListProjects()
	if err != nil {
		return nil, err
	}
	out := make([]MonitorTask, 0)
	for _, project := range projects {
		tasks, err := a.store.ListTasks(project.ID, nil)
		if err != nil {
			return nil, err
		}
		tasks = a.refreshTaskStatuses(tasks)
		for _, task := range tasks {
			if !isLiveTaskRuntime(task) {
				continue
			}
			out = append(out, MonitorTask{
				Task:        a.taskDTO(task),
				ProjectName: project.Name,
				ProjectPath: project.Path,
			})
		}
	}
	return out, nil
}

func (a *App) ListTaskTranscript(taskID string, limit int) ([]TaskTranscriptMessage, error) {
	if a.directMode {
		return a.directListTaskTranscript(taskID, limit)
	}
	ctx, cancel := a.runtimeRequestContext(runtimeClientTimeout)
	defer cancel()
	messages, err := agxruntime.NewClient().TaskTranscript(ctx, taskID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]TaskTranscriptMessage, 0, len(messages))
	for _, message := range messages {
		out = append(out, TaskTranscriptMessage{
			ID:        message.ID,
			TaskID:    message.TaskID,
			TurnID:    message.TurnID,
			Role:      message.Role,
			Body:      message.Body,
			CreatedAt: message.CreatedAt,
			UpdatedAt: message.UpdatedAt,
		})
	}
	return out, nil
}

func (a *App) CreateTask(projectID, title, description, agentName string, allMighty bool, workspaceMode string) (Task, error) {
	return a.createTask(projectID, title, description, agentName, allMighty, nil, parseDesktopWorkspaceMode(workspaceMode))
}

func (a *App) CreateTaskNoPrompt(projectID, title, agentName string, allMighty bool, workspaceMode string) (Task, error) {
	emptyPrompt := ""
	return a.createTask(projectID, title, "", agentName, allMighty, &emptyPrompt, parseDesktopWorkspaceMode(workspaceMode))
}

func (a *App) CreateDiscordTask(projectID, title, description, agentName string, allMighty bool, workspaceMode string) (Task, error) {
	return a.createDiscordTask(context.Background(), projectID, title, description, agentName, allMighty, parseDesktopWorkspaceMode(workspaceMode))
}

func (a *App) createDiscordTask(ctx context.Context, projectID, title, description, agentName string, allMighty bool, workspaceMode db.WorkspaceMode) (Task, error) {
	if !a.directMode {
		if status := a.DiscordStatus(); !status.Connected {
			return Task{}, fmt.Errorf("Discord is not connected")
		}
		title = strings.TrimSpace(title)
		if title == "" {
			return Task{}, fmt.Errorf("task title is required")
		}
		var descriptionPtr *string
		if strings.TrimSpace(description) != "" {
			descriptionPtr = display.PtrString(description)
		}
		requestCtx, cancel := a.runtimeRequestContext(runtimeClientTimeout)
		defer cancel()
		task, err := agxruntime.NewClient().RunNewDiscordTaskWithWorkspace(requestCtx, projectID, title, descriptionPtr, agentName, allMighty, workspaceMode)
		if err != nil {
			return Task{}, err
		}
		a.emitMetadataEvent(projectID)
		a.emitDiscordStatusEvent()
		return runtimeTaskDTO(task), nil
	}
	if status := a.DiscordStatus(); !status.Connected {
		return Task{}, fmt.Errorf("Discord is not connected")
	}
	project, err := a.store.GetProject(projectID)
	if err != nil {
		return Task{}, err
	}
	if agentName == "" {
		agentName = a.defaultAgentForProject(project)
	}
	if !isStructuredAgentName(agentName) {
		return Task{}, fmt.Errorf("agent %q does not support Discord task control", agentName)
	}
	task, err := a.createStructuredAgentTask(ctx, projectID, title, description, agentName, allMighty, workspaceMode)
	if err != nil {
		return Task{}, err
	}
	if _, err := a.DiscordSoftSync(); err != nil {
		return Task{}, a.withDesktopCleanupError(err, "sync Discord task channel", func() error {
			return deleteDesktopTaskForCleanup(a, task.ID)
		})
	}
	if strings.TrimSpace(description) != "" {
		dbTask, err := a.store.GetTask(task.ID)
		if err != nil {
			return Task{}, err
		}
		if err := a.agentEvents.SendTaskMessage(ctx, dbTask, project, description); err != nil {
			return Task{}, err
		}
		_ = a.store.AppendTaskTranscriptMessage(task.ID, "user", description, nil, nil)
		_ = a.store.UpdateTaskLastUserPrompt(task.ID, description)
		a.emitMetadataEvent(projectID)
	}
	refreshed, err := a.store.GetTask(task.ID)
	if err != nil {
		return Task{}, err
	}
	return a.taskDTO(refreshed), nil
}

func (a *App) createTask(projectID, title, description, agentName string, allMighty bool, initialPrompt *string, workspaceMode db.WorkspaceMode) (Task, error) {
	if a.directMode {
		return a.directCreateTask(projectID, title, description, agentName, allMighty, initialPrompt, workspaceMode)
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return Task{}, fmt.Errorf("task title is required")
	}
	var descriptionPtr *string
	if description != "" || initialPrompt != nil {
		descriptionPtr = display.PtrString(description)
	}
	ctx, cancel := a.runtimeRequestContext(runtimeClientTimeout)
	defer cancel()
	task, err := agxruntime.NewClient().RunNewTaskWithInitialPromptWorkspace(ctx, projectID, title, descriptionPtr, agentName, allMighty, initialPrompt, workspaceMode)
	if err != nil {
		return Task{}, err
	}
	a.emitMetadataEvent(projectID)
	a.syncDiscordAsync()
	return runtimeTaskDTO(task), nil
}

func (a *App) defaultAgentForProject(project db.Project) string {
	cfg, _ := config.LoadWithWarnings(project.Path)
	agentName := cfg.DefaultAgent
	if project.DefaultAgent != nil && *project.DefaultAgent != "" {
		agentName = *project.DefaultAgent
	}
	return agentName
}

func (a *App) UpdateTaskTitle(taskID, title string) (Task, error) {
	if a.directMode {
		return a.directUpdateTaskTitle(taskID, title)
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return Task{}, fmt.Errorf("task title is required")
	}
	ctx, cancel := a.runtimeRequestContext(runtimeClientTimeout)
	defer cancel()
	updated, err := agxruntime.NewClient().UpdateTaskTitle(ctx, taskID, title)
	if err != nil {
		return Task{}, err
	}
	a.emitMetadataEvent(updated.ProjectID)
	a.syncDiscordAsync()
	return runtimeTaskDTO(updated), nil
}

func (a *App) CreateStructuredCodexTask(ctx context.Context, projectID, title, description string, allMighty bool) (Task, error) {
	if !a.directMode {
		return a.createDiscordTask(ctx, projectID, title, description, "codex", allMighty, db.WorkspaceModeWorktree)
	}
	return a.createStructuredAgentTask(ctx, projectID, title, description, "codex", allMighty, db.WorkspaceModeWorktree)
}

func (a *App) CreateStructuredClaudeTask(ctx context.Context, projectID, title, description string, allMighty bool) (Task, error) {
	if !a.directMode {
		return a.createDiscordTask(ctx, projectID, title, description, "claude", allMighty, db.WorkspaceModeWorktree)
	}
	return a.createStructuredAgentTask(ctx, projectID, title, description, "claude", allMighty, db.WorkspaceModeWorktree)
}

func (a *App) createStructuredAgentTask(ctx context.Context, projectID, title, description, agentName string, allMighty bool, workspaceMode db.WorkspaceMode) (Task, error) {
	if !a.directMode {
		return Task{}, fmt.Errorf("structured desktop tasks are only available in direct test mode")
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return Task{}, fmt.Errorf("task title is required")
	}
	project, err := a.store.GetProject(projectID)
	if err != nil {
		return Task{}, err
	}
	registry := a.registryForProject(project.Path)
	ag, err := registry.Get(agentName)
	if err != nil {
		return Task{}, err
	}
	if !ag.IsAvailable() {
		return Task{}, fmt.Errorf("agent %q is not available on PATH", ag.Name)
	}
	task, err := a.store.CreateTaskRuntimeModeInterfaceWorkspace(db.NewTaskID(), project.ID, title, display.PtrString(description), agentName, allMighty, db.TaskInterfaceDiscord, workspaceMode, db.StatusActive, nil, nil, nil)
	if err != nil {
		return Task{}, err
	}
	prepared, err := a.prepareTaskWorktree(project, task, workspaceMode)
	if err != nil {
		return Task{}, a.withDesktopCleanupError(err, "prepare structured desktop task worktree", func() error {
			return deleteDesktopTaskRowForCleanup(a.store, task.ID)
		})
	}
	if err := a.store.UpdateTaskRuntimeBase(task.ID, nil, db.StatusActive, prepared.Path, prepared.Branch, prepared.Base); err != nil {
		return Task{}, a.withDesktopCleanupError(err, "update structured desktop task runtime", func() error {
			return errors.Join(
				removePreparedDesktopWorktreeForCleanup(project, prepared),
				deleteDesktopTaskRowForCleanup(a.store, task.ID),
			)
		})
	}
	task.WorktreePath = prepared.Path
	task.BranchName = prepared.Branch
	task.BaseBranch = prepared.Base
	if err := a.agentEvents.PrepareTask(ctx, task, project); err != nil {
		return Task{}, a.withDesktopCleanupError(err, "prepare structured desktop task", func() error {
			return errors.Join(
				removePreparedDesktopWorktreeForCleanup(project, prepared),
				deleteDesktopTaskRowForCleanup(a.store, task.ID),
			)
		})
	}
	a.emitMetadataEvent(projectID)
	a.syncDiscordAsync()
	refreshed, err := a.store.GetTask(task.ID)
	if err != nil {
		return Task{}, err
	}
	return a.taskDTO(refreshed), nil
}

func (a *App) RunTask(taskID string) error {
	if a.directMode {
		return a.directRunTask(taskID)
	}
	ctx, cancel := a.runtimeRequestContext(runtimeClientTimeout)
	defer cancel()
	task, err := agxruntime.NewClient().GetTask(ctx, taskID)
	if err != nil {
		return err
	}
	if task.Interface == string(db.TaskInterfaceDiscord) {
		return fmt.Errorf("task is controlled by Discord")
	}
	if _, err := agxruntime.NewClient().RunTask(ctx, task.ID); err != nil {
		return err
	}
	a.emitMetadataEvent(task.ProjectID)
	a.syncDiscordAsync()
	return nil
}

func (a *App) StopTask(taskID string) error {
	if a.directMode {
		return a.directStopTask(taskID)
	}
	ctx, cancel := a.runtimeRequestContext(runtimeClientTimeout)
	defer cancel()
	task, err := agxruntime.NewClient().StopTask(ctx, taskID)
	if err != nil {
		return err
	}
	a.StopLogStream(task.ID)
	_ = a.removeStream(task.ID)
	a.emitMetadataEvent(task.ProjectID)
	return nil
}

func (a *App) DeleteTask(taskID string) error {
	if a.directMode {
		return a.directDeleteTask(taskID)
	}
	ctx, cancel := a.runtimeRequestContext(runtimeClientTimeout)
	defer cancel()
	task, err := agxruntime.NewClient().GetTask(ctx, taskID)
	if err != nil {
		return err
	}
	if err := agxruntime.NewClient().DeleteTask(ctx, task.ID); err != nil {
		return err
	}
	a.StopLogStream(task.ID)
	_ = a.removeStream(task.ID)
	a.emitMetadataEvent(task.ProjectID)
	return nil
}

func (a *App) SendMessage(taskID, message string) error {
	if a.directMode {
		return a.directSendMessage(taskID, message)
	}
	ctx, cancel := a.runtimeRequestContext(runtimeClientTimeout)
	defer cancel()
	task, err := agxruntime.NewClient().GetTask(ctx, taskID)
	if err != nil {
		return err
	}
	if task.Interface == string(db.TaskInterfaceDiscord) {
		return fmt.Errorf("task is controlled by Discord")
	}
	_, err = agxruntime.NewClient().SendTaskMessage(ctx, task.ID, message)
	return err
}

func (a *App) RecordTaskInput(taskID, message string) error {
	if a.directMode {
		return a.directRecordTaskInput(taskID, message)
	}
	ctx, cancel := a.runtimeRequestContext(runtimeClientTimeout)
	defer cancel()
	task, err := agxruntime.NewClient().RecordTaskInput(ctx, taskID, message)
	if err != nil {
		return err
	}
	a.emitMetadataEvent(task.ProjectID)
	return nil
}

func (a *App) SendInput(taskID, data string) error {
	if a.directMode {
		return a.directSendInput(taskID, data)
	}
	ctx, cancel := a.runtimeRequestContext(runtimeClientTimeout)
	defer cancel()
	task, err := agxruntime.NewClient().GetTask(ctx, taskID)
	if err != nil {
		return err
	}
	if task.Interface == string(db.TaskInterfaceDiscord) {
		return fmt.Errorf("task is controlled by Discord")
	}
	return agxruntime.NewClient().SendTaskInput(ctx, task.ID, data)
}

func (a *App) ResizeTaskTerminal(taskID string, cols, rows int) error {
	if a.directMode {
		return a.directResizeTaskTerminal(taskID, cols, rows)
	}
	ctx, cancel := a.runtimeRequestContext(runtimeClientTimeout)
	defer cancel()
	return agxruntime.NewClient().ResizeTaskTerminal(ctx, taskID, cols, rows)
}

func (a *App) GetLogs(taskID string, lines int) (string, error) {
	if a.directMode {
		return a.directGetLogs(taskID, lines)
	}
	ctx, cancel := a.runtimeRequestContext(runtimeClientTimeout)
	defer cancel()
	return agxruntime.NewClient().TaskLogs(ctx, taskID, lines)
}

func (a *App) StartLogStream(taskID string, lines int) error {
	if a.directMode {
		return a.directStartLogStream(taskID, lines)
	}
	if a.ctx == nil {
		return nil
	}
	ctx, cancel := context.WithCancel(a.ctx)
	a.streamMu.Lock()
	a.stopLogStreamLocked(taskID)
	a.streams[taskID] = cancel
	a.streamMu.Unlock()

	go a.forwardRuntimeLogStream(ctx, taskID, lines)
	return nil
}

func (a *App) StopLogStream(taskID string) {
	a.streamMu.Lock()
	a.stopLogStreamLocked(taskID)
	a.streamMu.Unlock()
}

func (a *App) GetTaskStatus(taskID string) (string, error) {
	if a.directMode {
		return a.directGetTaskStatus(taskID)
	}
	ctx, cancel := a.runtimeRequestContext(runtimeClientTimeout)
	defer cancel()
	task, err := agxruntime.NewClient().GetTask(ctx, taskID)
	if err != nil {
		return string(db.StatusOffline), err
	}
	return string(task.Status), nil
}

func (a *App) RecoverRuntime() error {
	if !a.directMode {
		status := a.RuntimeStatus()
		if status.Error != "" {
			return errors.New(status.Error)
		}
		a.recovery = status.Recovery
		return nil
	}
	result, err := session.NewManager(a.store, a.tmux, a.registryForProject("")).RecoverLiveTasks()
	if err != nil {
		return err
	}
	a.recovery = result
	return nil
}

func (a *App) RecoverySummary() session.RecoveryResult {
	if !a.directMode {
		return a.RuntimeStatus().Recovery
	}
	return a.recovery
}

func (a *App) ListAvailableAgents(projectID string) ([]Agent, error) {
	if !a.directMode {
		ctx, cancel := a.runtimeRequestContext(runtimeClientTimeout)
		defer cancel()
		agents, err := agxruntime.NewClient().ListAgents(ctx, projectID)
		if err != nil {
			return nil, err
		}
		out := make([]Agent, 0, len(agents))
		for _, ag := range agents {
			out = append(out, Agent{
				Name:        ag.Name,
				Command:     ag.Command,
				Description: ag.Description,
				Available:   ag.Available,
			})
		}
		return out, nil
	}
	var registry *agent.Registry
	if projectID == "" {
		registry = a.registryForProject("")
	} else {
		path, err := a.projectPath(projectID)
		if err != nil {
			return nil, err
		}
		registry = a.registryForProject(path)
	}
	agents := registry.All()
	out := make([]Agent, 0, len(agents))
	for _, ag := range agents {
		out = append(out, Agent{
			Name:        ag.Name,
			Command:     ag.Command,
			Description: ag.Description,
			Available:   ag.IsAvailable(),
		})
	}
	return out, nil
}

func (a *App) ListDirectory(projectID, relativePath string, showHidden bool) ([]FileEntry, error) {
	path, err := a.projectPath(projectID)
	if err != nil {
		return nil, err
	}
	return a.listDirectoryAtRoot(path, relativePath, showHidden)
}

func (a *App) ListTaskDirectory(taskID, relativePath string, showHidden bool) ([]FileEntry, error) {
	root, err := a.taskFileRoot(taskID)
	if err != nil {
		return nil, err
	}
	return a.listDirectoryAtRoot(root, relativePath, showHidden)
}

func (a *App) listDirectoryAtRoot(rootPath, relativePath string, showHidden bool) ([]FileEntry, error) {
	root, dir, err := resolveProjectPath(rootPath, relativePath)
	if err != nil {
		return nil, err
	}
	dir, err = resolveRealProjectPath(root, dir)
	if err != nil {
		return nil, err
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]FileEntry, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if shouldHideFile(name, showHidden) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		fullPath, err := resolveRealProjectPath(root, filepath.Join(dir, name))
		if err != nil {
			continue
		}
		path, err := filepath.Rel(realRoot, fullPath)
		if err != nil {
			return nil, err
		}
		path = filepath.ToSlash(path)
		if isGitIgnored(root, path) {
			continue
		}
		out = append(out, FileEntry{
			Name:  name,
			Path:  path,
			IsDir: entry.IsDir(),
			Size:  info.Size(),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

func (a *App) ReadFile(projectID, relativePath string) (string, error) {
	path, err := a.projectPath(projectID)
	if err != nil {
		return "", err
	}
	return readFileAtRoot(path, relativePath)
}

func (a *App) ReadTaskFile(taskID, relativePath string) (string, error) {
	root, err := a.taskFileRoot(taskID)
	if err != nil {
		return "", err
	}
	return readFileAtRoot(root, relativePath)
}

func readFileAtRoot(rootPath, relativePath string) (string, error) {
	root, path, err := resolveProjectPath(rootPath, relativePath)
	if err != nil {
		return "", err
	}
	path, err = resolveRealProjectPath(root, path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("cannot read directory %q", relativePath)
	}
	if info.Size() > maxReadFileBytes {
		return "", fmt.Errorf("file is too large to preview: %s", relativePath)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return "", fmt.Errorf("file appears to be binary: %s", relativePath)
	}
	return string(data), nil
}

func (a *App) SearchFiles(projectID, query string, limit int) ([]string, error) {
	path, err := a.projectPath(projectID)
	if err != nil {
		return nil, err
	}
	return searchFilesAtRoot(path, query, limit)
}

func (a *App) SearchTaskFiles(taskID, query string, limit int) ([]string, error) {
	root, err := a.taskFileRoot(taskID)
	if err != nil {
		return nil, err
	}
	return searchFilesAtRoot(root, query, limit)
}

func searchFilesAtRoot(rootPath, query string, limit int) ([]string, error) {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 25
	}
	files, err := gitVisibleFiles(rootPath)
	if err != nil {
		return nil, err
	}
	type scoredFile struct {
		path  string
		score int
	}
	var scored []scoredFile
	for _, file := range files {
		if score, ok := fuzzyScore(file, query); ok {
			scored = append(scored, scoredFile{path: file, score: score})
		}
	}
	sort.Slice(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score < scored[j].score
		}
		return scored[i].path < scored[j].path
	})
	if len(scored) > limit {
		scored = scored[:limit]
	}
	matches := make([]string, 0, len(scored))
	for _, file := range scored {
		matches = append(matches, file.path)
	}
	return matches, nil
}

func (a *App) taskFileRoot(taskID string) (string, error) {
	if !a.directMode {
		ctx, cancel := a.runtimeRequestContext(runtimeClientTimeout)
		defer cancel()
		client := agxruntime.NewClient()
		task, err := client.GetTask(ctx, taskID)
		if err != nil {
			return "", err
		}
		if task.WorktreePath != nil && strings.TrimSpace(*task.WorktreePath) != "" {
			root, err := requireUsableTaskWorktree(*task.WorktreePath)
			if err != nil {
				return "", err
			}
			return root, nil
		}
		project, err := client.GetProject(ctx, task.ProjectID)
		if err != nil {
			return "", err
		}
		return project.Path, nil
	}
	task, err := a.store.GetTask(taskID)
	if err != nil {
		return "", err
	}
	if task.WorktreePath != nil && strings.TrimSpace(*task.WorktreePath) != "" {
		root, err := requireUsableTaskWorktree(*task.WorktreePath)
		if err != nil {
			return "", err
		}
		return root, nil
	}
	project, err := a.store.GetProject(task.ProjectID)
	if err != nil {
		return "", err
	}
	return project.Path, nil
}

func requireUsableTaskWorktree(path string) (string, error) {
	clean := filepath.Clean(strings.TrimSpace(path))
	if clean == "." || clean == "" {
		return "", fmt.Errorf("task worktree path is empty")
	}
	info, err := os.Stat(clean)
	if err != nil {
		return "", fmt.Errorf("task worktree is unavailable: %s", clean)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("task worktree is not a directory: %s", clean)
	}
	return clean, nil
}

func (a *App) ComposePrompt(message string, contextPaths []string) string {
	cleaned := cleanContextPaths(contextPaths)
	if len(cleaned) == 0 {
		return message
	}
	return fmt.Sprintf("Read these files first and use them as context: %s\n%s", strings.Join(cleaned, ", "), message)
}

func (a *App) ComposePromptWithFiles(projectID, message string, contextPaths []string, includeContents bool) (string, error) {
	path, err := a.projectPath(projectID)
	if err != nil {
		return "", err
	}
	return a.composePromptWithFilesAtRoot(path, message, contextPaths, includeContents)
}

func (a *App) ComposeTaskPromptWithFiles(taskID, message string, contextPaths []string, includeContents bool) (string, error) {
	root, err := a.taskFileRoot(taskID)
	if err != nil {
		return "", err
	}
	return a.composePromptWithFilesAtRoot(root, message, contextPaths, includeContents)
}

func (a *App) composePromptWithFilesAtRoot(rootPath, message string, contextPaths []string, includeContents bool) (string, error) {
	prompt := a.ComposePrompt(message, contextPaths)
	if !includeContents {
		return prompt, nil
	}
	cleaned := cleanContextPaths(contextPaths)
	if len(cleaned) == 0 {
		return prompt, nil
	}
	var b strings.Builder
	var totalBytes int64
	fmt.Fprintln(&b, "Use these file contents as context:")
	for _, path := range cleaned {
		if err := a.appendContextPath(&b, rootPath, path, &totalBytes); err != nil {
			return "", err
		}
	}
	fmt.Fprintf(&b, "\n%s", message)
	return b.String(), nil
}

func (a *App) appendContextPath(b *strings.Builder, rootPath, path string, totalBytes *int64) error {
	ref := parseContextReference(path)
	root, resolved, err := resolveProjectPath(rootPath, ref.Path)
	if err != nil {
		return err
	}
	resolved, err = resolveRealProjectPath(root, resolved)
	if err != nil {
		return err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return a.appendContextFileRange(b, rootPath, ref, totalBytes)
	}
	files, err := gitVisibleFiles(rootPath)
	if err != nil {
		return err
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	dirRel, err := filepath.Rel(realRoot, resolved)
	if err != nil {
		return err
	}
	dirRel = filepath.ToSlash(dirRel)
	dirRel = strings.TrimSuffix(dirRel, "/")
	if dirRel == "." {
		dirRel = ""
	}
	prefix := dirRel
	if prefix != "" {
		prefix += "/"
	}
	count := 0
	for _, file := range files {
		if prefix != "" && !strings.HasPrefix(file, prefix) {
			continue
		}
		if count >= maxContextFiles {
			fmt.Fprintf(b, "\n--- %s ---\n[skipped remaining files: directory context is limited to %d files]\n", path, maxContextFiles)
			break
		}
		if err := a.appendContextFile(b, rootPath, file, totalBytes); err != nil {
			fmt.Fprintf(b, "\n--- %s ---\n[skipped: %s]\n", file, err)
			continue
		}
		count++
		if *totalBytes >= maxContextBytes {
			fmt.Fprintf(b, "\n--- %s ---\n[skipped remaining files: context content is limited to %d bytes]\n", path, maxContextBytes)
			break
		}
	}
	if count == 0 {
		fmt.Fprintf(b, "\n--- %s ---\n[directory contains no tracked visible files]\n", path)
	}
	return nil
}

func (a *App) appendContextFile(b *strings.Builder, rootPath, path string, totalBytes *int64) error {
	return a.appendContextFileRange(b, rootPath, contextReference{Raw: path, Path: path}, totalBytes)
}

func (a *App) appendContextFileRange(b *strings.Builder, rootPath string, ref contextReference, totalBytes *int64) error {
	if *totalBytes >= maxContextBytes {
		return nil
	}
	contents, err := readFileAtRoot(rootPath, ref.Path)
	if err != nil {
		return err
	}
	if ref.StartLine > 0 {
		contents = selectLineRange(contents, ref.StartLine, ref.EndLine)
	}
	remaining := maxContextBytes - *totalBytes
	truncated := false
	if int64(len(contents)) > remaining {
		contents = contents[:remaining]
		truncated = true
	}
	*totalBytes += int64(len(contents))
	fmt.Fprintf(b, "\n--- %s ---\n", ref.Raw)
	fmt.Fprintln(b, "```")
	fmt.Fprintln(b, contents)
	if truncated {
		fmt.Fprintf(b, "\n[truncated: context content is limited to %d bytes]\n", maxContextBytes)
	}
	fmt.Fprintln(b, "```")
	return nil
}

type contextReference struct {
	Raw       string
	Path      string
	StartLine int
	EndLine   int
}

func parseContextReference(value string) contextReference {
	ref := contextReference{Raw: value, Path: value}
	index := strings.LastIndex(value, ":L")
	if index < 0 {
		return ref
	}
	path := value[:index]
	rangeText := value[index+2:]
	if path == "" || rangeText == "" {
		return ref
	}
	startText, endText, hasEnd := strings.Cut(rangeText, "-L")
	if !hasEnd {
		startText, endText, hasEnd = strings.Cut(rangeText, "-")
	}
	start, err := strconv.Atoi(startText)
	if err != nil || start <= 0 {
		return ref
	}
	end := start
	if hasEnd {
		parsedEnd, err := strconv.Atoi(endText)
		if err != nil || parsedEnd <= 0 {
			return ref
		}
		end = parsedEnd
	}
	if end < start {
		start, end = end, start
	}
	ref.Path = path
	ref.StartLine = start
	ref.EndLine = end
	return ref
}

func selectLineRange(contents string, start, end int) string {
	if start <= 0 {
		return contents
	}
	if end < start {
		end = start
	}
	lines := strings.Split(contents, "\n")
	if start > len(lines) {
		return ""
	}
	if end > len(lines) {
		end = len(lines)
	}
	return strings.Join(lines[start-1:end], "\n")
}

func cleanContextPaths(contextPaths []string) []string {
	cleaned := make([]string, 0, len(contextPaths))
	seen := map[string]bool{}
	for _, path := range contextPaths {
		path = strings.TrimSpace(filepath.ToSlash(path))
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		cleaned = append(cleaned, path)
	}
	return cleaned
}

func (a *App) streamPath(taskID string) (string, error) {
	dir := filepath.Join(config.ConfigDir(), "streams")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return streamFilePath(dir, taskID), nil
}

func streamFilePath(dir, taskID string) string {
	name := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' {
			return r
		}
		return '-'
	}, taskID)
	return filepath.Join(dir, name+".log")
}

func (a *App) removeStream(taskID string) error {
	path := streamFilePath(filepath.Join(config.ConfigDir(), "streams"), taskID)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (a *App) cleanupTaskStreams(tasks []db.Task) error {
	errs := make([]error, 0)
	for _, task := range tasks {
		a.StopLogStream(task.ID)
		if err := a.removeStream(task.ID); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (a *App) allTasks() ([]db.Task, error) {
	projects, err := a.store.ListProjects()
	if err != nil {
		return nil, err
	}
	var tasks []db.Task
	for _, project := range projects {
		projectTasks, err := a.store.ListTasks(project.ID, nil)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, projectTasks...)
	}
	return tasks, nil
}

func (a *App) followLogFile(ctx context.Context, taskID, path string, offset int64) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			data, nextOffset, err := readAppendedFile(path, offset, maxStreamLogBytes)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				a.emitLogEvent(taskID, LogEvent{TaskID: taskID, Error: err.Error()})
				continue
			}
			offset = nextOffset
			if data != "" {
				a.emitLogEvent(taskID, LogEvent{TaskID: taskID, Data: data})
			}
		}
	}
}

func (a *App) forwardRuntimeLogStream(ctx context.Context, taskID string, lines int) {
	events, err := agxruntime.NewClient().TaskLogStream(ctx, taskID, lines)
	if err != nil {
		a.emitLogEvent(taskID, LogEvent{TaskID: taskID, Error: err.Error()})
		return
	}
	for event := range events {
		a.emitLogEvent(taskID, LogEvent{
			TaskID: event.TaskID,
			Data:   event.Data,
			Reset:  event.Reset,
			Error:  event.Error,
		})
	}
}

func readAppendedFile(path string, offset, maxSize int64) (string, int64, error) {
	streamFileMu.Lock()
	defer streamFileMu.Unlock()
	if maxSize > 0 {
		nextOffset, err := compactStreamFileLocked(path, maxSize)
		if err != nil {
			return "", offset, err
		}
		if nextOffset >= 0 && offset > nextOffset {
			offset = 0
		}
	}
	file, err := os.Open(path)
	if err != nil {
		return "", offset, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", offset, err
	}
	if info.Size() < offset {
		offset = 0
	}
	if info.Size() == offset {
		return "", offset, nil
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return "", offset, err
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return "", offset, err
	}
	return string(data), offset + int64(len(data)), nil
}

func compactStreamFile(path string, maxSize int64) (int64, error) {
	streamFileMu.Lock()
	defer streamFileMu.Unlock()
	return compactStreamFileLocked(path, maxSize)
}

func compactStreamFileLocked(path string, maxSize int64) (int64, error) {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return -1, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return -1, err
	}
	if info.Size() <= maxSize {
		return -1, nil
	}
	keep := maxSize / 2
	if keep <= 0 {
		keep = maxSize
	}
	if _, err := file.Seek(info.Size()-keep, io.SeekStart); err != nil {
		return -1, err
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return -1, err
	}
	if err := file.Truncate(0); err != nil {
		return -1, err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return -1, err
	}
	if _, err := file.Write(data); err != nil {
		return -1, err
	}
	return int64(len(data)), nil
}

func (a *App) emitLogEvent(taskID string, event LogEvent) {
	if a.ctx == nil {
		return
	}
	wailsruntime.EventsEmit(a.ctx, logEventName(taskID), event)
}

func logEventName(taskID string) string {
	return "agx:logs:" + taskID
}

func (a *App) emitDiscordStatusEvent() {
	if a.ctx == nil {
		return
	}
	wailsruntime.EventsEmit(a.ctx, discordStatusEventName(), a.DiscordStatus())
}

func (a *App) emitRuntimeStatusEvent(status RuntimeStatusInfo) {
	if a.ctx == nil {
		return
	}
	wailsruntime.EventsEmit(a.ctx, runtimeStatusEventName(), status)
}

func (a *App) forwardRuntimeEvents(ctx context.Context) {
	if a.directMode {
		return
	}
	for {
		events, err := agxruntime.NewClient().Events(ctx)
		if err != nil {
			a.emitRuntimeStatusEvent(a.RuntimeStatus())
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
				continue
			}
		}
		for event := range events {
			a.forwardRuntimeEvent(event)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}

func (a *App) forwardRuntimeEvent(event agxruntime.Event) {
	switch event.Type {
	case "runtime.status":
		var status agxruntime.Status
		if err := json.Unmarshal(event.Payload, &status); err == nil {
			a.emitRuntimeStatusEvent(runtimeStatusDTO(status))
		} else {
			a.emitRuntimeStatusEvent(a.RuntimeStatus())
		}
	case "discord.status":
		var status agxdiscord.Status
		if err := json.Unmarshal(event.Payload, &status); err == nil && a.ctx != nil {
			wailsruntime.EventsEmit(a.ctx, discordStatusEventName(), a.discordStatusDTO(status))
		} else {
			a.emitDiscordStatusEvent()
		}
	case "project.changed", "project.deleted", "task.changed", "task.deleted":
		a.emitMetadataEvent("")
	}
}

func (a *App) syncDiscordAsync() {
	a.discordSyncMu.Lock()
	if a.discordSoftSyncRunning {
		a.discordSoftSyncPending = true
		a.discordSyncMu.Unlock()
		return
	}
	a.discordSoftSyncRunning = true
	a.discordSyncMu.Unlock()
	go func() {
		for {
			ctx, cancel := a.runtimeRequestContext(15 * time.Second)
			if _, err := agxruntime.NewClient().DiscordSoftSync(ctx); err != nil {
				log.Printf("operation=%q error=%v", "desktop_discord_soft_sync_background", err)
			}
			cancel()
			a.emitDiscordStatusEvent()

			a.discordSyncMu.Lock()
			if !a.discordSoftSyncPending {
				a.discordSoftSyncRunning = false
				a.discordSyncMu.Unlock()
				return
			}
			a.discordSoftSyncPending = false
			a.discordSyncMu.Unlock()
		}
	}()
}

func (a *App) deleteDiscordTaskChannelAsync(taskID string) {
	go func() {
		ctx, cancel := a.runtimeRequestContext(15 * time.Second)
		defer cancel()
		if _, err := agxruntime.NewClient().DiscordSoftSync(ctx); err != nil {
			log.Printf("operation=%q task=%s error=%v", "desktop_discord_task_cleanup_sync", display.ShortID(taskID), err)
		}
		a.emitDiscordStatusEvent()
	}()
}

func discordStatusEventName() string {
	return "discord:status"
}

func runtimeStatusEventName() string {
	return "runtime:status"
}

func (a *App) emitMetadataEvent(projectID string) {
	if a.ctx == nil {
		return
	}
	wailsruntime.EventsEmit(a.ctx, metadataEventName(), MetadataEvent{ProjectID: projectID})
}

func metadataEventName() string {
	return "agx:metadata"
}

func (a *App) startMetadataWatcher(parent context.Context) {
	a.metadataMu.Lock()
	defer a.metadataMu.Unlock()
	if a.metadataCancel != nil {
		a.metadataCancel()
	}
	ctx, cancel := context.WithCancel(parent)
	a.metadataCancel = cancel
	go a.watchMetadataFiles(ctx)
}

func (a *App) watchMetadataFiles(ctx context.Context) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	last := a.metadataStamp()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			next := a.metadataStamp()
			if next != last {
				last = next
				a.emitMetadataEvent("")
			}
		}
	}
}

func (a *App) metadataStamp() string {
	paths := []string{
		db.DefaultDBPath(),
		db.DefaultDBPath() + "-wal",
		filepath.Join(config.ConfigDir(), "config.toml"),
	}
	if a.directMode && a.store != nil {
		if projects, err := a.store.ListProjects(); err == nil {
			for _, project := range projects {
				paths = append(paths, filepath.Join(project.Path, ".agx", "config.toml"))
			}
		}
	}
	sort.Strings(paths)
	var b strings.Builder
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		fmt.Fprintf(&b, "%s:%d:%d;", filepath.Base(path), info.ModTime().UnixNano(), info.Size())
	}
	return b.String()
}

func (a *App) stopLogStreamLocked(taskID string) {
	cancel := a.streams[taskID]
	if cancel != nil {
		cancel()
		delete(a.streams, taskID)
	}
}

func (a *App) stopAllLogStreams() {
	a.streamMu.Lock()
	defer a.streamMu.Unlock()
	for taskID, cancel := range a.streams {
		cancel()
		delete(a.streams, taskID)
	}
}

func (a *App) managerForProject(project db.Project) *session.Manager {
	return session.NewManager(a.store, a.tmux, a.registryForProject(project.Path))
}

func (a *App) prepareTaskWorktree(project db.Project, task db.Task, workspaceMode db.WorkspaceMode) (worktree.Prepared, error) {
	projectConfig, err := config.LoadEffectiveProjectConfig(project.Path)
	if err != nil {
		return worktree.Prepared{}, err
	}
	projectConfig.Worktree.Enabled = normalizeDesktopWorkspaceMode(workspaceMode) == db.WorkspaceModeWorktree
	return worktree.PrepareForTask(project, task.ID, projectConfig.Worktree, task.WorktreePath, task.BranchName)
}

func removePreparedDesktopWorktree(project db.Project, prepared worktree.Prepared) error {
	if !prepared.Created {
		return nil
	}
	return worktree.Remove(project, prepared.Path, prepared.Branch, prepared.Base)
}

func (a *App) withDesktopCleanupError(primary error, operation string, cleanup func() error) error {
	cleanupErr := cleanup()
	if cleanupErr == nil {
		return primary
	}
	log.Printf("operation=%q error=%v", operation, cleanupErr)
	return errors.Join(primary, fmt.Errorf("%s cleanup failed: %w", operation, cleanupErr))
}

func removePreparedDesktopWorktreeForCleanup(project db.Project, prepared worktree.Prepared) error {
	if err := removePreparedDesktopWorktree(project, prepared); err != nil {
		return fmt.Errorf("remove prepared desktop worktree: %w", err)
	}
	return nil
}

func deleteDesktopTaskRowForCleanup(store *db.Store, taskID string) error {
	if err := store.DeleteTask(taskID); err != nil {
		return fmt.Errorf("delete desktop task row: %w", err)
	}
	return nil
}

func deleteDesktopTaskForCleanup(app *App, taskID string) error {
	if err := app.DeleteTask(taskID); err != nil {
		return fmt.Errorf("delete desktop task %s: %w", display.ShortID(taskID), err)
	}
	return nil
}

func parseDesktopWorkspaceMode(value string) db.WorkspaceMode {
	mode, err := db.ParseWorkspaceMode(value)
	if err != nil {
		return db.WorkspaceModeWorktree
	}
	return mode
}

func normalizeDesktopWorkspaceMode(mode db.WorkspaceMode) db.WorkspaceMode {
	if mode == "" {
		return db.WorkspaceModeWorktree
	}
	return mode
}

func (a *App) registryForProject(projectPath string) *agent.Registry {
	a.registryMu.Lock()
	defer a.registryMu.Unlock()
	stamp := registryConfigStamp(projectPath)
	entry := a.registries[projectPath]
	if entry.registry == nil || entry.stamp != stamp {
		entry = registryCacheEntry{
			registry: agent.RegistryForProject(projectPath),
			stamp:    stamp,
		}
		a.registries[projectPath] = entry
	}
	return entry.registry
}

func registryConfigStamp(projectPath string) string {
	paths := []string{filepath.Join(config.ConfigDir(), "config.toml")}
	if projectPath != "" {
		paths = append(paths, filepath.Join(projectPath, ".agx", "config.toml"))
	}
	var b strings.Builder
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		fmt.Fprintf(&b, "%s:%d:%d;", path, info.ModTime().UnixNano(), info.Size())
	}
	return b.String()
}

func (a *App) taskDTO(task db.Task) Task {
	return Task{
		ID:               task.ID,
		ProjectID:        task.ProjectID,
		Title:            task.Title,
		Description:      task.Description,
		LastUserPrompt:   task.LastUserPrompt,
		Interface:        string(task.Interface),
		Status:           string(task.Status),
		Agent:            task.Agent,
		AllMighty:        task.AllMighty,
		WorkspaceMode:    string(normalizeDesktopWorkspaceMode(task.WorkspaceMode)),
		SessionName:      task.SessionName,
		WorktreePath:     task.WorktreePath,
		BranchName:       task.BranchName,
		AgentThreadID:    task.AgentThreadID,
		AgentEventCursor: task.AgentEventCursor,
		AgentStreamKind:  task.AgentStreamKind,
		CreatedAt:        task.CreatedAt,
		UpdatedAt:        task.UpdatedAt,
	}
}

func runtimeTaskDTO(task agxruntime.Task) Task {
	return Task{
		ID:               task.ID,
		ProjectID:        task.ProjectID,
		Title:            task.Title,
		Description:      task.Description,
		LastUserPrompt:   task.LastUserPrompt,
		Interface:        task.Interface,
		Status:           string(task.Status),
		Agent:            task.Agent,
		AllMighty:        task.AllMighty,
		WorkspaceMode:    task.WorkspaceMode,
		SessionName:      task.SessionName,
		WorktreePath:     task.WorktreePath,
		BranchName:       task.BranchName,
		AgentThreadID:    task.AgentThreadID,
		AgentEventCursor: task.AgentEventCursor,
		AgentStreamKind:  task.AgentStreamKind,
		CreatedAt:        task.CreatedAt,
		UpdatedAt:        task.UpdatedAt,
	}
}

func runtimeProjectDTO(project agxruntime.Project, tasks []agxruntime.Task) Project {
	dto := Project{
		ID:            project.ID,
		Name:          project.Name,
		Path:          project.Path,
		Description:   project.Description,
		DefaultAgent:  project.DefaultAgent,
		AccessGranted: project.AccessGranted,
		AccessError:   project.AccessError,
		Languages:     languageStats(project.Path),
		TaskCount:     len(tasks),
		LastOpened:    project.LastOpened,
		CreatedAt:     project.CreatedAt,
	}
	for _, task := range tasks {
		switch string(task.Status) {
		case string(db.StatusActive):
			dto.ActiveCount++
		case string(db.StatusWaiting):
			dto.WaitingCount++
		case string(db.StatusComplete):
			dto.CompleteCount++
		case string(db.StatusOffline):
			dto.OfflineCount++
		}
	}
	return dto
}

func (a *App) directListProjects() ([]Project, error) {
	projects, err := a.store.ListProjects()
	if err != nil {
		return nil, err
	}
	out := make([]Project, 0, len(projects))
	for _, project := range projects {
		tasks, err := a.store.ListTasks(project.ID, nil)
		if err != nil {
			return nil, err
		}
		tasks = a.refreshTaskStatuses(tasks)
		out = append(out, a.projectDTO(project, tasks))
	}
	return out, nil
}

func (a *App) directGrantProjectAccess(projectID string) (Project, error) {
	project, err := a.store.GetProject(projectID)
	if err != nil {
		return Project{}, err
	}
	if err := worktree.ValidateProject(project.Path); err != nil {
		if repairErr := repairProjectAccess(project.Path); repairErr != nil {
			return Project{}, repairErr
		}
		if err := worktree.ValidateProject(project.Path); err != nil {
			return Project{}, err
		}
	}
	if err := a.store.MarkProjectAccessGranted(project.Path); err != nil {
		return Project{}, err
	}
	tasks, err := a.store.ListTasks(project.ID, nil)
	if err != nil {
		return Project{}, err
	}
	a.emitMetadataEvent(project.ID)
	return a.projectDTO(project, tasks), nil
}

func (a *App) directRegisterProject(path, name, description string) (Project, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return Project{}, fmt.Errorf("project path is required")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return Project{}, fmt.Errorf("project name is required")
	}
	info, err := os.Stat(path)
	if err != nil {
		return Project{}, err
	}
	if !info.IsDir() {
		return Project{}, fmt.Errorf("project path is not a directory: %s", path)
	}
	if err := ensureGitRepository(path); err != nil {
		return Project{}, fmt.Errorf("project path is not a git repository: %s", path)
	}
	project, err := a.store.EnsureProjectDetails(path, name, display.PtrString(description), nil)
	if err != nil {
		return Project{}, err
	}
	tasks, err := a.store.ListTasks(project.ID, nil)
	if err != nil {
		return Project{}, err
	}
	a.emitMetadataEvent(project.ID)
	return a.projectDTO(project, tasks), nil
}

func (a *App) directUpdateProject(projectID, name, description string) (Project, error) {
	if err := a.store.UpdateProjectDetails(projectID, name, display.PtrString(description)); err != nil {
		return Project{}, err
	}
	project, err := a.store.GetProject(projectID)
	if err != nil {
		return Project{}, err
	}
	tasks, err := a.store.ListTasks(project.ID, nil)
	if err != nil {
		return Project{}, err
	}
	a.emitMetadataEvent(project.ID)
	return a.projectDTO(project, tasks), nil
}

func (a *App) directGetProject(id string) (Project, error) {
	project, err := a.store.GetProject(id)
	if err != nil {
		return Project{}, err
	}
	tasks, err := a.store.ListTasks(project.ID, nil)
	if err != nil {
		return Project{}, err
	}
	return a.projectDTO(project, tasks), nil
}

func (a *App) projectPath(projectID string) (string, error) {
	if a.directMode {
		project, err := a.store.GetProject(projectID)
		if err != nil {
			return "", err
		}
		return project.Path, nil
	}
	ctx, cancel := a.runtimeRequestContext(runtimeClientTimeout)
	defer cancel()
	project, err := agxruntime.NewClient().GetProject(ctx, projectID)
	if err != nil {
		return "", err
	}
	return project.Path, nil
}

func (a *App) directListTaskTranscript(taskID string, limit int) ([]TaskTranscriptMessage, error) {
	messages, err := a.store.ListTaskTranscriptMessages(taskID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]TaskTranscriptMessage, 0, len(messages))
	for _, message := range messages {
		out = append(out, TaskTranscriptMessage{
			ID:        message.ID,
			TaskID:    message.TaskID,
			TurnID:    message.TurnID,
			Role:      message.Role,
			Body:      message.Body,
			CreatedAt: message.CreatedAt,
			UpdatedAt: message.UpdatedAt,
		})
	}
	return out, nil
}

func (a *App) directCreateTask(projectID, title, description, agentName string, allMighty bool, initialPrompt *string, workspaceMode db.WorkspaceMode) (Task, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return Task{}, fmt.Errorf("task title is required")
	}
	var descriptionPtr *string
	if description != "" || initialPrompt != nil {
		descriptionPtr = display.PtrString(description)
	}
	project, err := a.store.GetProject(projectID)
	if err != nil {
		return Task{}, err
	}
	if agentName == "" {
		agentName = a.defaultAgentForProject(project)
	}
	registry := a.registryForProject(project.Path)
	if _, err := registry.Get(agentName); err != nil {
		return Task{}, err
	}
	task, err := a.managerForProject(project).RunNewTaskWithOptions(project, title, descriptionPtr, agentName, session.RunOptions{AllMighty: allMighty, InitialPrompt: initialPrompt, WorkspaceMode: workspaceMode})
	if err != nil {
		return Task{}, err
	}
	a.emitMetadataEvent(projectID)
	a.syncDiscordAsync()
	return a.taskDTO(task), nil
}

func (a *App) directUpdateTaskTitle(taskID, title string) (Task, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return Task{}, fmt.Errorf("task title is required")
	}
	task, err := a.store.GetTask(taskID)
	if err != nil {
		return Task{}, err
	}
	if err := a.store.UpdateTask(task.ID, &title, nil, nil); err != nil {
		return Task{}, err
	}
	updated, err := a.store.GetTask(task.ID)
	if err != nil {
		return Task{}, err
	}
	a.emitMetadataEvent(updated.ProjectID)
	a.syncDiscordAsync()
	return a.taskDTO(updated), nil
}

func (a *App) directRunTask(taskID string) error {
	task, err := a.store.GetTask(taskID)
	if err != nil {
		return err
	}
	if task.Interface == db.TaskInterfaceDiscord {
		return fmt.Errorf("task is controlled by Discord")
	}
	project, err := a.store.GetProject(task.ProjectID)
	if err != nil {
		return err
	}
	if err := a.managerForProject(project).RunTask(task); err != nil {
		return err
	}
	a.emitMetadataEvent(project.ID)
	a.syncDiscordAsync()
	return nil
}

func (a *App) directStopTask(taskID string) error {
	task, err := a.store.GetTask(taskID)
	if err != nil {
		return err
	}
	project, err := a.store.GetProject(task.ProjectID)
	if err != nil {
		return err
	}
	if task.AgentStreamKind != nil && *task.AgentStreamKind != "" {
		if err := a.agentEvents.StopTask(context.Background(), task); err != nil {
			return err
		}
		if err := a.store.UpdateTaskStatus(task.ID, db.StatusOffline); err != nil {
			return err
		}
		a.emitMetadataEvent(project.ID)
		a.deleteDiscordTaskChannelAsync(task.ID)
		return nil
	}
	if err := a.managerForProject(project).StopTask(task); err != nil {
		return err
	}
	a.StopLogStream(task.ID)
	if err := a.removeStream(task.ID); err != nil {
		return err
	}
	a.emitMetadataEvent(project.ID)
	a.deleteDiscordTaskChannelAsync(task.ID)
	return nil
}

func (a *App) directDeleteTask(taskID string) error {
	task, err := a.store.GetTask(taskID)
	if err != nil {
		return err
	}
	project, err := a.store.GetProject(task.ProjectID)
	if err != nil {
		return err
	}
	if task.AgentStreamKind != nil && *task.AgentStreamKind != "" {
		if err := a.agentEvents.StopTask(context.Background(), task); err != nil {
			return err
		}
	}
	if err := a.managerForProject(project).DeleteTask(task); err != nil {
		return err
	}
	a.StopLogStream(task.ID)
	if err := a.removeStream(task.ID); err != nil {
		return err
	}
	a.emitMetadataEvent(project.ID)
	a.deleteDiscordTaskChannelAsync(task.ID)
	return nil
}

func (a *App) directSendMessage(taskID, message string) error {
	task, err := a.store.GetTask(taskID)
	if err != nil {
		return err
	}
	if task.Interface == db.TaskInterfaceDiscord {
		return fmt.Errorf("task is controlled by Discord")
	}
	project, err := a.store.GetProject(task.ProjectID)
	if err != nil {
		return err
	}
	return a.managerForProject(project).SendMessage(task, message)
}

func (a *App) directRecordTaskInput(taskID, message string) error {
	task, err := a.store.GetTask(taskID)
	if err != nil {
		return err
	}
	if err := a.store.UpdateTaskLastUserPrompt(task.ID, message); err != nil {
		return err
	}
	a.emitMetadataEvent(task.ProjectID)
	return nil
}

func (a *App) directSendInput(taskID, data string) error {
	task, err := a.store.GetTask(taskID)
	if err != nil {
		return err
	}
	if task.Interface == db.TaskInterfaceDiscord {
		return fmt.Errorf("task is controlled by Discord")
	}
	project, err := a.store.GetProject(task.ProjectID)
	if err != nil {
		return err
	}
	return a.managerForProject(project).SendInput(task, data)
}

func (a *App) directResizeTaskTerminal(taskID string, cols, rows int) error {
	task, err := a.store.GetTask(taskID)
	if err != nil {
		return err
	}
	project, err := a.store.GetProject(task.ProjectID)
	if err != nil {
		return err
	}
	return a.managerForProject(project).ResizeTaskTerminal(task, cols, rows)
}

func (a *App) directGetLogs(taskID string, lines int) (string, error) {
	task, err := a.store.GetTask(taskID)
	if err != nil {
		return "", err
	}
	project, err := a.store.GetProject(task.ProjectID)
	if err != nil {
		return "", err
	}
	manager := a.managerForProject(project)
	streamPath, err := a.streamPath(taskID)
	if err != nil {
		return "", err
	}
	if _, err := compactStreamFile(streamPath, maxStreamLogBytes); err != nil && !os.IsNotExist(err) {
		return "", err
	}
	logs, err := manager.StreamLogs(task, streamPath, lines)
	if err == nil {
		return logs, nil
	}
	return manager.GetLogs(task, lines)
}

func (a *App) directStartLogStream(taskID string, lines int) error {
	if a.ctx == nil {
		return nil
	}
	task, err := a.store.GetTask(taskID)
	if err != nil {
		return err
	}
	project, err := a.store.GetProject(task.ProjectID)
	if err != nil {
		return err
	}
	manager := a.managerForProject(project)
	streamPath, err := a.streamPath(taskID)
	if err != nil {
		return err
	}
	if _, err := compactStreamFile(streamPath, maxStreamLogBytes); err != nil && !os.IsNotExist(err) {
		return err
	}
	logs, err := manager.StreamLogs(task, streamPath, lines)
	if err != nil {
		logs, err = manager.GetLogs(task, lines)
		if err != nil {
			return err
		}
	}

	ctx, cancel := context.WithCancel(a.ctx)
	a.streamMu.Lock()
	a.stopLogStreamLocked(taskID)
	a.streams[taskID] = cancel
	a.streamMu.Unlock()

	a.emitLogEvent(taskID, LogEvent{TaskID: taskID, Data: logs, Reset: true})
	go a.followLogFile(ctx, taskID, streamPath, int64(len(logs)))
	return nil
}

func (a *App) directGetTaskStatus(taskID string) (string, error) {
	task, err := a.store.GetTask(taskID)
	if err != nil {
		return string(db.StatusOffline), err
	}
	return string(a.detectAndStoreStatus(task)), nil
}

func (a *App) discordStatusDTO(status agxdiscord.Status) DiscordStatusInfo {
	a.discordSyncMu.Lock()
	syncJob := a.discordSyncJob
	a.discordSyncMu.Unlock()
	if status.Sync.Running || status.Sync.Stage != "" || status.Sync.Error != "" {
		syncJob = DiscordSyncJob{
			Running:     status.Sync.Running,
			Kind:        status.Sync.Kind,
			Stage:       status.Sync.Stage,
			StartedAt:   status.Sync.StartedAt,
			CompletedAt: status.Sync.CompletedAt,
			Error:       status.Sync.Error,
		}
	}
	return DiscordStatusInfo{
		Enabled:        status.Enabled,
		Connected:      status.Connected,
		GuildID:        status.GuildID,
		GuildName:      status.GuildName,
		AllowedUserIDs: append([]string(nil), status.AllowedUserIDs...),
		MaskedBotToken: status.MaskedBotToken,
		UptimeSeconds:  status.UptimeSeconds,
		Error:          status.Error,
		LockedBy:       status.LockedBy,
		Sync:           syncJob,
	}
}

func runtimeStatusDTO(status agxruntime.Status) RuntimeStatusInfo {
	startedAt := status.StartedAt
	return RuntimeStatusInfo{
		Running:       status.Running,
		PID:           status.PID,
		Version:       status.Version,
		StartedAt:     &startedAt,
		UptimeSeconds: status.UptimeSeconds,
		ConfigDir:     status.ConfigDir,
		SocketPath:    status.SocketPath,
		LockPath:      status.LockPath,
		Recovery:      status.Recovery,
	}
}

func (a *App) detectAndStoreStatus(task db.Task) db.TaskStatus {
	if task.SessionName == nil || *task.SessionName == "" {
		if task.AgentStreamKind != nil && *task.AgentStreamKind != "" {
			return task.Status
		}
		if task.Status != db.StatusOffline {
			_ = a.store.UpdateTaskStatus(task.ID, db.StatusOffline)
			a.emitMetadataEvent(task.ProjectID)
			a.syncDiscordAsync()
			return db.StatusOffline
		}
		return task.Status
	}
	lock := a.taskLock(task.ID)
	lock.Lock()
	defer lock.Unlock()
	project, err := a.store.GetProject(task.ProjectID)
	if err != nil {
		return task.Status
	}
	a.stateMu.Lock()
	snapshot := a.states[task.ID]
	if snapshot.lastActivity.IsZero() {
		snapshot.lastActivity = time.Now()
	}
	a.stateMu.Unlock()

	manager := a.managerForProject(project)
	status, output, err := manager.DetectStatus(task, snapshot.output, snapshot.lastActivity)
	if err != nil {
		return task.Status
	}
	if shouldRetireCompletedShell(task, snapshot, status, output) {
		status = db.StatusOffline
	}
	if status == db.StatusOffline {
		a.StopLogStream(task.ID)
		_ = a.removeStream(task.ID)
	}
	if output != snapshot.output {
		snapshot.lastActivity = time.Now()
	}
	snapshot.output = output

	a.stateMu.Lock()
	a.states[task.ID] = snapshot
	a.stateMu.Unlock()
	if status != task.Status {
		if err := a.store.UpdateTaskStatus(task.ID, status); err == nil {
			a.emitMetadataEvent(task.ProjectID)
			a.syncDiscordAsync()
		}
	}
	return status
}

func shouldRetireCompletedShell(task db.Task, snapshot stateSnapshot, status db.TaskStatus, output string) bool {
	return task.Status == db.StatusComplete && status == db.StatusComplete && snapshot.output != "" && output != snapshot.output
}

func (a *App) refreshTaskStatuses(tasks []db.Task) []db.Task {
	out := make([]db.Task, 0, len(tasks))
	for _, task := range tasks {
		if task.SessionName == nil || *task.SessionName == "" {
			out = append(out, task)
			continue
		}
		status := a.detectAndStoreStatus(task)
		if status != task.Status {
			if refreshed, err := a.store.GetTask(task.ID); err == nil {
				out = append(out, refreshed)
				continue
			}
			task.Status = status
		}
		out = append(out, task)
	}
	return out
}

func isLiveTaskRuntime(task db.Task) bool {
	if task.SessionName != nil && *task.SessionName != "" {
		return true
	}
	if task.Status != db.StatusActive && task.Status != db.StatusWaiting {
		return false
	}
	return task.AgentStreamKind != nil && *task.AgentStreamKind != ""
}

func (a *App) taskLock(taskID string) *sync.Mutex {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	lock := a.locks[taskID]
	if lock == nil {
		lock = &sync.Mutex{}
		a.locks[taskID] = lock
	}
	return lock
}

func (a *App) projectDTO(project db.Project, tasks []db.Task) Project {
	dto := Project{
		ID:           project.ID,
		Name:         project.Name,
		Path:         project.Path,
		Description:  project.Description,
		DefaultAgent: project.DefaultAgent,
		Languages:    languageStats(project.Path),
		TaskCount:    len(tasks),
		LastOpened:   project.LastOpened,
		CreatedAt:    project.CreatedAt,
	}
	granted, err := a.store.HasProjectAccessGrant(project.Path)
	if err != nil {
		message := err.Error()
		dto.AccessError = &message
	} else if granted {
		dto.AccessGranted = true
	}
	if !dto.AccessGranted && dto.AccessError == nil {
		message := "Grant access before creating tasks so AGX can create Git worktrees."
		dto.AccessError = &message
	}
	for _, task := range tasks {
		switch task.Status {
		case db.StatusActive:
			dto.ActiveCount++
		case db.StatusWaiting:
			dto.WaitingCount++
		case db.StatusComplete:
			dto.CompleteCount++
		case db.StatusOffline:
			dto.OfflineCount++
		}
	}
	return dto
}

func resolveProjectPath(projectPath, relativePath string) (string, string, error) {
	root, err := filepath.Abs(projectPath)
	if err != nil {
		return "", "", err
	}
	target := filepath.Clean(filepath.Join(root, relativePath))
	if target != root && !strings.HasPrefix(target, root+string(os.PathSeparator)) {
		return "", "", fmt.Errorf("path escapes project root: %s", relativePath)
	}
	return root, target, nil
}

func sameProjectPath(left, right string) bool {
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	if left == right {
		return true
	}
	leftReal, leftErr := filepath.EvalSymlinks(left)
	rightReal, rightErr := filepath.EvalSymlinks(right)
	return leftErr == nil && rightErr == nil && filepath.Clean(leftReal) == filepath.Clean(rightReal)
}

func resolveRealProjectPath(root, target string) (string, error) {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	realTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		return "", err
	}
	if realTarget != realRoot && !strings.HasPrefix(realTarget, realRoot+string(os.PathSeparator)) {
		return "", fmt.Errorf("path escapes project root through symlink: %s", target)
	}
	return realTarget, nil
}

func shouldHideFile(name string, showHidden bool) bool {
	if showHidden {
		return false
	}
	return strings.HasPrefix(name, ".")
}

func isGitIgnored(projectPath, rel string) bool {
	if rel == "." || rel == "" {
		return false
	}
	cmd := exec.Command("git", "-C", projectPath, "check-ignore", "-q", "--", rel)
	return cmd.Run() == nil
}

func gitVisibleFiles(projectPath string) ([]string, error) {
	cmd := exec.Command("git", "-C", projectPath, "ls-files", "-co", "--exclude-standard")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	files := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(filepath.ToSlash(line))
		if line == "" || shouldHideFile(filepath.Base(line), false) {
			continue
		}
		files = append(files, line)
	}
	return files, nil
}

func fuzzyScore(candidate, query string) (int, bool) {
	candidateRunes := []rune(strings.ToLower(candidate))
	queryRunes := []rune(strings.ToLower(query))
	if len(queryRunes) == 0 {
		return 0, true
	}
	score := 0
	queryIndex := 0
	lastMatch := -1
	for i, r := range candidateRunes {
		if queryIndex >= len(queryRunes) {
			break
		}
		if r != queryRunes[queryIndex] {
			continue
		}
		if lastMatch >= 0 {
			score += i - lastMatch - 1
		} else {
			score += i
		}
		lastMatch = i
		queryIndex++
	}
	if queryIndex != len(queryRunes) {
		return 0, false
	}
	if strings.Contains(string(candidateRunes), string(queryRunes)) {
		score -= len(queryRunes)
	}
	return score, true
}

func ensureGitRepository(path string) error {
	_, err := gitRoot(path)
	return err
}

func (a *App) ensureProjectWriteAccess(path string) error {
	err := ensureWritableProjectDirectory(path)
	if err == nil {
		return nil
	}
	if a.ctx == nil {
		return err
	}
	selected, dialogErr := a.selectDirectory("Grant AGX Write Access", path)
	if dialogErr != nil {
		return fmt.Errorf("%w; grant access dialog failed: %v", err, dialogErr)
	}
	if strings.TrimSpace(selected) == "" {
		return fmt.Errorf("%w; AGX needs write access to run tasks in this project", err)
	}
	if same, compareErr := sameGitRepository(path, selected); compareErr != nil {
		return fmt.Errorf("%w; selected path cannot be validated: %v", err, compareErr)
	} else if !same {
		return fmt.Errorf("%w; selected path must be the same git repository: %s", err, path)
	}
	if retryErr := ensureWritableProjectDirectory(path); retryErr != nil {
		return fmt.Errorf("%w; write access is still unavailable after selecting the project folder: %v", err, retryErr)
	}
	return nil
}

func ensureWritableProjectDirectory(path string) error {
	script := `test_path="$1/.agx-write-test-$$"; : > "$test_path" && rm -f "$test_path"`
	output, err := exec.Command("/bin/sh", "-c", script, "agx-write-test", path).CombinedOutput()
	if err != nil {
		return fmt.Errorf("project path is not writable by AGX child processes: %s: %s: %w", path, strings.TrimSpace(string(output)), err)
	}
	return nil
}

func repairProjectAccess(path string) error {
	if runtime.GOOS != "darwin" {
		return worktree.ValidateProject(path)
	}
	if err := runXattrClear(path); err == nil {
		return nil
	}
	if err := runXattrClearWithAdministratorPrivileges(path); err != nil {
		return err
	}
	return nil
}

func runXattrClear(path string) error {
	if output, err := exec.Command("xattr", "-cr", path).CombinedOutput(); err != nil {
		return fmt.Errorf("grant project access: %s: %w", strings.TrimSpace(string(output)), err)
	}
	return nil
}

func runXattrClearWithAdministratorPrivileges(path string) error {
	command := "/usr/bin/xattr -cr " + shellQuote(path)
	script := fmt.Sprintf(`do shell script "%s" with administrator privileges`, appleScriptString(command))
	output, err := exec.Command("osascript", "-e", script).CombinedOutput()
	if err != nil {
		return fmt.Errorf("grant project access with administrator privileges: %s: %w", strings.TrimSpace(string(output)), err)
	}
	return nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func sameGitRepository(left, right string) (bool, error) {
	leftRoot, err := gitRoot(left)
	if err != nil {
		return false, err
	}
	rightRoot, err := gitRoot(right)
	if err != nil {
		return false, err
	}
	leftRoot, err = filepath.EvalSymlinks(leftRoot)
	if err != nil {
		leftRoot = filepath.Clean(leftRoot)
	}
	rightRoot, err = filepath.EvalSymlinks(rightRoot)
	if err != nil {
		rightRoot = filepath.Clean(rightRoot)
	}
	return leftRoot == rightRoot, nil
}

func gitRoot(path string) (string, error) {
	cmd := exec.Command("git", "-C", path, "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	root := strings.TrimSpace(string(out))
	if root == "" {
		return "", fmt.Errorf("git root is empty")
	}
	return filepath.Abs(root)
}

func discoverGitRepositories(limit int) ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	roots := []string{
		home,
		filepath.Join(home, "github"),
		filepath.Join(home, "code"),
		filepath.Join(home, "dev"),
		filepath.Join(home, "src"),
		filepath.Join(home, "workspace"),
		filepath.Join(home, "projects"),
	}
	seenRoots := map[string]bool{}
	orderedRoots := make([]string, 0, len(roots))
	for _, root := range roots {
		root = filepath.Clean(root)
		if seenRoots[root] {
			continue
		}
		if info, err := os.Stat(root); err == nil && info.IsDir() {
			seenRoots[root] = true
			orderedRoots = append(orderedRoots, root)
		}
	}

	var repos []string
	seenRepos := map[string]bool{}
	scanLimit := limit * 10
	if scanLimit < limit {
		scanLimit = limit
	}
	for _, root := range orderedRoots {
		if len(repos) >= scanLimit {
			break
		}
		_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil || len(repos) >= scanLimit {
				return nil
			}
			if !entry.IsDir() {
				return nil
			}
			name := entry.Name()
			if name != filepath.Base(root) && shouldSkipRepoScanDir(name) {
				return filepath.SkipDir
			}
			if hasGitMetadata(path) {
				repo, err := gitRoot(path)
				if err == nil && !seenRepos[repo] {
					seenRepos[repo] = true
					repos = append(repos, repo)
				}
				return filepath.SkipDir
			}
			if repoScanDepth(root, path) >= 4 {
				return filepath.SkipDir
			}
			return nil
		})
	}
	sort.Strings(repos)
	return repos, nil
}

func shouldSkipRepoScanDir(name string) bool {
	switch name {
	case ".cache", ".config", ".docker", ".git", ".gopath", ".gradle", ".local", ".npm", ".pnpm-store", ".rustup", ".terraform", ".venv", "Library", "Applications", "Movies", "Music", "Pictures", "node_modules", "vendor":
		return true
	default:
		return strings.HasPrefix(name, ".") && name != "."
	}
}

func hasGitMetadata(path string) bool {
	info, err := os.Stat(filepath.Join(path, ".git"))
	return err == nil && (info.IsDir() || info.Mode().IsRegular())
}

func repoScanDepth(root, path string) int {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." {
		return 0
	}
	return len(strings.Split(rel, string(os.PathSeparator)))
}

func languageStats(projectPath string) []LanguageStat {
	files, err := gitVisibleFiles(projectPath)
	if err != nil || len(files) == 0 {
		return nil
	}
	type stat struct {
		files int
		bytes int64
	}
	stats := map[string]stat{}
	var total int64
	for _, file := range files {
		language := languageForPath(file)
		if language == "" {
			continue
		}
		info, err := os.Stat(filepath.Join(projectPath, filepath.FromSlash(file)))
		if err != nil || info.IsDir() {
			continue
		}
		size := info.Size()
		if size <= 0 {
			size = 1
		}
		current := stats[language]
		current.files++
		current.bytes += size
		stats[language] = current
		total += size
	}
	if total == 0 {
		return nil
	}
	out := make([]LanguageStat, 0, len(stats))
	for name, stat := range stats {
		out = append(out, LanguageStat{
			Name:       name,
			Files:      stat.files,
			Percentage: math.Round((float64(stat.bytes)/float64(total))*1000) / 10,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Percentage == out[j].Percentage {
			return out[i].Name < out[j].Name
		}
		return out[i].Percentage > out[j].Percentage
	})
	if len(out) > 5 {
		out = out[:5]
	}
	return out
}

func languageForPath(path string) string {
	base := strings.ToLower(filepath.Base(path))
	switch base {
	case "dockerfile", "containerfile":
		return "Dockerfile"
	case "makefile":
		return "Makefile"
	}
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".go":
		return "Go"
	case ".ts", ".tsx":
		return "TypeScript"
	case ".js", ".jsx", ".mjs", ".cjs":
		return "JavaScript"
	case ".py":
		return "Python"
	case ".rs":
		return "Rust"
	case ".java":
		return "Java"
	case ".kt", ".kts":
		return "Kotlin"
	case ".swift":
		return "Swift"
	case ".c", ".h":
		return "C"
	case ".cc", ".cpp", ".cxx", ".hpp", ".hh":
		return "C++"
	case ".cs":
		return "C#"
	case ".rb":
		return "Ruby"
	case ".php":
		return "PHP"
	case ".scala":
		return "Scala"
	case ".sh", ".bash", ".zsh":
		return "Shell"
	case ".html", ".htm":
		return "HTML"
	case ".css", ".scss", ".sass", ".less":
		return "CSS"
	case ".sql":
		return "SQL"
	case ".md", ".mdx":
		return "Markdown"
	case ".json", ".yaml", ".yml", ".toml", ".xml":
		return "Config"
	default:
		return ""
	}
}
