package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	core "github.com/afterlune/luneCore"
)

// Run starts the LuneAgent Bubble Tea TUI.
func Run(client *core.Core) error {
	ctx := context.Background()

	// 1. Always start a fresh session on launch (do not auto-load previous sessions)
	saved, err := client.CreateSession(ctx, json.RawMessage("null"))
	if err != nil {
		return fmt.Errorf("create initial session: %w", err)
	}

	// 2. Open session runtime
	runtime, err := client.OpenSessionRuntime(ctx, saved.ID)
	if err != nil {
		return fmt.Errorf("open session runtime: %w", err)
	}

	// 3. Initialize model
	model := newTUIModel(client, runtime, saved.ID)
	model.sessionTitle = saved.Title

	runtime.SetUserInteractionAvailable(true)
	defer runtime.SetUserInteractionAvailable(false)

	// 4. Run Bubble Tea v2 program
	program := tea.NewProgram(
		model,
		tea.WithInput(os.Stdin),
		tea.WithOutput(os.Stdout),
	)

	if _, err := program.Run(); err != nil {
		return fmt.Errorf("run TUI: %w", err)
	}
	return nil
}
