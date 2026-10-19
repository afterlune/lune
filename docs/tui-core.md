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

- **User Questions (`ask_user`)**: Interactive card in the transcript with radio-style choices and free text. Navigated via `Up`/`Down`, confirmed via `Enter`. The complete batch remains visible while Core accepts the answers; failures retain choices and notes for retry. Exact wait identities and task-attempt generations prevent delayed pause notifications from reopening an answered batch.
- **Agent Status**: The footer shows `disabled` when effective delegation limits prohibit children; otherwise it shows active workers and scheduling concurrency (`parallel`). `/status` and `/context` distinguish parallel capacity, cumulative children per agent, and recursion depth. `maxParallelAgents: 0` inherits the scheduler default, while `maxTeamChildrenPerAgent: 0` with no positive host/task override disables delegation.
- **Tool Execution Records**: Chronological tool cards with 3-line preview, toggled between compact and expanded via `Ctrl+T` or mouse click.
- **Tool Approvals**: Interactive card when permission mode requires user consent before running shell commands or modifying files.
- **Plan Review**: Interactive card triggered when a `<proposed_plan>` completes in Plan mode, offering same-context or new-context implementation. Approved plans remain archived; execution status comes from Core, without inferred step completion.
- **Model Recovery (`ActiveRecovery`)**: Interactive amber card for model rate limits or timeouts, offering retry (`Enter`) or discard (`Esc`).
- **Keyboard Shortcuts**: Cheat sheet modal (`?` or `/shortcuts`) with a 3-column layout (Compose, Session, Transcript).
- **Session Management**: Full lifecycle support with `/sessions`, `/open <prefix>`, `/rewind [N]`, `/clear`, and `/title [name]`.
- **Queued Turns**: Continuous prompt entry while tasks are running, with FIFO queued cards and `/cancel-queue`.
- **Workspace Diff**: Read-only colored git diff output via `/diff [path]`. Git writes use Core tools or an external Git client; there are no `/commit` or `/push` commands.

## Permissions, Modes, and Themes

- Four permission modes: `read-only`, `workspace` (default), `ask`, `full-access`. Cycled via `Shift+Tab` or set via `/permissions <mode>`.
- Plan mode (`/plan`): Locks permissions to `read-only` while planning. Restores previous permission mode when implementation is chosen.
- Theme Adaptability (`/theme`): Native background color negotiation via Bubble Tea v2 `tea.RequestBackgroundColor` and `tea.BackgroundColorMsg`, with manual override (`dark`, `light`, `auto`).

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
