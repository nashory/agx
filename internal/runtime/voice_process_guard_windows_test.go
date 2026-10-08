//go:build windows

package runtime

import (
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestWhisperProcessGuardKillsProcessOnClose(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestWhisperProcessGuardHelper$")
	cmd.Env = append(os.Environ(), "AGX_WHISPER_GUARD_HELPER=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	})
	guard, err := guardWhisperServerProcess(cmd.Process)
	if err != nil {
		t.Fatal(err)
	}
	if err := guard.Close(); err != nil {
		t.Fatal(err)
	}
	if err := guard.Close(); err != nil {
		t.Fatalf("second Close() = %v, want nil", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("guard close did not terminate child process")
	}
}

func TestWhisperProcessGuardHelper(t *testing.T) {
	if os.Getenv("AGX_WHISPER_GUARD_HELPER") != "1" {
		return
	}
	time.Sleep(30 * time.Second)
}
