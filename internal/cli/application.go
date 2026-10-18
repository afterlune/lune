package cli

import (
	"bufio"
	"io"
	"os"

	core "github.com/afterlune/luneCore"
)

// LaunchOptions configures initial CLI session settings.
type LaunchOptions struct {
	SessionID      string
	ModelProfile   string
	PermissionMode core.PermissionMode
	PlanMode       bool
	Stdin          io.Reader
	Stdout         io.Writer
	Stderr         io.Writer
}

// Run starts the interactive streaming CLI with default options.
func Run(client *core.Core) error {
	return RunWithOptions(client, LaunchOptions{})
}

// RunWithOptions starts the interactive streaming CLI with custom launch options.
func RunWithOptions(client *core.Core, opts LaunchOptions) error {
	stdin := opts.Stdin
	if stdin == nil {
		stdin = os.Stdin
	}
	stdout := opts.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	stderr := opts.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	return runCoreChatLoopWithOptions(coreCLIApplication{Core: client}, bufio.NewReader(stdin), stdout, stderr, startKeyboardInputWatcher, opts)
}
