//go:build darwin

package runtime

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func repairProjectAccessPlatform(path string) error {
	if err := runProjectAccessRepair(path, false); err == nil {
		return nil
	}
	return runProjectAccessRepair(path, true)
}

func projectAccessAncestorTargets(projectPath string) []string {
	home, _ := os.UserHomeDir()
	home = filepath.Clean(home)
	path := filepath.Clean(projectPath)
	var targets []string
	for {
		parent := filepath.Dir(path)
		if parent == path || parent == "." || parent == string(filepath.Separator) {
			break
		}
		targets = append(targets, parent)
		if home != "" && parent == home {
			break
		}
		path = parent
	}
	return uniqueStrings(targets)
}

func runProjectAccessRepair(path string, administrator bool) error {
	command := projectAccessRepairCommand(path)
	if administrator {
		return runShellWithAdministratorPrivileges(command)
	}
	output, err := exec.Command("/bin/sh", "-c", command).CombinedOutput()
	if err != nil {
		return fmt.Errorf("grant project access: %s: %w", strings.TrimSpace(string(output)), err)
	}
	return nil
}

func projectAccessRepairCommand(path string) string {
	var parts []string
	for _, target := range projectAccessRepairTargets(path) {
		parts = append(parts, "/usr/bin/xattr -cr "+shellQuote(target))
	}
	for _, target := range projectAccessAncestorTargets(path) {
		parts = append(parts, "/usr/bin/xattr -d com.apple.provenance "+shellQuote(target)+" 2>/dev/null || true")
	}
	return strings.Join(parts, " && ")
}

func runShellWithAdministratorPrivileges(command string) error {
	script := fmt.Sprintf(`do shell script "%s" with administrator privileges`, appleScriptString(command))
	output, err := exec.Command("osascript", "-e", script).CombinedOutput()
	if err != nil {
		return fmt.Errorf("grant project access with administrator privileges: %s: %w", strings.TrimSpace(string(output)), err)
	}
	return nil
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func appleScriptString(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return value
}
