package discord

import (
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestIncomingAttachmentsFromDiscord(t *testing.T) {
	got := incomingAttachmentsFromDiscord([]*discordgo.MessageAttachment{
		nil,
		{
			ID:          "att-1",
			Filename:    "screen.png",
			ContentType: "image/png",
			Size:        123,
			URL:         "https://cdn.discordapp.com/attachments/1/2/screen.png",
		},
	})
	if len(got) != 1 {
		t.Fatalf("attachments = %d, want 1", len(got))
	}
	if got[0].DiscordAttachmentID != "att-1" || got[0].Filename != "screen.png" || got[0].ContentType != "image/png" || got[0].SizeBytes != 123 || got[0].URL == "" {
		t.Fatalf("attachment = %#v", got[0])
	}
}
