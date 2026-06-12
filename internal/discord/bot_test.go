package discord

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

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
