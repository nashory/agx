//go:build !windows

package runtime

import (
	"io"
	"net"
	"net/http"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestNewClientHTTPConfiguresIdlePoolPosix(t *testing.T) {
	_, client := newClientHTTP(Paths{Socket: filepath.Join(t.TempDir(), "runtime.sock")})
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("client transport = %T, want *http.Transport", client.Transport)
	}
	if transport.IdleConnTimeout != clientIdleConnTimeout {
		t.Fatalf("IdleConnTimeout = %v, want %v", transport.IdleConnTimeout, clientIdleConnTimeout)
	}
	if transport.MaxIdleConns != clientMaxIdleConns {
		t.Fatalf("MaxIdleConns = %d, want %d", transport.MaxIdleConns, clientMaxIdleConns)
	}
	if transport.MaxIdleConnsPerHost != clientMaxIdleConnsPerHost {
		t.Fatalf("MaxIdleConnsPerHost = %d, want %d", transport.MaxIdleConnsPerHost, clientMaxIdleConnsPerHost)
	}
}

// TestUnixTransportReusesConnection proves the client keeps one keep-alive
// connection across many requests instead of leaking a socket per call.
func TestUnixTransportReusesConnection(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "runtime.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	var accepts int32
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})}
	go func() { _ = srv.Serve(countingListener{Listener: ln, accepts: &accepts}) }()
	t.Cleanup(func() { _ = srv.Close() })

	_, client := newClientHTTP(Paths{Socket: socket})
	for i := 0; i < 5; i++ {
		resp, err := client.Get("http://agx-runtime/ping")
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}
	if got := atomic.LoadInt32(&accepts); got != 1 {
		t.Fatalf("accepted %d connections, want 1 (keep-alive reuse)", got)
	}
}
