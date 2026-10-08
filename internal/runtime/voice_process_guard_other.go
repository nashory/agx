//go:build !windows

package runtime

import "os"

type noopWhisperProcessGuard struct{}

func guardWhisperServerProcess(*os.Process) (whisperProcessGuard, error) {
	return noopWhisperProcessGuard{}, nil
}

func (noopWhisperProcessGuard) Close() error { return nil }
