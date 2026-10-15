# Core-backed TUI

The command entry point delegates process setup to `internal/app`. The app selects either the line-oriented CLI (`internal/cli`) or the Bubble Tea interface (`internal/tui`), creates one `luneCore` client, and closes it after the selected frontend exits. Both frontends are hosts for `luneCore`; neither runs an agent loop or owns tool execution.

## Runtime boundary

- `CoreClient` in `internal/tui/interfaces.go` handles workspace and session management.
- `SessionRuntime` handles turn submission, waiting, events, interruption, recovery, and user decisions for one session.
- `Run` opens the saved session and its runtime, then projects Core events into the Bubble Tea view.
- The line-oriented CLI owns prompt input, commands, keyboard interruption, and approval prompts in `internal/cli`; it uses the same session runtime API.
- `internal/tui` remains one package because its update loop and views share private model state; its source files are grouped by runtime, commands, sessions, activity, and rendering responsibilities.

The interactive TUI owns the terminal's alternate screen so its transcript, composer, and status remain in fixed positions. Conversation history stays available in the session transcript and persists through Core when the TUI exits; it is not copied into native terminal scrollback. Use `/details` to list user messages and `/details <n>` to show one in the transcript again.

## Modes and themes

`/theme` and `ui.json` support `auto`, `dark`, and `light`. The TUI always inherits the terminal background; auto follows the terminal's queried colors, while dark and light choose the text and accent palette. `Shift+Tab` and `/permissions <mode>` change the active permission choice.

Plan mode is a separate UI state. While it is active, the host sets Core's global tool policy to `read-only` so write and command tools stay hidden and blocked; the TUI keeps the selected permission mode and restores it when Plan mode ends. The current Core API does not expose a per-turn permission intersection, so network tools use Core's read-only approval policy during Plan mode.

## Run and build

Run the command entry point; Go compiles the app, CLI, TUI, and Core adapters through the package dependencies:

```powershell
go run ./cmd
```

Build the sandbox helper and standalone executable for the current platform:

```powershell
.\scripts\build.ps1
```

The script places both artifacts in the ignored `bin/` directory. Core uses that helper for sandboxed command execution.

## Verify

```powershell
go test ./...
go build -mod=readonly ./...
```

To refresh the deterministic TUI screenshots:

```powershell
go test ./internal/tui -args -update-tui-screenshots
```
