package session

import (
	"fmt"
	"sync"
)

// fakeBackend is an in-memory Backend used to test manager task lifecycle logic
// without a real terminal engine. Unlike the fake tmux shell scripts, it has no
// shell dependency, so these tests also run on Windows.
type fakeBackend struct {
	mu sync.Mutex

	hasTmux   bool
	hasServer bool

	sessions     map[string]bool   // session name -> exists
	windows      map[string]bool   // target -> exists
	windowNames  map[string]string // target -> window name
	windowCounts map[string]int    // session -> window count
	paneVisible  map[string]string // target -> visible capture output
	paneHistory  map[string]string // target -> capture-with-history output
	paneCommand  map[string]string // target -> foreground command
	panePath     map[string]string // target -> pane working directory

	calls []string // ordered record of operations, for assertions
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{
		hasTmux:      true,
		hasServer:    true,
		sessions:     map[string]bool{},
		windows:      map[string]bool{},
		windowNames:  map[string]string{},
		windowCounts: map[string]int{},
		paneVisible:  map[string]string{},
		paneHistory:  map[string]string{},
		paneCommand:  map[string]string{},
		panePath:     map[string]string{},
	}
}

func (f *fakeBackend) record(format string, args ...any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fmt.Sprintf(format, args...))
}

// recorded returns a copy of the operations issued so far.
func (f *fakeBackend) recorded() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *fakeBackend) HasTmux() bool               { return f.hasTmux }
func (f *fakeBackend) HasServer() bool             { return f.hasServer }
func (f *fakeBackend) HasSession(name string) bool { return f.sessions[name] }

func (f *fakeBackend) CreateSession(name, workingDir string) error {
	f.record("CreateSession %s %s", name, workingDir)
	f.sessions[name] = true
	return nil
}

func (f *fakeBackend) SetOption(key, value string) error {
	f.record("SetOption %s %s", key, value)
	return nil
}

func (f *fakeBackend) KillSession(session string) error {
	f.record("KillSession %s", session)
	delete(f.sessions, session)
	return nil
}

func (f *fakeBackend) CreateWindow(session, windowName, workingDir, command string) error {
	f.record("CreateWindow %s %s %s", session, windowName, workingDir)
	target := session + ":" + windowName
	f.windows[target] = true
	f.windowNames[target] = windowName
	return nil
}

func (f *fakeBackend) WindowExists(target string) bool { return f.windows[target] }

func (f *fakeBackend) WindowCount(session string) (int, error) {
	return f.windowCounts[session], nil
}

func (f *fakeBackend) WindowName(target string) (string, error) {
	return f.windowNames[target], nil
}

func (f *fakeBackend) KillWindow(target string) error {
	f.record("KillWindow %s", target)
	delete(f.windows, target)
	return nil
}

func (f *fakeBackend) SendKeys(target, text string) error {
	f.record("SendKeys %s %q", target, text)
	return nil
}

func (f *fakeBackend) SendKey(target, key string) error {
	f.record("SendKey %s %s", target, key)
	return nil
}

func (f *fakeBackend) SendInput(target, data string) error {
	f.record("SendInput %s %q", target, data)
	return nil
}

func (f *fakeBackend) SendEnter(target string) error {
	f.record("SendEnter %s", target)
	return nil
}

func (f *fakeBackend) ResizeWindow(target string, cols, rows int) error {
	f.record("ResizeWindow %s %d %d", target, cols, rows)
	return nil
}

func (f *fakeBackend) CapturePane(target string) (string, error) {
	return f.paneVisible[target], nil
}

func (f *fakeBackend) CapturePaneWithHistory(target string, lines int) (string, error) {
	return f.paneHistory[target], nil
}

func (f *fakeBackend) ReplacePipePane(target, command string) error {
	f.record("ReplacePipePane %s %s", target, command)
	return nil
}

func (f *fakeBackend) StopPipePane(target string) error {
	f.record("StopPipePane %s", target)
	return nil
}

func (f *fakeBackend) PaneCurrentPath(target string) (string, error) {
	return f.panePath[target], nil
}

func (f *fakeBackend) PaneCurrentCommand(target string) (string, error) {
	return f.paneCommand[target], nil
}

// compile-time check that the test double satisfies the interface.
var _ Backend = (*fakeBackend)(nil)
