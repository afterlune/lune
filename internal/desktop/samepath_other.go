//go:build !windows

package desktop

import "path/filepath"

// samePath reports whether two workspace paths point at the same directory.
// POSIX filesystems are case sensitive: /w/App and /w/app are different
// workspaces, and reusing the state of the wrong one is worse than reopening.
func samePath(left, right string) bool {
	return filepath.Clean(left) == filepath.Clean(right)
}
