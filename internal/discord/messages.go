package discord

// IncomingTaskMessage is a user message accepted from a Discord task channel.
// The runtime service owns persistence and prompt construction for any
// attachments referenced here.
type IncomingTaskMessage struct {
	Text             string
	DiscordMessageID string
	Attachments      []IncomingAttachment
}

type IncomingAttachment struct {
	DiscordAttachmentID string
	Filename            string
	ContentType         string
	SizeBytes           int64
	URL                 string
}

type SendTaskMessageResult struct {
	Notice string
}
