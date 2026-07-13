//go:build windows

package desktop

import (
	"os/exec"
	"testing"

	"golang.org/x/sys/windows"
)

func TestConfigureRuntimeCommandDetachesOnWindows(t *testing.T) {
	cmd := exec.Command("agx.exe", "runtime", "start")
	configureRuntimeCommand(cmd)

	if cmd.SysProcAttr == nil {
		t.Fatal("SysProcAttr = nil, want Windows process attributes")
	}
	flags := uint32(cmd.SysProcAttr.CreationFlags)
	for _, want := range []uint32{windows.DETACHED_PROCESS, windows.CREATE_NEW_PROCESS_GROUP} {
		if flags&want == 0 {
			t.Fatalf("CreationFlags = %#x, missing %#x", flags, want)
		}
	}
}
