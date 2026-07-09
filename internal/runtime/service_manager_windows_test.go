//go:build windows

package runtime

import (
	"context"
	"testing"
)

func TestCurrentRuntimeServiceManagerIsWindows(t *testing.T) {
	if name := CurrentRuntimeServiceManager().Name(); name != "windows-service" {
		t.Fatalf("Name() = %q, want windows-service", name)
	}
}

// TestWindowsServiceManagerStatusIsSafe verifies Status reports a coherent result
// without requiring the service to be installed or the process to be elevated.
// Install/Uninstall need Administrator rights and a real SCM, so they are covered
// by manual Windows validation.
func TestWindowsServiceManagerStatusIsSafe(t *testing.T) {
	status := CurrentRuntimeServiceManager().Status(context.Background())
	if status.Manager != "windows-service" {
		t.Fatalf("Status().Manager = %q, want windows-service", status.Manager)
	}
	switch status.State {
	case "missing", "installed", "active", "unavailable":
	default:
		t.Fatalf("Status().State = %q, want a known state", status.State)
	}
}
