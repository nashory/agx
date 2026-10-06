package museapp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/nashory/agx/internal/processtree"
)

const StreamKind = "muse-msp"

type Options struct {
	Command        string
	DisableSandbox bool
	TrustWorkspace bool
}

type Notification struct {
	Method    string
	RequestID string
	Params    json.RawMessage
	rawID     json.RawMessage
}

type response struct {
	result json.RawMessage
	err    error
}

type rpcMessage struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type CallError struct {
	Code    int
	Message string
	Data    json.RawMessage
}

func (e *CallError) Error() string {
	return fmt.Sprintf("Muse MSP %d: %s", e.Code, e.Message)
}

type Client struct {
	writer io.Writer
	closer io.Closer

	nextID  atomic.Int64
	writeMu sync.Mutex

	mu      sync.Mutex
	pending map[int64]chan response
	events  chan Notification
	done    chan struct{}
	err     error

	stderrMu  sync.Mutex
	stderrBuf []string
}

const (
	maxFrameBytes  = 64 * 1024 * 1024
	maxStderrLines = 100
)

func Start(ctx context.Context, opts Options) (*Client, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	command := strings.TrimSpace(opts.Command)
	if command == "" {
		command = "muse"
	}
	args := []string{"serve"}
	if opts.DisableSandbox {
		args = append(args, "--disable-sandbox")
	}
	if opts.TrustWorkspace {
		args = append(args, "--trust-workspace")
	}
	cmd := exec.Command(command, args...)
	processtree.Prepare(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	client := NewClient(stdout, stdin, closerFunc(func() error {
		_ = stdin.Close()
		_ = processtree.Terminate(cmd)
		return cmd.Wait()
	}))
	client.captureStderr(stderr)
	return client, nil
}

func NewClient(reader io.Reader, writer io.Writer, closer io.Closer) *Client {
	c := &Client{
		writer:  writer,
		closer:  writerCloser{writer: writer, closer: closer},
		pending: map[int64]chan response{},
		events:  make(chan Notification, 2048),
		done:    make(chan struct{}),
	}
	go c.readLoop(reader)
	return c
}

func (c *Client) Events() <-chan Notification { return c.events }

func (c *Client) Close() error {
	if c == nil || c.closer == nil {
		return nil
	}
	return c.closer.Close()
}

func (c *Client) RecentStderr() string {
	if c == nil {
		return ""
	}
	c.stderrMu.Lock()
	defer c.stderrMu.Unlock()
	return strings.Join(c.stderrBuf, "\n")
}

func (c *Client) Call(ctx context.Context, method string, params, out any) error {
	raw, err := c.callRaw(ctx, method, params)
	if err != nil || out == nil || len(raw) == 0 || string(raw) == "null" {
		return err
	}
	return json.Unmarshal(raw, out)
}

func (c *Client) Notify(method string, params any) error {
	payload := map[string]any{"jsonrpc": "2.0", "method": method}
	if params != nil {
		payload["params"] = params
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return c.writeMessage(data)
}

func (c *Client) Respond(n Notification, result any) error {
	if len(n.rawID) == 0 {
		return fmt.Errorf("Muse MSP notification has no request id")
	}
	payload := map[string]any{"jsonrpc": "2.0", "id": n.rawID, "result": result}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return c.writeMessage(data)
}

func (c *Client) callRaw(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if strings.TrimSpace(method) == "" {
		return nil, fmt.Errorf("Muse MSP method is required")
	}
	id := c.nextID.Add(1)
	wait := make(chan response, 1)
	c.mu.Lock()
	if c.err != nil {
		err := c.err
		c.mu.Unlock()
		return nil, err
	}
	c.pending[id] = wait
	c.mu.Unlock()
	payload := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		payload["params"] = params
	}
	data, err := json.Marshal(payload)
	if err != nil {
		c.removePending(id)
		return nil, err
	}
	if err := c.writeMessage(data); err != nil {
		c.removePending(id)
		return nil, err
	}
	select {
	case <-ctx.Done():
		c.removePending(id)
		return nil, ctx.Err()
	case <-c.done:
		c.mu.Lock()
		err := c.err
		c.mu.Unlock()
		if err == nil {
			err = io.EOF
		}
		return nil, err
	case response := <-wait:
		return response.result, response.err
	}
}

func (c *Client) writeMessage(data []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err := c.writer.Write(append(data, '\n'))
	return err
}

func (c *Client) readLoop(reader io.Reader) {
	defer close(c.done)
	defer close(c.events)
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), maxFrameBytes)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var message rpcMessage
		if err := json.Unmarshal(line, &message); err != nil {
			c.fail(fmt.Errorf("decode Muse MSP message: %w", err))
			return
		}
		if message.Method != "" {
			c.events <- Notification{Method: message.Method, RequestID: rawIDString(message.ID), Params: message.Params, rawID: append(json.RawMessage(nil), message.ID...)}
			continue
		}
		if len(message.ID) > 0 {
			c.handleResponse(message)
		}
	}
	if err := scanner.Err(); err != nil {
		c.fail(err)
	}
}

func (c *Client) handleResponse(message rpcMessage) {
	var id int64
	if err := json.Unmarshal(message.ID, &id); err != nil {
		c.fail(fmt.Errorf("decode Muse MSP response id: %w", err))
		return
	}
	c.mu.Lock()
	wait := c.pending[id]
	delete(c.pending, id)
	c.mu.Unlock()
	if wait == nil {
		return
	}
	if message.Error != nil {
		wait <- response{err: &CallError{Code: message.Error.Code, Message: message.Error.Message, Data: message.Error.Data}}
		return
	}
	wait <- response{result: message.Result}
}

func (c *Client) removePending(id int64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

func (c *Client) fail(err error) {
	c.mu.Lock()
	if c.err == nil {
		c.err = err
	}
	pending := c.pending
	c.pending = map[int64]chan response{}
	c.mu.Unlock()
	for _, wait := range pending {
		wait <- response{err: err}
	}
}

func (c *Client) captureStderr(reader io.Reader) {
	go func() {
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			c.stderrMu.Lock()
			c.stderrBuf = append(c.stderrBuf, line)
			if len(c.stderrBuf) > maxStderrLines {
				c.stderrBuf = c.stderrBuf[len(c.stderrBuf)-maxStderrLines:]
			}
			c.stderrMu.Unlock()
		}
	}()
}

func rawIDString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	return string(raw)
}

type closerFunc func() error

func (f closerFunc) Close() error { return f() }

type writerCloser struct {
	writer io.Writer
	closer io.Closer
}

func (c writerCloser) Close() error {
	if closer, ok := c.writer.(io.Closer); ok {
		_ = closer.Close()
	}
	if c.closer == nil {
		return nil
	}
	return c.closer.Close()
}
