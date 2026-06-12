package discord

import (
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestChoiceComponentIDRoundTrip(t *testing.T) {
	id := choiceComponentID("task-1", 2)
	taskID, index, ok := parseChoiceComponentID(id)
	if !ok || taskID != "task-1" || index != 2 {
		t.Fatalf("parseChoiceComponentID(%q) = %q, %d, %v; want task-1, 2, true", id, taskID, index, ok)
	}
}

func TestChoiceComponentsLimitRowsAndButtons(t *testing.T) {
	options := make([]InteractiveOption, 0, 30)
	for i := 0; i < 30; i++ {
		options = append(options, InteractiveOption{Label: "Option"})
	}
	components := choiceComponents(InteractivePrompt{TaskID: "task-1", Content: "Pick", Options: options})
	if len(components) != 5 {
		t.Fatalf("rows = %d, want 5", len(components))
	}
	for _, component := range components {
		row := component.(discordgo.ActionsRow)
		if len(row.Components) != 5 {
			t.Fatalf("row components = %d, want 5", len(row.Components))
		}
	}
}

func TestDisableChoiceComponentsMarksSelectedButton(t *testing.T) {
	selectedID := choiceComponentID("task-1", 1)
	components := []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{Label: "A", Style: discordgo.PrimaryButton, CustomID: choiceComponentID("task-1", 0)},
			discordgo.Button{Label: "B", Style: discordgo.PrimaryButton, CustomID: selectedID},
		}},
	}
	disabled := disableChoiceComponents(components, selectedID)
	row := disabled[0].(discordgo.ActionsRow)
	first := row.Components[0].(discordgo.Button)
	second := row.Components[1].(discordgo.Button)
	if !first.Disabled || !second.Disabled {
		t.Fatalf("buttons disabled = %v, %v; want both true", first.Disabled, second.Disabled)
	}
	if first.Style != discordgo.SecondaryButton || second.Style != discordgo.SuccessButton {
		t.Fatalf("styles = %v, %v; want secondary, success", first.Style, second.Style)
	}
}

func TestRememberIncomingMessageDedupesDiscordMessageID(t *testing.T) {
	bot := &Bot{}
	if !bot.rememberIncomingMessage("message-1") {
		t.Fatal("first message was rejected")
	}
	if bot.rememberIncomingMessage("message-1") {
		t.Fatal("duplicate message was accepted")
	}
	if !bot.rememberIncomingMessage("message-2") {
		t.Fatal("different message was rejected")
	}
}
