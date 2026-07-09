package discord

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type fakeOwnerClient struct {
	channel GuildChannel
}

func (f *fakeOwnerClient) EnsureControlChannel(ctx context.Context, guildID, name string) (string, error) {
	if f.channel.ID == "" {
		f.channel = GuildChannel{ID: "control-1", Name: name, Type: GuildChannelText}
	}
	return f.channel.ID, nil
}

func (f *fakeOwnerClient) ListGuildChannels(ctx context.Context, guildID string) ([]GuildChannel, error) {
	return []GuildChannel{f.channel}, nil
}

func (f *fakeOwnerClient) UpdateChannelTopic(ctx context.Context, channelID, topic string) error {
	f.channel.Topic = topic
	return nil
}

func TestOwnerTopicRoundTripPreservesExistingTopic(t *testing.T) {
	topic := topicWithOwner("Existing notes", "id=one host=mac")
	if got := ownerFromTopic(topic); got != "id=one host=mac" {
		t.Fatalf("ownerFromTopic() = %q", got)
	}
	if got := topicWithoutOwner(topic); got != "Existing notes" {
		t.Fatalf("topicWithoutOwner() = %q", got)
	}
}

func TestOwnerTopicTruncatesExistingTopicBeforeOwner(t *testing.T) {
	topic := topicWithOwner(strings.Repeat("x", 2000), "id=one host=mac")
	if len([]rune(topic)) > ownerTopicMaxRunes {
		t.Fatalf("topic length = %d, want <= %d", len([]rune(topic)), ownerTopicMaxRunes)
	}
	if got := ownerFromTopic(topic); got != "id=one host=mac" {
		t.Fatalf("ownerFromTopic() = %q", got)
	}
}

func TestClaimGuildOwnerRejectsExistingOwner(t *testing.T) {
	oldDelay := ownerClaimSettleDelay
	ownerClaimSettleDelay = 0
	t.Cleanup(func() { ownerClaimSettleDelay = oldDelay })

	client := &fakeOwnerClient{channel: GuildChannel{
		ID:    "control-1",
		Name:  controlChannelName,
		Type:  GuildChannelText,
		Topic: topicWithOwner("", "id=other host=mac"),
	}}
	_, err := claimGuildOwner(context.Background(), client, "guild-1", "id=self host=wsl")
	if !errors.Is(err, ErrGuildOwnerConflict) || !strings.Contains(err.Error(), "id=other") {
		t.Fatalf("claimGuildOwner() error = %v, want owner conflict", err)
	}
}

func TestClaimAndReleaseGuildOwner(t *testing.T) {
	oldDelay := ownerClaimSettleDelay
	ownerClaimSettleDelay = 0
	t.Cleanup(func() { ownerClaimSettleDelay = oldDelay })

	client := &fakeOwnerClient{channel: GuildChannel{
		ID:    "control-1",
		Name:  controlChannelName,
		Type:  GuildChannelText,
		Topic: "Existing notes",
	}}
	channelID, err := claimGuildOwner(context.Background(), client, "guild-1", "id=self host=wsl")
	if err != nil {
		t.Fatal(err)
	}
	if channelID != "control-1" {
		t.Fatalf("channelID = %q, want control-1", channelID)
	}
	if got := ownerFromTopic(client.channel.Topic); got != "id=self host=wsl" {
		t.Fatalf("ownerFromTopic() = %q", got)
	}
	if err := releaseGuildOwner(context.Background(), client, "guild-1", channelID, "id=self host=wsl"); err != nil {
		t.Fatal(err)
	}
	if got := client.channel.Topic; got != "Existing notes" {
		t.Fatalf("released topic = %q, want existing notes", got)
	}
}

func TestClaimGuildOwnerDetectsLostRace(t *testing.T) {
	oldDelay := ownerClaimSettleDelay
	ownerClaimSettleDelay = time.Nanosecond
	t.Cleanup(func() { ownerClaimSettleDelay = oldDelay })

	client := &racingOwnerClient{fakeOwnerClient: fakeOwnerClient{channel: GuildChannel{
		ID:   "control-1",
		Name: controlChannelName,
		Type: GuildChannelText,
	}}}
	_, err := claimGuildOwner(context.Background(), client, "guild-1", "id=self host=wsl")
	if !errors.Is(err, ErrGuildOwnerConflict) || !strings.Contains(err.Error(), "id=other") {
		t.Fatalf("claimGuildOwner() error = %v, want lost race conflict", err)
	}
}

type racingOwnerClient struct {
	fakeOwnerClient
	updates int
}

func (f *racingOwnerClient) UpdateChannelTopic(ctx context.Context, channelID, topic string) error {
	f.updates++
	if f.updates == 1 {
		f.channel.Topic = topicWithOwner("", "id=other host=mac")
		return nil
	}
	f.channel.Topic = topic
	return nil
}
