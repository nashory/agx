package discord

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
)

type Bot struct {
	session    *discordgo.Session
	progressMu sync.Mutex
	progress   map[string]*processingIndicator
	messageMu  sync.Mutex
	messages   map[string]time.Time
	commandsMu sync.Mutex
	appID      string
	commandIDs map[string]string
}

const discordDeleteConcurrency = 3
const progressEditMinInterval = 2 * time.Second
const choiceComponentPrefix = "agx:choice:"
const processedMessageRetention = 10 * time.Minute

type processingIndicator struct {
	messageID      string
	cancel         context.CancelFunc
	animated       bool
	lastContent    string
	lastEdit       time.Time
	pendingContent string
	pendingTimer   *time.Timer
}

func NewBot(token string) (*Bot, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, fmt.Errorf("discord bot token is required")
	}
	session, err := discordgo.New("Bot " + token)
	if err != nil {
		return nil, err
	}
	session.Client = &http.Client{Timeout: 15 * time.Second}
	session.Identify.Intents = discordgo.IntentsGuilds |
		discordgo.IntentsGuildMessages |
		discordgo.IntentsGuildMessageReactions |
		discordgo.IntentsMessageContent
	return &Bot{session: session, progress: map[string]*processingIndicator{}, messages: map[string]time.Time{}, commandIDs: map[string]string{}}, nil
}

func (b *Bot) Open(ctx context.Context) error {
	if b == nil || b.session == nil {
		return fmt.Errorf("discord bot is not initialized")
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- b.session.Open()
	}()
	select {
	case <-ctx.Done():
		_ = b.session.Close()
		return ctx.Err()
	case err := <-errCh:
		return err
	}
}

func (b *Bot) Close() error {
	if b == nil || b.session == nil {
		return nil
	}
	b.clearProcessingIndicators()
	return b.session.Close()
}

func (b *Bot) RegisterCommands(guildID string) error {
	if b == nil || b.session == nil {
		return fmt.Errorf("discord bot is not initialized")
	}
	appID, err := b.applicationID()
	if err != nil {
		return err
	}
	desired := ApplicationCommands()
	// Skip the rate-limited bulk overwrite when the guild's commands already
	// match. Re-registering on every connect otherwise accumulates against
	// Discord's command rate limit, whose retry-after can be minutes long.
	if existing, err := b.session.ApplicationCommands(appID, guildID); err == nil && commandsEquivalent(existing, desired) {
		b.rememberCommands(appID, existing)
		return nil
	}
	created, err := b.session.ApplicationCommandBulkOverwrite(appID, guildID, desired)
	if err != nil {
		return fmt.Errorf("register Discord commands: %w", err)
	}
	b.rememberCommands(appID, created)
	return nil
}

func (b *Bot) ConfigureCommandPermissions(ctx context.Context, guildID, controlChannelID string, taskChannelIDs []string) error {
	if b == nil || b.session == nil || strings.TrimSpace(guildID) == "" {
		return nil
	}
	appID, commandIDs, err := b.commandMap(guildID)
	if err != nil {
		return err
	}
	controlChannelID = strings.TrimSpace(controlChannelID)
	allChannelsID, err := discordgo.GuildAllChannelsID(guildID)
	if err != nil {
		return err
	}
	controlPermissions := []*discordgo.ApplicationCommandPermissions{
		{ID: allChannelsID, Type: discordgo.ApplicationCommandPermissionTypeChannel, Permission: false},
	}
	if controlChannelID != "" {
		controlPermissions = append(controlPermissions, &discordgo.ApplicationCommandPermissions{ID: controlChannelID, Type: discordgo.ApplicationCommandPermissionTypeChannel, Permission: true})
	}
	taskPermissions := []*discordgo.ApplicationCommandPermissions{
		{ID: allChannelsID, Type: discordgo.ApplicationCommandPermissionTypeChannel, Permission: false},
	}
	for _, channelID := range uniqueNonEmpty(taskChannelIDs) {
		if len(taskPermissions) >= 100 {
			break
		}
		taskPermissions = append(taskPermissions, &discordgo.ApplicationCommandPermissions{ID: channelID, Type: discordgo.ApplicationCommandPermissionTypeChannel, Permission: true})
	}
	for name, commandID := range commandIDs {
		if err := ctx.Err(); err != nil {
			return err
		}
		permissions := controlPermissions
		if isHeartbeatCommand(name) {
			permissions = append([]*discordgo.ApplicationCommandPermissions{}, controlPermissions...)
			for _, permission := range taskPermissions[1:] {
				if len(permissions) >= 100 {
					break
				}
				permissions = append(permissions, permission)
			}
		}
		if isTaskOnlyCommand(name) {
			permissions = taskPermissions
		}
		if err := b.session.ApplicationCommandPermissionsEdit(appID, guildID, commandID, &discordgo.ApplicationCommandPermissionsList{Permissions: permissions}); err != nil {
			return fmt.Errorf("configure /%s permissions: %w", name, err)
		}
	}
	return nil
}

func (b *Bot) applicationID() (string, error) {
	if b.session.State != nil && b.session.State.User != nil && b.session.State.User.ID != "" {
		return b.session.State.User.ID, nil
	}
	user, err := b.session.User("@me")
	if err != nil {
		return "", err
	}
	return user.ID, nil
}

func (b *Bot) rememberCommands(appID string, commands []*discordgo.ApplicationCommand) {
	commandIDs := map[string]string{}
	for _, command := range commands {
		if command != nil && strings.TrimSpace(command.Name) != "" && strings.TrimSpace(command.ID) != "" {
			commandIDs[command.Name] = command.ID
		}
	}
	b.commandsMu.Lock()
	b.appID = appID
	b.commandIDs = commandIDs
	b.commandsMu.Unlock()
}

func (b *Bot) commandMap(guildID string) (string, map[string]string, error) {
	b.commandsMu.Lock()
	appID := b.appID
	commandIDs := map[string]string{}
	for name, commandID := range b.commandIDs {
		commandIDs[name] = commandID
	}
	b.commandsMu.Unlock()
	if appID != "" && len(commandIDs) > 0 {
		return appID, commandIDs, nil
	}
	resolvedAppID, err := b.applicationID()
	if err != nil {
		return "", nil, err
	}
	commands, err := b.session.ApplicationCommands(resolvedAppID, guildID)
	if err != nil {
		return "", nil, err
	}
	b.rememberCommands(resolvedAppID, commands)
	commandIDs = map[string]string{}
	for _, command := range commands {
		if command != nil && strings.TrimSpace(command.Name) != "" && strings.TrimSpace(command.ID) != "" {
			commandIDs[command.Name] = command.ID
		}
	}
	return resolvedAppID, commandIDs, nil
}

func uniqueNonEmpty(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func (b *Bot) EnsureControlChannel(ctx context.Context, guildID, name string) (string, error) {
	return b.ensureTextChannel(ctx, guildID, "", name, "")
}

func (b *Bot) EnsureCategory(ctx context.Context, guildID, name string) (string, error) {
	channel, err := b.findGuildChannel(guildID, name, discordgo.ChannelTypeGuildCategory, "")
	if err != nil {
		return "", err
	}
	if channel != nil {
		return channel.ID, nil
	}
	return b.CreateCategory(ctx, guildID, name)
}

func (b *Bot) CreateCategory(ctx context.Context, guildID, name string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	created, err := b.session.GuildChannelCreateComplex(guildID, discordgo.GuildChannelCreateData{
		Name: name,
		Type: discordgo.ChannelTypeGuildCategory,
	})
	if err != nil {
		return "", err
	}
	return created.ID, nil
}

func (b *Bot) EnsureTextChannel(ctx context.Context, guildID, categoryID, name, topic string) (string, error) {
	return b.ensureTextChannel(ctx, guildID, categoryID, SanitizeTextChannelName(name), topic)
}

func (b *Bot) CreateTextChannel(ctx context.Context, guildID, categoryID, name, topic string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	created, err := b.session.GuildChannelCreateComplex(guildID, discordgo.GuildChannelCreateData{
		Name:     SanitizeTextChannelName(name),
		Type:     discordgo.ChannelTypeGuildText,
		ParentID: categoryID,
		Topic:    topic,
	})
	if err != nil {
		return "", err
	}
	return created.ID, nil
}

func (b *Bot) UpdateChannelTopic(ctx context.Context, channelID, topic string) error {
	_, err := b.session.ChannelEdit(channelID, &discordgo.ChannelEdit{Topic: topic})
	return err
}

func (b *Bot) UpdateTextChannel(ctx context.Context, channelID, name, topic string) error {
	_, err := b.session.ChannelEdit(channelID, &discordgo.ChannelEdit{
		Name:  SanitizeTextChannelName(name),
		Topic: topic,
	})
	return err
}

func (b *Bot) DeleteChannel(ctx context.Context, channelID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := b.session.ChannelDelete(channelID)
	return err
}

func (b *Bot) ListGuildChannels(ctx context.Context, guildID string) ([]GuildChannel, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if b == nil || b.session == nil {
		return nil, fmt.Errorf("discord bot is not initialized")
	}
	channels, err := b.session.GuildChannels(guildID)
	if err != nil {
		return nil, err
	}
	out := make([]GuildChannel, 0, len(channels))
	for _, channel := range channels {
		channelType := GuildChannelOther
		switch channel.Type {
		case discordgo.ChannelTypeGuildCategory:
			channelType = GuildChannelCategory
		case discordgo.ChannelTypeGuildText:
			channelType = GuildChannelText
		}
		out = append(out, GuildChannel{
			ID:       channel.ID,
			Name:     channel.Name,
			ParentID: channel.ParentID,
			Topic:    channel.Topic,
			Type:     channelType,
		})
	}
	return out, nil
}

func deleteDiscordChannelsConcurrently(ctx context.Context, channelIDs []string, concurrency int, deleteFn func(context.Context, string) error) error {
	channelIDs = uniqueNonEmpty(channelIDs)
	if len(channelIDs) == 0 {
		return nil
	}
	if concurrency < 1 {
		concurrency = 1
	}
	if concurrency > len(channelIDs) {
		concurrency = len(channelIDs)
	}

	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	jobs := make(chan string)
	errCh := make(chan error, 1)
	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for channelID := range jobs {
				if err := workerCtx.Err(); err != nil {
					return
				}
				if err := deleteFn(workerCtx, channelID); err != nil {
					select {
					case errCh <- err:
						cancel()
					default:
					}
					return
				}
			}
		}()
	}

sendLoop:
	for _, channelID := range channelIDs {
		select {
		case <-workerCtx.Done():
			break sendLoop
		case jobs <- channelID:
		}
	}
	close(jobs)
	wg.Wait()

	select {
	case err := <-errCh:
		return err
	default:
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

func (b *Bot) ResetManagedChannels(ctx context.Context, guildID string, _ []ProjectSummary, preserveControlChannelID string) error {
	if b == nil || b.session == nil {
		return fmt.Errorf("discord bot is not initialized")
	}
	channels, err := b.ListGuildChannels(ctx, guildID)
	if err != nil {
		return err
	}
	nonCategoryChannelIDs := []string{}
	categoryChannelIDs := []string{}
	for _, channel := range channels {
		if channel.ID == preserveControlChannelID {
			continue
		}
		if channel.Type == GuildChannelCategory {
			categoryChannelIDs = append(categoryChannelIDs, channel.ID)
		} else {
			nonCategoryChannelIDs = append(nonCategoryChannelIDs, channel.ID)
		}
	}
	if err := deleteDiscordChannelsConcurrently(ctx, nonCategoryChannelIDs, discordDeleteConcurrency, b.DeleteChannel); err != nil {
		return fmt.Errorf("delete Discord non-category channels: %w", err)
	}
	if err := deleteDiscordChannelsConcurrently(ctx, categoryChannelIDs, discordDeleteConcurrency, b.DeleteChannel); err != nil {
		return fmt.Errorf("delete Discord categories: %w", err)
	}
	return nil
}

func (b *Bot) SendMessage(ctx context.Context, channelID, content string) error {
	return b.sendMessageWithSession(ctx, b.session, channelID, content)
}

type messageSendSession interface {
	ChannelMessageSend(string, string, ...discordgo.RequestOption) (*discordgo.Message, error)
}

func (b *Bot) sendMessageWithSession(ctx context.Context, session messageSendSession, channelID, content string) error {
	if strings.TrimSpace(content) == "" {
		return nil
	}
	b.stopProcessingIndicator(context.Background(), channelID)
	_, err := session.ChannelMessageSend(channelID, content)
	return err
}

func (b *Bot) SendInteractivePrompt(ctx context.Context, channelID string, prompt InteractivePrompt) error {
	if strings.TrimSpace(prompt.Content) == "" {
		return nil
	}
	b.stopProcessingIndicator(context.Background(), channelID)
	components := choiceComponents(prompt)
	message := &discordgo.MessageSend{Content: prompt.Content}
	if len(components) > 0 {
		message.Components = components
	}
	_, err := b.session.ChannelMessageSendComplex(channelID, message)
	return err
}

func choiceComponents(prompt InteractivePrompt) []discordgo.MessageComponent {
	taskID := strings.TrimSpace(prompt.TaskID)
	if taskID == "" {
		return nil
	}
	rows := []discordgo.MessageComponent{}
	row := discordgo.ActionsRow{}
	for index, option := range prompt.Options {
		label := truncateComponentLabel(option.Label)
		if label == "" {
			continue
		}
		if len(row.Components) == 5 {
			rows = append(rows, row)
			row = discordgo.ActionsRow{}
		}
		if len(rows) == 5 {
			break
		}
		row.Components = append(row.Components, discordgo.Button{
			Label:    label,
			Style:    discordgo.PrimaryButton,
			CustomID: choiceComponentID(taskID, index),
		})
	}
	if len(row.Components) > 0 && len(rows) < 5 {
		rows = append(rows, row)
	}
	return rows
}

func choiceComponentID(taskID string, index int) string {
	return choiceComponentPrefix + strings.TrimSpace(taskID) + ":" + strconv.Itoa(index)
}

func parseChoiceComponentID(customID string) (string, int, bool) {
	customID = strings.TrimSpace(customID)
	if !strings.HasPrefix(customID, choiceComponentPrefix) {
		return "", 0, false
	}
	rest := strings.TrimPrefix(customID, choiceComponentPrefix)
	taskID, indexText, ok := strings.Cut(rest, ":")
	if !ok || strings.TrimSpace(taskID) == "" {
		return "", 0, false
	}
	index, err := strconv.Atoi(indexText)
	if err != nil || index < 0 {
		return "", 0, false
	}
	return taskID, index, true
}

func truncateComponentLabel(label string) string {
	label = strings.TrimSpace(label)
	if label == "" {
		return ""
	}
	return truncateUTF8(label, 80)
}

func (b *Bot) UpdateProgressMessage(ctx context.Context, channelID, content string) error {
	if b == nil || b.session == nil || strings.TrimSpace(channelID) == "" || strings.TrimSpace(content) == "" {
		return nil
	}
	content = strings.TrimSpace(content)
	b.progressMu.Lock()
	indicator := b.progress[channelID]
	if indicator != nil && indicator.animated {
		indicator.cancel()
		indicator.cancel = func() {}
		indicator.animated = false
		indicator.lastEdit = time.Time{}
		b.progress[channelID] = indicator
	}
	if indicator != nil {
		messageID := indicator.messageID
		if indicator.lastContent == content || indicator.pendingContent == content {
			b.progressMu.Unlock()
			return nil
		}
		if delay := progressUpdateDelay(indicator.lastEdit, time.Now()); delay > 0 {
			indicator.pendingContent = content
			if indicator.pendingTimer == nil {
				indicator.pendingTimer = time.AfterFunc(delay, func() {
					b.flushPendingProgressUpdate(channelID, messageID)
				})
			}
			b.progressMu.Unlock()
			return nil
		}
		if indicator.pendingTimer != nil {
			indicator.pendingTimer.Stop()
			indicator.pendingTimer = nil
		}
		indicator.pendingContent = ""
		indicator.lastContent = content
		indicator.lastEdit = time.Now()
		b.progressMu.Unlock()
		_, err := b.session.ChannelMessageEdit(channelID, messageID, content)
		return err
	}
	b.progressMu.Unlock()
	message, err := b.session.ChannelMessageSend(channelID, content)
	if err != nil {
		return err
	}
	indicatorCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	b.progressMu.Lock()
	b.progress[channelID] = &processingIndicator{messageID: message.ID, cancel: cancel, lastContent: content, lastEdit: time.Now()}
	b.progressMu.Unlock()
	go func() {
		<-indicatorCtx.Done()
		b.progressMu.Lock()
		if current := b.progress[channelID]; current != nil && current.messageID == message.ID {
			delete(b.progress, channelID)
		}
		b.progressMu.Unlock()
	}()
	return nil
}

func (b *Bot) flushPendingProgressUpdate(channelID, messageID string) {
	b.progressMu.Lock()
	indicator := b.progress[channelID]
	if indicator == nil || indicator.messageID != messageID || indicator.pendingContent == "" {
		b.progressMu.Unlock()
		return
	}
	content := indicator.pendingContent
	indicator.pendingContent = ""
	indicator.pendingTimer = nil
	indicator.lastContent = content
	indicator.lastEdit = time.Now()
	b.progressMu.Unlock()
	_, _ = b.session.ChannelMessageEdit(channelID, messageID, content)
}

func progressUpdateDelay(lastEdit, now time.Time) time.Duration {
	if lastEdit.IsZero() {
		return 0
	}
	elapsed := now.Sub(lastEdit)
	if elapsed >= progressEditMinInterval {
		return 0
	}
	return progressEditMinInterval - elapsed
}

func (b *Bot) ClearProgressMessage(ctx context.Context, channelID string) error {
	b.stopProcessingIndicator(ctx, channelID)
	return nil
}

func (b *Bot) startProcessingIndicator(ctx context.Context, channelID string) {
	if b == nil || b.session == nil || strings.TrimSpace(channelID) == "" {
		return
	}
	b.stopProcessingIndicator(ctx, channelID)
	message, err := b.session.ChannelMessageSend(channelID, "⏳ Thinking...")
	if err != nil {
		return
	}
	indicatorCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	b.progressMu.Lock()
	b.progress[channelID] = &processingIndicator{messageID: message.ID, cancel: cancel, animated: true, lastContent: "⏳ Thinking...", lastEdit: time.Now()}
	b.progressMu.Unlock()
	go b.animateProcessingIndicator(indicatorCtx, channelID, message.ID)
}

func (b *Bot) animateProcessingIndicator(ctx context.Context, channelID, messageID string) {
	frames := []string{"⏳ Thinking...", "⌛ Thinking...", "🔄 Thinking...", "💭 Thinking..."}
	ticker := time.NewTicker(progressEditMinInterval)
	defer ticker.Stop()
	index := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			index = (index + 1) % len(frames)
			_, _ = b.session.ChannelMessageEdit(channelID, messageID, frames[index])
		}
	}
}

func (b *Bot) stopProcessingIndicator(ctx context.Context, channelID string) {
	if b == nil || b.session == nil || strings.TrimSpace(channelID) == "" {
		return
	}
	b.progressMu.Lock()
	indicator := b.progress[channelID]
	delete(b.progress, channelID)
	b.progressMu.Unlock()
	if indicator == nil {
		return
	}
	if indicator.cancel != nil {
		indicator.cancel()
	}
	if indicator.pendingTimer != nil {
		indicator.pendingTimer.Stop()
	}
	if err := b.session.ChannelMessageDelete(channelID, indicator.messageID); err != nil {
		_, _ = b.session.ChannelMessageEdit(channelID, indicator.messageID, "✅ Output received.")
	}
}

func (b *Bot) clearProcessingIndicators() {
	b.progressMu.Lock()
	indicators := b.progress
	b.progress = map[string]*processingIndicator{}
	b.progressMu.Unlock()
	for _, indicator := range indicators {
		indicator.cancel()
		if indicator.pendingTimer != nil {
			indicator.pendingTimer.Stop()
		}
	}
}

func (b *Bot) ensureTextChannel(ctx context.Context, guildID, categoryID, name, topic string) (string, error) {
	channels, err := b.findGuildChannels(guildID, name, discordgo.ChannelTypeGuildText, categoryID)
	if err != nil {
		return "", err
	}
	if len(channels) > 0 {
		channel := channels[0]
		if channel.Topic != topic && topic != "" {
			if err := b.UpdateChannelTopic(ctx, channel.ID, topic); err != nil {
				return "", err
			}
		}
		return channel.ID, nil
	}
	created, err := b.session.GuildChannelCreateComplex(guildID, discordgo.GuildChannelCreateData{
		Name:     name,
		Type:     discordgo.ChannelTypeGuildText,
		ParentID: categoryID,
		Topic:    topic,
	})
	if err != nil {
		return "", err
	}
	return created.ID, nil
}

func (b *Bot) findGuildChannel(guildID, name string, channelType discordgo.ChannelType, parentID string) (*discordgo.Channel, error) {
	channels, err := b.findGuildChannels(guildID, name, channelType, parentID)
	if err != nil {
		return nil, err
	}
	if len(channels) == 0 {
		return nil, nil
	}
	return channels[0], nil
}

func (b *Bot) findGuildChannels(guildID, name string, channelType discordgo.ChannelType, parentID string) ([]*discordgo.Channel, error) {
	if b == nil || b.session == nil {
		return nil, fmt.Errorf("discord bot is not initialized")
	}
	channels, err := b.session.GuildChannels(guildID)
	if err != nil {
		return nil, err
	}
	matches := make([]*discordgo.Channel, 0, 1)
	for _, channel := range channels {
		if channel.Name == name && channel.Type == channelType && (parentID == "" || channel.ParentID == parentID) {
			matches = append(matches, channel)
		}
	}
	return matches, nil
}

func (b *Bot) AddCommandHandler(router *CommandRouter) {
	if b == nil || b.session == nil || router == nil {
		return
	}
	b.session.AddHandler(func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		b.handleCommandInteraction(s, router, i)
	})
}

type commandHandlerSession interface {
	Channel(string, ...discordgo.RequestOption) (*discordgo.Channel, error)
	InteractionRespond(*discordgo.Interaction, *discordgo.InteractionResponse, ...discordgo.RequestOption) error
	InteractionResponseEdit(*discordgo.Interaction, *discordgo.WebhookEdit, ...discordgo.RequestOption) (*discordgo.Message, error)
}

func (b *Bot) handleCommandInteraction(session commandHandlerSession, router *CommandRouter, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionApplicationCommand {
		return
	}
	input := CommandInputFromInteraction(i)
	if input.ChannelID != "" {
		if channel, err := session.Channel(input.ChannelID); err == nil && channel != nil {
			input.ChannelName = channel.Name
		}
	}
	ctx := context.Background()
	flags := discordgo.MessageFlags(0)
	if !router.IsAuthorized(input) {
		flags = discordgo.MessageFlagsEphemeral
	} else if allowed, _, err := router.IsAllowedSlashChannel(ctx, input); err == nil && !allowed {
		flags = discordgo.MessageFlagsEphemeral
	}
	if err := session.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Flags: flags},
	}); err != nil {
		return
	}
	response, err := router.Execute(ctx, input)
	content := response.Content
	if err != nil {
		content = "AGX command failed: " + err.Error()
	}
	if strings.TrimSpace(content) == "" {
		content = "Done."
	}
	_, _ = session.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &content})
}

func (b *Bot) AddComponentHandler(router *CommandRouter) {
	if b == nil || b.session == nil || router == nil {
		return
	}
	b.session.AddHandler(func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		b.handleComponentInteraction(s, router, i)
	})
}

type componentHandlerSession interface {
	InteractionRespond(*discordgo.Interaction, *discordgo.InteractionResponse, ...discordgo.RequestOption) error
	ChannelMessageSend(string, string, ...discordgo.RequestOption) (*discordgo.Message, error)
	ChannelMessageEditComplex(*discordgo.MessageEdit, ...discordgo.RequestOption) (*discordgo.Message, error)
}

func (b *Bot) handleComponentInteraction(session componentHandlerSession, router *CommandRouter, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionMessageComponent {
		return
	}
	data := i.MessageComponentData()
	taskID, _, ok := parseChoiceComponentID(data.CustomID)
	if !ok {
		return
	}
	input := CommandInput{
		GuildID:   i.GuildID,
		ChannelID: i.ChannelID,
		Options:   map[string]string{},
	}
	if i.Member != nil && i.Member.User != nil {
		input.UserID = i.Member.User.ID
	}
	if input.UserID == "" && i.User != nil {
		input.UserID = i.User.ID
	}
	if !router.IsAuthorized(input) {
		_ = session.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{Content: "You are not allowed to control AGX from Discord.", Flags: discordgo.MessageFlagsEphemeral},
		})
		return
	}
	choice := componentLabel(i.Message, data.CustomID)
	if choice == "" {
		choice = "option"
	}
	if err := session.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseDeferredMessageUpdate}); err != nil {
		return
	}
	response, err := router.HandleComponentChoice(context.Background(), input, taskID, choice)
	if err != nil {
		_ = b.sendMessageWithSession(context.Background(), session, i.ChannelID, "AGX choice failed: "+err.Error())
		return
	}
	b.markChoiceSelectedWithSession(session, i.ChannelID, i.Message, data.CustomID, choice)
	if strings.TrimSpace(response.Content) != "" {
		_ = b.sendMessageWithSession(context.Background(), session, i.ChannelID, response.Content)
	}
}

func componentLabel(message *discordgo.Message, customID string) string {
	if message == nil {
		return ""
	}
	for _, component := range message.Components {
		row, ok := component.(*discordgo.ActionsRow)
		if !ok {
			if value, ok := component.(discordgo.ActionsRow); ok {
				row = &value
			}
		}
		if row == nil {
			continue
		}
		for _, child := range row.Components {
			button, ok := child.(*discordgo.Button)
			if !ok {
				if value, ok := child.(discordgo.Button); ok {
					button = &value
				}
			}
			if button != nil && button.CustomID == customID {
				return strings.TrimSpace(button.Label)
			}
		}
	}
	return ""
}

func (b *Bot) markChoiceSelected(channelID string, message *discordgo.Message, customID, choice string) {
	b.markChoiceSelectedWithSession(b.session, channelID, message, customID, choice)
}

type messageEditSession interface {
	ChannelMessageEditComplex(*discordgo.MessageEdit, ...discordgo.RequestOption) (*discordgo.Message, error)
}

func (b *Bot) markChoiceSelectedWithSession(session messageEditSession, channelID string, message *discordgo.Message, customID, choice string) {
	if b == nil || b.session == nil || message == nil {
		return
	}
	content := strings.TrimSpace(message.Content)
	if content == "" {
		content = "Agent requested input."
	}
	content += "\n\nSelected: `" + strings.ReplaceAll(truncateUTF8(choice, 120), "`", "'") + "`"
	components := disableChoiceComponents(message.Components, customID)
	_, _ = session.ChannelMessageEditComplex(&discordgo.MessageEdit{
		ID:         message.ID,
		Channel:    channelID,
		Content:    &content,
		Components: &components,
	})
}

func disableChoiceComponents(components []discordgo.MessageComponent, selectedCustomID string) []discordgo.MessageComponent {
	out := make([]discordgo.MessageComponent, 0, len(components))
	for _, component := range components {
		row, ok := component.(*discordgo.ActionsRow)
		if !ok {
			if value, ok := component.(discordgo.ActionsRow); ok {
				row = &value
			}
		}
		if row == nil {
			out = append(out, component)
			continue
		}
		nextRow := discordgo.ActionsRow{Components: make([]discordgo.MessageComponent, 0, len(row.Components))}
		for _, child := range row.Components {
			button, ok := child.(*discordgo.Button)
			if !ok {
				if value, ok := child.(discordgo.Button); ok {
					button = &value
				}
			}
			if button == nil {
				nextRow.Components = append(nextRow.Components, child)
				continue
			}
			next := *button
			next.Disabled = true
			if next.CustomID == selectedCustomID {
				next.Style = discordgo.SuccessButton
			} else {
				next.Style = discordgo.SecondaryButton
			}
			nextRow.Components = append(nextRow.Components, next)
		}
		out = append(out, nextRow)
	}
	return out
}

func (b *Bot) AddPlainMessageHandler(router *CommandRouter) {
	if b == nil || b.session == nil || router == nil {
		return
	}
	b.session.AddHandler(func(s *discordgo.Session, m *discordgo.MessageCreate) {
		b.handlePlainMessage(s, router, m)
	})
}

type plainMessageHandlerSession interface {
	ChannelMessageSend(string, string, ...discordgo.RequestOption) (*discordgo.Message, error)
	MessageReactionAdd(string, string, string, ...discordgo.RequestOption) error
}

func (b *Bot) handlePlainMessage(session plainMessageHandlerSession, router *CommandRouter, m *discordgo.MessageCreate) {
	if m == nil || m.Author == nil || m.Author.Bot {
		return
	}
	content := strings.TrimSpace(m.Content)
	attachments := incomingAttachmentsFromDiscord(m.Attachments)
	if content == "" && len(attachments) == 0 {
		return
	}
	if !b.rememberIncomingMessage(m.ID) {
		return
	}
	input := CommandInput{
		GuildID:   m.GuildID,
		ChannelID: m.ChannelID,
		UserID:    m.Author.ID,
		Options:   map[string]string{},
	}
	ctx := context.Background()
	if router.service == nil {
		_ = b.sendMessageWithSession(ctx, session, m.ChannelID, "AGX message failed: discord command service is not configured")
		return
	}
	if !router.IsAuthorized(input) {
		_ = b.sendMessageWithSession(ctx, session, m.ChannelID, "You are not allowed to control AGX from Discord.")
		return
	}
	taskID, err := router.taskID(ctx, input)
	if err != nil {
		if errors.Is(err, ErrChannelNotLinked) {
			return
		}
		_ = b.sendMessageWithSession(ctx, session, m.ChannelID, "AGX message failed: "+err.Error())
		return
	}
	if isPlainKillMessage(content) {
		if _, err := router.HandlePlainMessage(ctx, input, content); err != nil {
			_ = b.sendMessageWithSession(ctx, session, m.ChannelID, "AGX message failed: "+err.Error())
		}
		return
	}
	b.startProcessingIndicator(ctx, m.ChannelID)
	response, err := router.handlePlainTaskMessage(ctx, taskID, IncomingTaskMessage{Text: content, DiscordMessageID: m.ID, Attachments: attachments})
	if err != nil {
		_ = b.sendMessageWithSession(ctx, session, m.ChannelID, "AGX message failed: "+err.Error())
		return
	}
	if response.React {
		_ = session.MessageReactionAdd(m.ChannelID, m.ID, "🚀")
	}
	if strings.TrimSpace(response.Content) != "" {
		_ = b.sendMessageWithSession(ctx, session, m.ChannelID, response.Content)
		return
	}
}

func (b *Bot) rememberIncomingMessage(messageID string) bool {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return true
	}
	now := time.Now()
	b.messageMu.Lock()
	defer b.messageMu.Unlock()
	if b.messages == nil {
		b.messages = map[string]time.Time{}
	}
	for id, seenAt := range b.messages {
		if now.Sub(seenAt) > processedMessageRetention {
			delete(b.messages, id)
		}
	}
	if _, ok := b.messages[messageID]; ok {
		return false
	}
	b.messages[messageID] = now
	return true
}

func incomingAttachmentsFromDiscord(attachments []*discordgo.MessageAttachment) []IncomingAttachment {
	out := make([]IncomingAttachment, 0, len(attachments))
	for _, attachment := range attachments {
		if attachment == nil {
			continue
		}
		out = append(out, IncomingAttachment{
			DiscordAttachmentID: attachment.ID,
			Filename:            attachment.Filename,
			ContentType:         attachment.ContentType,
			SizeBytes:           int64(attachment.Size),
			URL:                 attachment.URL,
		})
	}
	return out
}

func (b *Bot) AddReactionHandler(router *CommandRouter) {
	if b == nil || b.session == nil || router == nil {
		return
	}
	b.session.AddHandler(func(s *discordgo.Session, r *discordgo.MessageReactionAdd) {
		if r == nil || r.UserID == "" {
			return
		}
		if s.State != nil && s.State.User != nil && r.UserID == s.State.User.ID {
			return
		}
		input := CommandInput{
			GuildID:   r.GuildID,
			ChannelID: r.ChannelID,
			UserID:    r.UserID,
			Options:   map[string]string{},
		}
		response, err := router.HandleReaction(context.Background(), input, r.Emoji.Name)
		if err != nil {
			if errors.Is(err, ErrChannelNotLinked) {
				return
			}
			_ = b.SendMessage(context.Background(), r.ChannelID, "AGX reaction failed: "+err.Error())
			return
		}
		if strings.TrimSpace(response.Content) != "" {
			_ = b.SendMessage(context.Background(), r.ChannelID, response.Content)
		}
	})
}

func (b *Bot) GuildName(guildID string) string {
	if b == nil || b.session == nil || guildID == "" {
		return ""
	}
	guild, err := b.session.State.Guild(guildID)
	if err == nil && guild != nil {
		return guild.Name
	}
	guild, err = b.session.Guild(guildID)
	if err != nil || guild == nil {
		return ""
	}
	return guild.Name
}
