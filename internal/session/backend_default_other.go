//go:build !windows

package session

import "github.com/nashory/agx/internal/tmux"

// DefaultBackend returns the session backend for the current platform. On
// macOS, Linux, and other Unix platforms this is the tmux backend.
func DefaultBackend() Backend {
	return tmux.NewController()
}
