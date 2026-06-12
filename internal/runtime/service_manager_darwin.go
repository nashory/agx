//go:build darwin

package runtime

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type launchdServiceManager struct{}

func CurrentRuntimeServiceManager() RuntimeServiceManager {
	return launchdServiceManager{}
}

func (launchdServiceManager) Name() string {
	return "launchd"
}

func (launchdServiceManager) Install(ctx context.Context, executable string, noStart bool) (string, error) {
	plistPath, err := LaunchAgentPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o700); err != nil {
		return "", err
	}
	stdoutPath, _ := RuntimeLogPaths()
	if err := os.MkdirAll(filepath.Dir(stdoutPath), 0o700); err != nil {
		return "", err
	}
	plist, err := RenderLaunchAgentPlist(executable, LaunchdEnvironmentPath(os.Getenv("PATH"), executable))
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(plistPath, plist, 0o600); err != nil {
		return "", err
	}
	if noStart {
		return fmt.Sprintf("installed %s", plistPath), nil
	}
	if err := launchctl(ctx, "bootstrap", fmt.Sprintf("gui/%d", os.Getuid()), plistPath); err != nil {
		return "", err
	}
	if err := launchctl(ctx, "kickstart", "-k", fmt.Sprintf("gui/%d/%s", os.Getuid(), LaunchAgentLabel)); err != nil {
		return "", err
	}
	return "started AGX runtime service", nil
}

func (launchdServiceManager) Uninstall(ctx context.Context) (string, error) {
	plistPath, err := LaunchAgentPath()
	if err != nil {
		return "", err
	}
	_ = launchctl(ctx, "bootout", fmt.Sprintf("gui/%d", os.Getuid()), plistPath)
	if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
		return "", err
	}
	return fmt.Sprintf("uninstalled %s", plistPath), nil
}

func (launchdServiceManager) Status(ctx context.Context) RuntimeServiceStatus {
	status := RuntimeServiceStatus{
		Manager:   "launchd",
		PathLabel: "launchd plist",
		State:     "missing",
	}
	plistPath, err := LaunchAgentPath()
	if err != nil {
		status.State = "unavailable"
		status.Detail = err.Error()
		return status
	}
	status.Path = plistPath
	if _, err := os.Stat(plistPath); err == nil {
		status.State = "installed"
	} else if !os.IsNotExist(err) {
		status.State = "unknown"
		status.Detail = err.Error()
	}
	out, err := exec.CommandContext(ctx, "launchctl", "print", fmt.Sprintf("gui/%d/%s", os.Getuid(), LaunchAgentLabel)).CombinedOutput()
	if err != nil {
		text := strings.TrimSpace(string(out))
		if text != "" {
			status.Detail = text
		}
		return status
	}
	status.State = "loaded"
	return status
}

func launchctl(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "launchctl", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("launchctl %v: %w: %s", args, err, strings.TrimSpace(string(out)))
	}
	return nil
}
