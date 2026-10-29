package tui

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	core "github.com/afterlune/luneCore"
)

func TestApplySessionModelProfilePreservesSettingsAndRejectsUnknownProfile(t *testing.T) {
	workspace := t.TempDir()
	config := `{"models":{"providers":{"host":{"protocol":"responses","baseURL":"https://example.invalid","apiKey":"test"}},"profiles":{"primary":{"provider":"host","model":"primary-model"},"fast":{"provider":"host","model":"fast-model"}},"defaultProfile":"primary"},"runtime":{}}`
	if err := os.WriteFile(filepath.Join(workspace, "config.json"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	client, err := core.New(core.Config{Workspace: workspace})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Close(context.Background()); err != nil {
			t.Errorf("close Core: %v", err)
		}
	})

	ctx := context.Background()
	session, err := client.CreateSession(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	window := int64(1_000_000)
	reserve := int64(32_000)
	autoCompact := true
	threshold := 0.82
	settings := core.SessionSettings{
		ModelProfile: "primary", ReasoningEffort: "high",
		ContextWindow: &window, ReserveOutputTokens: &reserve, AutoCompact: &autoCompact, AutoCompactThreshold: &threshold,
	}
	if err := client.SetSessionSettings(ctx, session.ID, settings); err != nil {
		t.Fatal(err)
	}
	if err := applySessionModelProfile(ctx, client, session.ID, "fast"); err != nil {
		t.Fatal(err)
	}
	saved, err := client.GetSession(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := saved.Settings
	if got.ModelProfile != "fast" || got.ReasoningEffort != settings.ReasoningEffort || got.ContextWindow == nil || *got.ContextWindow != window || got.ReserveOutputTokens == nil || *got.ReserveOutputTokens != reserve || got.AutoCompact == nil || !*got.AutoCompact || got.AutoCompactThreshold == nil || *got.AutoCompactThreshold != threshold {
		t.Fatalf("model profile update lost session settings: %+v", got)
	}
	if err := applySessionModelProfile(ctx, client, session.ID, "missing"); err == nil {
		t.Fatal("unknown profile unexpectedly succeeded")
	}
	unchanged, err := client.GetSession(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Settings.ModelProfile != "fast" || unchanged.Settings.ReasoningEffort != settings.ReasoningEffort || unchanged.Settings.ContextWindow == nil || *unchanged.Settings.ContextWindow != window {
		t.Fatalf("failed profile update changed session settings: %+v", unchanged.Settings)
	}
}
