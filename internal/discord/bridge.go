package discord

import (
	"context"
	"errors"
	"fmt"
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
	lifecycle sync.Mutex
	mu        sync.Mutex
	syncMu    sync.Mutex
	cfg       config.DiscordConfig
	bot       *Bot
	lock      *Lock
	service   CommandService
	events    AgentEventSubscriber
	store     *db.Store
	streams   map[string]taskStream
	startedAt time.Time
	lastErr   string
	connected bool
}

type taskStream struct {
	channelID string
	cancel    context.CancelFunc
}

// NewBridge constructs a disconnected bridge with cfg. Dependencies such as the
// store and command service can be attached before Start.
func NewBridge(cfg config.DiscordConfig) *Bridge {
	return &Bridge{cfg: cfg, streams: map[string]taskStream{}}
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

	if err := ValidateConfig(cfg); err != nil {
		b.setError(err)
		return err
	}

	lock, err := AcquireLock(mode)
	if err != nil {
		b.setError(err)
		return err
	}
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
	if service != nil {
		if err := bot.RegisterCommands(cfg.GuildID); err != nil {
			_ = bot.Close()
			_ = lock.Release()
			b.setError(err)
			return err
		}
	}
	b.mu.Lock()
	b.bot = bot
	b.lock = lock
	b.startedAt = time.Now()
	b.connected = true
	b.lastErr = ""
	b.mu.Unlock()
	if store != nil {
		go b.syncActiveTasksAfterStart(store, bot, cfg.GuildID)
	} else {
		b.syncTaskStreams(context.Background())
	}
	return nil
}

func (b *Bridge) syncActiveTasksAfterStart(store *db.Store, bot *Bot, guildID string) {
	b.syncMu.Lock()
	defer b.syncMu.Unlock()
	err := NewSyncer(store, bot, guildID).SyncActiveTasks(context.Background())
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
	b.cancelTaskStreamsLocked()
	b.bot = nil
	b.lock = nil
	b.connected = false
	b.startedAt = time.Time{}
	b.lastErr = ""
	b.mu.Unlock()

	var err error
	if bot != nil {
		err = bot.Close()
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
	return status
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

// SoftSync reconciles current AGX task/project state into Discord and removes
// orphaned mapped task channels. It preserves expected channels and avoids the
// full destructive rebuild performed by HardSync.
func (b *Bridge) SoftSync(ctx context.Context) error {
	b.syncMu.Lock()
	defer b.syncMu.Unlock()

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
	b.syncMu.Lock()
	defer b.syncMu.Unlock()
	if err := NewSyncer(store, bot, cfg.GuildID).SyncTaskChannel(ctx, taskID); err != nil {
		b.setError(err)
		return err
	}
	b.setError(nil)
	b.syncTaskStreams(ctx)
	return nil
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
	b.syncMu.Lock()
	defer b.syncMu.Unlock()
	if err := NewSyncer(store, bot, cfg.GuildID).DeleteTaskChannelWithFallback(ctx, taskID, fallbackChannelID); err != nil {
		b.setError(err)
		return err
	}
	b.setError(nil)
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
	b.syncMu.Lock()
	defer b.syncMu.Unlock()

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
