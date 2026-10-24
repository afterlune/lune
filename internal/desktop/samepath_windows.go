//go:build windows

package desktop

import (
	"path/filepath"
	"strings"
)

// samePath reports whether two workspace paths point at the same directory.
// Windows filesystems are case insensitive, so the comparison ignores case.
func samePath(left, right string) bool {
	return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
}
