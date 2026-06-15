package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/nashory/agx/internal/db"
	agxdiscord "github.com/nashory/agx/internal/discord"
	"github.com/nashory/agx/internal/session"
)

const (
	ErrorCodeInternal       = "internal_error"
	ErrorCodeValidation     = "validation_error"
	ErrorCodeNotFound       = "not_found"
	ErrorCodeConflict       = "conflict"
	ErrorCodeTimeout        = "timeout"
	ErrorCodeCleanupFailed  = "cleanup_failed"
	ErrorCodePartialSuccess = "partial_success"
	ErrorCodeSyncInProgress = "sync_in_progress"
)

type errorResponse struct {
	Error          string `json:"error"`
	Code           string `json:"code"`
	Retryable      bool   `json:"retryable,omitempty"`
	PartialSuccess bool   `json:"partialSuccess,omitempty"`
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func writeError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	if err == db.ErrProjectNotFound || err == db.ErrTaskNotFound {
		status = http.StatusNotFound
	}
	writeErrorStatus(w, status, err)
}

func writeErrorStatus(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(runtimeErrorResponse(status, err))
}

func runtimeErrorResponse(status int, err error) errorResponse {
	response := errorResponse{
		Error: err.Error(),
		Code:  ErrorCodeInternal,
	}
	switch {
	case errors.As(err, new(session.TaskCleanupError)):
		response.Code = ErrorCodeCleanupFailed
		response.PartialSuccess = true
	case isPartialSuccessRuntimeError(err):
		response.Code = ErrorCodePartialSuccess
		response.PartialSuccess = true
	case errors.Is(err, agxdiscord.ErrSyncInProgress):
		response.Code = ErrorCodeSyncInProgress
		response.Retryable = true
	case errors.Is(err, context.DeadlineExceeded):
		response.Code = ErrorCodeTimeout
		response.Retryable = true
	case status == http.StatusBadRequest:
		response.Code = ErrorCodeValidation
	case status == http.StatusNotFound:
		response.Code = ErrorCodeNotFound
	case status == http.StatusConflict:
		response.Code = ErrorCodeConflict
		response.Retryable = true
	}
	return response
}

func isPartialSuccessRuntimeError(err error) bool {
	var partial interface{ PartialSuccess() bool }
	return errors.As(err, &partial) && partial.PartialSuccess()
}
