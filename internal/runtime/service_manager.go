package runtime

import (
	"context"
	"path/filepath"
)

const RuntimeServiceLabel = "dev.agx.runtime"

// RuntimeServiceStatus describes the host service manager state for the AGX
// runtime. It is diagnostic metadata only; runtime reachability still comes
// from the Unix socket API.
type RuntimeServiceStatus struct {
	Manager   string
	PathLabel string
	Path      string
	State     string
	Detail    string
}

// RuntimeServiceManager installs, removes, and diagnoses the platform-specific
// user service that can keep the runtime daemon alive.
type RuntimeServiceManager interface {
	Name() string
	Install(ctx context.Context, executable string, noStart bool) (string, error)
	Uninstall(ctx context.Context) (string, error)
	Status(ctx context.Context) RuntimeServiceStatus
}

// RuntimeLogPaths returns the stdout and stderr log paths used by foreground
// starts and platform service managers.
func RuntimeLogPaths() (stdoutPath, stderrPath string) {
	logDir := filepath.Join(DefaultPaths().ConfigDir, "logs")
	return filepath.Join(logDir, "runtime.log"), filepath.Join(logDir, "runtime.err.log")
}
