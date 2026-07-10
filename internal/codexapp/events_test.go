package codexapp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/nashory/agx/internal/agentstream"
)

func TestExtractErrorMessagePrefersDirectFields(t *testing.T) {
	cases := []struct {
		name   string
		params string
		want   string
	}{
		{"message", `{"message":"boom"}`, "boom"},
		{"detail", `{"detail":"disk full"}`, "disk full"},
		{"reason", `{"reason":"timed out"}`, "timed out"},
		{"nested error string", `{"error":"upstream refused"}`, "upstream refused"},
		{"nested error object", `{"error":{"message":"bad token"}}`, "bad token"},
		{"nested data object", `{"data":{"detail":"quota exceeded"}}`, "quota exceeded"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ExtractErrorMessage(json.RawMessage(tc.params)); got != tc.want {
				t.Fatalf("ExtractErrorMessage(%s) = %q, want %q", tc.params, got, tc.want)
			}
		})
	}
}

func TestExtractErrorMessageFallsBackToRawParams(t *testing.T) {
	params := `{"code":500,"trace":"panic at line 12"}`
	got := ExtractErrorMessage(json.RawMessage(params))
	if !strings.Contains(got, "panic at line 12") {
		t.Fatalf("ExtractErrorMessage(%s) = %q, want raw params fallback", params, got)
	}
}

func TestExtractErrorMessageEmptyForNoDetail(t *testing.T) {
	for _, params := range []string{"", "{}", "null", `{"message":"   "}`} {
		if got := ExtractErrorMessage(json.RawMessage(params)); got != "" {
			t.Fatalf("ExtractErrorMessage(%q) = %q, want empty", params, got)
		}
	}
}

func TestMapNotificationErrorUsesFallbackWhenNoDetail(t *testing.T) {
	event, ok, err := MapNotification(agentstream.TaskSummary{ID: "task-1"}, Notification{
		Method: NotifyError,
		Params: json.RawMessage(`{}`),
	})
	if err != nil || !ok {
		t.Fatalf("MapNotification ok=%v err=%v", ok, err)
	}
	if event.Kind != agentstream.EventError || event.Error != ErrorNoDetail {
		t.Fatalf("event = %#v, want error kind with fallback message", event)
	}
}

func TestMapNotificationErrorSurfacesDetail(t *testing.T) {
	event, ok, err := MapNotification(agentstream.TaskSummary{ID: "task-1"}, Notification{
		Method: NotifyError,
		Params: json.RawMessage(`{"message":"stream closed unexpectedly"}`),
	})
	if err != nil || !ok {
		t.Fatalf("MapNotification ok=%v err=%v", ok, err)
	}
	if event.Error != "stream closed unexpectedly" {
		t.Fatalf("event.Error = %q, want extracted detail", event.Error)
	}
}
