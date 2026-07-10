package runtime

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nashory/agx/internal/agent"
	"github.com/nashory/agx/internal/config"
	"github.com/nashory/agx/internal/db"
	agxdiscord "github.com/nashory/agx/internal/discord"
	"github.com/nashory/agx/internal/display"
	"github.com/nashory/agx/internal/session"
)

// Service owns the long-running AGX runtime daemon. It is the single process
// that owns the SQLite store, session backend, Discord bridge, and structured
// agent event streams so desktop and CLI clients can coordinate through one
// serialized API surface.
type Service struct {
	paths          Paths
	version        string
	started        time.Time
	bus            *EventBus
	store          *db.Store
	sessionBackend session.Backend
	recovery       session.RecoveryResult
	locks          map[string]*sync.Mutex
	states         map[string]runtimeTaskState
	locksMu        sync.Mutex
	requestSeq     atomic.Uint64
	discord        *agxdiscord.Bridge
	agents         *agentEventService
	attachments    attachmentDownloader
	voice          voiceTranscriber

	discordSyncMu      sync.Mutex
	discordSyncRunning bool
	discordSyncPending bool
	discordHardSyncJob agxdiscord.SyncStatusSummary

	lock   *Lock
	server *http.Server
	ln     net.Listener

	// transportToken is the bearer token required by the localhost TCP
	// transport. It is populated only on native Windows (see transport_windows.go)
	// and left empty on Unix, where the socket permissions gate access.
	transportToken string
	// transportAddress is the loopback TCP address the runtime serves on. It is
	// populated only on native Windows; empty means the Unix socket transport.
	transportAddress string

	backgroundCtx    context.Context
	backgroundCancel context.CancelFunc
	shutdownOnce     sync.Once
	shutdownCh       chan struct{}
}

// Status is the runtime health snapshot returned to clients and emitted on the
// event stream.
type Status struct {
	Running       bool                   `json:"running"`
	PID           int                    `json:"pid"`
	Version       string                 `json:"version"`
	StartedAt     time.Time              `json:"startedAt"`
	UptimeSeconds int64                  `json:"uptimeSeconds"`
	ConfigDir     string                 `json:"configDir"`
	SocketPath    string                 `json:"socketPath"`
	LockPath      string                 `json:"lockPath"`
	Transport     string                 `json:"transport,omitempty"`
	Recovery      session.RecoveryResult `json:"recovery"`
}

// NewService constructs an idle runtime service. Start must be called before
// the service owns the database, socket, or Discord bridge.
func NewService(version string) *Service {
	backgroundCtx, backgroundCancel := context.WithCancel(context.Background())
	service := &Service{
		paths:            DefaultPaths(),
		version:          version,
		started:          time.Now().UTC(),
		bus:              NewEventBus(),
		sessionBackend:   session.DefaultBackend(),
		locks:            map[string]*sync.Mutex{},
		states:           map[string]runtimeTaskState{},
		attachments:      defaultAttachmentDownloader(),
		voice:            defaultVoiceTranscriber(),
		backgroundCtx:    backgroundCtx,
		backgroundCancel: backgroundCancel,
		shutdownCh:       make(chan struct{}),
	}
	service.discord = agxdiscord.NewBridge(config.DiscordConfig{})
	service.agents = newAgentEventService(service)
	return service
}

type runtimeTaskState struct {
	output       string
	lastActivity time.Time
}

const (
	discordTaskSyncTimeout       = 8 * time.Second
	discordTaskManualSyncTimeout = 2 * time.Minute
	// discordConnectTimeout bounds a Discord connect performed on the service
	// background context so a disconnected CLI client cannot leave it running
	// forever, while still allowing enough time for the gateway handshake,
	// owner claim, and command registration.
	discordConnectTimeout = 2 * time.Minute
)

// Start acquires the daemon lock, opens the runtime database, recovers persisted
// task state, and serves the Unix-socket API until ctx is canceled or Shutdown
// is called. Startup intentionally performs recovery before accepting clients so
// stale task/session state is not exposed through the API.
func (s *Service) Start(ctx context.Context) (err error) {
	started := time.Now()
	logRuntimeOperation("runtime_start",
		"status", "starting",
		"config_dir", s.paths.ConfigDir,
		"socket", s.paths.Socket,
		"version", s.version,
	)
	defer func() {
		if err != nil && !errors.Is(err, context.Canceled) {
			logRuntimeOperation("runtime_start",
				"status", "failed",
				"elapsed_ms", time.Since(started).Milliseconds(),
				"error", err,
			)
		}
	}()
	if err := os.MkdirAll(s.paths.ConfigDir, 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	if err := os.Chmod(s.paths.ConfigDir, 0o700); err != nil {
		return fmt.Errorf("chmod config dir: %w", err)
	}
	lock, err := AcquireLock(s.paths.Lock)
	if err != nil {
		return err
	}
	s.lock = lock
	ln, err := s.bindListener()
	if err != nil {
		_ = lock.Release()
		return err
	}
	store, err := db.Open()
	if err != nil {
		_ = ln.Close()
		_ = s.cleanupListener()
		_ = lock.Release()
		return fmt.Errorf("open runtime database: %w", err)
	}
	s.store = store
	if err := s.cleanupOrphanAttachments(); err != nil {
		_ = store.Close()
		_ = ln.Close()
		_ = s.cleanupListener()
		_ = lock.Release()
		return fmt.Errorf("cleanup orphan attachments: %w", err)
	}
	cfg, _ := config.LoadGlobal()
	s.discord.Configure(cfg.Discord)
	s.discord.SetStore(store)
	s.discord.SetCommandService(discordCommandService{runtime: s})
	s.discord.SetAgentEventSubscriber(runtimeAgentSubscriber{runtime: s})
	recovery, err := session.NewManager(store, s.sessionBackend, agent.RegistryForProject("")).RecoverLiveTasks()
	if err != nil {
		_ = store.Close()
		_ = ln.Close()
		_ = s.cleanupListener()
		_ = lock.Release()
		return fmt.Errorf("recover runtime tasks: %w", err)
	}
	s.recovery = recovery
	s.ln = ln
	s.server = &http.Server{Handler: s.wrapTransportAuth(s.routes())}
	s.bus.Publish("runtime.status", s.Status())
	logRuntimeOperation("runtime_recovery",
		"offline", recovery.Offline,
		"cleared", recovery.Cleared,
		"orphans", recovery.Orphans,
	)
	logRuntimeOperation("runtime_start",
		"status", "serving",
		"elapsed_ms", time.Since(started).Milliseconds(),
		"socket", s.paths.Socket,
		"discord_enabled", cfg.Discord.Enabled,
	)
	if cfg.Discord.Enabled {
		go func() {
			startCtx, cancel := s.backgroundTimeout(15 * time.Second)
			defer cancel()
			if err := s.discord.Start(startCtx, "runtime"); err != nil {
				logRuntimeOperation("discord_startup", "error", err)
			}
			s.bus.Publish("discord.status", s.discord.Status())
		}()
	}

	errCh := make(chan error, 1)
	go func() {
		err := s.server.Serve(ln)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errCh <- err
	}()

	select {
	case <-ctx.Done():
		_ = s.Shutdown(context.Background())
		return ctx.Err()
	case <-s.shutdownCh:
		return <-errCh
	case err := <-errCh:
		return err
	}
}

func (s *Service) managerForProject(project db.Project) *session.Manager {
	return session.NewManager(s.store, s.sessionBackend, agent.RegistryForProject(project.Path))
}

// taskLock returns a per-task mutex for operations that mutate tmux/runtime
// state. Project-level operations use synthetic keys such as "project:<id>" to
// prevent conflicting task creation modes from racing.
func (s *Service) taskLock(taskID string) *sync.Mutex {
	s.locksMu.Lock()
	defer s.locksMu.Unlock()
	if s.locks[taskID] == nil {
		s.locks[taskID] = &sync.Mutex{}
	}
	return s.locks[taskID]
}

// refreshTaskStatuses reconciles persisted task rows with live tmux state before
// returning them to clients. The returned slice contains refreshed database rows
// where status changed successfully, preserving the original row on read errors.
func (s *Service) refreshTaskStatuses(tasks []db.Task) []db.Task {
	out := make([]db.Task, 0, len(tasks))
	for _, task := range tasks {
		status := s.detectAndStoreStatus(task)
		if status != task.Status {
			if refreshed, err := s.store.GetTask(task.ID); err == nil {
				out = append(out, refreshed)
				continue
			}
			task.Status = status
		}
		out = append(out, task)
	}
	return out
}

// detectAndStoreStatus is the runtime's source of truth for legacy tmux task
// liveness. Structured agent tasks without a tmux session are left untouched;
// legacy tasks without a session are marked offline and active tmux tasks are
// sampled through the session manager.
func (s *Service) detectAndStoreStatus(task db.Task) db.TaskStatus {
	if task.SessionName == nil || *task.SessionName == "" {
		if task.AgentStreamKind != nil && *task.AgentStreamKind != "" {
			return task.Status
		}
		if task.Status != db.StatusOffline {
			_ = s.store.UpdateTaskStatus(task.ID, db.StatusOffline)
			updated := task
			updated.Status = db.StatusOffline
			s.bus.Publish("task.changed", s.taskDTO(updated))
			return db.StatusOffline
		}
		return task.Status
	}
	lock := s.taskLock(task.ID)
	lock.Lock()
	defer lock.Unlock()
	project, err := s.store.GetProject(task.ProjectID)
	if err != nil {
		return task.Status
	}
	s.locksMu.Lock()
	state := s.states[task.ID]
	if state.lastActivity.IsZero() {
		state.lastActivity = time.Now()
	}
	s.locksMu.Unlock()

	status, output, err := s.managerForProject(project).DetectStatus(task, state.output, state.lastActivity)
	if err != nil {
		return task.Status
	}
	if shouldRetireCompletedRuntimeShell(task, state, status, output) {
		status = db.StatusOffline
	}
	if output != state.output {
		state.lastActivity = time.Now()
	}
	state.output = output

	s.locksMu.Lock()
	s.states[task.ID] = state
	s.locksMu.Unlock()
	if status != task.Status {
		if err := s.store.UpdateTaskStatus(task.ID, status); err == nil {
			updated := task
			updated.Status = status
			s.bus.Publish("task.changed", s.taskDTO(updated))
		}
	}
	return status
}

// shouldRetireCompletedRuntimeShell detects the common case where a completed
// wrapper shell stays open and later receives user input. Once output changes
// after completion, AGX treats the managed task as offline rather than active.
func shouldRetireCompletedRuntimeShell(task db.Task, state runtimeTaskState, status db.TaskStatus, output string) bool {
	return task.Status == db.StatusComplete && status == db.StatusComplete && state.output != "" && output != state.output
}

// Shutdown stops the HTTP server, Discord bridge, structured agent runtimes, and
// database exactly once. It is safe to call from both the API handler and the
// Start context cancellation path.
func (s *Service) Shutdown(ctx context.Context) error {
	started := time.Now()
	var err error
	s.shutdownOnce.Do(func() {
		logRuntimeOperation("runtime_shutdown", "status", "stopping")
		if s.backgroundCancel != nil {
			s.backgroundCancel()
		}
		s.bus.Publish("runtime.status", map[string]any{"running": false})
		if s.server != nil {
			err = s.server.Shutdown(ctx)
		}
		if s.ln != nil {
			_ = s.ln.Close()
		}
		if s.discord != nil {
			if stopErr := s.discord.Stop(); err == nil {
				err = stopErr
			}
		}
		if s.agents != nil {
			if closeErr := s.agents.Close(); err == nil {
				err = closeErr
			}
		}
		if s.store != nil {
			if closeErr := s.store.Close(); err == nil {
				err = closeErr
			}
			s.store = nil
		}
		if removeErr := s.cleanupListener(); err == nil && removeErr != nil {
			err = removeErr
		}
		if s.lock != nil {
			if releaseErr := s.lock.Release(); err == nil {
				err = releaseErr
			}
		}
		if err == nil {
			logRuntimeOperation("runtime_shutdown",
				"status", "stopped",
				"elapsed_ms", time.Since(started).Milliseconds(),
			)
		} else {
			logRuntimeOperation("runtime_shutdown",
				"status", "failed",
				"elapsed_ms", time.Since(started).Milliseconds(),
				"error", err,
			)
		}
		close(s.shutdownCh)
	})
	return err
}

func (s *Service) backgroundContext() context.Context {
	if s.backgroundCtx == nil {
		return context.Background()
	}
	return s.backgroundCtx
}

func (s *Service) backgroundTimeout(timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(s.backgroundContext(), timeout)
}

func (s *Service) emitMetadataEvent(projectID string) {
	s.bus.Publish("project.changed", map[string]string{"projectId": projectID})
}

func (s *Service) discordStatus() agxdiscord.Status {
	status := s.discord.Status()
	hardSync := s.discordSyncStatus()
	if status.Sync.Running {
		return status
	}
	status.Sync = hardSync
	return status
}

func (s *Service) discordSyncStatus() agxdiscord.SyncStatusSummary {
	s.discordSyncMu.Lock()
	defer s.discordSyncMu.Unlock()
	return s.discordHardSyncJob
}

func (s *Service) startDiscordHardSync(preserveControlChannelID string) error {
	if !s.discord.Status().Connected {
		cfg, _ := config.LoadGlobal()
		s.discord.Configure(cfg.Discord)
		s.discord.SetStore(s.store)
		startCtx, cancel := s.backgroundTimeout(15 * time.Second)
		defer cancel()
		if err := s.discord.Start(startCtx, "runtime"); err != nil {
			return err
		}
	}
	now := time.Now()
	s.discordSyncMu.Lock()
	if s.discordHardSyncJob.Running {
		s.discordSyncMu.Unlock()
		return nil
	}
	s.discordHardSyncJob = agxdiscord.SyncStatusSummary{
		Running:   true,
		Kind:      "hard",
		Stage:     "Starting hard sync",
		StartedAt: &now,
	}
	s.discordSyncMu.Unlock()
	s.bus.Publish("discord.status", s.discordStatus())

	go func() {
		ctx, cancel := s.backgroundTimeout(10 * time.Minute)
		defer cancel()
		err := s.discord.HardSyncPreserving(ctx, preserveControlChannelID)
		completed := time.Now()
		s.discordSyncMu.Lock()
		s.discordHardSyncJob.Running = false
		s.discordHardSyncJob.CompletedAt = &completed
		if err != nil {
			s.discordHardSyncJob.Stage = "Hard sync failed"
			s.discordHardSyncJob.Error = err.Error()
		} else {
			s.discordHardSyncJob.Stage = "Hard sync completed"
			s.discordHardSyncJob.Error = ""
		}
		s.discordSyncMu.Unlock()
		s.bus.Publish("discord.status", s.discordStatus())
	}()
	return nil
}

// syncDiscordAsync coalesces background soft-sync requests. If a task/project
// change arrives while a sync is running, one additional sync is queued instead
// of spawning an unbounded number of Discord API calls.
func (s *Service) syncDiscordAsync() {
	if s.discord == nil || !s.discord.Status().Connected {
		return
	}
	s.discordSyncMu.Lock()
	if s.discordSyncRunning {
		s.discordSyncPending = true
		s.discordSyncMu.Unlock()
		return
	}
	s.discordSyncRunning = true
	s.discordSyncMu.Unlock()
	go func() {
		for {
			ctx, cancel := s.backgroundTimeout(15 * time.Second)
			if err := s.discord.SoftSync(ctx); err != nil {
				logRuntimeOperation("discord_soft_sync_background", "error", err)
			}
			cancel()
			s.bus.Publish("discord.status", s.discord.Status())

			s.discordSyncMu.Lock()
			if !s.discordSyncPending {
				s.discordSyncRunning = false
				s.discordSyncMu.Unlock()
				return
			}
			s.discordSyncPending = false
			s.discordSyncMu.Unlock()
		}
	}()
}

// syncDiscordTaskNow refreshes one Discord task channel in the foreground so a
// newly-created Discord task does not wait for the next full soft-sync pass.
func (s *Service) syncDiscordTaskNow(taskID string) error {
	if s.discord == nil || !s.discord.Status().Connected {
		return nil
	}
	ctx, cancel := s.backgroundTimeout(discordTaskSyncTimeout)
	defer cancel()
	err := s.discord.SyncTaskChannel(ctx, taskID)
	s.bus.Publish("discord.status", s.discord.Status())
	return err
}

// syncDiscordTaskBestEffort tries the low-latency foreground path first and
// queues a retry if Discord is slow or temporarily rejects the channel update.
func (s *Service) syncDiscordTaskBestEffort(taskID string) {
	if err := s.syncDiscordTaskNow(taskID); err != nil {
		logRuntimeOperation("discord_task_sync_foreground", "task", display.ShortID(taskID), "error", err)
		if s.discord != nil && s.discord.Status().Connected {
			s.discord.RefreshTaskStreams(s.backgroundContext())
		}
		s.syncDiscordTaskAsync(taskID)
	}
}

// syncDiscordTaskAsync refreshes one Discord task channel after a task mutation
// without blocking the caller.
func (s *Service) syncDiscordTaskAsync(taskID string) {
	if s.discord == nil || !s.discord.Status().Connected {
		return
	}
	go func() {
		if err := s.syncDiscordTaskNow(taskID); err != nil {
			logRuntimeOperation("discord_task_sync_background", "task", display.ShortID(taskID), "error", err)
		}
	}()
}

// Status returns the daemon's in-memory status snapshot. It does not probe the
// socket or lock file; clients use transport success/failure for reachability.
func (s *Service) Status() Status {
	return Status{
		Running:       true,
		PID:           os.Getpid(),
		Version:       s.version,
		StartedAt:     s.started,
		UptimeSeconds: int64(time.Since(s.started).Seconds()),
		ConfigDir:     s.paths.ConfigDir,
		SocketPath:    s.paths.Socket,
		LockPath:      s.paths.Lock,
		Transport:     s.transportLabel(),
		Recovery:      s.recovery,
	}
}

// transportLabel describes the transport the runtime is serving on: the loopback
// TCP address on native Windows, or the Unix socket path elsewhere.
func (s *Service) transportLabel() string {
	if s.transportAddress != "" {
		return "tcp " + s.transportAddress
	}
	return "unix " + s.paths.Socket
}
