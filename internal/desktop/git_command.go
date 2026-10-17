package desktop

import (
	"context"
	"os/exec"
)

func newGitCommand(ctx context.Context, args ...string) *exec.Cmd {
	command := exec.CommandContext(ctx, "git", args...)
	configureGitCommand(command)
	return command
}
