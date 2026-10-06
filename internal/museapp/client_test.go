package museapp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"
)

func TestClientCallAndNotification(t *testing.T) {
	reader, serverWriter := io.Pipe()
	serverReader, writer := io.Pipe()
	client := NewClient(reader, writer, nil)
	defer client.Close()

	go func() {
		defer serverWriter.Close()
		line, _ := bufio.NewReader(serverReader).ReadString('\n')
		var request map[string]any
		_ = json.Unmarshal([]byte(line), &request)
		id := request["id"]
		_, _ = serverWriter.Write([]byte(`{"jsonrpc":"2.0","id":` + jsonNumber(id) + `,"result":{"ok":true}}` + "\n"))
		_, _ = serverWriter.Write([]byte(`{"jsonrpc":"2.0","method":"item/delta","params":{"delta":"hi"}}` + "\n"))
	}()

	var result struct {
		OK bool `json:"ok"`
	}
	if err := client.Call(context.Background(), "test", map[string]string{"value": "x"}, &result); err != nil {
		t.Fatal(err)
	}
	if !result.OK {
		t.Fatal("result not decoded")
	}
	select {
	case event := <-client.Events():
		if event.Method != "item/delta" || !strings.Contains(string(event.Params), "hi") {
			t.Fatalf("event = %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("notification not received")
	}
}

func TestClientRespondsToServerRequest(t *testing.T) {
	input := bytes.NewBufferString(`{"jsonrpc":"2.0","id":"request-1","method":"approval/request","params":{}}` + "\n")
	var output bytes.Buffer
	client := NewClient(input, &output, nil)
	event := <-client.Events()
	if err := client.Respond(event, map[string]any{"presented": true}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `"id":"request-1"`) || !strings.Contains(output.String(), `"presented":true`) {
		t.Fatalf("response = %s", output.String())
	}
}

func jsonNumber(value any) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}
