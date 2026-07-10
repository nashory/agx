package discord

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/nashory/agx/internal/db"
)

// ownerReleaseAttempts and ownerReleaseRetryDelay make ownership release on
// shutdown resilient to transient Discord API errors, so a stopped runtime does
// not leave a dangling owner record that blocks the next startup.
var (
	ownerReleaseAttempts   = 3
	ownerReleaseRetryDelay = 500 * time.Millisecond
)

const (
	ownerTopicPrefix   = "AGX owner:"
	ownerTopicMaxRunes = 1024
)

var ownerClaimSettleDelay = 250 * time.Millisecond

// ownerNow returns the current time for owner record timestamps. It is a variable
// so tests can control time.
var ownerNow = func() time.Time { return time.Now().UTC() }

var ErrGuildOwnerConflict = errors.New("discord guild is already owned by another AGX runtime")

type ownerClient interface {
	EnsureControlChannel(ctx context.Context, guildID, name string) (string, error)
	ListGuildChannels(ctx context.Context, guildID string) ([]GuildChannel, error)
	UpdateChannelTopic(ctx context.Context, channelID, topic string) error
}

func newGuildOwner(mode string) string {
	return newGuildOwnerEpoch(mode, 0)
}

// newGuildOwnerEpoch builds an owner record at the given generation epoch.
// Takeover increments the epoch so a competing takeover can be detected during
// the fencing recheck.
func newGuildOwnerEpoch(mode string, epoch int) string {
	host, _ := os.Hostname()
	host = strings.TrimSpace(host)
	if host == "" {
		host = "unknown-host"
	}
	mode = strings.TrimSpace(mode)
	if mode == "" {
		mode = "runtime"
	}
	return fmt.Sprintf("id=%s host=%s pid=%d mode=%s epoch=%d started=%s",
		db.NewTaskID(), compactOwnerField(host), os.Getpid(), compactOwnerField(mode), epoch, ownerNow().Format(time.RFC3339))
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
	log.Printf("operation=%q phase=%q guild=%q", "discord_owner_claim", "ensure_control_channel", guildID)
	controlChannelID, err := client.EnsureControlChannel(ctx, guildID, controlChannelName)
	if err != nil {
		return "", err
	}
	log.Printf("operation=%q phase=%q control_channel=%q", "discord_owner_claim", "read_control_channel", controlChannelID)
	channel, err := findControlGuildChannel(ctx, client, guildID, controlChannelID)
	if err != nil {
		return "", err
	}
	if existing := ownerFromTopic(channel.Topic); existing != "" && !sameOwner(existing, owner) {
		if ownerIsSameHost(existing, owner) {
			// Leftover record from a previous run on this same host. The runtime
			// lock guarantees that predecessor is gone, so reclaim it instead of
			// failing, which keeps a crashed or restarted runtime from locking
			// itself out of its own guild.
			log.Printf("operation=%q previous_owner=%q", "discord_owner_reclaim_same_host", existing)
		} else {
			// A different host owns the guild. Ownership is only recorded and
			// checked at connect time (there is no liveness heartbeat), so AGX
			// never steals it automatically; the operator must run an explicit
			// takeover once they know the other runtime is gone.
			return "", guildOwnerConflictError(existing)
		}
	}
	log.Printf("operation=%q phase=%q control_channel=%q", "discord_owner_claim", "update_topic", controlChannelID)
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
	if existing := ownerFromTopic(channel.Topic); !sameOwner(existing, owner) {
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
	var lastErr error
	for attempt := 0; attempt < ownerReleaseAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(ownerReleaseRetryDelay):
			}
		}
		channel, err := findControlGuildChannel(ctx, client, guildID, controlChannelID)
		if err != nil {
			lastErr = err
			continue
		}
		if !sameOwner(ownerFromTopic(channel.Topic), owner) {
			// Already released, or the record now belongs to another runtime.
			return nil
		}
		if err := client.UpdateChannelTopic(ctx, controlChannelID, topicWithoutOwner(channel.Topic)); err != nil {
			lastErr = fmt.Errorf("release Discord guild owner: %w", err)
			continue
		}
		return nil
	}
	return lastErr
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
