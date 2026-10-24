//go:build !windows && !darwin

package desktop

import "testing"

// TestSamePathKeepsCaseOnPosixFilesystems is the regression guard for treating
// the workspace "/w/app" as the already-open "/w/App".
func TestSamePathKeepsCaseOnPosixFilesystems(t *testing.T) {
	if samePath("/w/App", "/w/app") {
		t.Fatal("case-sensitive filesystems must keep the two paths distinct")
	}
	if !samePath("/w/app/", "/w/app") {
		t.Fatal("cleaning must still match the same directory")
	}
	if samePath("/w/app", "/w/other") {
		t.Fatal("different directories must not match")
	}
}
