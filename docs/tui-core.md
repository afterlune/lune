# Core-backed TUI

The command entry point delegates process setup to `internal/app`. The app selects either the line-oriented CLI (`internal/cli`) or the Bubble Tea interface (`internal/tui`), creates one `luneCore` client, and closes it after the selected frontend exits. Both frontends are hosts for `luneCore`; neither runs an agent loop or owns tool execution.

## Runtime boundary

- `CoreClient` in `internal/tui/interfaces.go` handles workspace and session management.
- `SessionRuntime` handles turn submission, waiting, events, interruption, recovery, and user decisions for one session.
- `Run` opens the saved session and its runtime, then projects Core events into the Bubble Tea view.
- The line-oriented CLI owns prompt input, commands, keyboard interruption, and approval prompts in `internal/cli`; it uses the same session runtime API.
- `internal/tui` remains one package because its update loop and views share private model state; its source files are grouped by runtime, commands, sessions, activity, and rendering responsibilities.

The interactive TUI owns the terminal's alternate screen so its transcript, composer, and status remain in fixed positions. Conversation history stays available in the session transcript and persists through Core when the TUI exits; it is not copied into native terminal scrollback. Use `/details` to list user messages and `/details <n>` to show one in the transcript again.

## Questions and execution records

Clarification questions occupy a dedicated bottom panel. With at least two suggested answers, Up/Down or number keys select an answer; Enter submits it, and Tab toggles notes. With fewer than two answers, the panel opens directly as a free-text question without a choice list. Esc hides the panel without answering; `/continue` reopens it. PgUp/PgDn scroll long questions. Submission is asynchronous and locked until its result arrives; errors preserve the draft and refresh the current wait identity. The ordinary composer draft is kept separately.

Tool calls appear individually in chronological order. Commands show their actual command text, execution status and exit code; successful file edits show a bounded diff. The default preview contains three lines. F4 focuses the records, Up/Down selects a record, and Enter expands or collapses it. These keys and the question controls can be changed with `/keymap`. Background command polls update the original record instead of creating extra rows.

Core supplies transient tool arguments and structured execution details through `EventToolDetails`; the durable lifecycle log omits these payloads. The TUI strips terminal control sequences before displaying output. Historical records show the information actually saved in the session; missing output is not reconstructed. Model stream events carry request identity and purpose so final responses can reconcile with streamed messages.

Markdown tables use terminal cell widths and wrap inside their columns. Narrow windows display table rows as labelled records instead. Code fences remain literal.

## Modes and themes

`/theme` and `ui.json` support `auto`, `dark`, and `light`. The TUI always inherits the terminal background; auto follows the terminal's queried colors, while dark and light choose the text and accent palette. `Shift+Enter` inserts a newline; Enter submits. `Shift+Tab` and `/permissions <mode>` change the active permission choice outside Plan mode.

Plan mode locks Core's global tool policy to `read-only`, hiding and blocking write and command tools. The TUI remembers the previous permission mode and restores it when Plan mode ends; permission changes are disabled while it is active. Choosing an implementation option in plan review exits Plan mode and restores the previous mode before implementation starts. The current Core API does not expose a per-turn permission intersection, so network tools use Core's read-only approval policy during Plan mode.

## Run and build

Run the command entry point; Go compiles the app, CLI, TUI, and Core adapters through the package dependencies:

```powershell
go run ./cmd
```

When developing corresponding Core changes in the sibling `../luneCore` checkout, use a local workspace:

```powershell
go work init . ../luneCore
```

`go.work` and `go.work.sum` are local development files. Publish the Core revision and update the application's module requirement before releasing a standalone application checkout. The question API now uses labelled options with descriptions and requires `ExecutionID` when resolving an answer; older paused-question checkpoints need to be discarded and the task started again.

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
