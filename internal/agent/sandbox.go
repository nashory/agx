package agent

import "runtime"

// SandboxDisableArgs returns the flag that disables the OS-level sandbox codex
// and Claude apply in all-mighty mode. The flag name is OS-specific and the
// agent binaries only accept the flag matching the host OS, so passing the wrong
// one is an "unknown option" error. Verified against codex/claude --help:
//
//	macOS   --dangerously-disable-osx-sandbox
//	Windows --dangerously-disable-win-sandbox
//	Linux   --dangerously-disable-linux-sandbox
//
// Platforms without such a sandbox get no flag.
func SandboxDisableArgs() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{"--dangerously-disable-osx-sandbox"}
	case "windows":
		return []string{"--dangerously-disable-win-sandbox"}
	case "linux":
		return []string{"--dangerously-disable-linux-sandbox"}
	default:
		return nil
	}
}
