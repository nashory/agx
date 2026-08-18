package discord

import (
	"context"
	"testing"

	"github.com/bwmarrin/discordgo"
)

type recordingMessageCreateSession struct {
	method   string
	url      string
	bucketID string
	payload  idempotentMessagePayload
}

func (s *recordingMessageCreateSession) RequestWithBucketID(method, urlStr string, data interface{}, bucketID string, options ...discordgo.RequestOption) ([]byte, error) {
	s.method = method
	s.url = urlStr
	s.bucketID = bucketID
	s.payload = data.(idempotentMessagePayload)
	return []byte(`{}`), nil
}

func TestSendMessageOnceUsesEnforcedNonce(t *testing.T) {
	bot := &Bot{progress: map[string]*processingIndicator{}}
	session := &recordingMessageCreateSession{}
	if err := bot.sendMessageOnceWithSession(context.Background(), session, "channel-1", "delivery-1", "done", nil); err != nil {
		t.Fatal(err)
	}
	wantEndpoint := discordgo.EndpointChannelMessages("channel-1")
	if session.method != "POST" || session.url != wantEndpoint || session.bucketID != wantEndpoint {
		t.Fatalf("request = %s %s bucket=%s", session.method, session.url, session.bucketID)
	}
	if session.payload.Content != "done" || session.payload.Nonce != "delivery-1" || !session.payload.EnforceNonce {
		t.Fatalf("payload = %#v", session.payload)
	}
}
