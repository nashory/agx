//go:build windows

package runtime

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestValidBearerToken(t *testing.T) {
	cases := []struct {
		name     string
		header   string
		expected string
		want     bool
	}{
		{"match", "Bearer secret", "secret", true},
		{"wrong token", "Bearer nope", "secret", false},
		{"missing prefix", "secret", "secret", false},
		{"empty header", "", "secret", false},
		{"empty token in header", "Bearer ", "secret", false},
		{"empty expected", "Bearer secret", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := validBearerToken(tc.header, tc.expected); got != tc.want {
				t.Fatalf("validBearerToken(%q, %q) = %v, want %v", tc.header, tc.expected, got, tc.want)
			}
		})
	}
}

func TestEndpointRoundTrip(t *testing.T) {
	paths := Paths{ConfigDir: t.TempDir()}
	want := runtimeEndpoint{Address: "127.0.0.1:54321", Token: "deadbeef"}
	if err := writeEndpoint(paths, want); err != nil {
		t.Fatalf("writeEndpoint() error = %v", err)
	}
	got, err := readEndpoint(paths)
	if err != nil {
		t.Fatalf("readEndpoint() error = %v", err)
	}
	if got != want {
		t.Fatalf("readEndpoint() = %#v, want %#v", got, want)
	}
}

func TestReadEndpointRejectsMissingAndIncomplete(t *testing.T) {
	paths := Paths{ConfigDir: t.TempDir()}
	if _, err := readEndpoint(paths); err == nil {
		t.Fatal("readEndpoint() on missing file error = nil, want error")
	}
	if err := writeEndpoint(paths, runtimeEndpoint{Address: "127.0.0.1:1"}); err != nil {
		t.Fatalf("writeEndpoint() error = %v", err)
	}
	if _, err := readEndpoint(paths); err == nil {
		t.Fatal("readEndpoint() on tokenless endpoint error = nil, want error")
	}
}

func TestWrapTransportAuthRejectsUnauthenticated(t *testing.T) {
	service := &Service{transportToken: "secret"}
	handler := service.wrapTransportAuth(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	authorized := httpRecorderRequest(t, handler, "Bearer secret")
	if authorized != http.StatusOK {
		t.Fatalf("authorized request status = %d, want 200", authorized)
	}
	unauthorized := httpRecorderRequest(t, handler, "")
	if unauthorized != http.StatusUnauthorized {
		t.Fatalf("unauthorized request status = %d, want 401", unauthorized)
	}
	wrong := httpRecorderRequest(t, handler, "Bearer nope")
	if wrong != http.StatusUnauthorized {
		t.Fatalf("wrong-token request status = %d, want 401", wrong)
	}
}

// TestWindowsTransportEndToEnd starts a real runtime over loopback TCP and drives
// it through the authenticated client, proving the native Windows transport works
// end to end.
func TestWindowsTransportEndToEnd(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("AGX_CONFIG_DIR", configDir)
	service := NewService("test-version")
	errCh := make(chan error, 1)
	go func() { errCh <- service.Start(context.Background()) }()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = NewClient().Shutdown(ctx)
		<-errCh
		if _, err := readEndpoint(DefaultPaths()); err == nil {
			t.Fatal("runtime endpoint still exists after shutdown")
		}
	}()

	waitForRuntimeReady(t)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	status, err := NewClient().Status(ctx)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if !status.Running || status.Version != "test-version" {
		t.Fatalf("Status() = %#v, want running test-version", status)
	}

	project, err := NewClient().CreateProject(ctx, t.TempDir(), "Windows Transport", nil, nil)
	if err != nil {
		t.Fatalf("CreateProject() error = %v", err)
	}
	projects, err := NewClient().ListProjects(ctx)
	if err != nil {
		t.Fatalf("ListProjects() error = %v", err)
	}
	if len(projects) != 1 || projects[0].ID != project.ID {
		t.Fatalf("ListProjects() = %#v, want created project", projects)
	}
}

// TestWindowsTransportRejectsUnauthenticatedClient confirms a raw request to the
// loopback port without the token is refused.
func TestWindowsTransportRejectsUnauthenticatedClient(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("AGX_CONFIG_DIR", configDir)
	service := NewService("test-version")
	errCh := make(chan error, 1)
	go func() { errCh <- service.Start(context.Background()) }()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = NewClient().Shutdown(ctx)
		<-errCh
	}()

	waitForRuntimeReady(t)

	endpoint, err := readEndpoint(DefaultPaths())
	if err != nil {
		t.Fatalf("readEndpoint() error = %v", err)
	}
	resp, err := http.Get("http://" + endpoint.Address + "/v1/status")
	if err != nil {
		t.Fatalf("raw GET error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("raw GET status = %d, want 401", resp.StatusCode)
	}
}

func httpRecorderRequest(t *testing.T, handler http.Handler, authorization string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, "http://agx-runtime/v1/status", nil)
	if err != nil {
		t.Fatal(err)
	}
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	rec := newStatusRecorder()
	handler.ServeHTTP(rec, req)
	return rec.status
}

// statusRecorder is a minimal http.ResponseWriter that records the status code.
type statusRecorder struct {
	header http.Header
	status int
}

func newStatusRecorder() *statusRecorder {
	return &statusRecorder{header: http.Header{}, status: http.StatusOK}
}

func (r *statusRecorder) Header() http.Header         { return r.header }
func (r *statusRecorder) Write(b []byte) (int, error) { return len(b), nil }
func (r *statusRecorder) WriteHeader(status int)      { r.status = status }

func waitForRuntimeReady(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
		_, err := NewClient().Status(ctx)
		cancel()
		if err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("runtime did not become ready over TCP transport")
}
