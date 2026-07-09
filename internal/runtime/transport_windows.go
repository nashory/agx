//go:build windows

package runtime

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Native Windows has no usable Unix socket, so the runtime serves its HTTP API
// over localhost TCP. Because any local process could reach a loopback port, the
// transport is authenticated: the server generates a random bearer token on
// startup, writes it alongside the chosen address to an endpoint file in the
// user-scoped config directory, and rejects every request without a matching
// token. The listener binds loopback only and never a LAN interface.

const endpointFile = "runtime.endpoint.json"

// runtimeEndpoint is the descriptor the server publishes and the client reads to
// locate and authenticate against the runtime.
type runtimeEndpoint struct {
	Address string `json:"address"`
	Token   string `json:"token"`
}

func endpointPath(paths Paths) string {
	return filepath.Join(paths.ConfigDir, endpointFile)
}

// bindListener starts the loopback TCP listener, generates the auth token, and
// publishes the endpoint descriptor for clients.
func (s *Service) bindListener() (net.Listener, error) {
	if err := os.MkdirAll(s.paths.ConfigDir, 0o700); err != nil {
		return nil, fmt.Errorf("create config dir: %w", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen runtime tcp: %w", err)
	}
	token, err := generateToken()
	if err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("generate runtime token: %w", err)
	}
	endpoint := runtimeEndpoint{Address: ln.Addr().String(), Token: token}
	if err := writeEndpoint(s.paths, endpoint); err != nil {
		_ = ln.Close()
		return nil, err
	}
	s.transportToken = token
	logRuntimeOperation("runtime_transport",
		"transport", "tcp",
		"address", endpoint.Address,
		"endpoint_file", endpointPath(s.paths),
	)
	return ln, nil
}

// cleanupListener removes the endpoint descriptor so a stopped runtime does not
// leave a stale token behind.
func (s *Service) cleanupListener() error {
	if err := os.Remove(endpointPath(s.paths)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// wrapTransportAuth rejects any request whose bearer token does not match the
// runtime's token, even on loopback.
func (s *Service) wrapTransportAuth(h http.Handler) http.Handler {
	expected := s.transportToken
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !validBearerToken(r.Header.Get("Authorization"), expected) {
			logRuntimeOperation("runtime_auth_rejected",
				"path", r.URL.Path,
				"remote", r.RemoteAddr,
			)
			writeErrorStatus(w, http.StatusUnauthorized, errors.New("runtime request is not authorized"))
			return
		}
		h.ServeHTTP(w, r)
	})
}

// newClientHTTP returns a client that dials the runtime over loopback TCP. The
// endpoint descriptor is read lazily on each request so a client constructed
// before the runtime starts still works once it is running, and so it picks up a
// restarted runtime's new address and token.
func newClientHTTP(paths Paths) (string, *http.Client) {
	return "http://agx-runtime", &http.Client{
		Transport: &windowsClientTransport{paths: paths, base: &http.Transport{}},
	}
}

// windowsClientTransport rewrites each request to the runtime's loopback address
// and attaches the bearer token before delegating to a standard TCP transport.
type windowsClientTransport struct {
	paths Paths
	base  *http.Transport
	mu    sync.Mutex
}

func (t *windowsClientTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	endpoint, err := t.readEndpoint()
	if err != nil {
		return nil, err
	}
	req.URL.Scheme = "http"
	req.URL.Host = endpoint.Address
	req.Host = endpoint.Address
	req.Header.Set("Authorization", "Bearer "+endpoint.Token)
	return t.base.RoundTrip(req)
}

func (t *windowsClientTransport) readEndpoint() (runtimeEndpoint, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return readEndpoint(t.paths)
}

func writeEndpoint(paths Paths, endpoint runtimeEndpoint) error {
	data, err := json.Marshal(endpoint)
	if err != nil {
		return fmt.Errorf("marshal runtime endpoint: %w", err)
	}
	if err := os.WriteFile(endpointPath(paths), data, 0o600); err != nil {
		return fmt.Errorf("write runtime endpoint: %w", err)
	}
	return nil
}

func readEndpoint(paths Paths) (runtimeEndpoint, error) {
	data, err := os.ReadFile(endpointPath(paths))
	if err != nil {
		return runtimeEndpoint{}, fmt.Errorf("read runtime endpoint: %w", err)
	}
	var endpoint runtimeEndpoint
	if err := json.Unmarshal(data, &endpoint); err != nil {
		return runtimeEndpoint{}, fmt.Errorf("parse runtime endpoint: %w", err)
	}
	if endpoint.Address == "" || endpoint.Token == "" {
		return runtimeEndpoint{}, errors.New("runtime endpoint is incomplete")
	}
	return endpoint, nil
}

func generateToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

// validBearerToken reports whether header carries the expected bearer token,
// using a constant-time comparison to avoid leaking the token via timing.
func validBearerToken(header, expected string) bool {
	if expected == "" {
		return false
	}
	token, ok := strings.CutPrefix(header, "Bearer ")
	if !ok {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(expected)) == 1
}
