package discord

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func noSettleDelay(t *testing.T) {
	t.Helper()
	prev := ownerClaimSettleDelay
	ownerClaimSettleDelay = 0
	t.Cleanup(func() { ownerClaimSettleDelay = prev })
}

func ownerRecord(id string, epoch int) string {
	return ownerRecordOnHost(id, "this-host", epoch)
}

// remoteOwnerRecord is an owner record from a different host, used to exercise the
// cross-host conflict path (same-host records are auto-reclaimed).
func remoteOwnerRecord(id string, epoch int) string {
	return ownerRecordOnHost(id, "other-host", epoch)
}

func ownerRecordOnHost(id, host string, epoch int) string {
	return fmt.Sprintf("id=%s host=%s pid=1 mode=runtime epoch=%d started=2026-07-09T12:00:00Z", id, host, epoch)
}

func TestParseOwnerInfo(t *testing.T) {
	info := parseOwnerInfo(ownerRecord("a", 3))
	if info.id != "a" || info.epoch != 3 || info.host != "this-host" {
		t.Fatalf("parseOwnerInfo() = %#v", info)
	}
	legacy := parseOwnerInfo("id=a host=mac pid=1 mode=runtime")
	if legacy.id != "a" || legacy.epoch != 0 {
		t.Fatalf("legacy parseOwnerInfo() = %#v", legacy)
	}
}

func TestSameOwnerMatchesByID(t *testing.T) {
	a := ownerRecord("same", 0)
	b := ownerRecord("same", 5) // epoch differs
	if !sameOwner(a, b) {
		t.Fatal("records with the same id should be the same owner")
	}
	if sameOwner(a, ownerRecord("other", 0)) {
		t.Fatal("records with different ids should differ")
	}
}

func TestClaimGuildOwnerRejectsDifferentHostOwner(t *testing.T) {
	noSettleDelay(t)
	client := &fakeOwnerClient{channel: GuildChannel{
		ID:    "control-1",
		Name:  controlChannelName,
		Type:  GuildChannelText,
		Topic: topicWithOwner("", remoteOwnerRecord("other", 4)),
	}}
	_, err := claimGuildOwner(context.Background(), client, "guild-1", ownerRecord("self", 0))
	if !errors.Is(err, ErrGuildOwnerConflict) {
		t.Fatalf("claimGuildOwner() error = %v, want ErrGuildOwnerConflict", err)
	}
}

func TestClaimGuildOwnerReclaimsSameHostLeftover(t *testing.T) {
	noSettleDelay(t)
	// A record from THIS host (a crashed/restarted predecessor). The runtime lock
	// guarantees it is gone, so claim reclaims it rather than failing.
	client := &fakeOwnerClient{channel: GuildChannel{
		ID:    "control-1",
		Name:  controlChannelName,
		Type:  GuildChannelText,
		Topic: topicWithOwner("", ownerRecord("dead-predecessor", 2)),
	}}
	channelID, err := claimGuildOwner(context.Background(), client, "guild-1", ownerRecord("self", 0))
	if err != nil {
		t.Fatalf("claimGuildOwner() error = %v, want same-host reclaim to succeed", err)
	}
	if channelID != "control-1" {
		t.Fatalf("channelID = %q, want control-1", channelID)
	}
	if got := parseOwnerInfo(ownerFromTopic(client.channel.Topic)).id; got != "self" {
		t.Fatalf("topic owner id = %q, want self after reclaim", got)
	}
}

func TestTakeoverGuildOwnerIncrementsEpoch(t *testing.T) {
	noSettleDelay(t)
	client := &fakeOwnerClient{channel: GuildChannel{
		ID:    "control-1",
		Name:  controlChannelName,
		Type:  GuildChannelText,
		Topic: topicWithOwner("", remoteOwnerRecord("old", 4)),
	}}
	channelID, newOwner, err := takeoverGuildOwner(context.Background(), client, "guild-1", ownerRecord("self", 0))
	if err != nil {
		t.Fatalf("takeoverGuildOwner() error = %v", err)
	}
	if channelID != "control-1" {
		t.Fatalf("channelID = %q, want control-1", channelID)
	}
	if got := parseOwnerInfo(newOwner); got.id != "self" || got.epoch != 5 {
		t.Fatalf("new owner = %#v, want id=self epoch=5", got)
	}
	if got := parseOwnerInfo(ownerFromTopic(client.channel.Topic)); got.id != "self" || got.epoch != 5 {
		t.Fatalf("topic owner = %#v, want id=self epoch=5", got)
	}
}

// returningOwnerClient simulates a competing runtime winning the channel during
// the fencing grace window after our takeover write.
type returningOwnerClient struct {
	fakeOwnerClient
	updates int
}

func (c *returningOwnerClient) UpdateChannelTopic(ctx context.Context, channelID, topic string) error {
	c.updates++
	if c.updates == 1 {
		// Ignore our takeover write; a competing owner wins the channel instead.
		c.channel.Topic = topicWithOwner("", remoteOwnerRecord("competitor", 9))
		return nil
	}
	c.channel.Topic = topic
	return nil
}

func TestTakeoverAbortsWhenAnotherRuntimeWinsFencing(t *testing.T) {
	noSettleDelay(t)
	client := &returningOwnerClient{
		fakeOwnerClient: fakeOwnerClient{channel: GuildChannel{
			ID:    "control-1",
			Name:  controlChannelName,
			Type:  GuildChannelText,
			Topic: topicWithOwner("", remoteOwnerRecord("old", 4)),
		}},
	}
	_, _, err := takeoverGuildOwner(context.Background(), client, "guild-1", ownerRecord("self", 0))
	if !errors.Is(err, ErrGuildOwnerConflict) {
		t.Fatalf("takeoverGuildOwner() error = %v, want conflict when another runtime wins fencing", err)
	}
}

func TestWithEpoch(t *testing.T) {
	if got := parseOwnerInfo(withEpoch(ownerRecord("self", 1), 7)); got.epoch != 7 {
		t.Fatalf("withEpoch epoch = %d, want 7", got.epoch)
	}
	// Legacy owner without an epoch field gains one.
	legacy := "id=self host=mac pid=1 mode=runtime started=x"
	if got := parseOwnerInfo(withEpoch(legacy, 3)); got.epoch != 3 {
		t.Fatalf("withEpoch on legacy epoch = %d, want 3", got.epoch)
	}
}

// flakyReleaseClient fails a set number of topic updates before succeeding, to
// exercise release retries.
type flakyReleaseClient struct {
	fakeOwnerClient
	failUpdates int
	updates     int
}

func (c *flakyReleaseClient) UpdateChannelTopic(ctx context.Context, channelID, topic string) error {
	c.updates++
	if c.updates <= c.failUpdates {
		return fmt.Errorf("transient discord error")
	}
	c.channel.Topic = topic
	return nil
}

func TestReleaseGuildOwnerRetriesOnTransientError(t *testing.T) {
	prevDelay := ownerReleaseRetryDelay
	ownerReleaseRetryDelay = 0
	t.Cleanup(func() { ownerReleaseRetryDelay = prevDelay })

	owner := ownerRecord("self", 1)
	client := &flakyReleaseClient{
		fakeOwnerClient: fakeOwnerClient{channel: GuildChannel{
			ID:    "control-1",
			Name:  controlChannelName,
			Type:  GuildChannelText,
			Topic: topicWithOwner("", owner),
		}},
		failUpdates: ownerReleaseAttempts - 1,
	}
	if err := releaseGuildOwner(context.Background(), client, "guild-1", "control-1", owner); err != nil {
		t.Fatalf("releaseGuildOwner() error = %v, want success after retries", err)
	}
	if ownerFromTopic(client.channel.Topic) != "" {
		t.Fatalf("owner not cleared after retried release: %q", client.channel.Topic)
	}
}
