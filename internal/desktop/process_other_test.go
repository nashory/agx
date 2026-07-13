//go:build !windows

package desktop

import (
	"os/exec"
	"testing"
)

func TestConfigureRuntimeCommandNoopOffWindows(t *testing.T) {
	cmd := exec.Command("agx", "runtime", "start")
	configureRuntimeCommand(cmd)

	if cmd.SysProcAttr != nil {
		t.Fatalf("SysProcAttr = %#v, want nil", cmd.SysProcAttr)
	}
}
