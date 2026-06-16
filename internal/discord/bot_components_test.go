package discord

import (
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
)

func TestChoiceComponentIDRoundTrip(t *testing.T) {
	id := choiceComponentID("task-1", 2)
	taskID, index, ok := parseChoiceComponentID(id)
	if !ok || taskID != "task-1" || index != 2 {
		t.Fatalf("parseChoiceComponentID(%q) = %q, %d, %v; want task-1, 2, true", id, taskID, index, ok)
	}
}

func TestParseChoiceComponentIDRejectsInvalidPayloads(t *testing.T) {
	for _, customID := range []string{
		"",
		"other:task-1:0",
		choiceComponentPrefix + ":0",
		choiceComponentPrefix + "task-1",
		choiceComponentPrefix + "task-1:not-a-number",
		choiceComponentPrefix + "task-1:-1",
	} {
		if taskID, index, ok := parseChoiceComponentID(customID); ok {
			t.Fatalf("parseChoiceComponentID(%q) = %q, %d, true; want rejected", customID, taskID, index)
		}
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

func TestChoiceComponentsSkipEmptyLabelsAndRequireTaskID(t *testing.T) {
	if components := choiceComponents(InteractivePrompt{Content: "Pick", Options: []InteractiveOption{{Label: "A"}}}); components != nil {
		t.Fatalf("components without task id = %#v, want nil", components)
	}
	components := choiceComponents(InteractivePrompt{
		TaskID:  "task-1",
		Content: "Pick",
		Options: []InteractiveOption{
			{Label: "   "},
			{Label: "Keep"},
			{Label: "Also keep"},
		},
	})
	if len(components) != 1 {
		t.Fatalf("rows = %d, want one row", len(components))
	}
	row := components[0].(discordgo.ActionsRow)
	if len(row.Components) != 2 {
		t.Fatalf("buttons = %d, want two non-empty options", len(row.Components))
	}
	first := row.Components[0].(discordgo.Button)
	if first.Label != "Keep" || first.CustomID != choiceComponentID("task-1", 1) {
		t.Fatalf("first button = %#v, want original option index preserved", first)
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

func TestComponentLabelReadsPointerAndValueRows(t *testing.T) {
	customID := choiceComponentID("task-1", 0)
	message := &discordgo.Message{Components: []discordgo.MessageComponent{
		&discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			&discordgo.Button{Label: "Pointer", CustomID: customID},
		}},
	}}
	if got := componentLabel(message, customID); got != "Pointer" {
		t.Fatalf("componentLabel(pointer) = %q, want Pointer", got)
	}

	message.Components = []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{Label: "Value", CustomID: customID},
		}},
	}
	if got := componentLabel(message, customID); got != "Value" {
		t.Fatalf("componentLabel(value) = %q, want Value", got)
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

func TestRememberIncomingMessagePrunesExpiredIDs(t *testing.T) {
	bot := &Bot{messages: map[string]time.Time{
		"old": time.Now().Add(-processedMessageRetention - time.Minute),
		"new": time.Now(),
	}}
	if !bot.rememberIncomingMessage("next") {
		t.Fatal("new message was rejected")
	}
	if _, ok := bot.messages["old"]; ok {
		t.Fatalf("expired message ID was not pruned: %#v", bot.messages)
	}
	if _, ok := bot.messages["new"]; !ok {
		t.Fatalf("fresh message ID was pruned: %#v", bot.messages)
	}
}
