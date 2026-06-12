package runtime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type ResetOptions struct {
	IncludeConfig bool
}

type ResetResult struct {
	Removed []string `json:"removed"`
}

func ResetState(ctx context.Context, opts ResetOptions) (ResetResult, error) {
	paths := DefaultPaths()
	client := NewClient()
	if err := client.Shutdown(ctx); err == nil {
		if err := waitForSocketRemoval(ctx, paths.Socket); err != nil {
			return ResetResult{}, err
		}
	}
	lock, err := AcquireLock(paths.Lock)
	if err != nil {
		return ResetResult{}, fmt.Errorf("runtime still appears to be running; stop it before reset: %w", err)
	}
	_ = lock.Release()

	targets := []string{
		filepath.Join(paths.ConfigDir, "agx.db"),
		filepath.Join(paths.ConfigDir, "agx.db-shm"),
		filepath.Join(paths.ConfigDir, "agx.db-wal"),
		filepath.Join(paths.ConfigDir, "discord.lock"),
		filepath.Join(paths.ConfigDir, "streams"),
		filepath.Join(paths.ConfigDir, "worktrees"),
		attachmentRoot(paths),
		paths.Socket,
		paths.Lock,
	}
	if opts.IncludeConfig {
		targets = append(targets, filepath.Join(paths.ConfigDir, "config.toml"))
	}
	var result ResetResult
	for _, target := range targets {
		if _, err := os.Lstat(target); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return result, err
		}
		if err := os.RemoveAll(target); err != nil {
			return result, fmt.Errorf("remove %s: %w", target, err)
		}
		result.Removed = append(result.Removed, target)
	}
	if err := os.MkdirAll(paths.ConfigDir, 0o700); err != nil {
		return result, fmt.Errorf("create config dir: %w", err)
	}
	if err := os.Chmod(paths.ConfigDir, 0o700); err != nil {
		return result, fmt.Errorf("chmod config dir: %w", err)
	}
	return result, nil
}

func waitForSocketRemoval(ctx context.Context, path string) error {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for runtime socket removal: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}
