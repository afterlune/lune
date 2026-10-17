//go:build !windows

package desktop

import "os/exec"

func configureGitCommand(_ *exec.Cmd) {}
