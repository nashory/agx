//go:build !windows

package runtime

import "fmt"

// RunAsWindowsService is only meaningful on native Windows; on other platforms it
// reports that the runtime is not running under a Windows service.
func RunAsWindowsService(version string) error {
	return fmt.Errorf("the Windows service runner is only available on native Windows")
}
