package runtime

import (
	"errors"
	"fmt"
	"strings"

	"github.com/nashory/agx/internal/db"
)

func parseWorkspaceMode(value string) (db.WorkspaceMode, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return db.WorkspaceModeWorktree, nil
	}
	mode, err := db.ParseWorkspaceMode(value)
	if err != nil {
		return "", err
	}
	return mode, nil
}

func normalizeWorkspaceMode(mode db.WorkspaceMode) db.WorkspaceMode {
	if mode == "" {
		return db.WorkspaceModeWorktree
	}
	return mode
}

func (s *Service) ensureProjectWorkspaceAvailable(projectID string, mode db.WorkspaceMode) error {
	if normalizeWorkspaceMode(mode) != db.WorkspaceModeProject {
		return nil
	}
	task, err := s.store.ActiveProjectWorkspaceTask(projectID)
	if errors.Is(err, db.ErrTaskNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("another project-mode task is already active for this project: %s", task.ID)
}
