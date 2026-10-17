//go:build windows

package desktop

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

func configureGitCommand(command *exec.Cmd) {
	attributes := command.SysProcAttr
	if attributes == nil {
		attributes = &syscall.SysProcAttr{}
	}
	attributes.HideWindow = true
	attributes.CreationFlags |= windows.CREATE_NO_WINDOW
	command.SysProcAttr = attributes
}
