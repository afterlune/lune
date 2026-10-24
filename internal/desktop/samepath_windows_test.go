//go:build windows

package desktop

import "testing"

// TestSamePathIgnoresCaseOnWindows pins the Windows comparison: workspace paths
// are case insensitive, so two spellings must resolve to one workspace.
func TestSamePathIgnoresCaseOnWindows(t *testing.T) {
	if !samePath(`C:\Work\App`, `c:\work\app`) {
		t.Fatal("Windows paths must compare case insensitively")
	}
	if !samePath(`C:\Work\App\`, `C:\Work\App`) {
		t.Fatal("cleaning must still match the same directory")
	}
	if samePath(`C:\Work\App`, `C:\Work\Other`) {
		t.Fatal("different directories must not match")
	}
}
