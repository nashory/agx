package main

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/nashory/agx/internal/db"
)

func findGitRoot(dir string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return filepath.Clean(strings.TrimSpace(string(out))), nil
}

func exitCodeFor(err error) int {
	if err == nil {
		return 0
	}
	msg := err.Error()
	switch {
	case errors.Is(err, db.ErrTaskNotFound):
		return 3
	case errors.Is(err, db.ErrProjectNotFound):
		return 4
	case strings.Contains(msg, "tmux"):
		return 5
	case strings.Contains(msg, "agent"):
		return 2
	default:
		return 1
	}
}

func maskedToken(token string) string {
	if token == "" {
		return "(not set)"
	}
	if len(token) <= 4 {
		return token + "..."
	}
	return token[:4] + "..."
}

func emptyPlaceholder(value string) string {
	if value == "" {
		return "(not set)"
	}
	return value
}

func firstConfiguredUser(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}
