package desktop

import "testing"

// TestRuntimeClientReused verifies the desktop constructs the runtime client
// once and reuses it for every API call. Reuse keeps a single HTTP transport and
// connection pool alive, which is what prevents the loopback socket leak that
// exhausted WinSock buffers (WSAENOBUFS) when a fresh client was built per call.
func TestRuntimeClientReused(t *testing.T) {
	var calls int
	fake := &fakeRuntimeClient{}
	previous := newRuntimeClient
	newRuntimeClient = func() runtimeClient {
		calls++
		return fake
	}
	t.Cleanup(func() { newRuntimeClient = previous })

	app := NewAppWithStore(nil)
	first := app.runtimeClient()
	second := app.runtimeClient()

	if calls != 1 {
		t.Fatalf("newRuntimeClient called %d times, want 1 (client must be reused)", calls)
	}
	if first != second {
		t.Fatal("runtimeClient returned different instances; expected a single shared client")
	}
}
