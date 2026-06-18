package runtime

import (
	"bytes"
	"log"
	"net/http"
	"strings"
	"testing"
)

func TestRuntimeRequestLoggingCapturesErrorDetails(t *testing.T) {
	service, _ := newRuntimeAPITestService(t)
	var logs bytes.Buffer
	previousOutput := log.Writer()
	previousFlags := log.Flags()
	log.SetOutput(&logs)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(previousOutput)
		log.SetFlags(previousFlags)
	})

	status, payload := runtimeAPIErrorResponse(t, service, http.MethodPost, "/v1/tasks", createTaskRequest{Title: "missing project"})
	if status != http.StatusBadRequest || payload.Code != ErrorCodeValidation {
		t.Fatalf("create task error = (%d, %#v), want validation bad request", status, payload)
	}
	got := logs.String()
	for _, want := range []string{
		`operation="task_create"`,
		`method="POST"`,
		`path="/v1/tasks"`,
		`status=400`,
		`error_code="validation_error"`,
		`error="project id is required"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("runtime request log %q missing %s", got, want)
		}
	}
}

func TestDiagnosticLogRedactsSensitiveFields(t *testing.T) {
	var logs bytes.Buffer
	previousOutput := log.Writer()
	previousFlags := log.Flags()
	log.SetOutput(&logs)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(previousOutput)
		log.SetFlags(previousFlags)
	})

	logRuntimeOperation("discord_connect", "bot_token", "super-secret-token", "guild", "guild-1")
	got := logs.String()
	if strings.Contains(got, "super-secret-token") {
		t.Fatalf("diagnostic log leaked token: %q", got)
	}
	if !strings.Contains(got, `bot_token="[redacted]"`) || !strings.Contains(got, `guild="guild-1"`) {
		t.Fatalf("diagnostic log = %q, want redacted token and non-sensitive guild", got)
	}
}
