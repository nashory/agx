//go:build !windows

package main

import (
	"github.com/nashory/agx/internal/display"
	agxruntime "github.com/nashory/agx/internal/runtime"
	"github.com/nashory/agx/internal/tmux"
)

// attachToTaskSession attaches the current terminal to the task's tmux window.
func attachToTaskSession(project agxruntime.Project, windowName string) error {
	sessionName := tmux.SafeSessionName(project.Name + "-" + display.ShortID(project.ID))
	return tmux.NewController().Attach(tmux.Target(sessionName, windowName))
}
