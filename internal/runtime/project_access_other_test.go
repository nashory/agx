//go:build !darwin

package runtime

import (
	"strings"
	"testing"
)

func TestProjectAccessRepairDoesNotUseMacOSXattr(t *testing.T) {
	err := repairProjectAccess("/path/that/does/not/exist")
	if err == nil {
		t.Fatal("repairProjectAccess() error = nil, want validation error")
	}
	text := err.Error()
	if strings.Contains(text, "xattr") || strings.Contains(text, "osascript") {
		t.Fatalf("repairProjectAccess() = %q, should not use macOS repair tools", text)
	}
	if !strings.Contains(text, "project directory") {
		t.Fatalf("repairProjectAccess() = %q, want project validation error", text)
	}
}
