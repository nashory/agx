package museapp

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"
)

func TestMuseServeEchoIntegration(t *testing.T) {
	if os.Getenv("AGX_MUSE_INTEGRATION") != "1" {
		t.Skip("set AGX_MUSE_INTEGRATION=1 to test the installed Muse MSP host")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	client, err := Start(ctx, Options{Provider: "echo", Ephemeral: true})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	sessionID := NewCommandID()
	started, err := client.SessionStart(ctx, sessionID, t.TempDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	turn, err := client.TurnStart(ctx, sessionID, "hello from AGX", "queue")
	if err != nil {
		t.Fatal(err)
	}
	if turn.TurnID == "" {
		t.Fatal("Muse returned an empty turn id")
	}
	seenMessage := false
	for {
		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for Muse turn: %v", ctx.Err())
		case notification, ok := <-client.Events():
			if !ok {
				t.Fatalf("Muse event stream closed: %s", client.RecentStderr())
			}
			if notification.Method == NotifyItemCompleted {
				var params itemParams
				if json.Unmarshal(notification.Params, &params) == nil && params.Item.Kind == "agentMessage" {
					seenMessage = params.Item.Text != ""
				}
			}
			if notification.Method == NotifyTurnCompleted {
				if !seenMessage {
					t.Fatal("turn completed without an agent message")
				}
				page, err := client.ViewPage(ctx, sessionID, started.ViewCursor, 1000)
				if err != nil && !IsMethodNotFound(err) {
					t.Fatalf("page session view: %v", err)
				}
				if err == nil && len(page.Events) == 0 {
					t.Fatal("session view page was empty")
				}
				if IsMethodNotFound(err) {
					if err := client.ViewUnsubscribe(ctx, sessionID); err != nil {
						t.Fatalf("unsubscribe session view: %v", err)
					}
					if err := client.ViewSubscribe(ctx, sessionID, started.ViewCursor); err != nil {
						if !IsMethodNotFound(err) {
							t.Fatalf("resubscribe session view: %v", err)
						}
					}
				}
				return
			}
		}
	}
}
