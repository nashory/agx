//go:build !windows

package worktree

import (
	"fmt"
	"os/exec"
	"strings"
)

// probeChildWritable checks that a child process spawned by AGX can write to
// path. Using /bin/sh as the child mirrors how AGX runs agents and catches macOS
// TCC cases where the parent can write but children cannot.
func probeChildWritable(path string) error {
	script := `test_path="$1/.agx-write-test-$$"; : > "$test_path" && rm -f "$test_path"`
	output, err := exec.Command("/bin/sh", "-c", script, "agx-write-test", path).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w", strings.TrimSpace(string(output)), err)
	}
	return nil
}
