//go:build windows

package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// windowsServiceManager installs the runtime as a Windows service via the Service
// Control Manager.
//
// Note: the service runs under its configured account (LocalSystem by default),
// which has a different profile than the interactive user. The installer pins the
// service to the installing user's AGX config directory via --config-dir so the
// service and the user's CLI share runtime state; operators may still prefer to
// reconfigure the service to run under their own account.
type windowsServiceManager struct{}

func CurrentRuntimeServiceManager() RuntimeServiceManager {
	return windowsServiceManager{}
}

func (windowsServiceManager) Name() string {
	return "windows-service"
}

func (windowsServiceManager) Install(ctx context.Context, executable string, noStart bool) (string, error) {
	if strings.TrimSpace(executable) == "" {
		return "", fmt.Errorf("agx path is required")
	}
	manager, err := mgr.Connect()
	if err != nil {
		return "", fmt.Errorf("connect to service manager (run as Administrator): %w", err)
	}
	defer manager.Disconnect()

	// Remove any existing service so the binary path and arguments are refreshed.
	if existing, err := manager.OpenService(windowsServiceName); err == nil {
		_, _ = existing.Control(svc.Stop)
		_ = existing.Delete()
		existing.Close()
		waitForServiceDeletion(manager)
	}

	args := []string{"runtime", "service-run", "--config-dir", DefaultPaths().ConfigDir}
	service, err := manager.CreateService(windowsServiceName, executable, mgr.Config{
		DisplayName: "AGX Runtime",
		Description: "AGX runtime daemon",
		StartType:   mgr.StartAutomatic,
	}, args...)
	if err != nil {
		return "", fmt.Errorf("create runtime service: %w", err)
	}
	defer service.Close()

	if noStart {
		return fmt.Sprintf("installed %s", windowsServiceName), nil
	}
	if err := service.Start(); err != nil {
		return "", fmt.Errorf("start runtime service: %w", err)
	}
	return "started AGX runtime service", nil
}

func (windowsServiceManager) Uninstall(ctx context.Context) (string, error) {
	manager, err := mgr.Connect()
	if err != nil {
		return "", fmt.Errorf("connect to service manager (run as Administrator): %w", err)
	}
	defer manager.Disconnect()

	service, err := manager.OpenService(windowsServiceName)
	if err != nil {
		return fmt.Sprintf("%s is not installed", windowsServiceName), nil
	}
	defer service.Close()
	_, _ = service.Control(svc.Stop)
	if err := service.Delete(); err != nil {
		return "", fmt.Errorf("delete runtime service: %w", err)
	}
	return fmt.Sprintf("uninstalled %s", windowsServiceName), nil
}

func (windowsServiceManager) Status(ctx context.Context) RuntimeServiceStatus {
	status := RuntimeServiceStatus{
		Manager:   "windows-service",
		PathLabel: "windows service",
		Path:      windowsServiceName,
		State:     "missing",
	}
	manager, err := mgr.Connect()
	if err != nil {
		status.State = "unavailable"
		status.Detail = err.Error()
		return status
	}
	defer manager.Disconnect()

	service, err := manager.OpenService(windowsServiceName)
	if err != nil {
		return status
	}
	defer service.Close()
	status.State = "installed"
	current, err := service.Query()
	if err != nil {
		status.Detail = err.Error()
		return status
	}
	if current.State == svc.Running {
		status.State = "active"
	} else {
		status.Detail = fmt.Sprintf("service state %d", current.State)
	}
	return status
}

// waitForServiceDeletion gives the SCM a moment to finish removing a service
// before a fresh one is created with the same name.
func waitForServiceDeletion(manager *mgr.Mgr) {
	for i := 0; i < 20; i++ {
		service, err := manager.OpenService(windowsServiceName)
		if err != nil {
			return
		}
		service.Close()
		time.Sleep(100 * time.Millisecond)
	}
}
