package runtime

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/nashory/agx/internal/worktree"
)

func validateProjectAccess(path string) error {
	for _, target := range projectAccessProbeTargets(path) {
		if err := ensureWritableDirectory(target.Path, target.Label); err != nil {
			return err
		}
	}
	return worktree.ValidateProject(path)
}

func validateOrRepairProjectAccess(path string) error {
	if err := validateProjectAccess(path); err != nil {
		if repairErr := repairProjectAccess(path); repairErr != nil {
			return repairErr
		}
		if err := validateProjectAccess(path); err != nil {
			return err
		}
	}
	return nil
}

func repairProjectAccess(path string) error {
	return repairProjectAccessPlatform(path)
}

type accessProbeTarget struct {
	Path  string
	Label string
}

func projectAccessProbeTargets(projectPath string) []accessProbeTarget {
	targets := []accessProbeTarget{{Path: projectPath, Label: "project directory"}}
	if indexPath, err := gitPath(projectPath, "index"); err == nil && strings.TrimSpace(indexPath) != "" {
		targets = append(targets, accessProbeTarget{Path: filepath.Dir(indexPath), Label: "git index directory"})
	}
	if commonDir, err := gitCommonDir(projectPath); err == nil && strings.TrimSpace(commonDir) != "" {
		targets = append(targets, accessProbeTarget{Path: commonDir, Label: "git common directory"})
	}
	return uniqueAccessProbeTargets(targets)
}

func uniqueAccessProbeTargets(targets []accessProbeTarget) []accessProbeTarget {
	seen := map[string]bool{}
	out := make([]accessProbeTarget, 0, len(targets))
	for _, target := range targets {
		path := filepath.Clean(strings.TrimSpace(target.Path))
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		target.Path = path
		out = append(out, target)
	}
	return out
}

func ensureWritableDirectory(path, label string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("%s is not accessible: %s: %w", label, path, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory: %s", label, path)
	}
	file, err := os.CreateTemp(path, ".agx-write-test-*")
	if err != nil {
		return fmt.Errorf("%s is not writable by the AGX runtime process: %s: %w", label, path, err)
	}
	name := file.Name()
	if closeErr := file.Close(); closeErr != nil {
		_ = os.Remove(name)
		return fmt.Errorf("%s write probe failed: %s: %w", label, path, closeErr)
	}
	if err := os.Remove(name); err != nil {
		return fmt.Errorf("%s cleanup probe failed: %s: %w", label, path, err)
	}
	return nil
}

func gitPath(projectPath, name string) (string, error) {
	out, err := exec.Command("git", "-C", projectPath, "rev-parse", "--git-path", name).Output()
	if err != nil {
		return "", err
	}
	path := strings.TrimSpace(string(out))
	if path == "" {
		return "", nil
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(projectPath, path)
	}
	return filepath.Clean(path), nil
}

func gitCommonDir(projectPath string) (string, error) {
	out, err := exec.Command("git", "-C", projectPath, "rev-parse", "--git-common-dir").Output()
	if err != nil {
		return "", err
	}
	path := strings.TrimSpace(string(out))
	if path == "" {
		return "", nil
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(projectPath, path)
	}
	return filepath.Clean(path), nil
}

func projectAccessRepairTargets(projectPath string) []string {
	targets := []string{projectPath}
	for _, target := range projectAccessProbeTargets(projectPath) {
		targets = append(targets, target.Path)
	}
	return uniqueStrings(targets)
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = filepath.Clean(strings.TrimSpace(value))
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}
