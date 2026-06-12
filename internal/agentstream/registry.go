package agentstream

import (
	"errors"
	"fmt"
	"strings"
)

type UnsupportedError struct {
	TaskID string
	Agent  string
}

func (e UnsupportedError) Error() string {
	agent := strings.TrimSpace(e.Agent)
	if agent == "" {
		agent = "unknown"
	}
	if strings.TrimSpace(e.TaskID) == "" {
		return fmt.Sprintf("agent %q does not support structured streaming", agent)
	}
	return fmt.Sprintf("task %s agent %q does not support structured streaming", e.TaskID, agent)
}

func IsUnsupported(err error) bool {
	var unsupported UnsupportedError
	return errors.As(err, &unsupported)
}
