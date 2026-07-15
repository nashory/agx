//go:build !windows

package runtime

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
)

// The runtime speaks its HTTP API over a Unix-domain socket on every non-Windows
// platform. The socket's 0600 permissions restrict access to the owning user, so
// no additional authentication is layered on top. Native Windows uses an
// authenticated localhost TCP transport instead (see transport_windows.go).

// bindListener creates and secures the Unix socket the runtime serves on.
func (s *Service) bindListener() (net.Listener, error) {
	if err := os.Remove(s.paths.Socket); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("remove stale socket: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.paths.Socket), 0o700); err != nil {
		return nil, fmt.Errorf("create socket dir: %w", err)
	}
	ln, err := net.Listen("unix", s.paths.Socket)
	if err != nil {
		return nil, fmt.Errorf("listen runtime socket: %w", err)
	}
	if err := os.Chmod(s.paths.Socket, 0o600); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("chmod runtime socket: %w", err)
	}
	return ln, nil
}

// cleanupListener removes the Unix socket file. It is safe to call repeatedly and
// when the socket was never created.
func (s *Service) cleanupListener() error {
	if err := os.Remove(s.paths.Socket); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// wrapTransportAuth is a no-op: the socket permissions already gate access.
func (s *Service) wrapTransportAuth(h http.Handler) http.Handler {
	return h
}

// newClientHTTP returns the base URL and HTTP client used to reach the runtime
// over its Unix socket.
func newClientHTTP(paths Paths) (string, *http.Client) {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", paths.Socket)
		},
		MaxIdleConns:        clientMaxIdleConns,
		MaxIdleConnsPerHost: clientMaxIdleConnsPerHost,
		IdleConnTimeout:     clientIdleConnTimeout,
	}
	return "http://agx-runtime", &http.Client{Transport: transport}
}
