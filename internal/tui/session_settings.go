package tui

import (
	"context"

	core "github.com/afterlune/luneCore"
)

func applySessionModelProfile(ctx context.Context, client *core.Core, sessionID, profile string) error {
	saved, err := client.GetSession(ctx, sessionID)
	if err != nil {
		return err
	}
	settings := saved.Settings
	settings.ModelProfile = profile
	return client.SetSessionSettings(ctx, sessionID, settings)
}
