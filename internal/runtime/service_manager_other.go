//go:build !darwin && !linux && !windows

package runtime

import (
	"context"
	"fmt"
)

type unsupportedServiceManager struct{}

func CurrentRuntimeServiceManager() RuntimeServiceManager {
	return unsupportedServiceManager{}
}

func (unsupportedServiceManager) Name() string {
	return "unsupported"
}

func (unsupportedServiceManager) Install(context.Context, string, bool) (string, error) {
	return "", fmt.Errorf("runtime service installation is not supported on this platform")
}

func (unsupportedServiceManager) Uninstall(context.Context) (string, error) {
	return "", fmt.Errorf("runtime service installation is not supported on this platform")
}

func (unsupportedServiceManager) Status(context.Context) RuntimeServiceStatus {
	return RuntimeServiceStatus{
		Manager: "unsupported",
		State:   "unsupported",
		Detail:  "runtime service installation is not supported on this platform",
	}
}
