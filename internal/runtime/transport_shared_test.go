package runtime

import (
	"net"
	"net/http"
	"sync/atomic"
	"testing"
)

// countingListener counts accepted connections so tests can assert that
// keep-alive reuse dials the runtime only once across many requests.
type countingListener struct {
	net.Listener
	accepts *int32
}

func (l countingListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err == nil {
		atomic.AddInt32(l.accepts, 1)
	}
	return conn, err
}

func TestNewAPIServerSetsIdleTimeouts(t *testing.T) {
	srv := newAPIServer(http.NewServeMux())
	if srv.IdleTimeout != serverIdleTimeout {
		t.Fatalf("IdleTimeout = %v, want %v", srv.IdleTimeout, serverIdleTimeout)
	}
	if srv.ReadHeaderTimeout != serverReadHeaderTimeout {
		t.Fatalf("ReadHeaderTimeout = %v, want %v", srv.ReadHeaderTimeout, serverReadHeaderTimeout)
	}
	// WriteTimeout must stay unset: it would abort the long-lived SSE responses
	// served by /v1/events and /v1/tasks/{id}/stream.
	if srv.WriteTimeout != 0 {
		t.Fatalf("WriteTimeout = %v, want 0 so streaming responses are not cut off", srv.WriteTimeout)
	}
}
