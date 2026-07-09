package runtime

import (
	"context"
	"errors"
	"net/http"
	"testing"

	agxdiscord "github.com/nashory/agx/internal/discord"
	"github.com/nashory/agx/internal/session"
)

type testPartialSuccessError struct{}

func (testPartialSuccessError) Error() string {
	return "partial success"
}

func (testPartialSuccessError) PartialSuccess() bool {
	return true
}

func TestRuntimeErrorResponseCodes(t *testing.T) {
	tests := []struct {
		name          string
		status        int
		err           error
		wantCode      string
		wantRetryable bool
		wantPartial   bool
	}{
		{
			name:     "validation",
			status:   http.StatusBadRequest,
			err:      errors.New("invalid request"),
			wantCode: ErrorCodeValidation,
		},
		{
			name:     "not found",
			status:   http.StatusNotFound,
			err:      errors.New("missing"),
			wantCode: ErrorCodeNotFound,
		},
		{
			name:          "conflict",
			status:        http.StatusConflict,
			err:           errors.New("already active"),
			wantCode:      ErrorCodeConflict,
			wantRetryable: true,
		},
		{
			name:          "timeout",
			status:        http.StatusInternalServerError,
			err:           context.DeadlineExceeded,
			wantCode:      ErrorCodeTimeout,
			wantRetryable: true,
		},
		{
			name:          "sync in progress",
			status:        http.StatusInternalServerError,
			err:           agxdiscord.ErrSyncInProgress,
			wantCode:      ErrorCodeSyncInProgress,
			wantRetryable: true,
		},
		{
			name:     "discord owner conflict",
			status:   http.StatusInternalServerError,
			err:      agxdiscord.ErrGuildOwnerConflict,
			wantCode: ErrorCodeConflict,
		},
		{
			name:        "cleanup failed",
			status:      http.StatusInternalServerError,
			err:         session.TaskCleanupError{TaskID: "task-123", Err: errors.New("remove worktree")},
			wantCode:    ErrorCodeCleanupFailed,
			wantPartial: true,
		},
		{
			name:        "partial success",
			status:      http.StatusInternalServerError,
			err:         testPartialSuccessError{},
			wantCode:    ErrorCodePartialSuccess,
			wantPartial: true,
		},
		{
			name:     "internal fallback",
			status:   http.StatusInternalServerError,
			err:      errors.New("boom"),
			wantCode: ErrorCodeInternal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := runtimeErrorResponse(tt.status, tt.err)
			if got.Code != tt.wantCode || got.Retryable != tt.wantRetryable || got.PartialSuccess != tt.wantPartial {
				t.Fatalf("runtimeErrorResponse() = %#v, want code=%s retryable=%v partial=%v", got, tt.wantCode, tt.wantRetryable, tt.wantPartial)
			}
		})
	}
}
