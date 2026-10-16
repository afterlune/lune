package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	core "github.com/afterlune/luneCore"
)

// Run starts the Codex-styled Bubble Tea TUI.
func Run(client *core.Core) error {
	ctx := context.Background()

	// 1. Load latest session or create a fresh one
	saved, found, err := client.LatestSession(ctx)
	if err != nil {
		return fmt.Errorf("load latest session: %w", err)
	}
	if !found {
		saved, err = client.CreateSession(ctx, json.RawMessage("null"))
		if err != nil {
			return fmt.Errorf("create initial session: %w", err)
		}
	}

	// 2. Open session runtime
	runtime, err := client.OpenSessionRuntime(ctx, saved.ID)
	if err != nil {
		return fmt.Errorf("open session runtime: %w", err)
	}

	// 3. Initialize model
	model := newTUIModel(client, runtime, saved.ID)
	model.sessionTitle = saved.Title
	if len(saved.History) > 0 {
		model.entries = transcriptFromHistory(saved.History)
	}

	state, err := runtime.State(ctx)
	if err == nil && (state.Task.Status == core.TaskRunning || state.Task.Status == core.TaskQueued) {
		model.running = true
		model.runningExecution = state.Task.ExecutionID
	}

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
