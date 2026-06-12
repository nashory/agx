//go:build darwin

package runtime

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectAccessRepairCommandCoversProjectAndAncestors(t *testing.T) {
	projectPath := filepath.Join(string(filepath.Separator), "tmp", "agx-public", "repo")
	command := projectAccessRepairCommand(projectPath)
	for _, want := range []string{
		"/usr/bin/xattr -cr '/tmp/agx-public/repo'",
		"/usr/bin/xattr -d com.apple.provenance '/tmp/agx-public'",
	} {
		if !strings.Contains(command, want) {
			t.Fatalf("projectAccessRepairCommand() = %q, missing %q", command, want)
		}
	}
}
