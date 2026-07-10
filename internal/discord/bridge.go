package discord

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/nashory/agx/internal/agentstream"
	"github.com/nashory/agx/internal/config"
	"github.com/nashory/agx/internal/db"
)

// Status reports Discord bridge configuration, connection state, and the last
// operational error safe for UI display.
type Status struct {
	Enabled        bool              `json:"enabled"`
	Connected      bool              `json:"connected"`
	GuildID        string            `json:"guildId,omitempty"`
	GuildName      string            `json:"guildName,omitempty"`
	AllowedUserIDs []string          `json:"allowedUserIds,omitempty"`
	MaskedBotToken string            `json:"maskedBotToken,omitempty"`
	UptimeSeconds  int64             `json:"uptimeSeconds"`
	Error          string            `json:"error,omitempty"`
	LockedBy       string            `json:"lockedBy,omitempty"`
	StartedAt      time.Time         `json:"startedAt,omitempty"`
	Sync           SyncStatusSummary `json:"sync"`
}

// Bridge owns the Discord bot connection and coordinates channel sync, command
// routing, and live structured-agent event forwarding for one AGX runtime.
type Bridge struct {
	lifecycle          sync.Mutex
	mu                 sync.Mutex
	hardSync           sync.RWMutex
	taskSync           chan struct{}
	maintSync          chan struct{}
	chanSync           chan struct{}
	syncState          sync.Mutex
	active             map[string]activeSync
	permMu             sync.Mutex
	permRun            bool
	permNext           bool
	cfg                config.DiscordConfig
	bot                *Bot
	lock               *Lock
	service            CommandService
	events             AgentEventSubscriber
	store              *db.Store
	streams            map[string]taskStream
	owner              string
	ownerChan          string
	ownerHeartbeatStop chan struct{}
	startedAt          time.Time
	lastErr            string
	connected          bool
}

type taskStream struct {
	channelID string
	cancel    context.CancelFunc
}

var ErrSyncInProgress = errors.New("discord sync is already running")

type activeSync struct {
	SyncID      string
	Kind        string
	Priority    string
	TaskID      string
	CurrentStep string
	StartedAt   time.Time
}

type syncInProgressError struct {
	owners []activeSync
}

func (e syncInProgressError) Error() string {
	if len(e.owners) == 0 {
		return ErrSyncInProgress.Error()
	}
	owner := e.owners[0]
	detail := owner.Kind
	if owner.TaskID != "" {
		detail += " " + owner.TaskID
	}
	if owner.CurrentStep != "" {
		detail += " at " + owner.CurrentStep
	}
	return ErrSyncInProgress.Error() + ": " + detail
}

func (e syncInProgressError) Unwrap() error {
	return ErrSyncInProgress
}

// NewBridge constructs a disconnected bridge with cfg. Dependencies such as the
// store and command service can be attached before Start.
func NewBridge(cfg config.DiscordConfig) *Bridge {
	return &Bridge{
		cfg:       cfg,
		taskSync:  make(chan struct{}, 1),
		maintSync: make(chan struct{}, 1),
		chanSync:  make(chan struct{}, 1),
		active:    map[string]activeSync{},
		streams:   map[string]taskStream{},
	}
}

// Configure replaces the bridge config and clears the last error. It does not
// start, stop, or reconnect the bot by itself.
func (b *Bridge) Configure(cfg config.DiscordConfig) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.cfg = cfg
	b.lastErr = ""
}

func (b *Bridge) SetCommandService(service CommandService) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.service = service
}

func (b *Bridge) SetAgentEventSubscriber(events AgentEventSubscriber) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = events
}

func (b *Bridge) SetStore(store *db.Store) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.store = store
}

// Start validates config, acquires the Discord bridge lock, and opens the bot.
// Task-channel sync can involve many Discord REST calls, so it runs after the
// bridge is marked connected instead of blocking the connect/status path.
func (b *Bridge) Start(ctx context.Context, mode string) error {
	return b.start(ctx, mode, true, false)
}

// StartWithoutInitialSync opens the bot without launching the background full
// channel sync. It is used by foreground sync operations that already know the
// exact sync work they need to perform.
func (b *Bridge) StartWithoutInitialSync(ctx context.Context, mode string) error {
	return b.start(ctx, mode, false, false)
}

// StartWithTakeover behaves like Start but, when the guild is owned by a stale
// runtime, explicitly takes ownership instead of failing. It never takes over
// from an owner that still looks alive.
func (b *Bridge) StartWithTakeover(ctx context.Context, mode string) error {
	return b.start(ctx, mode, true, true)
}

func (b *Bridge) start(ctx context.Context, mode string, initialSync, takeover bool) error {
	b.lifecycle.Lock()
	defer b.lifecycle.Unlock()

	b.mu.Lock()
	if b.connected {
		b.mu.Unlock()
		return nil
	}
	cfg := b.cfg
	service := b.service
	store := b.store
	b.mu.Unlock()

	started := time.Now()
	logConnectPhase("begin", started, "mode", mode, "guild", cfg.GuildID, "takeover", takeover)

	if err := ValidateConfig(cfg); err != nil {
		b.setError(err)
		return err
	}

	lock, err := AcquireLock(mode)
	if err != nil {
		b.setError(err)
		return err
	}
	logConnectPhase("lock_acquired", started)
	bot, err := NewBot(cfg.BotToken)
	if err != nil {
		_ = lock.Release()
		b.setError(err)
		return err
	}
	if service != nil {
		router := NewCommandRouter(cfg, service)
		bot.AddCommandHandler(router)
		bot.AddComponentHandler(router)
		bot.AddPlainMessageHandler(router)
		bot.AddReactionHandler(router)
	}
	if err := bot.Open(ctx); err != nil {
		_ = bot.Close()
		_ = lock.Release()
		b.setError(err)
		return err
	}
	logConnectPhase("gateway_opened", started)
	owner := newGuildOwner(mode)
	var ownerChannelID string
	if takeover {
		ownerChannelID, owner, err = takeoverGuildOwner(ctx, bot, cfg.GuildID, owner)
	} else {
		ownerChannelID, err = claimGuildOwner(ctx, bot, cfg.GuildID, owner)
	}
	if err != nil {
		_ = bot.Close()
		_ = lock.Release()
		b.setError(err)
		logConnectPhase("owner_claim_failed", started, "error", err.Error())
		return err
	}
	logConnectPhase("owner_claimed", started, "control_channel", ownerChannelID)
	if service != nil {
		// Command registration is best-effort: Discord persists guild commands
		// across restarts, so a rate-limited or slow command endpoint must not
		// fail the whole connect. Log and proceed with the existing commands.
		if err := bot.RegisterCommands(ctx, cfg.GuildID); err != nil {
			log.Printf("operation=%q status=%q guild=%q error=%q", "discord_register_commands", "skipped", cfg.GuildID, err)
		}
		logConnectPhase("commands_registered", started)
	}
	heartbeatStop := make(chan struct{})
	b.mu.Lock()
	b.bot = bot
	b.lock = lock
	b.startedAt = time.Now()
	b.connected = true
	b.owner = owner
	b.ownerChan = ownerChannelID
	b.ownerHeartbeatStop = heartbeatStop
	b.lastErr = ""
	b.mu.Unlock()
	logConnectPhase("connected", started, "guild", cfg.GuildID)
	go b.runOwnerHeartbeat(bot, cfg.GuildID, ownerChannelID, owner, heartbeatStop)
	if store != nil && initialSync {
		go b.syncActiveTasksAfterStart(store, bot, cfg.GuildID)
	} else {
		b.syncTaskStreams(context.Background())
	}
	return nil
}

// logConnectPhase emits a structured, timestamped connect-progress line so the
// runtime log shows exactly which phase a stuck or slow connect reached.
func logConnectPhase(phase string, started time.Time, fields ...any) {
	args := make([]any, 0, len(fields)+2)
	args = append(args, "operation", "discord_connect", "phase", phase, "elapsed_ms", time.Since(started).Milliseconds())
	args = append(args, fields...)
	var b strings.Builder
	for i := 0; i+1 < len(args); i += 2 {
		if i > 0 {
			b.WriteByte(' ')
		}
		fmt.Fprintf(&b, "%s=%q", args[i], fmt.Sprint(args[i+1]))
	}
	log.Print(b.String())
}

// runOwnerHeartbeat periodically refreshes this runtime's owner heartbeat so
// other runtimes can tell it is alive. If the control channel is taken over by
// another runtime (a newer epoch), it self-fences by stopping the bridge instead
// of contending for the guild.
func (b *Bridge) runOwnerHeartbeat(bot *Bot, guildID, channelID, owner string, stop chan struct{}) {
	ticker := time.NewTicker(ownerHeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			refreshed, superseded, err := refreshGuildOwner(ctx, bot, guildID, channelID, owner)
			cancel()
			if err != nil {
				continue
			}
			if superseded {
				go func() {
					_ = b.Stop()
					b.setError(fmt.Errorf("discord ownership was taken over by another AGX runtime"))
				}()
				return
			}
			owner = refreshed
			b.mu.Lock()
			if b.owner != "" && sameOwner(b.owner, owner) {
				b.owner = owner
			}
			b.mu.Unlock()
		}
	}
}

func (b *Bridge) syncActiveTasksAfterStart(store *db.Store, bot *Bot, guildID string) {
	release, err := b.beginMaintenanceSync(context.Background(), "initial")
	if err != nil {
		return
	}
	defer release()
	err = NewSyncer(store, bot, guildID).SyncActiveTasks(context.Background())
	if err != nil {
		b.setError(err)
		return
	}
	b.syncTaskStreams(context.Background())
}

// Stop disconnects the bot, releases the bridge lock, and cancels all live task
// event streams. It is safe to call on an already-disconnected bridge.
func (b *Bridge) Stop() error {
	b.lifecycle.Lock()
	defer b.lifecycle.Unlock()

	b.mu.Lock()
	bot := b.bot
	lock := b.lock
	cfg := b.cfg
	owner := b.owner
	ownerChannelID := b.ownerChan
	heartbeatStop := b.ownerHeartbeatStop
	b.cancelTaskStreamsLocked()
	b.bot = nil
	b.lock = nil
	b.owner = ""
	b.ownerChan = ""
	b.ownerHeartbeatStop = nil
	b.connected = false
	b.startedAt = time.Time{}
	b.lastErr = ""
	b.mu.Unlock()
	if heartbeatStop != nil {
		close(heartbeatStop)
	}

	var err error
	if bot != nil {
		err = releaseGuildOwner(context.Background(), bot, cfg.GuildID, ownerChannelID, owner)
		if closeErr := bot.Close(); err == nil {
			err = closeErr
		}
	}
	if lock != nil {
		if lockErr := lock.Release(); err == nil {
			err = lockErr
		}
	}
	return err
}

// ValidateConfig enforces the minimum safe Discord configuration. AGX requires a
// non-empty allowlist because Discord commands can control local code execution.
func ValidateConfig(cfg config.DiscordConfig) error {
	if !cfg.Enabled {
		return fmt.Errorf("discord integration is disabled")
	}
	if strings.TrimSpace(cfg.BotToken) == "" {
		return fmt.Errorf("discord bot token is required")
	}
	if strings.TrimSpace(cfg.GuildID) == "" {
		return fmt.Errorf("discord guild id is required")
	}
	hasAllowedUser := false
	for _, allowed := range cfg.AllowedUserIDs {
		if strings.TrimSpace(allowed) != "" {
			hasAllowedUser = true
			break
		}
	}
	if !hasAllowedUser {
		return fmt.Errorf("allowed Discord user is required")
	}
	return nil
}

// Status returns a defensive snapshot of bridge state with the bot token masked.
func (b *Bridge) Status() Status {
	b.mu.Lock()
	cfg := b.cfg
	connected := b.connected
	startedAt := b.startedAt
	lastErr := b.lastErr
	lock := b.lock
	bot := b.bot
	b.mu.Unlock()

	status := Status{
		Enabled:        cfg.Enabled,
		Connected:      connected,
		GuildID:        cfg.GuildID,
		AllowedUserIDs: append([]string(nil), cfg.AllowedUserIDs...),
		MaskedBotToken: maskedSecretPrefix(cfg.BotToken),
		Error:          lastErr,
		StartedAt:      startedAt,
	}
	if connected && !startedAt.IsZero() {
		status.UptimeSeconds = int64(time.Since(startedAt).Seconds())
	}
	if lock != nil {
		status.LockedBy = lock.Path()
	}
	if bot != nil {
		status.GuildName = bot.GuildName(cfg.GuildID)
	}
	status.Sync = b.activeSyncStatus()
	return status
}

func (b *Bridge) activeSyncStatus() SyncStatusSummary {
	owners := b.activeSyncOwners()
	if len(owners) == 0 {
		return SyncStatusSummary{}
	}
	owner := owners[0]
	startedAt := owner.StartedAt
	return SyncStatusSummary{
		Running:     true,
		Kind:        owner.Kind,
		SyncID:      owner.SyncID,
		Priority:    owner.Priority,
		TaskID:      owner.TaskID,
		CurrentStep: owner.CurrentStep,
		StartedAt:   &startedAt,
		ElapsedMs:   time.Since(owner.StartedAt).Milliseconds(),
	}
}

func maskedSecretPrefix(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if len(value) <= 4 {
		return value + "..."
	}
	return value[:4] + "..."
}

func (b *Bridge) beginTaskSync(ctx context.Context, taskID string) (func(), error) {
	return b.beginLaneSync(ctx, b.taskSync, activeSync{
		SyncID:   db.NewTaskID(),
		Kind:     "task",
		Priority: "high",
		TaskID:   strings.TrimSpace(taskID),
	}, true)
}

func (b *Bridge) beginMaintenanceSync(ctx context.Context, kind string) (func(), error) {
	return b.beginLaneSync(ctx, b.maintSync, activeSync{
		SyncID:   db.NewTaskID(),
		Kind:     strings.TrimSpace(kind),
		Priority: "low",
	}, false)
}

func (b *Bridge) beginChannelSync(ctx context.Context) (func(), error) {
	select {
	case b.chanSync <- struct{}{}:
		return func() { <-b.chanSync }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (b *Bridge) beginLaneSync(ctx context.Context, lane chan struct{}, owner activeSync, wait bool) (func(), error) {
	if wait {
		select {
		case lane <- struct{}{}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	} else {
		select {
		case lane <- struct{}{}:
		default:
			return nil, syncInProgressError{owners: b.activeSyncOwners()}
		}
	}
	if !b.hardSync.TryRLock() {
		<-lane
		return nil, syncInProgressError{owners: b.activeSyncOwners()}
	}
	owner.StartedAt = time.Now()
	b.setActiveSync(owner)
	return func() {
		b.clearActiveSync(owner.SyncID)
		<-lane
		b.hardSync.RUnlock()
	}, nil
}

func (b *Bridge) beginHardSync(kind string) (func(), error) {
	if !b.hardSync.TryLock() {
		return nil, syncInProgressError{owners: b.activeSyncOwners()}
	}
	owner := activeSync{
		SyncID:    db.NewTaskID(),
		Kind:      strings.TrimSpace(kind),
		Priority:  "exclusive",
		StartedAt: time.Now(),
	}
	b.setActiveSync(owner)
	return func() {
		b.clearActiveSync(owner.SyncID)
		b.hardSync.Unlock()
	}, nil
}

func (b *Bridge) setActiveSync(owner activeSync) {
	b.syncState.Lock()
	defer b.syncState.Unlock()
	b.active[owner.SyncID] = owner
}

func (b *Bridge) clearActiveSync(syncID string) {
	b.syncState.Lock()
	defer b.syncState.Unlock()
	delete(b.active, syncID)
}

func (b *Bridge) activeSyncOwners() []activeSync {
	b.syncState.Lock()
	defer b.syncState.Unlock()
	owners := make([]activeSync, 0, len(b.active))
	for _, owner := range b.active {
		owners = append(owners, owner)
	}
	return owners
}

func (b *Bridge) queuePermissionRefresh() {
	b.permMu.Lock()
	if b.permRun {
		b.permNext = true
		b.permMu.Unlock()
		return
	}
	b.permRun = true
	b.permMu.Unlock()

	go b.permissionRefreshLoop()
}

func (b *Bridge) permissionRefreshLoop() {
	for {
		b.refreshCommandPermissionsBestEffort()

		b.permMu.Lock()
		if !b.permNext {
			b.permRun = false
			b.permMu.Unlock()
			return
		}
		b.permNext = false
		b.permMu.Unlock()
	}
}

func (b *Bridge) refreshCommandPermissionsBestEffort() {
	const attempts = 3
	for attempt := 0; attempt < attempts; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		err := b.refreshCommandPermissions(ctx)
		cancel()
		if err == nil {
			return
		}
		if !errors.Is(err, ErrSyncInProgress) {
			b.setError(err)
			return
		}
		time.Sleep(time.Duration(attempt+1) * time.Second)
	}
}

func (b *Bridge) refreshCommandPermissions(ctx context.Context) error {
	b.mu.Lock()
	cfg := b.cfg
	bot := b.bot
	store := b.store
	connected := b.connected
	b.mu.Unlock()
	if !connected || bot == nil || store == nil {
		return nil
	}
	release, err := b.beginMaintenanceSync(ctx, "permissions")
	if err != nil {
		return err
	}
	defer release()
	if err := NewSyncer(store, bot, cfg.GuildID).RefreshCommandPermissions(ctx); err != nil {
		return err
	}
	return nil
}

// SoftSync reconciles current AGX task/project state into Discord and removes
// orphaned mapped task channels. It preserves expected channels and avoids the
// full destructive rebuild performed by HardSync.
func (b *Bridge) SoftSync(ctx context.Context) error {
	release, err := b.beginMaintenanceSync(ctx, "soft")
	if err != nil {
		return err
	}
	defer release()
	releaseChannels, err := b.beginChannelSync(ctx)
	if err != nil {
		return err
	}
	defer releaseChannels()

	b.mu.Lock()
	cfg := b.cfg
	bot := b.bot
	store := b.store
	connected := b.connected
	b.mu.Unlock()
	if !connected || bot == nil {
		return fmt.Errorf("discord bridge is not connected")
	}
	if store == nil {
		return fmt.Errorf("discord sync store is not configured")
	}
	if err := NewSyncer(store, bot, cfg.GuildID).SyncActiveTasksWithCleanup(ctx, true); err != nil {
		b.setError(err)
		return err
	}
	b.setError(nil)
	b.syncTaskStreams(ctx)
	return nil
}

func (b *Bridge) Sync(ctx context.Context) error {
	return b.SoftSync(ctx)
}

// SyncTaskChannel performs low-latency reconciliation for one task. It is a
// no-op when the bridge is disconnected so runtime task mutations can call it
// opportunistically.
func (b *Bridge) SyncTaskChannel(ctx context.Context, taskID string) error {
	b.mu.Lock()
	cfg := b.cfg
	bot := b.bot
	store := b.store
	connected := b.connected
	b.mu.Unlock()
	if !connected || bot == nil || store == nil {
		return nil
	}
	release, err := b.beginTaskSync(ctx, taskID)
	if err != nil {
		return err
	}
	defer release()
	releaseChannels, err := b.beginChannelSync(ctx)
	if err != nil {
		return err
	}
	defer releaseChannels()
	if err := NewSyncer(store, bot, cfg.GuildID).SyncTaskChannelFast(ctx, taskID); err != nil {
		b.setError(err)
		return err
	}
	b.setError(nil)
	b.syncTaskStreams(ctx)
	b.queuePermissionRefresh()
	return nil
}

// RefreshTaskStreams starts or refreshes semantic event forwarders for already
// mapped structured tasks without performing Discord channel REST sync.
func (b *Bridge) RefreshTaskStreams(ctx context.Context) {
	b.syncTaskStreams(ctx)
}

// DeleteTaskChannel stops any live event stream for taskID and deletes its
// mapped Discord channel when connected.
func (b *Bridge) DeleteTaskChannel(ctx context.Context, taskID string) error {
	return b.DeleteTaskChannelWithFallback(ctx, taskID, "")
}

func (b *Bridge) DeleteTaskChannelWithFallback(ctx context.Context, taskID, fallbackChannelID string) error {
	b.mu.Lock()
	cfg := b.cfg
	bot := b.bot
	store := b.store
	connected := b.connected
	if stream, ok := b.streams[taskID]; ok {
		stream.cancel()
		delete(b.streams, taskID)
	}
	b.mu.Unlock()
	if !connected || bot == nil || store == nil {
		return nil
	}
	release, err := b.beginTaskSync(ctx, taskID)
	if err != nil {
		return err
	}
	defer release()
	releaseChannels, err := b.beginChannelSync(ctx)
	if err != nil {
		return err
	}
	defer releaseChannels()
	if err := NewSyncer(store, bot, cfg.GuildID).DeleteTaskChannelWithFallback(ctx, taskID, fallbackChannelID); err != nil {
		b.setError(err)
		return err
	}
	b.setError(nil)
	b.queuePermissionRefresh()
	return nil
}

// HardSync rebuilds AGX-managed Discord state from scratch.
func (b *Bridge) HardSync(ctx context.Context) error {
	return b.HardSyncPreserving(ctx, "")
}

// HardSyncPreserving deletes managed project/task channels, clears mappings,
// rebuilds categories/channels, and optionally preserves a known control
// channel. Use this when Discord state is suspected to be inconsistent.
func (b *Bridge) HardSyncPreserving(ctx context.Context, preserveControlChannelID string) error {
	release, err := b.beginHardSync("hard")
	if err != nil {
		return err
	}
	defer release()

	b.mu.Lock()
	cfg := b.cfg
	bot := b.bot
	store := b.store
	service := b.service
	connected := b.connected
	b.mu.Unlock()

	if !connected || bot == nil {
		return fmt.Errorf("discord bridge is not connected")
	}
	if store == nil {
		return fmt.Errorf("discord sync store is not configured")
	}
	projects := []ProjectSummary{}
	if service != nil {
		listed, err := service.ListProjects(ctx)
		if err != nil {
			return err
		}
		projects = listed
	} else {
		listed, err := store.ListProjects()
		if err != nil {
			return err
		}
		for _, project := range listed {
			projects = append(projects, ProjectSummary{ID: project.ID, Name: project.Name, Path: project.Path})
		}
	}
	preserveControlChannelID = strings.TrimSpace(preserveControlChannelID)
	if preserveControlChannelID == "" {
		if mapping, err := store.GetDiscordMapping(db.DiscordAGXControl, db.DiscordControlAGXID); err == nil {
			preserveControlChannelID = mapping.DiscordID
		}
	}
	if err := bot.ResetManagedChannels(ctx, cfg.GuildID, projects, preserveControlChannelID); err != nil {
		return err
	}
	if err := deleteDiscordMappings(store); err != nil {
		return err
	}
	if err := NewRebuildSyncer(store, bot, cfg.GuildID).SyncActiveTasks(ctx); err != nil {
		return err
	}
	b.syncTaskStreams(ctx)
	return nil
}

func (b *Bridge) ResetManagedState(ctx context.Context) error {
	return b.HardSync(ctx)
}

func deleteDiscordMappings(store *db.Store) error {
	mappings, err := store.ListDiscordMappings()
	if err != nil {
		return err
	}
	for _, mapping := range mappings {
		if err := store.DeleteDiscordMapping(mapping.AGXType, mapping.AGXID); err != nil && !errors.Is(err, db.ErrDiscordMappingNotFound) {
			return err
		}
	}
	return nil
}

// syncTaskStreams starts semantic event forwarders for live structured tasks and
// cancels streams whose task/channel mapping changed. It is called after channel
// sync so each stream has a current Discord channel target.
func (b *Bridge) syncTaskStreams(ctx context.Context) {
	b.mu.Lock()
	service := b.service
	events := b.events
	bot := b.bot
	connected := b.connected
	existing := make(map[string]taskStream, len(b.streams))
	for taskID, stream := range b.streams {
		existing[taskID] = stream
	}
	b.mu.Unlock()

	if !connected || service == nil || bot == nil {
		return
	}
	tasks, err := service.ListTasks(ctx)
	if err != nil {
		b.setError(err)
		return
	}
	desired := map[string]TaskSummary{}
	for _, task := range tasks {
		if shouldStreamTask(task) {
			desired[task.ID] = task
		}
	}
	for taskID, stream := range existing {
		task, ok := desired[taskID]
		if ok && task.ChannelID == stream.channelID {
			continue
		}
		stream.cancel()
		b.mu.Lock()
		delete(b.streams, taskID)
		b.mu.Unlock()
	}
	for taskID, task := range desired {
		if stream, ok := existing[taskID]; ok && stream.channelID == task.ChannelID {
			continue
		}
		b.startTaskStream(service, events, bot, task)
	}
}

// shouldStreamTask returns true only for live Discord tasks backed by a
// structured agent stream. Legacy tmux log mirroring is handled separately.
func shouldStreamTask(task TaskSummary) bool {
	if strings.TrimSpace(task.ID) == "" || strings.TrimSpace(task.ChannelID) == "" {
		return false
	}
	if strings.TrimSpace(task.Interface) != "" && strings.TrimSpace(task.Interface) != "discord" {
		return false
	}
	return isStructuredStreamTask(task) && isLiveTaskStatus(task.Status)
}

func isLiveTaskStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "active", "waiting":
		return true
	default:
		return false
	}
}

func isStructuredStreamTask(task TaskSummary) bool {
	return task.AgentThreadID != nil &&
		strings.TrimSpace(*task.AgentThreadID) != "" &&
		task.AgentStreamKind != nil &&
		strings.TrimSpace(*task.AgentStreamKind) != ""
}

// startTaskStream subscribes to one structured task's agent events and forwards
// them as semantic Discord messages until the task stream is canceled or ends.
func (b *Bridge) startTaskStream(service CommandService, events AgentEventSubscriber, bot MessageSender, task TaskSummary) {
	ctx, cancel := context.WithCancel(context.Background())
	if !isStructuredStreamTask(task) {
		cancel()
		return
	}
	if events == nil {
		cancel()
		return
	}
	b.mu.Lock()
	if !b.connected {
		b.mu.Unlock()
		cancel()
		return
	}
	if existing, ok := b.streams[task.ID]; ok && existing.channelID == task.ChannelID {
		b.mu.Unlock()
		cancel()
		return
	}
	if existing, ok := b.streams[task.ID]; ok {
		existing.cancel()
	}
	b.streams[task.ID] = taskStream{channelID: task.ChannelID, cancel: cancel}
	b.mu.Unlock()

	stream, err := events.SubscribeAgentEvents(ctx, task)
	if err != nil {
		cancel()
		b.removeTaskStream(task.ID, task.ChannelID)
		if agentstream.IsUnsupported(err) {
			return
		}
		b.setError(err)
		return
	}
	if ctx.Err() != nil {
		b.removeTaskStream(task.ID, task.ChannelID)
		return
	}

	go func() {
		defer cancel()
		defer b.removeTaskStream(task.ID, task.ChannelID)
		if err := NewSemanticEventForwarder(bot).Forward(ctx, task.ChannelID, stream); err != nil && ctx.Err() == nil {
			b.setError(err)
		}
	}()
}

func (b *Bridge) cancelTaskStreamsLocked() {
	for taskID, stream := range b.streams {
		stream.cancel()
		delete(b.streams, taskID)
	}
}

func (b *Bridge) removeTaskStream(taskID, channelID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if stream, ok := b.streams[taskID]; ok && stream.channelID == channelID {
		delete(b.streams, taskID)
	}
}

func (b *Bridge) setError(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err == nil {
		b.lastErr = ""
		return
	}
	b.lastErr = err.Error()
}
