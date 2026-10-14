package tui

import (
	"context"

	core "github.com/afterlune/luneCore"
)

type coreApplication struct{ *core.Core }

func (a coreApplication) OpenSessionRuntime(ctx context.Context, sessionID string) (SessionRuntime, error) {
	return a.Core.OpenSessionRuntime(ctx, sessionID)
}

var _ CoreClient = coreApplication{}
