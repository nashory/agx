//go:build linux

package runtime

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const systemdUnitName = RuntimeServiceLabel + ".service"

type systemdUserServiceManager struct{}

func CurrentRuntimeServiceManager() RuntimeServiceManager {
	return systemdUserServiceManager{}
}

func (systemdUserServiceManager) Name() string {
	return "systemd"
}

func SystemdUserUnitPath() (string, error) {
	home, err := systemdHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "systemd", "user", systemdUnitName), nil
}

func systemdHomeDir() (string, error) {
	if home := os.Getenv("HOME"); filepath.IsAbs(home) {
		return home, nil
	}
	return os.UserHomeDir()
}

func RenderSystemdUserUnit(executable, envPath, home, configDir string) ([]byte, error) {
	if strings.TrimSpace(executable) == "" {
		return nil, fmt.Errorf("agx path is required")
	}
	var buf bytes.Buffer
	buf.WriteString("[Unit]\n")
	buf.WriteString("Description=AGX Runtime\n\n")
	buf.WriteString("[Service]\n")
	buf.WriteString("Type=simple\n")
	buf.WriteString("ExecStart=")
	buf.WriteString(systemdQuote(executable))
	buf.WriteString(" runtime start\n")
	buf.WriteString("Restart=always\n")
	buf.WriteString("RestartSec=2\n")
	if envPath != "" {
		buf.WriteString("Environment=")
		buf.WriteString(systemdQuote("PATH=" + envPath))
		buf.WriteByte('\n')
	}
	if home != "" {
		buf.WriteString("Environment=")
		buf.WriteString(systemdQuote("HOME=" + home))
		buf.WriteByte('\n')
	}
	if configDir != "" {
		buf.WriteString("Environment=")
		buf.WriteString(systemdQuote("AGX_CONFIG_DIR=" + configDir))
		buf.WriteByte('\n')
	}
	buf.WriteString("\n[Install]\n")
	buf.WriteString("WantedBy=default.target\n")
	return buf.Bytes(), nil
}

func systemdQuote(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	return `"` + replacer.Replace(value) + `"`
}

func (systemdUserServiceManager) Install(ctx context.Context, executable string, noStart bool) (string, error) {
	unitPath, err := SystemdUserUnitPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(unitPath), 0o700); err != nil {
		return "", err
	}
	stdoutPath, _ := RuntimeLogPaths()
	if err := os.MkdirAll(filepath.Dir(stdoutPath), 0o700); err != nil {
		return "", err
	}
	home, _ := systemdHomeDir()
	unit, err := RenderSystemdUserUnit(executable, os.Getenv("PATH"), home, DefaultPaths().ConfigDir)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(unitPath, unit, 0o600); err != nil {
		return "", err
	}
	if err := systemctlUser(ctx, "daemon-reload"); err != nil {
		return "", err
	}
	if noStart {
		return fmt.Sprintf("installed %s", unitPath), nil
	}
	if err := systemctlUser(ctx, "enable", systemdUnitName); err != nil {
		return "", err
	}
	if err := systemctlUser(ctx, "restart", systemdUnitName); err != nil {
		return "", err
	}
	return "started AGX runtime service", nil
}

func (systemdUserServiceManager) Uninstall(ctx context.Context) (string, error) {
	unitPath, err := SystemdUserUnitPath()
	if err != nil {
		return "", err
	}
	_ = systemctlUser(ctx, "stop", systemdUnitName)
	_ = systemctlUser(ctx, "disable", systemdUnitName)
	if err := os.Remove(unitPath); err != nil && !os.IsNotExist(err) {
		return "", err
	}
	_ = systemctlUser(ctx, "daemon-reload")
	return fmt.Sprintf("uninstalled %s", unitPath), nil
}

func (systemdUserServiceManager) Status(ctx context.Context) RuntimeServiceStatus {
	status := RuntimeServiceStatus{
		Manager:   "systemd",
		PathLabel: "systemd unit",
		State:     "missing",
	}
	unitPath, err := SystemdUserUnitPath()
	if err != nil {
		status.State = "unavailable"
		status.Detail = err.Error()
		return status
	}
	status.Path = unitPath
	if _, err := os.Stat(unitPath); err == nil {
		status.State = "installed"
	} else if !os.IsNotExist(err) {
		status.State = "unknown"
		status.Detail = err.Error()
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		status.Detail = "systemctl not found"
		return status
	}
	out, err := exec.CommandContext(ctx, "systemctl", "--user", "is-active", systemdUnitName).CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err == nil && text == "active" {
		status.State = "active"
		return status
	}
	if text != "" {
		status.Detail = text
	}
	return status
}

func systemctlUser(ctx context.Context, args ...string) error {
	fullArgs := append([]string{"--user"}, args...)
	cmd := exec.CommandContext(ctx, "systemctl", fullArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl %v: %w: %s", fullArgs, err, strings.TrimSpace(string(out)))
	}
	return nil
}
