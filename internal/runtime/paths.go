package runtime

import (
	"path/filepath"

	"github.com/nashory/agx/internal/config"
)

const (
	socketFile = "runtime.sock"
	lockFile   = "runtime.lock"
)

// Paths contains the runtime-owned filesystem locations derived from the AGX
// config directory.
type Paths struct {
	ConfigDir string `json:"configDir"`
	Socket    string `json:"socket"`
	Lock      string `json:"lock"`
}

// DefaultPaths returns the socket and lock paths used by the singleton runtime
// daemon for the current environment.
func DefaultPaths() Paths {
	dir := config.ConfigDir()
	return Paths{
		ConfigDir: dir,
		Socket:    filepath.Join(dir, socketFile),
		Lock:      filepath.Join(dir, lockFile),
	}
}
