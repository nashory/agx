package discord

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/nashory/agx/internal/config"
)

type Lock struct {
	file *os.File
	path string
}

func AcquireLock(mode string) (*Lock, error) {
	path := filepath.Join(config.ConfigDir(), "discord.lock")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		existing, _ := os.ReadFile(path)
		_ = file.Close()
		return nil, fmt.Errorf("discord bridge already running: %s", string(existing))
	}
	if err := file.Truncate(0); err != nil {
		_ = file.Close()
		return nil, err
	}
	if _, err := file.Seek(0, 0); err != nil {
		_ = file.Close()
		return nil, err
	}
	if _, err := fmt.Fprintf(file, "pid=%d mode=%s started=%s\n", os.Getpid(), mode, time.Now().Format(time.RFC3339)); err != nil {
		_ = file.Close()
		return nil, err
	}
	return &Lock{file: file, path: path}, nil
}

func (l *Lock) Release() error {
	if l == nil || l.file == nil {
		return nil
	}
	err := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	if closeErr := l.file.Close(); err == nil {
		err = closeErr
	}
	l.file = nil
	return err
}

func (l *Lock) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}
