# Core-backed TUI

The terminal UI is a host for `luneCore`. It does not run an agent loop or own tool execution.

## Runtime boundary

- `CoreClient` in `internal/tui/interfaces.go` handles workspace and session management.
- `SessionRuntime` handles turn submission, waiting, events, interruption, recovery, and user decisions for one session.
- `Run` opens the saved session and its runtime, then projects Core events into the Bubble Tea view.
- The line-oriented CLI uses the same session runtime API.

Stable conversation entries are printed once into native terminal scrollback. The live model response, input, and optional Activity & Usage details are redrawn. Use `/details` to list user messages and `/details <n>` to print one again.

## Modes and themes

`/theme` and `ui.json` support `auto`, `dark`, and `light`. Auto follows the terminal's queried foreground and background colors; dark and light are persistent manual overrides. `Shift+Tab` and `/permissions <mode>` change the active permission choice.

Plan mode is a separate UI state. While it is active, the host sets Core's global tool policy to `read-only` so write and command tools stay hidden and blocked; the TUI keeps the selected permission mode and restores it when Plan mode ends. The current Core API does not expose a per-turn permission intersection, so network tools use Core's read-only approval policy during Plan mode.

## Run and build

Run the whole command package so `main.go` and the Core adapter files are compiled together:

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
