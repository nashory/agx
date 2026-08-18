package codexapp

import (
	"bufio"
	"encoding/json"
	"net"
	"testing"
)

func TestCancelUserInputRequestReturnsEmptyAnswers(t *testing.T) {
	server, clientConn := net.Pipe()
	defer server.Close()
	defer clientConn.Close()
	client := NewClient(clientConn, clientConn, clientConn)
	reader := bufio.NewReader(server)
	if _, err := server.Write([]byte(`{"id":"input-1","method":"item/tool/requestUserInput","params":{"questions":[{"id":"color"}]}}` + "\n")); err != nil {
		t.Fatal(err)
	}
	notification := <-client.Events()
	if !IsInputRequest(notification) {
		t.Fatalf("IsInputRequest() = false for %#v", notification)
	}
	errCh := make(chan error, 1)
	go func() { errCh <- client.CancelInputRequest(notification) }()
	line, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
	var response struct {
		ID     string `json:"id"`
		Result struct {
			Answers map[string]any `json:"answers"`
		} `json:"result"`
	}
	if err := json.Unmarshal(line, &response); err != nil {
		t.Fatal(err)
	}
	if response.ID != "input-1" || response.Result.Answers == nil || len(response.Result.Answers) != 0 {
		t.Fatalf("response = %#v, want no invented answers", response)
	}
}

func TestIsInputRequestRequiresSupportedMethodAndID(t *testing.T) {
	if IsInputRequest(Notification{Method: NotifyUserInputRequest}) {
		t.Fatal("request without id was accepted")
	}
	if IsInputRequest(Notification{Method: NotifyAgentMessageDelta, RequestID: "1"}) {
		t.Fatal("ordinary notification was accepted")
	}
}

func TestCancelMCPElicitationRequest(t *testing.T) {
	server, clientConn := net.Pipe()
	defer server.Close()
	defer clientConn.Close()
	client := NewClient(clientConn, clientConn, clientConn)
	reader := bufio.NewReader(server)
	if _, err := server.Write([]byte(`{"id":42,"method":"mcpServer/elicitation/request","params":{}}` + "\n")); err != nil {
		t.Fatal(err)
	}
	notification := <-client.Events()
	errCh := make(chan error, 1)
	go func() { errCh <- client.CancelInputRequest(notification) }()
	line, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
	var response struct {
		ID     int `json:"id"`
		Result struct {
			Action string `json:"action"`
		} `json:"result"`
	}
	if err := json.Unmarshal(line, &response); err != nil {
		t.Fatal(err)
	}
	if response.ID != 42 || response.Result.Action != "cancel" {
		t.Fatalf("response = %#v, want cancel", response)
	}
}
