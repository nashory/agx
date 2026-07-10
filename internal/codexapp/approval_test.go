package codexapp

import (
	"bufio"
	"encoding/json"
	"net"
	"testing"
)

func TestIsApprovalRequest(t *testing.T) {
	cases := []struct {
		name string
		n    Notification
		want bool
	}{
		{"command", Notification{Method: NotifyCommandApprovalRequest, RequestID: "1"}, true},
		{"file change", Notification{Method: NotifyFileChangeApprovalRequest, RequestID: "1"}, true},
		{"no id", Notification{Method: NotifyCommandApprovalRequest}, false},
		{"plain notification", Notification{Method: NotifyAgentMessageDelta, RequestID: "1"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsApprovalRequest(tc.n); got != tc.want {
				t.Fatalf("IsApprovalRequest() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestApproveRequestEchoesStringID verifies AGX answers an approval request with
// the server's exact JSON-RPC id and an "accept" decision the app-server accepts.
func TestApproveRequestEchoesStringID(t *testing.T) {
	server, clientConn := net.Pipe()
	defer server.Close()
	defer clientConn.Close()
	client := NewClient(clientConn, clientConn, clientConn)
	reader := bufio.NewReader(server)

	if _, err := server.Write([]byte(`{"id":"approval-1","method":"item/commandExecution/requestApproval","params":{"threadId":"t"}}` + "\n")); err != nil {
		t.Fatal(err)
	}
	notification := <-client.Events()
	if !IsApprovalRequest(notification) {
		t.Fatalf("IsApprovalRequest() = false for %#v", notification)
	}

	errCh := make(chan error, 1)
	go func() { errCh <- client.ApproveRequest(notification, DecisionAccept) }()

	line, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("ApproveRequest() error = %v", err)
	}

	var response struct {
		ID     string `json:"id"`
		Result struct {
			Decision string `json:"decision"`
		} `json:"result"`
	}
	if err := json.Unmarshal(line, &response); err != nil {
		t.Fatalf("decode response %q: %v", line, err)
	}
	if response.ID != "approval-1" {
		t.Fatalf("response id = %q, want approval-1", response.ID)
	}
	if response.Result.Decision != "accept" {
		t.Fatalf("decision = %q, want accept", response.Result.Decision)
	}
}

// TestApproveRequestEchoesNumericID verifies a numeric server id is echoed back
// as a number, not a string, matching the JSON-RPC request.
func TestApproveRequestEchoesNumericID(t *testing.T) {
	server, clientConn := net.Pipe()
	defer server.Close()
	defer clientConn.Close()
	client := NewClient(clientConn, clientConn, clientConn)
	reader := bufio.NewReader(server)

	if _, err := server.Write([]byte(`{"id":42,"method":"item/fileChange/requestApproval","params":{}}` + "\n")); err != nil {
		t.Fatal(err)
	}
	notification := <-client.Events()

	errCh := make(chan error, 1)
	go func() { errCh <- client.ApproveRequest(notification, DecisionDecline) }()

	line, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("ApproveRequest() error = %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(line, &raw); err != nil {
		t.Fatalf("decode response %q: %v", line, err)
	}
	if string(raw["id"]) != "42" {
		t.Fatalf("response id = %s, want numeric 42", raw["id"])
	}
}

func TestRespondWithoutRequestIDFails(t *testing.T) {
	server, clientConn := net.Pipe()
	defer server.Close()
	defer clientConn.Close()
	client := NewClient(clientConn, clientConn, clientConn)
	if err := client.Respond(Notification{Method: "x"}, nil); err == nil {
		t.Fatal("Respond() without a request id = nil, want error")
	}
}
