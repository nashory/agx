package codexapp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// StreamKind identifies Codex app-server event streams persisted on tasks.
const StreamKind = "codex-app-server"

// Options configures how the Codex app-server subprocess is launched.
type Options struct {
	Command string
}

// Client is a JSON-RPC client for `codex app-server --listen stdio://`.
//
// It multiplexes request/response calls with server notifications on the same
// stdio stream and exposes notifications through Events.
type Client struct {
	writer io.Writer
	closer io.Closer

	nextID atomic.Int64

	writeMu sync.Mutex

	mu      sync.Mutex
	pending map[int64]chan response
	events  chan Notification
	done    chan struct{}
	err     error

	stderrMu  sync.Mutex
	stderrBuf []string
}

// maxStderrLines bounds the retained app-server stderr so a chatty subprocess
// cannot grow the buffer without limit; only the most recent lines matter for
// diagnosing a failure.
const maxStderrLines = 100

// Notification is a server-initiated Codex app-server message. A server-initiated
// request (one that expects a response, such as an approval request) carries a
// non-empty RequestID; rawID preserves its original JSON id so Respond can echo
// it back with the correct type.
type Notification struct {
	Method    string
	RequestID string
	Params    json.RawMessage

	rawID json.RawMessage
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
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// CallError is a JSON-RPC error returned by the Codex app-server. Callers can
// inspect Code and Message to handle specific failures (for example a rejected
// thread-settings override under a managed Codex config).
type CallError struct {
	Code    int
	Message string
}

func (e *CallError) Error() string {
	return fmt.Sprintf("codex app-server %d: %s", e.Code, e.Message)
}

// Start launches the Codex app-server subprocess and returns a connected client.
// Closing the client kills the subprocess and waits for it to exit.
func Start(ctx context.Context, opts Options) (*Client, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	command := strings.TrimSpace(opts.Command)
	if command == "" {
		command = "codex"
	}
	cmd := exec.Command(command, appServerArgs()...)
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
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		return cmd.Wait()
	}))
	client.captureStderr(stderr)
	return client, nil
}

// captureStderr drains the app-server stderr into a bounded ring buffer so a
// failure that codex only reports on stderr (a crash, auth error, or panic) can
// still be surfaced to the operator. Codex stderr is otherwise discarded.
func (c *Client) captureStderr(reader io.Reader) {
	if c == nil || reader == nil {
		return
	}
	go func() {
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := strings.TrimRight(scanner.Text(), "\r")
			if strings.TrimSpace(line) == "" {
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

// RecentStderr returns the most recent app-server stderr lines captured so far.
func (c *Client) RecentStderr() string {
	if c == nil {
		return ""
	}
	c.stderrMu.Lock()
	defer c.stderrMu.Unlock()
	return strings.Join(c.stderrBuf, "\n")
}

func appServerArgs() []string {
	args := make([]string, 0, 4)
	if runtime.GOOS == "darwin" {
		args = append(args, "--dangerously-disable-osx-sandbox")
	}
	return append(args, "app-server", "--listen", "stdio://")
}

// NewClient creates a client over an existing reader/writer pair. It is used by
// tests and by Start after wiring process stdio.
func NewClient(reader io.Reader, writer io.Writer, closer io.Closer) *Client {
	client := &Client{
		writer:  writer,
		closer:  closer,
		pending: map[int64]chan response{},
		events:  make(chan Notification, 128),
		done:    make(chan struct{}),
	}
	go client.readLoop(reader)
	return client
}

// Close closes the underlying app-server connection or process. It is safe on a
// nil client or a client without a closer.
func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	if c.closer == nil {
		return nil
	}
	return c.closer.Close()
}

// Events returns the notification stream. The channel closes when the reader
// loop exits.
func (c *Client) Events() <-chan Notification {
	return c.events
}

// Call sends one JSON-RPC request and decodes the result into result when
// non-nil. It removes pending calls on context cancellation or transport
// failure.
func (c *Client) Call(ctx context.Context, method string, params any, result any) error {
	raw, err := c.callRaw(ctx, method, params)
	if err != nil {
		return err
	}
	if result == nil || len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return json.Unmarshal(raw, result)
}

// Respond replies to a server-initiated request using the same JSON-RPC id the
// server sent. It is used to answer approval requests and is safe to call
// concurrently with in-flight Call requests.
func (c *Client) Respond(n Notification, result any) error {
	if len(n.rawID) == 0 {
		return fmt.Errorf("codex app-server notification has no request id to respond to")
	}
	c.mu.Lock()
	failErr := c.err
	c.mu.Unlock()
	if failErr != nil {
		return failErr
	}
	payload := map[string]any{"id": n.rawID}
	if result != nil {
		payload["result"] = result
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return c.writeMessage(data)
}

// writeMessage serializes writes to the app-server so concurrent Call and
// Respond invocations never interleave partial JSON-RPC frames on the stream.
func (c *Client) writeMessage(data []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err := c.writer.Write(append(data, '\n'))
	return err
}

func (c *Client) callRaw(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if strings.TrimSpace(method) == "" {
		return nil, fmt.Errorf("codex app-server method is required")
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

	payload := map[string]any{"id": id, "method": method}
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

func (c *Client) readLoop(reader io.Reader) {
	defer close(c.done)
	defer close(c.events)
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var message rpcMessage
		if err := json.Unmarshal([]byte(line), &message); err != nil {
			c.fail(fmt.Errorf("decode codex app-server message: %w", err))
			return
		}
		if message.Method != "" {
			c.publishNotification(Notification{Method: message.Method, RequestID: rawIDString(message.ID), Params: message.Params, rawID: message.ID})
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

func rawIDString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	return string(raw)
}

func (c *Client) publishNotification(notification Notification) {
	select {
	case c.events <- notification:
	default:
	}
}

func (c *Client) handleResponse(message rpcMessage) {
	id, err := parseID(message.ID)
	if err != nil {
		c.fail(err)
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
		wait <- response{err: &CallError{Code: message.Error.Code, Message: message.Error.Message}}
		return
	}
	wait <- response{result: message.Result}
}

func (c *Client) removePending(id int64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

// fail permanently marks the client failed and wakes every pending request with
// err. Later calls return the stored error.
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

func parseID(raw json.RawMessage) (int64, error) {
	var number int64
	if err := json.Unmarshal(raw, &number); err == nil {
		return number, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return 0, fmt.Errorf("decode codex app-server response id: %w", err)
	}
	return strconv.ParseInt(text, 10, 64)
}

type closerFunc func() error

func (f closerFunc) Close() error {
	return f()
}
