//go:build windows

package session

import (
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

const conptyTestSession = "proj:task-1"

func startConptyTestTask(t *testing.T, backend *conptyBackend, command string) (session, window string) {
	t.Helper()
	session, window = "proj", "task-1"
	if err := backend.CreateSession(session, t.TempDir()); err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	if err := backend.CreateWindow(session, window, t.TempDir(), command); err != nil {
		t.Fatalf("CreateWindow() error = %v", err)
	}
	t.Cleanup(func() { _ = backend.KillWindow(session + ":" + window) })
	return session, window
}

func waitForCapture(t *testing.T, backend *conptyBackend, target, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		out, err := backend.CapturePane(target)
		if err != nil {
			t.Fatalf("CapturePane() error = %v", err)
		}
		if strings.Contains(out, want) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	out, _ := backend.CapturePane(target)
	t.Fatalf("capture never contained %q; got:\n%s", want, out)
}

func TestConptyBackendCapturesOutput(t *testing.T) {
	backend := newConptyBackend()
	if !backend.HasTmux() {
		t.Skip("ConPTY is not available on this Windows version")
	}
	_, _ = startConptyTestTask(t, backend, `cmd.exe /c "echo AGXCONPTYTOKEN & pause"`)
	waitForCapture(t, backend, conptyTestSession, "AGXCONPTYTOKEN")

	if !backend.WindowExists(conptyTestSession) {
		t.Fatal("WindowExists() = false, want true for running task")
	}
	if count, err := backend.WindowCount("proj"); err != nil || count != 1 {
		t.Fatalf("WindowCount() = %d, %v; want 1", count, err)
	}
	if err := backend.ResizeWindow(conptyTestSession, 100, 30); err != nil {
		t.Fatalf("ResizeWindow() error = %v", err)
	}
}

func TestConptyBackendKillTerminatesProcess(t *testing.T) {
	backend := newConptyBackend()
	if !backend.HasTmux() {
		t.Skip("ConPTY is not available on this Windows version")
	}
	_, _ = startConptyTestTask(t, backend, `cmd.exe /c "echo READY & pause"`)
	waitForCapture(t, backend, conptyTestSession, "READY")

	task := backend.task(conptyTestSession)
	if task == nil || task.cpty == nil {
		t.Fatal("task not tracked after start")
	}
	pid := task.cpty.Pid()
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		t.Fatalf("OpenProcess(pid=%d) error = %v", pid, err)
	}
	defer windows.CloseHandle(handle)

	if err := backend.KillWindow(conptyTestSession); err != nil {
		t.Fatalf("KillWindow() error = %v", err)
	}
	if backend.WindowExists(conptyTestSession) {
		t.Fatal("WindowExists() = true after KillWindow, want false")
	}

	// The process must terminate shortly after the kill.
	event, err := windows.WaitForSingleObject(handle, 5000)
	if err != nil {
		t.Fatalf("WaitForSingleObject() error = %v", err)
	}
	if event != windows.WAIT_OBJECT_0 {
		t.Fatalf("process did not exit after KillWindow (wait result = 0x%x)", event)
	}
}

func TestConptyBackendSendInput(t *testing.T) {
	backend := newConptyBackend()
	if !backend.HasTmux() {
		t.Skip("ConPTY is not available on this Windows version")
	}
	// findstr with no match echoes typed lines back until EOF/Ctrl-C.
	_, _ = startConptyTestTask(t, backend, `cmd.exe /c "echo TYPE_NOW & findstr ."`)
	waitForCapture(t, backend, conptyTestSession, "TYPE_NOW")

	if err := backend.SendKeys(conptyTestSession, "ECHOED_INPUT"); err != nil {
		t.Fatalf("SendKeys() error = %v", err)
	}
	waitForCapture(t, backend, conptyTestSession, "ECHOED_INPUT")
}

func TestControlKeySequence(t *testing.T) {
	if seq, ok := controlKeySequence("C-c"); !ok || len(seq) != 1 || seq[0] != 0x03 {
		t.Fatalf("controlKeySequence(C-c) = %v, %v; want [3], true", seq, ok)
	}
	if _, ok := controlKeySequence("C-x"); ok {
		t.Fatal("controlKeySequence(C-x) ok = true, want false for unsupported key")
	}
}

func TestTailLines(t *testing.T) {
	input := "a\nb\nc\nd"
	if got := tailLines(input, 2); got != "c\nd" {
		t.Fatalf("tailLines(2) = %q, want %q", got, "c\nd")
	}
	if got := tailLines(input, 0); got != input {
		t.Fatalf("tailLines(0) = %q, want full input", got)
	}
	if got := tailLines(input, 10); got != input {
		t.Fatalf("tailLines(10) = %q, want full input", got)
	}
}
