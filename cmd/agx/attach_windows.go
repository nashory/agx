//go:build windows

package main

import (
	"fmt"

	agxruntime "github.com/nashory/agx/internal/runtime"
)

// attachToTaskSession reports that interactive attach is not yet available on
// native Windows. Interactive attach over ConPTY is deferred; use task logs or
// the Discord task channel to view output instead.
func attachToTaskSession(project agxruntime.Project, windowName string) error {
	return fmt.Errorf("interactive attach is not available on native Windows yet; use `agx task logs <id>` or the Discord task channel to view output")
}
