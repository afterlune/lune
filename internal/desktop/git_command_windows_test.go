//go:build windows

package desktop

import (
	"context"
	"testing"

	"golang.org/x/sys/windows"
)

func TestNewGitCommandHidesConsoleWindow(t *testing.T) {
	command := newGitCommand(context.Background(), "version")
	attributes := command.SysProcAttr
	if attributes == nil {
		t.Fatal("Git command has no Windows process attributes")
	}
	if !attributes.HideWindow {
		t.Fatal("Git command does not hide its window")
	}
	if attributes.CreationFlags&windows.CREATE_NO_WINDOW == 0 {
		t.Fatal("Git command does not set CREATE_NO_WINDOW")
	}
}
