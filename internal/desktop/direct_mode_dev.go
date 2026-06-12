//go:build !production

package desktop

import "os"

func desktopDirectModeEnabled() bool {
	return os.Getenv("AGX_DESKTOP_DIRECT_TEST") == "1"
}
