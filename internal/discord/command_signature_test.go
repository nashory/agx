package discord

import (
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestCommandsEquivalent(t *testing.T) {
	desired := ApplicationCommands()

	// Discord echoes the definitions back with the chat-input type set; that must
	// still count as equivalent so AGX skips re-registration.
	existing := ApplicationCommands()
	for _, c := range existing {
		c.Type = discordgo.ChatApplicationCommand
	}
	if !commandsEquivalent(existing, desired) {
		t.Fatal("commandsEquivalent() = false for identical command sets")
	}

	// Discord may return commands in a different order.
	reordered := ApplicationCommands()
	for i, j := 0, len(reordered)-1; i < j; i, j = i+1, j-1 {
		reordered[i], reordered[j] = reordered[j], reordered[i]
	}
	if !commandsEquivalent(reordered, desired) {
		t.Fatal("commandsEquivalent() = false for reordered command sets")
	}

	// A missing command must force re-registration.
	if commandsEquivalent(existing[:len(existing)-1], desired) {
		t.Fatal("commandsEquivalent() = true when a command is missing")
	}

	// A changed description must force re-registration.
	changed := ApplicationCommands()
	changed[0].Description += " (changed)"
	if commandsEquivalent(changed, desired) {
		t.Fatal("commandsEquivalent() = true when a description changed")
	}

	// A changed option must force re-registration.
	changedOpt := ApplicationCommands()
	for _, c := range changedOpt {
		if len(c.Options) > 0 {
			c.Options[0].Description += " (changed)"
			break
		}
	}
	if commandsEquivalent(changedOpt, desired) {
		t.Fatal("commandsEquivalent() = true when an option changed")
	}
}
