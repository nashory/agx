package discord

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ownerHeartbeatInterval is how often the active owner refreshes its heartbeat in
// the control-channel topic so other runtimes can tell it is alive.
var ownerHeartbeatInterval = 30 * time.Second

// ownerInfo is the parsed view of an owner record stored in the control-channel
// topic. Records written by older AGX versions omit epoch/heartbeat; those parse
// as epoch 0 with no heartbeat, which is treated as "not stale" so ownership is
// never stolen from an owner whose liveness cannot be determined.
type ownerInfo struct {
	id           string
	host         string
	epoch        int
	heartbeat    time.Time
	hasHeartbeat bool
}

func parseOwnerInfo(owner string) ownerInfo {
	info := ownerInfo{}
	for _, field := range strings.Fields(owner) {
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		switch key {
		case "id":
			info.id = value
		case "host":
			info.host = value
		case "epoch":
			if n, err := strconv.Atoi(value); err == nil {
				info.epoch = n
			}
		case "heartbeat":
			if value != "" {
				if t, err := time.Parse(time.RFC3339, value); err == nil {
					info.heartbeat = t
					info.hasHeartbeat = true
				}
			}
		}
	}
	return info
}

// ownerIsSameHost reports whether two owner records come from the same host. A
// leftover record from this host is a dead predecessor of this runtime (the
// runtime lock guarantees only one runtime per config per host), so it is safe
// to reclaim automatically on startup.
func ownerIsSameHost(a, b string) bool {
	ah, bh := parseOwnerInfo(a).host, parseOwnerInfo(b).host
	return ah != "" && ah == bh
}

// isStale reports whether the owner's heartbeat has expired. An owner without a
// heartbeat is never considered stale, so legacy records and owners of unknown
// liveness are protected from silent takeover.
func (o ownerInfo) isStale(now time.Time) bool {
	return o.hasHeartbeat && now.Sub(o.heartbeat) > ownerStaleThreshold
}

// sameOwner reports whether two owner records identify the same runtime. Records
// are compared by their stable id so a heartbeat refresh (which rewrites the
// record) is still recognized as the same owner.
func sameOwner(a, b string) bool {
	ai, bi := parseOwnerInfo(a), parseOwnerInfo(b)
	if ai.id != "" || bi.id != "" {
		return ai.id == bi.id
	}
	return strings.TrimSpace(a) == strings.TrimSpace(b)
}

func guildOwnerStaleError(owner string) error {
	return fmt.Errorf("%w: %s", ErrGuildOwnerStale, strings.TrimSpace(owner))
}

// takeoverGuildOwner performs an explicit ownership takeover from a stale owner.
// It refuses to take over from a live owner, increments the generation epoch so
// the previous owner can detect it has been superseded, and re-verifies after a
// fencing grace delay to guard against a returning owner or a competing takeover.
func takeoverGuildOwner(ctx context.Context, client ownerClient, guildID, owner string) (channelID, newOwner string, err error) {
	if client == nil {
		return "", "", fmt.Errorf("discord owner client is not configured")
	}
	guildID = strings.TrimSpace(guildID)
	owner = strings.TrimSpace(owner)
	if guildID == "" {
		return "", "", fmt.Errorf("discord guild id is required")
	}
	if owner == "" {
		return "", "", fmt.Errorf("discord owner is required")
	}
	controlChannelID, err := client.EnsureControlChannel(ctx, guildID, controlChannelName)
	if err != nil {
		return "", "", err
	}
	channel, err := findControlGuildChannel(ctx, client, guildID, controlChannelID)
	if err != nil {
		return "", "", err
	}
	existing := ownerFromTopic(channel.Topic)
	if existing == "" || sameOwner(existing, owner) {
		// Nothing to take over; a normal claim is sufficient.
		id, err := claimGuildOwner(ctx, client, guildID, owner)
		return id, owner, err
	}
	existingInfo := parseOwnerInfo(existing)
	if !existingInfo.isStale(ownerNow()) {
		// Never take ownership from an owner that still looks alive.
		return "", "", guildOwnerConflictError(existing)
	}

	// Claim with an incremented epoch so the previous owner self-fences.
	claimed := withEpoch(owner, existingInfo.epoch+1)
	if err := client.UpdateChannelTopic(ctx, controlChannelID, topicWithOwner(channel.Topic, claimed)); err != nil {
		return "", "", fmt.Errorf("take over Discord guild owner: %w", err)
	}
	if ownerClaimSettleDelay > 0 {
		timer := time.NewTimer(ownerClaimSettleDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return "", "", ctx.Err()
		case <-timer.C:
		}
	}
	// Fencing check: if the old owner refreshed its heartbeat or another runtime
	// won a concurrent takeover, our record no longer stands.
	channel, err = findControlGuildChannel(ctx, client, guildID, controlChannelID)
	if err != nil {
		return "", "", err
	}
	if current := ownerFromTopic(channel.Topic); !sameOwner(current, claimed) {
		return "", "", guildOwnerConflictError(current)
	}
	return controlChannelID, claimed, nil
}

// refreshGuildOwner updates the owner heartbeat. It returns the refreshed owner
// record, and reports superseded=true when the control channel is now owned by a
// different runtime, in which case the caller must self-fence and disconnect.
func refreshGuildOwner(ctx context.Context, client ownerClient, guildID, controlChannelID, owner string) (string, bool, error) {
	if client == nil || strings.TrimSpace(guildID) == "" || strings.TrimSpace(controlChannelID) == "" || strings.TrimSpace(owner) == "" {
		return owner, false, nil
	}
	channel, err := findControlGuildChannel(ctx, client, guildID, controlChannelID)
	if err != nil {
		return owner, false, err
	}
	current := ownerFromTopic(channel.Topic)
	if current != "" && !sameOwner(current, owner) {
		return owner, true, nil
	}
	refreshed := withFreshHeartbeat(owner)
	if err := client.UpdateChannelTopic(ctx, controlChannelID, topicWithOwner(channel.Topic, refreshed)); err != nil {
		return owner, false, err
	}
	return refreshed, false, nil
}

// withEpoch returns owner with its epoch field set to the given value.
func withEpoch(owner string, epoch int) string {
	info := parseOwnerInfo(owner)
	next := fmt.Sprintf("epoch=%d", epoch)
	current := fmt.Sprintf("epoch=%d", info.epoch)
	if strings.Contains(owner, current) {
		return strings.Replace(owner, current, next, 1)
	}
	return strings.TrimSpace(owner + " " + next)
}

// withFreshHeartbeat returns owner with its heartbeat field set to now.
func withFreshHeartbeat(owner string) string {
	info := parseOwnerInfo(owner)
	next := "heartbeat=" + ownerNow().Format(time.RFC3339)
	if info.hasHeartbeat {
		current := "heartbeat=" + info.heartbeat.UTC().Format(time.RFC3339)
		if strings.Contains(owner, current) {
			return strings.Replace(owner, current, next, 1)
		}
	}
	if strings.Contains(owner, "heartbeat=") {
		// Replace an empty heartbeat field.
		return replaceEmptyHeartbeat(owner, next)
	}
	return strings.TrimSpace(owner + " " + next)
}

func replaceEmptyHeartbeat(owner, next string) string {
	fields := strings.Fields(owner)
	for i, field := range fields {
		if field == "heartbeat=" {
			fields[i] = next
			return strings.Join(fields, " ")
		}
	}
	return strings.TrimSpace(owner + " " + next)
}
