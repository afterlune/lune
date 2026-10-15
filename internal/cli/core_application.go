package cli

import (
	"context"

	core "github.com/afterlune/luneCore"
)

type coreCLIApplication struct{ *core.Core }

func (a coreCLIApplication) OpenSessionRuntime(ctx context.Context, sessionID string) (cliRuntime, error) {
	return a.Core.OpenSessionRuntime(ctx, sessionID)
}

var _ cliCore = coreCLIApplication{}
