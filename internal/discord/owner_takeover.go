package discord

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ownerInfo is the parsed view of an owner record stored in the control-channel
// topic. Records written by older AGX versions omit epoch; those parse as epoch 0.
type ownerInfo struct {
	id    string
	host  string
	epoch int
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

// sameOwner reports whether two owner records identify the same runtime. Records
// are compared by their stable id so an epoch rewrite is still recognized as the
// same owner.
func sameOwner(a, b string) bool {
	ai, bi := parseOwnerInfo(a), parseOwnerInfo(b)
	if ai.id != "" || bi.id != "" {
		return ai.id == bi.id
	}
	return strings.TrimSpace(a) == strings.TrimSpace(b)
}

// takeoverGuildOwner performs an explicit, operator-requested ownership takeover.
// Because ownership carries no liveness heartbeat, AGX cannot tell whether the
// current owner is alive, so this trusts the operator's intent and forcibly
// claims the guild. It increments the generation epoch and re-verifies after a
// fencing grace delay to guard against a competing takeover.
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

	// Force the claim with an incremented epoch so a competing takeover is
	// detectable at the fencing recheck below.
	claimed := withEpoch(owner, parseOwnerInfo(existing).epoch+1)
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
	// Fencing check: if another runtime won a concurrent takeover, our record no
	// longer stands.
	channel, err = findControlGuildChannel(ctx, client, guildID, controlChannelID)
	if err != nil {
		return "", "", err
	}
	if current := ownerFromTopic(channel.Topic); !sameOwner(current, claimed) {
		return "", "", guildOwnerConflictError(current)
	}
	return controlChannelID, claimed, nil
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
