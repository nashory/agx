package discord

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/nashory/agx/internal/db"
	"github.com/nashory/agx/internal/display"
)

const controlChannelName = "agx-control"

// SyncClient is the minimal Discord channel API required by Syncer. Bot
// implementations may satisfy additional optional interfaces to enable rebuilds,
// permission updates, or guild-wide cleanup.
type SyncClient interface {
	EnsureControlChannel(ctx context.Context, guildID, name string) (string, error)
	EnsureCategory(ctx context.Context, guildID, name string) (string, error)
	EnsureTextChannel(ctx context.Context, guildID, categoryID, name, topic string) (string, error)
	UpdateTextChannel(ctx context.Context, channelID, name, topic string) error
	UpdateChannelTopic(ctx context.Context, channelID, topic string) error
	DeleteChannel(ctx context.Context, channelID string) error
}

type GuildChannelType string

const (
	GuildChannelCategory GuildChannelType = "category"
	GuildChannelText     GuildChannelType = "text"
	GuildChannelOther    GuildChannelType = "other"
)

// GuildChannel is a lightweight guild channel snapshot used during hard cleanup
// to remove channels AGX no longer expects.
type GuildChannel struct {
	ID       string
	Name     string
	ParentID string
	Type     GuildChannelType
}

type GuildChannelLister interface {
	ListGuildChannels(ctx context.Context, guildID string) ([]GuildChannel, error)
}

type DirectCreateClient interface {
	CreateCategory(ctx context.Context, guildID, name string) (string, error)
	CreateTextChannel(ctx context.Context, guildID, categoryID, name, topic string) (string, error)
}

type CommandPermissionClient interface {
	ConfigureCommandPermissions(ctx context.Context, guildID, controlChannelID string, taskChannelIDs []string) error
}

// Syncer mirrors AGX projects and live Discord-controlled tasks into Discord
// categories/channels and stores the AGX<->Discord mapping in the database.
type Syncer struct {
	store   *db.Store
	client  SyncClient
	guild   string
	rebuild bool
}

// NewSyncer creates an idempotent soft-syncer. It prefers updating existing
// mapped channels or finding matching Discord channels before creating new ones.
func NewSyncer(store *db.Store, client SyncClient, guildID string) *Syncer {
	return &Syncer{store: store, client: client, guild: guildID}
}

// NewRebuildSyncer creates a syncer for hard sync after managed Discord state
// has been deleted. When the client supports DirectCreateClient, it creates new
// channels instead of trying to reuse old names.
func NewRebuildSyncer(store *db.Store, client SyncClient, guildID string) *Syncer {
	return &Syncer{store: store, client: client, guild: guildID, rebuild: true}
}

// SyncTaskChannel reconciles a single task channel. Missing or no-longer-live
// tasks are treated as delete requests so low-latency task updates do not leave
// orphan channels behind.
func (s *Syncer) SyncTaskChannel(ctx context.Context, taskID string) error {
	if s.store == nil {
		return fmt.Errorf("discord sync store is not configured")
	}
	if s.client == nil {
		return fmt.Errorf("discord sync client is not configured")
	}
	if strings.TrimSpace(s.guild) == "" {
		return fmt.Errorf("discord guild id is required")
	}
	task, err := s.store.GetTask(taskID)
	if err != nil {
		if errors.Is(err, db.ErrTaskNotFound) {
			return s.DeleteTaskChannel(ctx, taskID)
		}
		return err
	}
	if !shouldMirrorTask(task) {
		return s.DeleteTaskChannel(ctx, taskID)
	}
	project, err := s.store.GetProject(task.ProjectID)
	if err != nil {
		return err
	}
	controlChannelID, err := s.client.EnsureControlChannel(ctx, s.guild, controlChannelName)
	if err != nil {
		return err
	}
	if _, err := s.store.UpsertDiscordMapping(db.DiscordAGXControl, db.DiscordControlAGXID, db.DiscordTypeChannel, controlChannelID); err != nil {
		return err
	}
	categoryID, err := s.ensureProjectCategory(ctx, project)
	if err != nil {
		return err
	}
	if _, err := s.ensureTaskChannel(ctx, project, task, categoryID); err != nil {
		return err
	}
	if permissions, ok := s.client.(CommandPermissionClient); ok {
		_ = permissions.ConfigureCommandPermissions(ctx, s.guild, controlChannelID, s.mappedTaskChannelIDs())
	}
	return nil
}

// SyncActiveTasks mirrors all active/waiting Discord tasks without deleting
// unexpected guild channels.
func (s *Syncer) SyncActiveTasks(ctx context.Context) error {
	return s.SyncActiveTasksWithCleanup(ctx, false)
}

// SyncActiveTasksWithCleanup mirrors all active/waiting Discord tasks. When
// cleanup is true, it also removes mapped task channels whose tasks are gone or
// no longer mirrored, plus unexpected AGX-managed guild channels reported by the
// Discord client.
func (s *Syncer) SyncActiveTasksWithCleanup(ctx context.Context, cleanup bool) error {
	if s.store == nil {
		return fmt.Errorf("discord sync store is not configured")
	}
	if s.client == nil {
		return fmt.Errorf("discord sync client is not configured")
	}
	if strings.TrimSpace(s.guild) == "" {
		return fmt.Errorf("discord guild id is required")
	}
	controlChannelID, err := s.client.EnsureControlChannel(ctx, s.guild, controlChannelName)
	if err != nil {
		return err
	}
	if _, err := s.store.UpsertDiscordMapping(db.DiscordAGXControl, db.DiscordControlAGXID, db.DiscordTypeChannel, controlChannelID); err != nil {
		return err
	}
	expectedChannelIDs := map[string]bool{controlChannelID: true}
	projects, err := s.store.ListProjects()
	if err != nil {
		return err
	}
	mirroredTaskIDs := map[string]bool{}
	taskChannelIDs := []string{}
	for _, project := range projects {
		tasks, err := s.store.ListTasks(project.ID, nil)
		if err != nil {
			return err
		}
		mirroredTasks := make([]db.Task, 0, len(tasks))
		for _, task := range tasks {
			if shouldMirrorTask(task) {
				mirroredTasks = append(mirroredTasks, task)
			}
		}
		if len(mirroredTasks) == 0 {
			continue
		}
		categoryID, err := s.ensureProjectCategory(ctx, project)
		if err != nil {
			return err
		}
		expectedChannelIDs[categoryID] = true
		for _, task := range mirroredTasks {
			channelID, err := s.ensureTaskChannel(ctx, project, task, categoryID)
			if err != nil {
				return err
			}
			mirroredTaskIDs[task.ID] = true
			taskChannelIDs = append(taskChannelIDs, channelID)
			expectedChannelIDs[channelID] = true
		}
	}
	if cleanup {
		if err := s.cleanupUnmirroredTaskChannels(ctx, mirroredTaskIDs); err != nil {
			return err
		}
		if err := s.cleanupUnexpectedGuildChannels(ctx, expectedChannelIDs); err != nil {
			return err
		}
	}
	if permissions, ok := s.client.(CommandPermissionClient); ok {
		_ = permissions.ConfigureCommandPermissions(ctx, s.guild, controlChannelID, taskChannelIDs)
	}
	return nil
}

func (s *Syncer) ensureProjectCategory(ctx context.Context, project db.Project) (string, error) {
	name := FormatCategoryName(project.Name)
	categoryID, err := s.createOrEnsureCategory(ctx, name)
	if err != nil {
		return "", err
	}
	if _, err := s.store.UpsertDiscordMapping(db.DiscordAGXProject, project.ID, db.DiscordTypeCategory, categoryID); err != nil {
		return "", err
	}
	return categoryID, nil
}

func (s *Syncer) ensureTaskChannel(ctx context.Context, project db.Project, task db.Task, categoryID string) (string, error) {
	name := TaskChannelName(task)
	topic := TaskTopic(project, task)
	if mapping, err := s.store.GetDiscordMapping(db.DiscordAGXTask, task.ID); err == nil {
		if err := s.client.UpdateTextChannel(ctx, mapping.DiscordID, name, topic); err == nil {
			return mapping.DiscordID, nil
		}
	}
	channelID, err := s.createOrEnsureTextChannel(ctx, categoryID, name, topic)
	if err != nil {
		return "", err
	}
	if _, err := s.store.UpsertDiscordMapping(db.DiscordAGXTask, task.ID, db.DiscordTypeChannel, channelID); err != nil {
		return "", err
	}
	return channelID, nil
}

func (s *Syncer) mappedTaskChannelIDs() []string {
	mappings, err := s.store.ListDiscordMappings()
	if err != nil {
		return nil
	}
	out := []string{}
	for _, mapping := range mappings {
		if mapping.AGXType == db.DiscordAGXTask && mapping.DiscordType == db.DiscordTypeChannel {
			out = append(out, mapping.DiscordID)
		}
	}
	return out
}

func (s *Syncer) createOrEnsureCategory(ctx context.Context, name string) (string, error) {
	if s.rebuild {
		if client, ok := s.client.(DirectCreateClient); ok {
			return client.CreateCategory(ctx, s.guild, name)
		}
	}
	return s.client.EnsureCategory(ctx, s.guild, name)
}

func (s *Syncer) createOrEnsureTextChannel(ctx context.Context, categoryID, name, topic string) (string, error) {
	if s.rebuild {
		if client, ok := s.client.(DirectCreateClient); ok {
			return client.CreateTextChannel(ctx, s.guild, categoryID, name, topic)
		}
	}
	return s.client.EnsureTextChannel(ctx, s.guild, categoryID, name, topic)
}

// DeleteTaskChannel deletes the Discord channel mapped to taskID and removes the
// mapping. It is a no-op if no mapping exists.
func (s *Syncer) DeleteTaskChannel(ctx context.Context, taskID string) error {
	return s.DeleteTaskChannelWithFallback(ctx, taskID, "")
}

// DeleteTaskChannelWithFallback deletes the mapped task channel, or the
// provided channel ID when the mapping was already removed. The fallback keeps
// `/kill` robust for the current Discord task channel even if local mapping
// state is stale.
func (s *Syncer) DeleteTaskChannelWithFallback(ctx context.Context, taskID, fallbackChannelID string) error {
	mapping, err := s.store.GetDiscordMapping(db.DiscordAGXTask, taskID)
	if err != nil {
		if errors.Is(err, db.ErrDiscordMappingNotFound) {
			fallbackChannelID = strings.TrimSpace(fallbackChannelID)
			if fallbackChannelID == "" {
				return nil
			}
			return s.client.DeleteChannel(ctx, fallbackChannelID)
		}
		return err
	}
	if err := s.client.DeleteChannel(ctx, mapping.DiscordID); err != nil {
		return err
	}
	return s.store.DeleteDiscordMapping(db.DiscordAGXTask, taskID)
}

// cleanupUnmirroredTaskChannels removes task mappings that no longer correspond
// to live Discord-controlled tasks. This is the soft-sync orphan cleanup path.
func (s *Syncer) cleanupUnmirroredTaskChannels(ctx context.Context, mirroredTaskIDs map[string]bool) error {
	mappings, err := s.store.ListDiscordMappings()
	if err != nil {
		return err
	}
	for _, mapping := range mappings {
		if mapping.AGXType != db.DiscordAGXTask || mirroredTaskIDs[mapping.AGXID] {
			continue
		}
		task, err := s.store.GetTask(mapping.AGXID)
		if err == nil && shouldMirrorTask(task) {
			continue
		}
		if err != nil && !errors.Is(err, db.ErrTaskNotFound) {
			return err
		}
		if err := s.client.DeleteChannel(ctx, mapping.DiscordID); err != nil {
			return err
		}
		if err := s.store.DeleteDiscordMapping(db.DiscordAGXTask, mapping.AGXID); err != nil && !errors.Is(err, db.ErrDiscordMappingNotFound) {
			return err
		}
	}
	return nil
}

// cleanupUnexpectedGuildChannels removes AGX playground channels that are not in
// the expected set. Non-categories are deleted before categories so Discord does
// not reject deletion of non-empty parents.
func (s *Syncer) cleanupUnexpectedGuildChannels(ctx context.Context, expectedChannelIDs map[string]bool) error {
	lister, ok := s.client.(GuildChannelLister)
	if !ok {
		return nil
	}
	channels, err := lister.ListGuildChannels(ctx, s.guild)
	if err != nil {
		return err
	}
	nonCategories := []string{}
	categories := []string{}
	deleted := map[string]bool{}
	for _, channel := range channels {
		if expectedChannelIDs[channel.ID] {
			continue
		}
		if channel.Type == GuildChannelCategory {
			categories = append(categories, channel.ID)
		} else {
			nonCategories = append(nonCategories, channel.ID)
		}
		deleted[channel.ID] = true
	}
	if err := deleteDiscordChannelsConcurrently(ctx, nonCategories, discordDeleteConcurrency, s.client.DeleteChannel); err != nil {
		return err
	}
	if err := deleteDiscordChannelsConcurrently(ctx, categories, discordDeleteConcurrency, s.client.DeleteChannel); err != nil {
		return err
	}
	if len(deleted) == 0 {
		return nil
	}
	mappings, err := s.store.ListDiscordMappings()
	if err != nil {
		return err
	}
	for _, mapping := range mappings {
		if !deleted[mapping.DiscordID] {
			continue
		}
		if err := s.store.DeleteDiscordMapping(mapping.AGXType, mapping.AGXID); err != nil && !errors.Is(err, db.ErrDiscordMappingNotFound) {
			return err
		}
	}
	return nil
}

func shouldMirrorTask(task db.Task) bool {
	if task.Interface != db.TaskInterfaceDiscord {
		return false
	}
	return task.Status == db.StatusActive || task.Status == db.StatusWaiting
}

// FormatCategoryName returns the Discord category name for a project.
func FormatCategoryName(projectName string) string {
	name := strings.TrimSpace(projectName)
	if name == "" {
		name = "project"
	}
	return truncateRunes("📁 "+name, 100)
}

// TaskChannelName returns a deterministic channel name containing the task title
// and short task ID. The short ID keeps names stable when titles collide.
func TaskChannelName(task db.Task) string {
	title := strings.TrimSpace(task.Title)
	if title == "" {
		title = "task"
	}
	return fmt.Sprintf("%s-%s", title, display.ShortID(task.ID))
}

// TaskTopic encodes task metadata into the Discord topic so users can identify
// the project, agent mode, workspace mode, and full task ID from Discord.
func TaskTopic(project db.Project, task db.Task) string {
	title := strings.TrimSpace(task.Title)
	if title == "" {
		title = display.ShortID(task.ID)
	}
	mode := "standard"
	if task.AllMighty {
		mode = "all-mighty"
	}
	workspaceMode := string(task.WorkspaceMode)
	if strings.TrimSpace(workspaceMode) == "" {
		workspaceMode = string(db.WorkspaceModeWorktree)
	}
	return truncateRunes(fmt.Sprintf("Project: %s | Task: %s | Agent: %s | Mode: %s | Workspace: %s | ID: %s", project.Name, title, task.Agent, mode, workspaceMode, task.ID), 1024)
}

// SanitizeTextChannelName normalizes arbitrary text into Discord's lowercase
// channel-name character set and enforces Discord's 100-rune limit.
func SanitizeTextChannelName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = regexp.MustCompile(`[^a-z0-9-]+`).ReplaceAllString(name, "-")
	name = regexp.MustCompile(`-{2,}`).ReplaceAllString(name, "-")
	name = strings.Trim(name, "-")
	if name == "" {
		name = "channel"
	}
	return truncateRunes(name, 100)
}

func truncateRunes(value string, max int) string {
	if max <= 0 {
		return ""
	}
	if utf8.RuneCountInString(value) <= max {
		return value
	}
	runes := []rune(value)
	return string(runes[:max])
}
