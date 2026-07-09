package discord

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/nashory/agx/internal/db"
)

const (
	ownerTopicPrefix   = "AGX owner:"
	ownerTopicMaxRunes = 1024
)

var ownerClaimSettleDelay = 250 * time.Millisecond

var ErrGuildOwnerConflict = errors.New("discord guild is already owned by another AGX runtime")

type ownerClient interface {
	EnsureControlChannel(ctx context.Context, guildID, name string) (string, error)
	ListGuildChannels(ctx context.Context, guildID string) ([]GuildChannel, error)
	UpdateChannelTopic(ctx context.Context, channelID, topic string) error
}

func newGuildOwner(mode string) string {
	host, _ := os.Hostname()
	host = strings.TrimSpace(host)
	if host == "" {
		host = "unknown-host"
	}
	mode = strings.TrimSpace(mode)
	if mode == "" {
		mode = "runtime"
	}
	return fmt.Sprintf("id=%s host=%s pid=%d mode=%s started=%s", db.NewTaskID(), compactOwnerField(host), os.Getpid(), compactOwnerField(mode), time.Now().UTC().Format(time.RFC3339))
}

func compactOwnerField(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, " ", "_")
	value = strings.ReplaceAll(value, "\n", "_")
	value = strings.ReplaceAll(value, "\r", "_")
	return value
}

func claimGuildOwner(ctx context.Context, client ownerClient, guildID, owner string) (string, error) {
	if client == nil {
		return "", fmt.Errorf("discord owner client is not configured")
	}
	guildID = strings.TrimSpace(guildID)
	owner = strings.TrimSpace(owner)
	if guildID == "" {
		return "", fmt.Errorf("discord guild id is required")
	}
	if owner == "" {
		return "", fmt.Errorf("discord owner is required")
	}
	controlChannelID, err := client.EnsureControlChannel(ctx, guildID, controlChannelName)
	if err != nil {
		return "", err
	}
	channel, err := findControlGuildChannel(ctx, client, guildID, controlChannelID)
	if err != nil {
		return "", err
	}
	if existing := ownerFromTopic(channel.Topic); existing != "" && existing != owner {
		return "", guildOwnerConflictError(existing)
	}
	if err := client.UpdateChannelTopic(ctx, controlChannelID, topicWithOwner(channel.Topic, owner)); err != nil {
		return "", fmt.Errorf("claim Discord guild owner: %w", err)
	}
	if ownerClaimSettleDelay > 0 {
		timer := time.NewTimer(ownerClaimSettleDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return "", ctx.Err()
		case <-timer.C:
		}
	}
	channel, err = findControlGuildChannel(ctx, client, guildID, controlChannelID)
	if err != nil {
		return "", err
	}
	if existing := ownerFromTopic(channel.Topic); existing != owner {
		if existing == "" {
			return "", fmt.Errorf("discord guild owner claim was not persisted")
		}
		return "", guildOwnerConflictError(existing)
	}
	return controlChannelID, nil
}

func releaseGuildOwner(ctx context.Context, client ownerClient, guildID, controlChannelID, owner string) error {
	if client == nil || strings.TrimSpace(guildID) == "" || strings.TrimSpace(controlChannelID) == "" || strings.TrimSpace(owner) == "" {
		return nil
	}
	channel, err := findControlGuildChannel(ctx, client, guildID, controlChannelID)
	if err != nil {
		return err
	}
	if ownerFromTopic(channel.Topic) != strings.TrimSpace(owner) {
		return nil
	}
	if err := client.UpdateChannelTopic(ctx, controlChannelID, topicWithoutOwner(channel.Topic)); err != nil {
		return fmt.Errorf("release Discord guild owner: %w", err)
	}
	return nil
}

func findControlGuildChannel(ctx context.Context, client ownerClient, guildID, controlChannelID string) (GuildChannel, error) {
	if err := ctx.Err(); err != nil {
		return GuildChannel{}, err
	}
	channels, err := client.ListGuildChannels(ctx, guildID)
	if err != nil {
		return GuildChannel{}, err
	}
	controlChannelID = strings.TrimSpace(controlChannelID)
	for _, channel := range channels {
		if strings.TrimSpace(channel.ID) == controlChannelID {
			return channel, nil
		}
	}
	for _, channel := range channels {
		if channel.Type == GuildChannelText && strings.EqualFold(strings.TrimSpace(channel.Name), controlChannelName) && strings.TrimSpace(channel.ParentID) == "" {
			return channel, nil
		}
	}
	return GuildChannel{}, fmt.Errorf("discord control channel %q was not found after creation", controlChannelName)
}

func guildOwnerConflictError(owner string) error {
	return fmt.Errorf("%w: %s", ErrGuildOwnerConflict, strings.TrimSpace(owner))
}

func ownerFromTopic(topic string) string {
	for _, line := range strings.Split(topic, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, ownerTopicPrefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, ownerTopicPrefix))
		}
	}
	return ""
}

func topicWithOwner(topic, owner string) string {
	topic = topicWithoutOwner(topic)
	ownerLine := ownerTopicPrefix + " " + strings.TrimSpace(owner)
	if strings.TrimSpace(topic) == "" {
		return truncateRunes(ownerLine, ownerTopicMaxRunes)
	}
	separator := "\n"
	available := ownerTopicMaxRunes - len([]rune(separator)) - len([]rune(ownerLine))
	if available <= 0 {
		return truncateRunes(ownerLine, ownerTopicMaxRunes)
	}
	return strings.TrimRight(truncateRunes(topic, available), "\n") + separator + ownerLine
}

func topicWithoutOwner(topic string) string {
	lines := []string{}
	for _, line := range strings.Split(topic, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), ownerTopicPrefix) {
			continue
		}
		lines = append(lines, line)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
