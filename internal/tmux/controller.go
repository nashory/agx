package tmux

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

type Controller struct {
	serverName string
}

var escapeKeys = map[string]string{
	"\x1b[A":  "Up",
	"\x1b[B":  "Down",
	"\x1b[C":  "Right",
	"\x1b[D":  "Left",
	"\x1b[H":  "Home",
	"\x1b[F":  "End",
	"\x1b[1~": "Home",
	"\x1b[4~": "End",
	"\x1b[3~": "DC",
	"\x1b[5~": "PPage",
	"\x1b[6~": "NPage",
}

func NewController() *Controller {
	return &Controller{serverName: "agx"}
}

func (t *Controller) HasTmux() bool {
	_, err := exec.LookPath("tmux")
	return err == nil
}

func (t *Controller) HasServer() bool {
	return t.run("list-sessions", "-F", "#{session_name}") == nil
}

func (t *Controller) CreateSession(name, workingDir string) error {
	if t.HasSession(name) {
		return nil
	}
	if err := t.run("new-session", "-d", "-s", name, "-c", workingDir); err != nil {
		if t.HasSession(name) {
			return nil
		}
		return err
	}
	return nil
}

func (t *Controller) HasSession(name string) bool {
	return t.run("has-session", "-t", name) == nil
}

func (t *Controller) SetOption(key, value string) error {
	return t.run("set-option", "-g", key, value)
}

func (t *Controller) CreateWindow(session, windowName, workingDir, command string) error {
	_ = t.run("set-window-option", "-g", "automatic-rename", "off")
	_ = t.run("set-window-option", "-g", "allow-rename", "off")
	return t.run("new-window", "-d", "-t", session+":", "-n", windowName, "-c", workingDir, command)
}

func (t *Controller) RenameWindow(target, windowName string) error {
	return t.run("rename-window", "-t", target, windowName)
}

func (t *Controller) WindowExists(target string) bool {
	session, window, ok := strings.Cut(target, ":")
	if !ok || session == "" || window == "" {
		return t.run("display-message", "-p", "-t", target, "#{window_id}") == nil
	}
	out, err := t.output("list-windows", "-t", session, "-F", "#{window_name}")
	if err != nil {
		return false
	}
	for _, name := range strings.Split(strings.TrimSpace(out), "\n") {
		if name == window {
			return true
		}
	}
	return false
}

func (t *Controller) WindowCount(session string) (int, error) {
	out, err := t.output("list-windows", "-t", session, "-F", "#{window_index}")
	if err != nil {
		return 0, err
	}
	lines := strings.Fields(out)
	return len(lines), nil
}

func (t *Controller) WindowName(target string) (string, error) {
	out, err := t.output("display-message", "-p", "-t", target, "#{window_name}")
	return strings.TrimSpace(out), err
}

func (t *Controller) PaneCurrentPath(target string) (string, error) {
	out, err := t.output("display-message", "-p", "-t", target, "#{pane_current_path}")
	return strings.TrimSpace(out), err
}

func (t *Controller) SendKeys(target, text string) error {
	if err := t.run("send-keys", "-t", target, "-l", "--", text); err != nil {
		return err
	}
	time.Sleep(75 * time.Millisecond)
	return t.SendEnter(target)
}

func (t *Controller) SendKey(target, key string) error {
	return t.run("send-keys", "-t", target, key)
}

func (t *Controller) SendInput(target, data string) error {
	for data != "" {
		if key, consumed, ok := inputKey(data); ok {
			if err := t.run("send-keys", "-t", target, key); err != nil {
				return err
			}
			data = data[consumed:]
			continue
		}
		literal, consumed := inputLiteral(data)
		if literal == "" || consumed == 0 {
			return nil
		}
		if err := t.run("send-keys", "-t", target, "-l", "--", literal); err != nil {
			return err
		}
		data = data[consumed:]
	}
	return nil
}

func (t *Controller) ResizeWindow(target string, cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return nil
	}
	return t.run("resize-window", "-t", target, "-x", fmt.Sprint(cols), "-y", fmt.Sprint(rows))
}

func (t *Controller) SendLiteral(target, text string) error {
	return t.run("send-keys", "-t", target, text)
}

func (t *Controller) SendEnter(target string) error {
	return t.run("send-keys", "-t", target, "Enter")
}

func inputKey(data string) (string, int, bool) {
	for sequence, key := range escapeKeys {
		if strings.HasPrefix(data, sequence) {
			return key, len(sequence), true
		}
	}
	switch data[0] {
	case '\r', '\n':
		return "Enter", 1, true
	case '\t':
		return "Tab", 1, true
	case 0x7f:
		return "BSpace", 1, true
	case 0x1b:
		return "Escape", 1, true
	}
	if data[0] >= 0x01 && data[0] <= 0x1a {
		return fmt.Sprintf("C-%c", 'a'+data[0]-1), 1, true
	}
	return "", 0, false
}

func inputLiteral(data string) (string, int) {
	for index, r := range data {
		if r == '\r' || r == '\n' || r == '\t' || r == 0x7f || r == 0x1b || (r >= 0x01 && r <= 0x1a) {
			if index == 0 {
				return "", 0
			}
			return data[:index], index
		}
	}
	return data, len(data)
}

func (t *Controller) CapturePane(target string) (string, error) {
	return t.output("capture-pane", "-t", target, "-p")
}

func (t *Controller) CapturePaneWithHistory(target string, lines int) (string, error) {
	start := fmt.Sprintf("-%d", lines)
	return t.output("capture-pane", "-t", target, "-p", "-S", start)
}

func (t *Controller) PipePane(target, command string) error {
	return t.run("pipe-pane", "-o", "-t", target, command)
}

func (t *Controller) ReplacePipePane(target, command string) error {
	return t.run("pipe-pane", "-t", target, command)
}

func (t *Controller) StopPipePane(target string) error {
	return t.run("pipe-pane", "-t", target)
}

func (t *Controller) KillWindow(target string) error {
	return t.run("kill-window", "-t", target)
}

func (t *Controller) KillSession(session string) error {
	return t.run("kill-session", "-t", session)
}

func (t *Controller) KillServer() error {
	return t.run("kill-server")
}

func (t *Controller) PaneCurrentCommand(target string) (string, error) {
	out, err := t.output("display-message", "-p", "-t", target, "#{pane_current_command}")
	return strings.TrimSpace(out), err
}

func (t *Controller) Attach(target string) error {
	cmd := exec.Command("tmux", "-L", t.serverName, "attach", "-t", target)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (t *Controller) run(args ...string) error {
	_, err := t.output(args...)
	return err
}

func (t *Controller) output(args ...string) (string, error) {
	fullArgs := append([]string{"-L", t.serverName}, args...)
	cmd := exec.Command("tmux", fullArgs...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return "", fmt.Errorf("%w: %s", err, msg)
		}
		return "", err
	}
	return stdout.String(), nil
}
