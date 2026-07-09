//go:build windows

package session

// DefaultBackend returns the session backend for the current platform. On native
// Windows this is the ConPTY backend.
func DefaultBackend() Backend {
	return newConptyBackend()
}
