package desktop

import (
	"bytes"
	"log"
	"strings"
	"testing"
)

func TestDesktopDiagnosticLogRedactsSensitiveFields(t *testing.T) {
	var logs bytes.Buffer
	previousOutput := log.Writer()
	previousFlags := log.Flags()
	log.SetOutput(&logs)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(previousOutput)
		log.SetFlags(previousFlags)
	})

	logDesktopOperation("discord_connect", "bot_token", "super-secret-token", "guild", "guild-1")
	got := logs.String()
	if strings.Contains(got, "super-secret-token") {
		t.Fatalf("desktop diagnostic log leaked token: %q", got)
	}
	if !strings.Contains(got, `bot_token="[redacted]"`) || !strings.Contains(got, `guild="guild-1"`) {
		t.Fatalf("desktop diagnostic log = %q, want redacted token and non-sensitive guild", got)
	}
}
