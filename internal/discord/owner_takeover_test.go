package discord

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func fixedOwnerClock(t *testing.T, base time.Time) {
	t.Helper()
	prev := ownerNow
	ownerNow = func() time.Time { return base }
	t.Cleanup(func() { ownerNow = prev })
}

func noSettleDelay(t *testing.T) {
	t.Helper()
	prev := ownerClaimSettleDelay
	ownerClaimSettleDelay = 0
	t.Cleanup(func() { ownerClaimSettleDelay = prev })
}

func ownerRecord(id string, epoch int, heartbeat time.Time) string {
	return fmt.Sprintf("id=%s host=mac pid=1 mode=runtime epoch=%d started=%s heartbeat=%s",
		id, epoch, heartbeat.UTC().Format(time.RFC3339), heartbeat.UTC().Format(time.RFC3339))
}

func TestParseOwnerInfoAndStaleness(t *testing.T) {
	base := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	fresh := parseOwnerInfo(ownerRecord("a", 3, base.Add(-10*time.Second)))
	if fresh.id != "a" || fresh.epoch != 3 || !fresh.hasHeartbeat {
		t.Fatalf("parseOwnerInfo() = %#v", fresh)
	}
	if fresh.isStale(base) {
		t.Fatal("recent heartbeat should not be stale")
	}
	stale := parseOwnerInfo(ownerRecord("a", 0, base.Add(-200*time.Second)))
	if !stale.isStale(base) {
		t.Fatal("old heartbeat should be stale")
	}
	legacy := parseOwnerInfo("id=a host=mac pid=1 mode=runtime started=x")
	if legacy.hasHeartbeat || legacy.isStale(base) {
		t.Fatal("legacy owner without heartbeat must never be stale")
	}
}

func TestSameOwnerMatchesByID(t *testing.T) {
	base := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	a := ownerRecord("same", 0, base)
	b := ownerRecord("same", 0, base.Add(30*time.Second)) // heartbeat differs
	if !sameOwner(a, b) {
		t.Fatal("records with the same id should be the same owner")
	}
	if sameOwner(a, ownerRecord("other", 0, base)) {
		t.Fatal("records with different ids should differ")
	}
}

func TestClaimGuildOwnerReturnsStaleForExpiredOwner(t *testing.T) {
	noSettleDelay(t)
	base := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	fixedOwnerClock(t, base)
	client := &fakeOwnerClient{channel: GuildChannel{
		ID:    "control-1",
		Name:  controlChannelName,
		Type:  GuildChannelText,
		Topic: topicWithOwner("", ownerRecord("dead", 4, base.Add(-200*time.Second))),
	}}
	_, err := claimGuildOwner(context.Background(), client, "guild-1", ownerRecord("self", 0, base))
	if !errors.Is(err, ErrGuildOwnerStale) {
		t.Fatalf("claimGuildOwner() error = %v, want ErrGuildOwnerStale", err)
	}
}

func TestClaimGuildOwnerRejectsFreshOwner(t *testing.T) {
	noSettleDelay(t)
	base := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	fixedOwnerClock(t, base)
	client := &fakeOwnerClient{channel: GuildChannel{
		ID:    "control-1",
		Name:  controlChannelName,
		Type:  GuildChannelText,
		Topic: topicWithOwner("", ownerRecord("alive", 4, base.Add(-5*time.Second))),
	}}
	_, err := claimGuildOwner(context.Background(), client, "guild-1", ownerRecord("self", 0, base))
	if !errors.Is(err, ErrGuildOwnerConflict) {
		t.Fatalf("claimGuildOwner() error = %v, want ErrGuildOwnerConflict", err)
	}
}

func TestTakeoverGuildOwnerFromStaleIncrementsEpoch(t *testing.T) {
	noSettleDelay(t)
	base := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	fixedOwnerClock(t, base)
	client := &fakeOwnerClient{channel: GuildChannel{
		ID:    "control-1",
		Name:  controlChannelName,
		Type:  GuildChannelText,
		Topic: topicWithOwner("", ownerRecord("dead", 4, base.Add(-200*time.Second))),
	}}
	channelID, newOwner, err := takeoverGuildOwner(context.Background(), client, "guild-1", ownerRecord("self", 0, base))
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

func TestTakeoverGuildOwnerRefusesLiveOwner(t *testing.T) {
	noSettleDelay(t)
	base := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	fixedOwnerClock(t, base)
	client := &fakeOwnerClient{channel: GuildChannel{
		ID:    "control-1",
		Name:  controlChannelName,
		Type:  GuildChannelText,
		Topic: topicWithOwner("", ownerRecord("alive", 4, base.Add(-5*time.Second))),
	}}
	_, _, err := takeoverGuildOwner(context.Background(), client, "guild-1", ownerRecord("self", 0, base))
	if !errors.Is(err, ErrGuildOwnerConflict) {
		t.Fatalf("takeoverGuildOwner() error = %v, want ErrGuildOwnerConflict for live owner", err)
	}
}

// returningOwnerClient simulates the previous owner reasserting ownership during
// the fencing grace window.
type returningOwnerClient struct {
	fakeOwnerClient
	base    time.Time
	updates int
}

func (c *returningOwnerClient) UpdateChannelTopic(ctx context.Context, channelID, topic string) error {
	c.updates++
	if c.updates == 1 {
		// Ignore our takeover write; the old owner comes back with a fresh beat.
		c.channel.Topic = topicWithOwner("", ownerRecord("dead", 4, c.base))
		return nil
	}
	c.channel.Topic = topic
	return nil
}

func TestTakeoverAbortsWhenOwnerReturnsDuringFencing(t *testing.T) {
	noSettleDelay(t)
	base := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	fixedOwnerClock(t, base)
	client := &returningOwnerClient{
		fakeOwnerClient: fakeOwnerClient{channel: GuildChannel{
			ID:    "control-1",
			Name:  controlChannelName,
			Type:  GuildChannelText,
			Topic: topicWithOwner("", ownerRecord("dead", 4, base.Add(-200*time.Second))),
		}},
		base: base,
	}
	_, _, err := takeoverGuildOwner(context.Background(), client, "guild-1", ownerRecord("self", 0, base))
	if !errors.Is(err, ErrGuildOwnerConflict) {
		t.Fatalf("takeoverGuildOwner() error = %v, want conflict when owner returns", err)
	}
}

func TestRefreshGuildOwnerUpdatesHeartbeat(t *testing.T) {
	base := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	owner := ownerRecord("self", 2, base.Add(-60*time.Second))
	client := &fakeOwnerClient{channel: GuildChannel{
		ID:    "control-1",
		Name:  controlChannelName,
		Type:  GuildChannelText,
		Topic: topicWithOwner("", owner),
	}}
	fixedOwnerClock(t, base)
	refreshed, superseded, err := refreshGuildOwner(context.Background(), client, "guild-1", "control-1", owner)
	if err != nil || superseded {
		t.Fatalf("refreshGuildOwner() = (%q, %v, %v), want fresh non-superseded", refreshed, superseded, err)
	}
	if got := parseOwnerInfo(refreshed); !got.heartbeat.Equal(base) {
		t.Fatalf("refreshed heartbeat = %v, want %v", got.heartbeat, base)
	}
}

func TestRefreshGuildOwnerDetectsSupersede(t *testing.T) {
	base := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	owner := ownerRecord("self", 2, base)
	client := &fakeOwnerClient{channel: GuildChannel{
		ID:    "control-1",
		Name:  controlChannelName,
		Type:  GuildChannelText,
		Topic: topicWithOwner("", ownerRecord("usurper", 3, base)),
	}}
	fixedOwnerClock(t, base)
	_, superseded, err := refreshGuildOwner(context.Background(), client, "guild-1", "control-1", owner)
	if err != nil {
		t.Fatal(err)
	}
	if !superseded {
		t.Fatal("refreshGuildOwner() superseded = false, want true when another runtime owns the guild")
	}
}

func TestWithEpochAndFreshHeartbeat(t *testing.T) {
	base := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	fixedOwnerClock(t, base)
	owner := ownerRecord("self", 1, base.Add(-time.Hour))
	if got := parseOwnerInfo(withEpoch(owner, 7)); got.epoch != 7 {
		t.Fatalf("withEpoch epoch = %d, want 7", got.epoch)
	}
	if got := parseOwnerInfo(withFreshHeartbeat(owner)); !got.heartbeat.Equal(base) {
		t.Fatalf("withFreshHeartbeat heartbeat = %v, want %v", got.heartbeat, base)
	}
	// Legacy owner without a heartbeat field gains one.
	legacy := "id=self host=mac pid=1 mode=runtime started=x"
	if !strings.Contains(withFreshHeartbeat(legacy), "heartbeat=") {
		t.Fatal("withFreshHeartbeat should add a heartbeat to a legacy owner")
	}
}
