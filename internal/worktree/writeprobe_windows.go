//go:build windows

package worktree

import "os"

// probeChildWritable checks that path is writable. Native Windows has no
// TCC-style parent/child access divergence, so a direct write test in this
// process is equivalent to a child's, and it avoids cmd.exe redirection/quoting
// pitfalls. If a policy such as Controlled Folder Access blocks writes, this
// fails just as a spawned agent would.
func probeChildWritable(path string) error {
	f, err := os.CreateTemp(path, ".agx-write-test-*")
	if err != nil {
		return err
	}
	name := f.Name()
	_ = f.Close()
	return os.Remove(name)
}
