package cli

import (
	"bufio"
	"os"

	core "github.com/afterlune/luneCore"
)

// LaunchOptions configures initial CLI session settings.
type LaunchOptions struct {
	SessionID      string
	ModelProfile   string
	PermissionMode core.PermissionMode
	PlanMode       bool
}

// Run starts the interactive streaming CLI with default options.
func Run(client *core.Core) error {
	return RunWithOptions(client, LaunchOptions{})
}

// RunWithOptions starts the interactive streaming CLI with custom launch options.
func RunWithOptions(client *core.Core, opts LaunchOptions) error {
	return runCoreChatLoopWithOptions(coreCLIApplication{Core: client}, bufio.NewReader(os.Stdin), os.Stdout, os.Stderr, startKeyboardInputWatcher, opts)
}
