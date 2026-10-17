# Core-backed TUI

The command entry point delegates process setup to `internal/app`. The app selects either the line-oriented CLI (`internal/cli`) or the Bubble Tea interface (`internal/tui`), creates one `luneCore` client, and closes it after the selected frontend exits. Both frontends are hosts for `luneCore`; neither runs an agent loop or owns tool execution.

For detailed interface design, layout, keyboard shortcuts, and interaction life-cycle, see the root specification [TUI.md](../TUI.md).

## Runtime boundary

- `CoreClient` in `internal/tui/interfaces.go` handles workspace and session management.
- `SessionRuntime` handles turn submission, waiting, events, interruption, recovery, and user decisions for one session.
- `Run` creates a clean, fresh session and opens its runtime, then projects Core events into the Bubble Tea view.
- The line-oriented CLI owns prompt input, commands, keyboard interruption, and approval prompts in `internal/cli`; it uses the same session runtime API.
- `internal/tui` remains one package because its update loop and views share private model state; its source files are grouped by runtime, commands, model, view, theme, and update responsibilities.

The interactive TUI owns the terminal's alternate screen so its transcript, composer, and status remain in fixed positions.

## Questions, Approvals, and Execution Records

- **User Questions (`ask_user`)**: Interactive card in the transcript with radio-style choices and free text. Navigated via `Up`/`Down`, confirmed via `Enter`.
- **Tool Execution Records**: Chronological tool cards with 3-line preview, toggled between compact and expanded via `Ctrl+T`.
- **Tool Approvals**: Interactive card when permission mode requires user consent before running shell commands or modifying files.
- **Plan Review**: Interactive card triggered when a `<proposed_plan>` completes in Plan mode, offering same-context or new-branch implementation.
- **Keyboard Shortcuts**: Cheat sheet modal (`?` or `/shortcuts`) with a 3-column layout (Compose, Session, Transcript).

## Permissions and Modes

- Four permission modes: `read-only`, `workspace` (default), `ask`, `full-access`. Cycled via `Shift+Tab` or set via `/permissions <mode>`.
- Plan mode (`/plan`): Locks permissions to `read-only` while planning. Restores previous permission mode when implementation is chosen.

## Build and Run

Run the command entry point:

```powershell
go run ./cmd
```

Build the standalone binary into `bin/`:

```powershell
go build -o bin/lune.exe ./cmd
```

## Verify

```powershell
go test ./...
```
