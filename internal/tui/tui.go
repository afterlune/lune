package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
	core "github.com/afterlune/luneCore"
)

// LaunchOptions configures initial TUI session settings.
type LaunchOptions struct {
	SessionID      string
	ModelProfile   string
	PermissionMode core.PermissionMode
	PlanMode       bool
	Input          io.Reader
	Output         io.Writer
}

// Run starts the LuneAgent Bubble Tea TUI with default options.
func Run(client *core.Core) error {
	return RunWithOptions(client, LaunchOptions{})
}

// RunWithOptions starts the LuneAgent Bubble Tea TUI with custom launch options.
func RunWithOptions(client *core.Core, opts LaunchOptions) error {
	ctx := context.Background()

	var sessionID string
	var sessionTitle string

	if opts.SessionID != "" {
		targetID := ""
		if s, err := client.GetSession(ctx, opts.SessionID); err == nil {
			targetID = s.ID
			sessionTitle = s.Title
		} else {
			sessions, err := client.ListSessions(ctx, 100, false)
			if err != nil {
				return fmt.Errorf("list sessions: %w", err)
			}
			var matches []core.Session
			for _, s := range sessions {
				if strings.HasPrefix(strings.ToLower(s.ID), strings.ToLower(opts.SessionID)) {
					matches = append(matches, s)
				}
			}
			if len(matches) == 0 {
				return fmt.Errorf("session not found: %q", opts.SessionID)
			}
			if len(matches) > 1 {
				return fmt.Errorf("session prefix %q is ambiguous (%d matches)", opts.SessionID, len(matches))
			}
			targetID = matches[0].ID
			sessionTitle = matches[0].Title
		}
		sessionID = targetID
	} else {
		saved, err := client.CreateSession(ctx, json.RawMessage("null"))
		if err != nil {
			return fmt.Errorf("create initial session: %w", err)
		}
		sessionID = saved.ID
		sessionTitle = saved.Title
	}

	// Apply model profile if provided
	if opts.ModelProfile != "" {
		_ = client.SetSessionSettings(ctx, sessionID, core.SessionSettings{
			ModelProfile: opts.ModelProfile,
		})
	}

	// Apply permission mode if provided
	savedMode := client.PermissionMode()
	if opts.PermissionMode != "" {
		if err := client.SetPermissionMode(opts.PermissionMode); err != nil {
			return fmt.Errorf("set permission mode: %w", err)
		}
		savedMode = opts.PermissionMode
	}

	// Apply plan mode if provided
	if opts.PlanMode {
		_ = client.SetPermissionMode(core.PermissionReadOnly)
	}

	// Open session runtime
	runtime, err := client.OpenSessionRuntime(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("open session runtime: %w", err)
	}

	// Initialize model
	in := opts.Input
	if in == nil {
		in = os.Stdin
	}
	out := opts.Output
	if out == nil {
		out = os.Stdout
	}

	model := newTUIModel(client, runtime, sessionID)
	model.output = out
	if sessionTitle != "" {
		model.sessionTitle = sessionTitle
	}
	if opts.PlanMode {
		model.planMode = true
		model.savedMode = savedMode
		model.permissionMode = core.PermissionReadOnly
	}
	if opts.ModelProfile != "" {
		model.currentModel = opts.ModelProfile
	}

	runtime.SetUserInteractionAvailable(true)
	defer runtime.SetUserInteractionAvailable(false)

	// Always restore terminal state (disable mouse tracking, leave alt screen, show cursor, reset attributes)
	// even if an unexpected panic or error occurs during execution.
	defer func() {
		_, _ = io.WriteString(out, "\x1b[?1000l\x1b[?1002l\x1b[?1003l\x1b[?1006l\x1b[?2004l\x1b[?1049l\x1b[?25h\x1b[0m")
	}()

	// Run Bubble Tea v2 program (WithWindowSize provides a fallback size when launched
	// with non-TTY pipes; real TTYs automatically override it via term.GetSize).
	program := tea.NewProgram(
		model,
		tea.WithInput(in),
		tea.WithOutput(out),
		tea.WithWindowSize(120, 36),
	)

	if _, err := program.Run(); err != nil {
		return fmt.Errorf("run TUI: %w", err)
	}
	return nil
}
