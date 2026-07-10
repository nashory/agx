package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/nashory/agx/internal/agentstream"
)

func TestClientCapturesRecentStderr(t *testing.T) {
	client := &Client{}
	client.captureStderr(strings.NewReader("first line\n\nsecond line\n"))
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(client.RecentStderr(), "second line") {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	got := client.RecentStderr()
	if !strings.Contains(got, "first line") || !strings.Contains(got, "second line") {
		t.Fatalf("RecentStderr() = %q, want captured stderr lines", got)
	}
	if strings.Contains(got, "\n\n") {
		t.Fatalf("RecentStderr() = %q, want blank lines skipped", got)
	}
}

func TestNilClientRecentStderrIsEmpty(t *testing.T) {
	if got := (*Client)(nil).RecentStderr(); got != "" {
		t.Fatalf("nil RecentStderr() = %q, want empty", got)
	}
}

func TestClientCallSendsRequestAndDecodesResponse(t *testing.T) {
	server, clientConn := net.Pipe()
	defer server.Close()
	defer clientConn.Close()
	client := NewClient(clientConn, clientConn, clientConn)

	done := make(chan error, 1)
	go func() {
		var request map[string]any
		if err := json.NewDecoder(server).Decode(&request); err != nil {
			done <- err
			return
		}
		if request["method"] != MethodInitialize {
			t.Errorf("method = %v, want %s", request["method"], MethodInitialize)
		}
		_, err := server.Write([]byte(`{"id":1,"result":{"userAgent":"codex-test","codexHome":"/tmp/codex"}}` + "\n"))
		done <- err
	}()

	response, err := client.Initialize(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if response.UserAgent != "codex-test" || response.CodexHome != "/tmp/codex" {
		t.Fatalf("response = %#v", response)
	}
}

func TestClientPublishesNotifications(t *testing.T) {
	server, clientConn := net.Pipe()
	defer server.Close()
	defer clientConn.Close()
	client := NewClient(clientConn, clientConn, clientConn)

	_, err := server.Write([]byte(`{"method":"item/agentMessage/delta","params":{"delta":"hello"}}` + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	notification := <-client.Events()
	if notification.Method != NotifyAgentMessageDelta || !strings.Contains(string(notification.Params), "hello") {
		t.Fatalf("notification = %#v", notification)
	}
}

func TestClientTreatsServerRequestAsNotification(t *testing.T) {
	server, clientConn := net.Pipe()
	defer server.Close()
	defer clientConn.Close()
	client := NewClient(clientConn, clientConn, clientConn)

	_, err := server.Write([]byte(`{"id":"approval-1","method":"item/commandExecution/requestApproval","params":{"threadId":"thread-1"}}` + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	notification := <-client.Events()
	if notification.Method != "item/commandExecution/requestApproval" || notification.RequestID != "approval-1" {
		t.Fatalf("notification = %#v", notification)
	}
}

func TestClientDoesNotBlockResponsesWhenNotificationBufferIsFull(t *testing.T) {
	server, clientConn := net.Pipe()
	defer server.Close()
	defer clientConn.Close()
	client := NewClient(clientConn, clientConn, clientConn)

	done := make(chan error, 1)
	go func() {
		for i := 0; i < cap(client.events)+10; i++ {
			if _, err := server.Write([]byte(`{"method":"warning","params":{"message":"x"}}` + "\n")); err != nil {
				done <- err
				return
			}
		}
		var request map[string]any
		if err := json.NewDecoder(server).Decode(&request); err != nil {
			done <- err
			return
		}
		_, err := server.Write([]byte(`{"id":1,"result":{"userAgent":"codex-test","codexHome":"/tmp/codex"}}` + "\n"))
		done <- err
	}()

	if _, err := client.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestAppServerArgsDisableOSXSandboxOnDarwin(t *testing.T) {
	args := appServerArgs()
	if runtime.GOOS == "darwin" {
		want := []string{"--dangerously-disable-osx-sandbox", "app-server", "--listen", "stdio://"}
		if strings.Join(args, "\x00") != strings.Join(want, "\x00") {
			t.Fatalf("appServerArgs() = %#v, want %#v", args, want)
		}
		return
	}
	if len(args) > 0 && args[0] == "--dangerously-disable-osx-sandbox" {
		t.Fatalf("appServerArgs() = %#v, did not expect macOS launcher flag on %s", args, runtime.GOOS)
	}
}

func TestThreadStartSendsDangerFullAccessSandboxPolicy(t *testing.T) {
	server, clientConn := net.Pipe()
	defer server.Close()
	defer clientConn.Close()
	client := NewClient(clientConn, clientConn, clientConn)

	done := make(chan error, 1)
	go func() {
		var request map[string]any
		if err := json.NewDecoder(server).Decode(&request); err != nil {
			done <- err
			return
		}
		if request["method"] != MethodThreadStart {
			t.Errorf("method = %v, want %s", request["method"], MethodThreadStart)
		}
		assertDangerFullAccessPolicy(t, request)
		_, err := server.Write([]byte(`{"id":1,"result":{"thread":{"id":"thread-1","cwd":"/repo"}}}` + "\n"))
		done <- err
	}()

	if _, err := client.ThreadStart(context.Background(), "/repo", true); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestThreadStartRetriesWithoutOverrideWhenManagedConfigRejects(t *testing.T) {
	server, clientConn := net.Pipe()
	defer server.Close()
	defer clientConn.Close()
	client := NewClient(clientConn, clientConn, clientConn)

	done := make(chan error, 1)
	go func() {
		dec := json.NewDecoder(server)
		var first map[string]any
		if err := dec.Decode(&first); err != nil {
			done <- err
			return
		}
		assertDangerFullAccessPolicy(t, first) // first attempt still carries the override
		if _, err := server.Write([]byte(`{"id":1,"error":{"code":-32600,"message":"invalid thread settings override: invalid value for approval_policy: Never is not in the allowed set [UnlessTrusted]"}}` + "\n")); err != nil {
			done <- err
			return
		}
		var second map[string]any
		if err := dec.Decode(&second); err != nil {
			done <- err
			return
		}
		params, _ := second["params"].(map[string]any)
		if _, ok := params["approvalPolicy"]; ok {
			t.Errorf("retry still sent approvalPolicy: %#v", params)
		}
		if _, ok := params["sandboxPolicy"]; ok {
			t.Errorf("retry still sent sandboxPolicy: %#v", params)
		}
		_, err := server.Write([]byte(`{"id":2,"result":{"thread":{"id":"thread-1","cwd":"/repo"}}}` + "\n"))
		done <- err
	}()

	if _, err := client.ThreadStart(context.Background(), "/repo", true); err != nil {
		t.Fatalf("ThreadStart() error = %v, want success after managed-config retry", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestTurnStartSendsDangerFullAccessSandboxPolicy(t *testing.T) {
	server, clientConn := net.Pipe()
	defer server.Close()
	defer clientConn.Close()
	client := NewClient(clientConn, clientConn, clientConn)

	done := make(chan error, 1)
	go func() {
		var request map[string]any
		if err := json.NewDecoder(server).Decode(&request); err != nil {
			done <- err
			return
		}
		if request["method"] != MethodTurnStart {
			t.Errorf("method = %v, want %s", request["method"], MethodTurnStart)
		}
		assertDangerFullAccessPolicy(t, request)
		_, err := server.Write([]byte(`{"id":1,"result":{"turn":{"id":"turn-1","status":"running"}}}` + "\n"))
		done <- err
	}()

	if _, err := client.TurnStart(context.Background(), "thread-1", "touch file", "/repo", true); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestThreadResumeTurnSteerAndInterruptMethods(t *testing.T) {
	tests := []struct {
		name       string
		call       func(context.Context, *Client) error
		wantMethod string
		wantParam  string
		response   string
	}{
		{
			name: "thread resume",
			call: func(ctx context.Context, client *Client) error {
				_, err := client.ThreadResume(ctx, "thread-1")
				return err
			},
			wantMethod: MethodThreadResume,
			wantParam:  `"threadId":"thread-1"`,
			response:   `{"id":1,"result":{"thread":{"id":"thread-1","cwd":"/repo"}}}`,
		},
		{
			name: "turn steer",
			call: func(ctx context.Context, client *Client) error {
				_, err := client.TurnSteer(ctx, "thread-1", "turn-1", "keep going")
				return err
			},
			wantMethod: MethodTurnSteer,
			wantParam:  `"text":"keep going"`,
			response:   `{"id":1,"result":null}`,
		},
		{
			name: "turn interrupt",
			call: func(ctx context.Context, client *Client) error {
				return client.TurnInterrupt(ctx, "thread-1", "turn-1")
			},
			wantMethod: MethodTurnInterrupt,
			wantParam:  `"turnId":"turn-1"`,
			response:   `{"id":1,"result":null}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server, clientConn := net.Pipe()
			defer server.Close()
			defer clientConn.Close()
			client := NewClient(clientConn, clientConn, clientConn)
			done := make(chan error, 1)
			go func() {
				var raw json.RawMessage
				if err := json.NewDecoder(server).Decode(&raw); err != nil {
					done <- err
					return
				}
				text := string(raw)
				if !strings.Contains(text, `"method":"`+test.wantMethod+`"`) || !strings.Contains(text, test.wantParam) {
					t.Errorf("request = %s, want method %s and param %s", text, test.wantMethod, test.wantParam)
				}
				_, err := server.Write([]byte(test.response + "\n"))
				done <- err
			}()
			if err := test.call(context.Background(), client); err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestClientHandlesErrorResponseAndInvalidIDs(t *testing.T) {
	server, clientConn := net.Pipe()
	defer server.Close()
	defer clientConn.Close()
	client := NewClient(clientConn, clientConn, clientConn)
	done := make(chan error, 1)
	go func() {
		var request map[string]any
		if err := json.NewDecoder(server).Decode(&request); err != nil {
			done <- err
			return
		}
		_, err := server.Write([]byte(`{"id":1,"error":{"code":400,"message":"bad request"}}` + "\n"))
		done <- err
	}()
	if err := client.Call(context.Background(), "bad/method", nil, nil); err == nil || !strings.Contains(err.Error(), "bad request") {
		t.Fatalf("Call() error = %v, want RPC error", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	if got, err := parseID(json.RawMessage(`"42"`)); err != nil || got != 42 {
		t.Fatalf("parseID string = (%d, %v), want 42", got, err)
	}
	if _, err := parseID(json.RawMessage(`{}`)); err == nil {
		t.Fatal("parseID object error = nil, want error")
	}
}

func TestClientCloseAndCallFailures(t *testing.T) {
	closed := false
	client := NewClient(strings.NewReader(""), failingWriter{}, closerFunc(func() error {
		closed = true
		return nil
	}))
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if !closed {
		t.Fatal("Close did not call closer")
	}
	if err := (*Client)(nil).Close(); err != nil {
		t.Fatalf("nil Close() error = %v, want nil", err)
	}
	if err := NewClient(strings.NewReader(""), failingWriter{}, nil).Close(); err != nil {
		t.Fatalf("Close without closer error = %v, want nil", err)
	}
	if err := client.Call(context.Background(), " ", nil, nil); err == nil || !strings.Contains(err.Error(), "method is required") {
		t.Fatalf("blank method error = %v, want method required", err)
	}
	if err := client.Call(context.Background(), "method", make(chan int), nil); err == nil {
		t.Fatal("Call with unmarshalable params error = nil, want error")
	}
	writerFailureClient := &Client{writer: failingWriter{}, pending: map[int64]chan response{}, events: make(chan Notification), done: make(chan struct{})}
	if err := writerFailureClient.Call(context.Background(), "method", nil, nil); err == nil || !errors.Is(err, errFailingWriter) {
		t.Fatalf("writer failure error = %v, want errFailingWriter", err)
	}
}

func assertDangerFullAccessPolicy(t *testing.T, request map[string]any) {
	t.Helper()
	params, ok := request["params"].(map[string]any)
	if !ok {
		t.Fatalf("params = %#v, want object", request["params"])
	}
	if params["approvalPolicy"] != "never" {
		t.Fatalf("approvalPolicy = %v, want never", params["approvalPolicy"])
	}
	sandbox, ok := params["sandboxPolicy"].(map[string]any)
	if !ok {
		t.Fatalf("sandboxPolicy = %#v, want object", params["sandboxPolicy"])
	}
	if sandbox["type"] != "dangerFullAccess" {
		t.Fatalf("sandboxPolicy.type = %v, want dangerFullAccess", sandbox["type"])
	}
}

func TestMapNotificationMapsAgentMessageDelta(t *testing.T) {
	event, ok, err := MapNotification(testTask(), Notification{
		Method: NotifyAgentMessageDelta,
		Params: json.RawMessage(`{"threadId":"thread-1","turnId":"turn-1","itemId":"item-1","delta":"hello"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected mapped event")
	}
	if event.Kind != "assistant_delta" || event.Text != "hello" || event.TurnID != "turn-1" {
		t.Fatalf("event = %#v", event)
	}
}

func TestMapNotificationMapsCommandOutputDelta(t *testing.T) {
	event, ok, err := MapNotification(testTask(), Notification{
		Method: NotifyCommandOutput,
		Params: json.RawMessage(`{"threadId":"thread-1","turnId":"turn-1","itemId":"item-1","delta":"building"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected mapped event")
	}
	if event.Kind != agentstream.EventCommandOutputDelta || event.Command == nil || event.Command.Stdout != "building" {
		t.Fatalf("event = %#v", event)
	}
}

func TestMapNotificationMapsCompletedAgentMessage(t *testing.T) {
	event, ok, err := MapNotification(testTask(), Notification{
		Method: NotifyItemCompleted,
		Params: json.RawMessage(`{"threadId":"thread-1","turnId":"turn-1","item":{"type":"agentMessage","id":"item-1","text":"final answer"}}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected mapped event")
	}
	if event.Kind != agentstream.EventAssistantMessage || event.Text != "final answer" || event.TurnID != "turn-1" {
		t.Fatalf("event = %#v", event)
	}
}

func TestMapNotificationMapsCompletedReasoningToThinkingDelta(t *testing.T) {
	event, ok, err := MapNotification(testTask(), Notification{
		Method: NotifyItemCompleted,
		Params: json.RawMessage(`{"threadId":"thread-1","turnId":"turn-1","item":{"type":"reasoning","id":"item-1","summary":"Use Read tools to inspect files."}}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected mapped event")
	}
	if event.Kind != agentstream.EventThinkingDelta || event.Text != "Use Read tools to inspect files." || event.TurnID != "turn-1" {
		t.Fatalf("event = %#v", event)
	}
}

func TestMapNotificationMapsCompletedToolCall(t *testing.T) {
	event, ok, err := MapNotification(testTask(), Notification{
		Method: NotifyItemCompleted,
		Params: json.RawMessage(`{"threadId":"thread-1","turnId":"turn-1","item":{"type":"toolCall","id":"item-1","name":"Read","input":{"file_path":"/tmp/README.md"}}}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected mapped event")
	}
	if event.Kind != agentstream.EventToolStarted || event.Tool == nil || event.Tool.Name != "Read" || !strings.Contains(event.Tool.Input, "README.md") {
		t.Fatalf("event = %#v", event)
	}
}

func TestMapNotificationMapsIdleThreadStatusToTurnCompleted(t *testing.T) {
	event, ok, err := MapNotification(testTask(), Notification{
		Method: NotifyThreadStatus,
		Params: json.RawMessage(`{"threadId":"thread-1","status":{"type":"idle"}}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected mapped event")
	}
	if event.Kind != agentstream.EventTurnCompleted {
		t.Fatalf("event = %#v", event)
	}
}

func testTask() agentstream.TaskSummary {
	return agentstream.TaskSummary{ID: "task-1", Agent: "codex"}
}

var errFailingWriter = errors.New("write failed")

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errFailingWriter
}
