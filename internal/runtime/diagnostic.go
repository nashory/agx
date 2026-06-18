package runtime

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/nashory/agx/internal/display"
)

type diagnosticResponseWriter struct {
	http.ResponseWriter
	status    int
	errorBody []byte
}

func (w *diagnosticResponseWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *diagnosticResponseWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if w.status >= http.StatusBadRequest && len(w.errorBody) < 4096 {
		remaining := 4096 - len(w.errorBody)
		if len(data) < remaining {
			remaining = len(data)
		}
		w.errorBody = append(w.errorBody, data[:remaining]...)
	}
	return w.ResponseWriter.Write(data)
}

func (w *diagnosticResponseWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (s *Service) withRequestLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		rw := &diagnosticResponseWriter{ResponseWriter: w}
		next.ServeHTTP(rw, r)
		status := rw.status
		if status == 0 {
			status = http.StatusOK
		}
		if !shouldLogRuntimeRequest(r.Method, r.URL.Path, status) {
			return
		}
		code, message := runtimeRequestError(status, rw.errorBody)
		logRuntimeOperation(runtimeRequestOperation(r.Method, r.URL.Path),
			"request_id", fmt.Sprintf("%d", s.requestSeq.Add(1)),
			"method", r.Method,
			"path", r.URL.Path,
			"status", status,
			"elapsed_ms", time.Since(started).Milliseconds(),
			"error_code", code,
			"error", message,
		)
	})
}

func shouldLogRuntimeRequest(method, path string, status int) bool {
	if path == "/v1/events" || strings.HasSuffix(path, "/stream") {
		return false
	}
	if status >= http.StatusBadRequest {
		return true
	}
	return method != http.MethodGet
}

func runtimeRequestOperation(method, path string) string {
	trimmed := strings.Trim(path, "/")
	parts := strings.Split(trimmed, "/")
	if len(parts) < 2 || parts[0] != "v1" {
		return "runtime_request"
	}
	switch parts[1] {
	case "tasks":
		return taskRequestOperation(method, parts)
	case "projects":
		return projectRequestOperation(method, parts)
	case "discord":
		return discordRequestOperation(method, parts)
	case "config":
		if method == http.MethodPatch {
			return "config_update"
		}
	case "shutdown":
		return "runtime_shutdown"
	}
	return "runtime_request"
}

func taskRequestOperation(method string, parts []string) string {
	if len(parts) == 2 && method == http.MethodPost {
		return "task_create"
	}
	if len(parts) < 4 {
		if method == http.MethodDelete {
			return "task_delete"
		}
		return "task_request"
	}
	switch parts[3] {
	case "run":
		return "task_run"
	case "stop":
		return "task_stop"
	case "interrupt":
		return "task_interrupt"
	case "message":
		return "task_message"
	case "input":
		return "task_input"
	case "resize":
		return "task_resize"
	case "record-input":
		return "task_record_input"
	}
	if method == http.MethodPatch {
		return "task_update"
	}
	if method == http.MethodDelete {
		return "task_delete"
	}
	return "task_request"
}

func projectRequestOperation(method string, parts []string) string {
	if len(parts) == 2 && method == http.MethodPost {
		return "project_create"
	}
	if method == http.MethodPatch {
		return "project_update"
	}
	if method == http.MethodDelete {
		return "project_delete"
	}
	if len(parts) >= 4 && parts[3] == "grant-access" {
		return "project_grant_access"
	}
	return "project_request"
}

func discordRequestOperation(method string, parts []string) string {
	if len(parts) < 3 {
		return "discord_request"
	}
	switch parts[2] {
	case "connect":
		return "discord_connect"
	case "disconnect":
		return "discord_disconnect"
	case "soft-sync":
		return "discord_soft_sync"
	case "hard-sync":
		return "discord_hard_sync"
	case "invite-url":
		return "discord_invite_url"
	case "tasks":
		if method == http.MethodPost && len(parts) >= 5 && parts[4] == "sync" {
			return "discord_task_sync_manual"
		}
	}
	return "discord_request"
}

func runtimeRequestErrorCode(status int) string {
	if status < http.StatusBadRequest {
		return ""
	}
	switch status {
	case http.StatusBadRequest:
		return ErrorCodeValidation
	case http.StatusNotFound:
		return ErrorCodeNotFound
	case http.StatusConflict:
		return ErrorCodeConflict
	case http.StatusGatewayTimeout, http.StatusRequestTimeout:
		return ErrorCodeTimeout
	default:
		return ErrorCodeInternal
	}
}

func runtimeRequestError(status int, body []byte) (string, string) {
	if status < http.StatusBadRequest {
		return "", ""
	}
	code := runtimeRequestErrorCode(status)
	var payload errorResponse
	if err := json.Unmarshal(body, &payload); err == nil {
		if payload.Code != "" {
			code = payload.Code
		}
		return code, payload.Error
	}
	return code, ""
}

func logRuntimeOperation(operation string, fields ...any) {
	log.Printf("operation=%q%s", operation, formatDiagnosticFields(fields...))
}

func formatDiagnosticFields(fields ...any) string {
	if len(fields) == 0 {
		return ""
	}
	var b strings.Builder
	for i := 0; i+1 < len(fields); i += 2 {
		key, ok := fields[i].(string)
		if !ok || key == "" {
			continue
		}
		value := sanitizeDiagnosticValue(key, fields[i+1])
		if value == "" {
			continue
		}
		b.WriteByte(' ')
		b.WriteString(key)
		b.WriteByte('=')
		b.WriteString(value)
	}
	return b.String()
}

func sanitizeDiagnosticValue(key string, value any) string {
	if value == nil {
		return ""
	}
	lower := strings.ToLower(key)
	if strings.Contains(lower, "token") || strings.Contains(lower, "authorization") || strings.Contains(lower, "secret") {
		return strconv.Quote("[redacted]")
	}
	switch typed := value.(type) {
	case string:
		if typed == "" {
			return ""
		}
		return strconv.Quote(typed)
	case fmt.Stringer:
		return strconv.Quote(typed.String())
	case bool:
		return strconv.FormatBool(typed)
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	case time.Duration:
		return strconv.FormatInt(typed.Milliseconds(), 10)
	case error:
		return strconv.Quote(typed.Error())
	default:
		return strconv.Quote(fmt.Sprint(typed))
	}
}

func shortDiagnosticID(id string) string {
	if strings.TrimSpace(id) == "" {
		return ""
	}
	return display.ShortID(id)
}
