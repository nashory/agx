package discord

import (
	"context"
	"errors"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/nashory/agx/internal/config"
)

type recordingBotSession struct {
	channelName  string
	responses    []*discordgo.InteractionResponse
	webhookEdits []*discordgo.WebhookEdit
	sent         []recordedBotMessage
	messageEdits []*discordgo.MessageEdit
	reactions    []recordedReaction
}

type recordedBotMessage struct {
	channelID string
	content   string
}

type recordedReaction struct {
	channelID string
	messageID string
	emoji     string
}

func (s *recordingBotSession) Channel(string, ...discordgo.RequestOption) (*discordgo.Channel, error) {
	return &discordgo.Channel{Name: s.channelName}, nil
}

func (s *recordingBotSession) InteractionRespond(_ *discordgo.Interaction, response *discordgo.InteractionResponse, _ ...discordgo.RequestOption) error {
	s.responses = append(s.responses, response)
	return nil
}

func (s *recordingBotSession) InteractionResponseEdit(_ *discordgo.Interaction, edit *discordgo.WebhookEdit, _ ...discordgo.RequestOption) (*discordgo.Message, error) {
	s.webhookEdits = append(s.webhookEdits, edit)
	return &discordgo.Message{ID: "webhook-message"}, nil
}

func (s *recordingBotSession) ChannelMessageSend(channelID, content string, _ ...discordgo.RequestOption) (*discordgo.Message, error) {
	s.sent = append(s.sent, recordedBotMessage{channelID: channelID, content: content})
	return &discordgo.Message{ID: "sent-message"}, nil
}

func (s *recordingBotSession) ChannelMessageEditComplex(edit *discordgo.MessageEdit, _ ...discordgo.RequestOption) (*discordgo.Message, error) {
	s.messageEdits = append(s.messageEdits, edit)
	return &discordgo.Message{ID: edit.ID}, nil
}

func (s *recordingBotSession) MessageReactionAdd(channelID, messageID, emoji string, _ ...discordgo.RequestOption) error {
	s.reactions = append(s.reactions, recordedReaction{channelID: channelID, messageID: messageID, emoji: emoji})
	return nil
}

func TestBotCommandHandlerDefersUnauthorizedSlashCommandEphemerally(t *testing.T) {
	bot := &Bot{}
	session := &recordingBotSession{channelName: "control"}
	router := NewCommandRouter(config.DiscordConfig{GuildID: "guild-1", AllowedUserIDs: []string{"allowed"}}, panicCommandService{t: t})

	bot.handleCommandInteraction(session, router, &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type:      discordgo.InteractionApplicationCommand,
		GuildID:   "guild-1",
		ChannelID: "channel-1",
		Member:    &discordgo.Member{User: &discordgo.User{ID: "denied"}},
		Data:      discordgo.ApplicationCommandInteractionData{Name: "ps"},
	}})

	if len(session.responses) != 1 {
		t.Fatalf("responses = %d, want one deferred response", len(session.responses))
	}
	response := session.responses[0]
	if response.Type != discordgo.InteractionResponseDeferredChannelMessageWithSource {
		t.Fatalf("response type = %v, want deferred channel response", response.Type)
	}
	if response.Data == nil || response.Data.Flags != discordgo.MessageFlagsEphemeral {
		t.Fatalf("response flags = %#v, want ephemeral", response.Data)
	}
	if len(session.webhookEdits) != 1 || session.webhookEdits[0].Content == nil || *session.webhookEdits[0].Content != "You are not allowed to control AGX from Discord." {
		t.Fatalf("webhook edits = %#v, want unauthorized message", session.webhookEdits)
	}
}

func TestBotPlainMessageHandlerRejectsUnauthorizedUserWithoutSendingToTask(t *testing.T) {
	bot := &Bot{}
	session := &recordingBotSession{}
	service := &fakeCommandService{channel: map[string]string{"channel-1": "task-1"}}
	router := NewCommandRouter(config.DiscordConfig{GuildID: "guild-1", AllowedUserIDs: []string{"allowed"}}, service)

	bot.handlePlainMessage(session, router, &discordgo.MessageCreate{Message: &discordgo.Message{
		ID:        "message-1",
		GuildID:   "guild-1",
		ChannelID: "channel-1",
		Content:   "hey",
		Author:    &discordgo.User{ID: "denied"},
	}})

	if service.sentText != "" {
		t.Fatalf("sentText = %q, want no task message", service.sentText)
	}
	if len(session.sent) != 1 || session.sent[0].content != "You are not allowed to control AGX from Discord." {
		t.Fatalf("sent messages = %#v, want unauthorized rejection", session.sent)
	}
}

func TestBotPlainMessageHandlerIgnoresDuplicateDiscordMessageID(t *testing.T) {
	bot := &Bot{}
	session := &recordingBotSession{}
	service := &fakeCommandService{channel: map[string]string{"channel-1": "task-1"}}
	router := NewCommandRouter(config.DiscordConfig{GuildID: "guild-1", AllowedUserIDs: []string{"user-1"}}, service)
	message := &discordgo.MessageCreate{Message: &discordgo.Message{
		ID:        "message-1",
		GuildID:   "guild-1",
		ChannelID: "channel-1",
		Content:   "hey",
		Author:    &discordgo.User{ID: "user-1"},
	}}

	bot.handlePlainMessage(session, router, message)
	bot.handlePlainMessage(session, router, message)

	if service.sentText != "hey" {
		t.Fatalf("sentText = %q, want first message delivered", service.sentText)
	}
	if len(session.sent) != 1 {
		t.Fatalf("sent messages = %#v, want second delivery ignored", session.sent)
	}
}

func TestBotPlainMessageHandlerSendsTaskNoticeAsChannelMessage(t *testing.T) {
	bot := &Bot{}
	session := &recordingBotSession{}
	service := &fakeCommandService{
		channel:    map[string]string{"channel-1": "task-1"},
		sendResult: SendTaskMessageResult{Notice: "Voice transcribed:\n> hello"},
	}
	router := NewCommandRouter(config.DiscordConfig{GuildID: "guild-1", AllowedUserIDs: []string{"user-1"}}, service)

	bot.handlePlainMessage(session, router, &discordgo.MessageCreate{Message: &discordgo.Message{
		ID:        "message-1",
		GuildID:   "guild-1",
		ChannelID: "channel-1",
		Attachments: []*discordgo.MessageAttachment{{
			ID:          "attachment-1",
			Filename:    "voice-message.ogg",
			ContentType: "audio/ogg",
			Size:        123,
			URL:         "https://cdn.example/voice-message.ogg",
		}},
		Author: &discordgo.User{ID: "user-1"},
	}})

	if len(session.sent) != 1 {
		t.Fatalf("sent messages = %#v, want one transcript notice", session.sent)
	}
	if session.sent[0].content != "Voice transcribed:\n> hello" {
		t.Fatalf("notice content = %q, want transcript notice", session.sent[0].content)
	}
	if len(session.reactions) != 1 || session.reactions[0].messageID != "message-1" || session.reactions[0].emoji != "🚀" {
		t.Fatalf("reactions = %#v, want rocket on original voice message", session.reactions)
	}
}

func TestBotComponentHandlerDisablesSelectedChoiceAndSendsChoice(t *testing.T) {
	bot := &Bot{session: &discordgo.Session{}}
	session := &recordingBotSession{}
	service := &fakeCommandService{channel: map[string]string{"channel-1": "task-1"}}
	router := NewCommandRouter(config.DiscordConfig{GuildID: "guild-1", AllowedUserIDs: []string{"user-1"}}, service)
	customID := choiceComponentID("task-1", 0)

	bot.handleComponentInteraction(session, router, &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type:      discordgo.InteractionMessageComponent,
		GuildID:   "guild-1",
		ChannelID: "channel-1",
		Member:    &discordgo.Member{User: &discordgo.User{ID: "user-1"}},
		Data:      discordgo.MessageComponentInteractionData{CustomID: customID},
		Message: &discordgo.Message{
			ID:      "message-1",
			Content: "Pick one",
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{Components: []discordgo.MessageComponent{
					discordgo.Button{Label: "Approve", Style: discordgo.PrimaryButton, CustomID: customID},
				}},
			},
		},
	}})

	if len(session.responses) != 1 || session.responses[0].Type != discordgo.InteractionResponseDeferredMessageUpdate {
		t.Fatalf("responses = %#v, want deferred message update", session.responses)
	}
	if service.sentText != "Approve" {
		t.Fatalf("sentText = %q, want selected choice", service.sentText)
	}
	if len(session.messageEdits) != 1 {
		t.Fatalf("message edits = %#v, want selected choice edit", session.messageEdits)
	}
	edit := session.messageEdits[0]
	if edit.Content == nil || *edit.Content != "Pick one\n\nSelected: `Approve`" {
		t.Fatalf("edit content = %#v, want selected choice content", edit.Content)
	}
	row := (*edit.Components)[0].(discordgo.ActionsRow)
	button := row.Components[0].(discordgo.Button)
	if !button.Disabled || button.Style != discordgo.SuccessButton {
		t.Fatalf("button = %#v, want disabled success button", button)
	}
}

func TestBotComponentHandlerReportsChoiceFailure(t *testing.T) {
	bot := &Bot{session: &discordgo.Session{}}
	session := &recordingBotSession{}
	service := &fakeCommandService{channel: map[string]string{"channel-1": "task-1"}, sendErr: errors.New("runtime down")}
	router := NewCommandRouter(config.DiscordConfig{GuildID: "guild-1", AllowedUserIDs: []string{"user-1"}}, service)
	customID := choiceComponentID("task-1", 0)

	bot.handleComponentInteraction(session, router, &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type:      discordgo.InteractionMessageComponent,
		GuildID:   "guild-1",
		ChannelID: "channel-1",
		User:      &discordgo.User{ID: "user-1"},
		Data:      discordgo.MessageComponentInteractionData{CustomID: customID},
		Message: &discordgo.Message{ID: "message-1", Components: []discordgo.MessageComponent{
			discordgo.ActionsRow{Components: []discordgo.MessageComponent{
				discordgo.Button{Label: "Approve", CustomID: customID},
			}},
		}},
	}})

	if len(session.sent) != 1 || session.sent[0].content != "AGX choice failed: runtime down" {
		t.Fatalf("sent messages = %#v, want choice failure", session.sent)
	}
	if len(session.messageEdits) != 0 {
		t.Fatalf("message edits = %#v, want no selected edit on failure", session.messageEdits)
	}
}

type panicCommandService struct {
	t *testing.T
}

func (s panicCommandService) ListTasks(context.Context) ([]TaskSummary, error) {
	s.t.Fatal("ListTasks should not be called")
	return nil, nil
}

func (s panicCommandService) ListProjects(context.Context) ([]ProjectSummary, error) {
	s.t.Fatal("ListProjects should not be called")
	return nil, nil
}

func (s panicCommandService) CreateProject(context.Context, string, string, string) (ProjectSummary, error) {
	s.t.Fatal("CreateProject should not be called")
	return ProjectSummary{}, nil
}

func (s panicCommandService) DeleteProject(context.Context, string) (ProjectSummary, error) {
	s.t.Fatal("DeleteProject should not be called")
	return ProjectSummary{}, nil
}

func (s panicCommandService) CreateTask(context.Context, string, string, string, string, string, bool) (TaskSummary, error) {
	s.t.Fatal("CreateTask should not be called")
	return TaskSummary{}, nil
}

func (s panicCommandService) DeleteTask(context.Context, string) (TaskSummary, error) {
	s.t.Fatal("DeleteTask should not be called")
	return TaskSummary{}, nil
}

func (s panicCommandService) IsControlChannel(context.Context, string) (bool, error) {
	s.t.Fatal("IsControlChannel should not be called")
	return false, nil
}

func (s panicCommandService) SoftSync(context.Context) error {
	s.t.Fatal("SoftSync should not be called")
	return nil
}

func (s panicCommandService) HardSync(context.Context, string) error {
	s.t.Fatal("HardSync should not be called")
	return nil
}

func (s panicCommandService) ResolveTaskByChannel(context.Context, string) (string, error) {
	s.t.Fatal("ResolveTaskByChannel should not be called")
	return "", nil
}

func (s panicCommandService) ResolveTask(context.Context, string) (TaskSummary, error) {
	s.t.Fatal("ResolveTask should not be called")
	return TaskSummary{}, nil
}

func (s panicCommandService) GetTask(context.Context, string) (TaskSummary, error) {
	s.t.Fatal("GetTask should not be called")
	return TaskSummary{}, nil
}

func (s panicCommandService) InterruptTask(context.Context, string) error {
	s.t.Fatal("InterruptTask should not be called")
	return nil
}

func (s panicCommandService) KillTask(context.Context, string, string) error {
	s.t.Fatal("KillTask should not be called")
	return nil
}

func (s panicCommandService) TaskLogs(context.Context, string, int) (string, error) {
	s.t.Fatal("TaskLogs should not be called")
	return "", nil
}

func (s panicCommandService) SendTaskMessage(context.Context, string, IncomingTaskMessage) (SendTaskMessageResult, error) {
	s.t.Fatal("SendTaskMessage should not be called")
	return SendTaskMessageResult{}, nil
}
