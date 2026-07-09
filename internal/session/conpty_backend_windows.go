//go:build windows

package session

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"unsafe"

	"github.com/UserExistsError/conpty"
	"golang.org/x/sys/windows"
)

// conptyBackend is the native Windows session backend. Each task is a single
// process tree attached to a ConPTY pseudo-console and enclosed in a Windows Job
// Object so the whole tree can be terminated on kill. Unlike tmux there is no
// server or persistent session container: sessions and windows are tracked in
// memory only, so tasks do not survive a runtime restart (recovery is limited by
// design in the MVP; see docs/WINDOWS_NATIVE_SUPPORT_DESIGN.md).
//
// This backend deliberately implements the tmux-shaped Backend interface so the
// session manager stays unchanged. The naming maps as:
//
//	project session  -> in-memory session marker
//	task window      -> one ConPTY process tree, keyed by "session:window"
//	pane capture     -> tail of an in-memory output buffer
//	pipe-pane        -> no-op (output is always captured in the buffer)
const (
	conptyOutputLimit = 256 * 1024 // retained tail of task output, in bytes
	conptyDefaultCols = 120
	conptyDefaultRows = 40
	// conptyForegroundCommand is returned by PaneCurrentCommand. It is not one of
	// the shell names DetectTaskStatus special-cases, so status is derived from
	// output activity rather than a POSIX shell exit-status file.
	conptyForegroundCommand = "conpty"
)

type conptyTask struct {
	session    string
	windowName string
	workDir    string
	cpty       *conpty.ConPty
	job        windows.Handle // 0 when no job could be assigned

	mu     sync.Mutex
	output []byte
	exited bool
}

func (t *conptyTask) appendOutput(p []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.output = append(t.output, p...)
	if len(t.output) > conptyOutputLimit {
		t.output = append([]byte(nil), t.output[len(t.output)-conptyOutputLimit:]...)
	}
}

func (t *conptyTask) snapshot() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.output)
}

func (t *conptyTask) markExited() {
	t.mu.Lock()
	t.exited = true
	t.mu.Unlock()
}

func (t *conptyTask) isExited() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.exited
}

// readLoop drains ConPTY output into the task buffer until the process exits.
// The ConPty handle is captured locally so it is safe to read here while close()
// concurrently tears the task down; closing the handle makes Read return an error
// and the loop exits.
func (t *conptyTask) readLoop() {
	cpty := t.cpty
	buf := make([]byte, 4096)
	for {
		n, err := cpty.Read(buf)
		if n > 0 {
			t.appendOutput(buf[:n])
		}
		if err != nil {
			t.markExited()
			return
		}
	}
}

// close terminates the whole process tree and releases handles.
func (t *conptyTask) close() error {
	var errs []error
	if t.job != 0 {
		if err := windows.TerminateJobObject(t.job, 1); err != nil {
			errs = append(errs, fmt.Errorf("terminate job object: %w", err))
		}
		if err := windows.CloseHandle(t.job); err != nil {
			errs = append(errs, fmt.Errorf("close job object: %w", err))
		}
		t.job = 0
	}
	if t.cpty != nil {
		// Close also terminates the attached process and releases its handles,
		// which makes the readLoop's Read return an error and exit. The field is
		// left non-nil so readLoop's captured handle and concurrent callers do not
		// race on it; the exited flag is the source of truth for liveness.
		if err := t.cpty.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close conpty: %w", err))
		}
	}
	t.markExited()
	return errors.Join(errs...)
}

type conptyBackend struct {
	mu       sync.Mutex
	tasks    map[string]*conptyTask
	sessions map[string]bool
}

func newConptyBackend() *conptyBackend {
	return &conptyBackend{
		tasks:    map[string]*conptyTask{},
		sessions: map[string]bool{},
	}
}

var _ Backend = (*conptyBackend)(nil)

func (b *conptyBackend) task(target string) *conptyTask {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.tasks[target]
}

// HasTmux reports whether ConPTY is usable on this Windows version.
func (b *conptyBackend) HasTmux() bool {
	return conpty.IsConPtyAvailable()
}

// HasServer is always false: ConPTY tasks are in-process children that do not
// survive a runtime restart, so startup recovery treats persisted tasks as
// offline.
func (b *conptyBackend) HasServer() bool {
	return false
}

func (b *conptyBackend) HasSession(name string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.sessions[name]
}

func (b *conptyBackend) CreateSession(name, workingDir string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sessions[name] = true
	return nil
}

// SetOption has no ConPTY equivalent (for example tmux scrollback history).
func (b *conptyBackend) SetOption(key, value string) error {
	return nil
}

func (b *conptyBackend) KillSession(session string) error {
	b.mu.Lock()
	var victims []*conptyTask
	for target, t := range b.tasks {
		if t.session == session {
			victims = append(victims, t)
			delete(b.tasks, target)
		}
	}
	delete(b.sessions, session)
	b.mu.Unlock()

	var errs []error
	for _, t := range victims {
		if err := t.close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// CreateWindow starts command as a ConPTY process tree enclosed in a Job Object.
func (b *conptyBackend) CreateWindow(session, windowName, workingDir, command string) error {
	target := session + ":" + windowName
	cpty, err := conpty.Start(
		command,
		conpty.ConPtyWorkDir(workingDir),
		conpty.ConPtyDimensions(conptyDefaultCols, conptyDefaultRows),
	)
	if err != nil {
		return fmt.Errorf("start conpty task: %w", err)
	}

	task := &conptyTask{session: session, windowName: windowName, workDir: workingDir, cpty: cpty}
	if job, err := newKillOnCloseJob(); err == nil {
		if assignErr := assignProcessToJob(job, cpty.Pid()); assignErr == nil {
			task.job = job
		} else {
			_ = windows.CloseHandle(job)
		}
	}

	go task.readLoop()

	b.mu.Lock()
	b.tasks[target] = task
	b.sessions[session] = true
	b.mu.Unlock()
	return nil
}

func (b *conptyBackend) WindowExists(target string) bool {
	t := b.task(target)
	return t != nil && !t.isExited()
}

func (b *conptyBackend) WindowCount(session string) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	count := 0
	for _, t := range b.tasks {
		if t.session == session {
			count++
		}
	}
	return count, nil
}

func (b *conptyBackend) WindowName(target string) (string, error) {
	t := b.task(target)
	if t == nil {
		return "", fmt.Errorf("no task window %s", target)
	}
	return t.windowName, nil
}

func (b *conptyBackend) KillWindow(target string) error {
	b.mu.Lock()
	t := b.tasks[target]
	delete(b.tasks, target)
	b.mu.Unlock()
	if t == nil {
		return nil
	}
	return t.close()
}

// SendKeys writes text followed by Enter, matching the tmux backend.
func (b *conptyBackend) SendKeys(target, text string) error {
	return b.write(target, []byte(text+"\r"))
}

func (b *conptyBackend) SendKey(target, key string) error {
	seq, ok := controlKeySequence(key)
	if !ok {
		return fmt.Errorf("unsupported key %q", key)
	}
	return b.write(target, seq)
}

func (b *conptyBackend) SendInput(target, data string) error {
	return b.write(target, []byte(data))
}

func (b *conptyBackend) SendEnter(target string) error {
	return b.write(target, []byte("\r"))
}

func (b *conptyBackend) write(target string, data []byte) error {
	t := b.task(target)
	if t == nil {
		return fmt.Errorf("no task window %s", target)
	}
	if t.isExited() {
		return fmt.Errorf("task window %s is closed", target)
	}
	_, err := t.cpty.Write(data)
	return err
}

func (b *conptyBackend) ResizeWindow(target string, cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return nil
	}
	t := b.task(target)
	if t == nil || t.isExited() {
		return nil
	}
	return t.cpty.Resize(cols, rows)
}

func (b *conptyBackend) CapturePane(target string) (string, error) {
	t := b.task(target)
	if t == nil {
		return "", nil
	}
	return t.snapshot(), nil
}

func (b *conptyBackend) CapturePaneWithHistory(target string, lines int) (string, error) {
	snapshot, err := b.CapturePane(target)
	if err != nil {
		return "", err
	}
	return tailLines(snapshot, lines), nil
}

// ReplacePipePane is a no-op: task output is always captured in the in-memory
// buffer returned by CapturePane, so no separate log redirection is needed.
func (b *conptyBackend) ReplacePipePane(target, command string) error {
	return nil
}

func (b *conptyBackend) StopPipePane(target string) error {
	return nil
}

// PaneCurrentPath returns the directory the task was started in. ConPTY cannot
// cheaply report a live working directory, so this is the task worktree, which is
// what the manager's safety check expects.
func (b *conptyBackend) PaneCurrentPath(target string) (string, error) {
	t := b.task(target)
	if t == nil {
		return "", fmt.Errorf("no task window %s", target)
	}
	return t.workDir, nil
}

func (b *conptyBackend) PaneCurrentCommand(target string) (string, error) {
	t := b.task(target)
	if t == nil {
		return "", fmt.Errorf("no task window %s", target)
	}
	return conptyForegroundCommand, nil
}

func controlKeySequence(key string) ([]byte, bool) {
	switch key {
	case "C-c":
		return []byte{0x03}, true
	case "Enter":
		return []byte("\r"), true
	default:
		return nil, false
	}
}

func tailLines(s string, lines int) string {
	if lines <= 0 {
		return s
	}
	parts := strings.Split(s, "\n")
	if len(parts) <= lines {
		return s
	}
	return strings.Join(parts[len(parts)-lines:], "\n")
}

// newKillOnCloseJob creates a Job Object that terminates every process in the job
// when the job handle is closed, so a killed task cannot leave orphaned children.
func newKillOnCloseJob() (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		_ = windows.CloseHandle(job)
		return 0, err
	}
	return job, nil
}

func assignProcessToJob(job windows.Handle, pid int) error {
	handle, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.PROCESS_SET_QUOTA, false, uint32(pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)
	return windows.AssignProcessToJobObject(job, handle)
}
