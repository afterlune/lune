package settings

import (
	"context"

	core "github.com/afterlune/luneCore"
)

const DefaultReasoningEffort = "max"

type sessionSettingsClient interface {
	GetSession(context.Context, string) (core.Session, error)
	SetSessionSettings(context.Context, string, core.SessionSettings) error
}

// ApplyTerminalDefaults stores a reasoning default only when the user has not set one.
func ApplyTerminalDefaults(ctx context.Context, client sessionSettingsClient, sessionID string) error {
	if client == nil || sessionID == "" {
		return nil
	}
	saved, err := client.GetSession(ctx, sessionID)
	if err != nil {
		return err
	}
	if saved.Settings.ReasoningEffort != "" {
		return nil
	}
	settings := saved.Settings
	settings.ReasoningEffort = DefaultReasoningEffort
	return client.SetSessionSettings(ctx, sessionID, settings)
}
