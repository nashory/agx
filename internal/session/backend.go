package session

import "github.com/nashory/agx/internal/tmux"

// Backend is the terminal engine the session manager drives to run, observe, and
// stop tasks. tmux is the only implementation today; native Windows will add a
// ConPTY-based implementation behind the same interface (see
// docs/WINDOWS_NATIVE_SUPPORT_DESIGN.md).
//
// The method set is intentionally derived from the exact operations the manager
// already performs, so introducing the interface is a mechanical, behavior-
// preserving refactor. It deliberately mirrors the tmux controller's shape for
// now; later phases may narrow it toward a higher-level session API once a second
// backend exists to justify the abstraction.
//
// *tmux.Controller satisfies this interface directly (see the assertion below),
// so callers keep constructing tmux.NewController() and pass it wherever a
// Backend is expected.
type Backend interface {
	// HasTmux reports whether the backend's engine is installed and usable.
	HasTmux() bool
	// HasServer reports whether the backend already has a live server holding
	// existing sessions, which recovery uses to decide task viability.
	HasServer() bool

	// HasSession reports whether the named project session container exists.
	HasSession(name string) bool
	// CreateSession creates a project-scoped session container rooted at
	// workingDir. Implementations must be idempotent.
	CreateSession(name, workingDir string) error
	// SetOption sets a global backend option (for example scrollback history).
	SetOption(key, value string) error
	// KillSession stops a project session container and all its tasks.
	KillSession(session string) error

	// CreateWindow starts one task, running command in workingDir.
	CreateWindow(session, windowName, workingDir, command string) error
	// WindowExists reports whether a task window is still alive.
	WindowExists(target string) bool
	// WindowCount returns how many windows a session currently holds.
	WindowCount(session string) (int, error)
	// WindowName returns the name of the window at target.
	WindowName(target string) (string, error)
	// KillWindow stops a single task window.
	KillWindow(target string) error

	// SendKeys writes prompt text followed by Enter.
	SendKeys(target, text string) error
	// SendKey sends a single named key such as "C-c".
	SendKey(target, key string) error
	// SendInput streams raw terminal bytes, translating control sequences.
	SendInput(target, data string) error
	// SendEnter presses Enter without any preceding text.
	SendEnter(target string) error

	// ResizeWindow resizes the interactive terminal.
	ResizeWindow(target string, cols, rows int) error
	// CapturePane snapshots the currently visible output.
	CapturePane(target string) (string, error)
	// CapturePaneWithHistory snapshots the last lines of scrollback output.
	CapturePaneWithHistory(target string, lines int) (string, error)

	// ReplacePipePane redirects the window's output stream into command,
	// replacing any existing redirect. Used to mirror output into a log file.
	ReplacePipePane(target, command string) error
	// StopPipePane stops mirroring the window's output.
	StopPipePane(target string) error

	// PaneCurrentPath returns the working directory of the window's pane, used to
	// verify a task is still in its expected worktree.
	PaneCurrentPath(target string) (string, error)
	// PaneCurrentCommand returns the foreground command in the window's pane,
	// used to classify task status.
	PaneCurrentCommand(target string) (string, error)
}

// Compile-time guarantee that the tmux controller satisfies Backend. If the
// interface and controller ever drift, the build fails here rather than at a
// call site.
var _ Backend = (*tmux.Controller)(nil)
