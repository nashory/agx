package discord

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
)

type recordingProgressSession struct {
	mu         sync.Mutex
	nextID     int
	sends      []string
	edits      []string
	editErrors []error
}

func (s *recordingProgressSession) ChannelMessageSend(_ string, content string, _ ...discordgo.RequestOption) (*discordgo.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	s.sends = append(s.sends, content)
	return &discordgo.Message{ID: fmt.Sprintf("message-%d", s.nextID)}, nil
}

func (s *recordingProgressSession) ChannelMessageEdit(_, _ string, content string, _ ...discordgo.RequestOption) (*discordgo.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.edits = append(s.edits, content)
	if len(s.editErrors) > 0 {
		err := s.editErrors[0]
		s.editErrors = s.editErrors[1:]
		return nil, err
	}
	return &discordgo.Message{ID: "edited"}, nil
}

func TestDeleteDiscordChannelsConcurrentlyHonorsLimitAndDeduplicates(t *testing.T) {
	var active int32
	var maxActive int32
	var mu sync.Mutex
	deleted := []string{}

	err := deleteDiscordChannelsConcurrently(context.Background(), []string{"a", "b", "a", "", "c", "d"}, 2, func(ctx context.Context, channelID string) error {
		current := atomic.AddInt32(&active, 1)
		for {
			previous := atomic.LoadInt32(&maxActive)
			if current <= previous || atomic.CompareAndSwapInt32(&maxActive, previous, current) {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
		mu.Lock()
		deleted = append(deleted, channelID)
		mu.Unlock()
		atomic.AddInt32(&active, -1)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(deleted), 4; got != want {
		t.Fatalf("deleted %d channels, want %d: %#v", got, want, deleted)
	}
	if got := atomic.LoadInt32(&maxActive); got > 2 {
		t.Fatalf("max active deletes = %d, want <= 2", got)
	}
}

func TestDeleteDiscordChannelsConcurrentlyReturnsDeleteError(t *testing.T) {
	want := errors.New("delete failed")
	err := deleteDiscordChannelsConcurrently(context.Background(), []string{"a", "b", "c"}, 2, func(ctx context.Context, channelID string) error {
		if channelID == "b" {
			return want
		}
		return nil
	})
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}

func TestDeleteDiscordChannelsConcurrentlyReturnsContextError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := deleteDiscordChannelsConcurrently(ctx, []string{"a"}, 1, func(ctx context.Context, channelID string) error {
		t.Fatal("delete should not run for canceled context")
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestProgressUpdateDelayCoalescesRecentEdits(t *testing.T) {
	now := time.Now()
	if delay := progressUpdateDelay(time.Time{}, now); delay != 0 {
		t.Fatalf("delay with no previous edit = %s, want 0", delay)
	}
	if delay := progressUpdateDelay(now.Add(-progressEditMinInterval), now); delay != 0 {
		t.Fatalf("delay after interval = %s, want 0", delay)
	}
	delay := progressUpdateDelay(now.Add(-500*time.Millisecond), now)
	if delay <= 0 || delay > progressEditMinInterval {
		t.Fatalf("delay after recent edit = %s, want within debounce interval", delay)
	}
}

func TestProgressMessageRecreatesDeletedDiscordMessage(t *testing.T) {
	unknownMessage := &discordgo.RESTError{
		Response: &http.Response{Status: "404 Not Found"},
		Message:  &discordgo.APIErrorMessage{Code: discordgo.ErrCodeUnknownMessage, Message: "Unknown Message"},
	}
	session := &recordingProgressSession{editErrors: []error{unknownMessage}}
	bot := &Bot{progressSession: session, progress: map[string]*processingIndicator{}}
	if err := bot.UpdateProgressMessage(context.Background(), "channel-1", "Thinking"); err != nil {
		t.Fatal(err)
	}
	bot.progressMu.Lock()
	bot.progress["channel-1"].lastEdit = time.Time{}
	bot.progressMu.Unlock()

	if err := bot.UpdateProgressMessage(context.Background(), "channel-1", "Reading files"); err != nil {
		t.Fatal(err)
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if len(session.sends) != 2 || session.sends[1] != "Reading files" {
		t.Fatalf("sends = %#v, want deleted progress message recreated", session.sends)
	}
	if len(session.edits) != 1 {
		t.Fatalf("edits = %#v, want one failed edit before recreation", session.edits)
	}
}

func TestProgressMessageDoesNotDuplicateOnTransientEditFailure(t *testing.T) {
	session := &recordingProgressSession{editErrors: []error{errors.New("temporary network error")}}
	bot := &Bot{progressSession: session, progress: map[string]*processingIndicator{}}
	if err := bot.UpdateProgressMessage(context.Background(), "channel-1", "Thinking"); err != nil {
		t.Fatal(err)
	}
	bot.progressMu.Lock()
	bot.progress["channel-1"].lastEdit = time.Time{}
	bot.progressMu.Unlock()

	err := bot.UpdateProgressMessage(context.Background(), "channel-1", "Reading files")
	if err == nil {
		t.Fatal("UpdateProgressMessage() error = nil, want transient edit error")
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if len(session.sends) != 1 {
		t.Fatalf("sends = %#v, transient edit failure must not create a duplicate message", session.sends)
	}
}

func TestUnknownDiscordMessageDetection(t *testing.T) {
	err := fmt.Errorf("wrapped: %w", &discordgo.RESTError{
		Response: &http.Response{Status: "404 Not Found"},
		Message:  &discordgo.APIErrorMessage{Code: discordgo.ErrCodeUnknownMessage},
	})
	if !isUnknownDiscordMessage(err) {
		t.Fatal("isUnknownDiscordMessage() = false, want true")
	}
	if isUnknownDiscordMessage(errors.New("network error")) {
		t.Fatal("isUnknownDiscordMessage() = true for transient error")
	}
}
