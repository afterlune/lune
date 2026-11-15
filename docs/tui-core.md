# Core-backed TUI

The command entry point delegates process setup to `internal/app`. The app selects either the line-oriented CLI (`internal/cli`) or the Bubble Tea interface (`internal/tui`), creates one `luneCore` client, and closes it after the selected frontend exits. Both frontends are hosts for `luneCore`; neither runs an agent loop or owns tool execution.

For detailed interface design, layout, keyboard shortcuts, and interaction life-cycle, see the root specification [TUI.md](../TUI.md).

## Runtime boundary

- `internal/tui` uses the `luneCore` client for workspace and session management.
- `SessionRuntime` handles turn submission, waiting, events, interruption, recovery, and user decisions for one session.
- `Run` starts a draft session: launching the TUI or running `/new` persists nothing, and the first submitted message creates the Core session (applying deferred title/model/effort choices) before its runtime is opened. Core events are then projected into the Bubble Tea view.
- The line-oriented CLI owns prompt input, commands, keyboard interruption, and approval prompts in `internal/cli`; it uses the same session runtime API.
- `internal/tui` remains one package because its update loop and views share private model state; source files are split by command domain, runtime events, model state, transcript rendering, panels, and modals.

The interactive TUI owns the terminal's alternate screen so its transcript, composer, and status remain in fixed positions.

## State ownership

`tuiModel` keeps only cross-cutting state (session identity, task run state, task cards). Everything else lives in a value type with one writer, so a change never has to chase the same number through several files:

| Owner | File | Holds |
| --- | --- | --- |
| `sessionUsage` | `internal/tui/usage.go` | Session totals, context window, last main-agent request, compaction markers, generation speed |
| `viewCache` | `internal/tui/view_cache.go` | Rendered entry blocks, line ranges, and the two stale markers (`cachedDirty` needs a re-render, `transcriptDirty` only needs a re-compose) |
| `modalState` | `internal/tui/modal_state.go` | Every overlay (shortcuts, agents, sessions, memory, MCP, theme/model/permission/effort pickers, process, pager, diff) with `active()`, `open()` and `close()` |
| `composerState` | `internal/tui/composer_state.go` | Textarea, autocomplete popup, `@` file scanner, prompt history, selection and copy feedback |
| `runState` | `internal/tui/run_state.go` | Running flags, Core task snapshot, queue, waiting bookkeeping, streaming buffers, restore freeze |

Rules that follow from this split:

- Usage numbers are written only through `sessionUsage` methods (`applyTotals`, `applyLeadRequest`, `addEventUsage`, `setContextWindow`, `setSpeed`, `beginCompaction`, `finishCompaction`, `resetCompaction`, `applyCompactResult`); `loadSessionSnapshot` and `refreshSessionUsageSnapshot` share them so a restored session renders a final footer in one pass.
- At most one overlay is open. Opening an overlay calls `modalState.open()`, which closes the previous one, so key routing (`handleKeyPress`) and layout (`overlaySection`) can never disagree.
- `overlaySection` is the single ordered list of overlays and task cards; it returns both the rendered text and the reserved height, so the layout can no longer measure one panel and draw another.
- The effort picker is owned by `tuiModel` rather than `modalState`, and a running MCP authentication is a status panel: both are rendered inside the same order but never take the keyboard away from a pending question or approval.
- Slash command metadata (name, help text, target frontend, session requirement, availability during a task) lives in `internal/slashcmd`; both frontends render their tables from it and keep only dispatch and subcommand rules locally.

## Questions, Approvals, and Execution Records

- **User Questions (`ask_user`)**: Interactive card above the composer with radio-style choices and free text. `Up`/`Down` selects options; `Left`/`Right` browses questions without wrapping or confirming them, retaining each question's selection, custom answer and notes. `Enter` confirms the current answer and advances to the next unconfirmed question; editing a confirmed answer requires confirmation again. Answers are submitted together in question order only after every question is confirmed. The complete batch remains visible while Core accepts the answers; failures retain drafts for retry. Exact wait identities and task-attempt generations prevent delayed pause notifications from reopening an answered batch.
- **Agent Status**: The footer shows `disabled` when effective delegation limits prohibit children; otherwise it shows active workers and scheduling concurrency (`parallel`). `/status` and `/context` distinguish parallel capacity, cumulative children per agent, and recursion depth. `runtime.maxParallelAgents: 0` inherits the scheduler default, while `runtime.maxTeamChildrenPerAgent: 0` with no positive host/task override disables delegation.
- **Tool Execution Records**: Chronological tool cards and every step inside a live or manually expanded tool round show a 3-line preview and a live elapsed badge while a step runs. Successful output previews its first 3 lines; running or failed output previews its last 3 lines so live progress and error summaries stay visible. Click a card or step to expand the full output inline, or run `/detail` to open the full-output pager. An expanded round also ends with a `⌃ Show less` row that collapses the block from the bottom, while `Ctrl+O` expands or collapses every round in the transcript without touching per-step expansion. File edits preview their first 3 diff rows and expand to the complete diff.
- **Model Narration vs Reasoning**: Mid-turn model text renders as primary-color prose behind a bold `•` marker, matching the final answer, while reasoning stays muted italic (`Thought for Xs`, folded by default). Only registered slash commands are dispatched; any other input that merely starts with `/` (for example a pasted absolute path) is submitted as a normal prompt.
- **Live Transcript**: Assistant, reasoning and subagent output stays in the main scrollable transcript while streaming; it does not occupy a separate fixed-height band. While the view follows the bottom, the live block is rendered from the retained tail (last 32 source lines) and stable historical lines and hit ranges are cached independently, and the viewport is handed one screenful at a time, so a long reply does not re-render its whole Markdown for every token; scrolling, selecting, reaching a proposed plan or finishing the turn materialises the complete message. Tool rounds belonging to the running execution remain expanded until that execution ends, unless the user collapses them.
- **Long-task Observability**: The terminal title carries the task state as plain words (`running`, `waiting`, `paused`) instead of icons, so background work stays visible from the tab strip. The scrolled-away pill, the footer status badges, the pinned queue band and the checkpoint banner use plain words too: colored emoji stay out of the terminal canvas, while typographic symbols such as `✓`, `!` and `↓` remain. A terminal bell rings once when a decision card appears and once when a turn that ran at least 10 seconds finishes; the scrolled-away pill also shows the running, paused, finished, failed, or cancelled state.
- **Tool Approvals**: Interactive card when permission mode requires user consent before running shell commands or modifying files.
- **Plan Review**: A completed, concrete `<proposed_plan>` can offer same-context or new-context implementation in either normal or Plan mode. The full plan appears inline with the surrounding assistant text, in the same order during streaming, completion and history restoration; full-width rules separate user turns only. Empty or ellipsis-only placeholders do not open review controls. Review choices wrap to fit the terminal. Implementation uses the currently selected permissions and exits Plan mode. Approved plans remain archived; execution status comes from Core, without inferred step completion.
- **Model Recovery (`ActiveRecovery`)**: Interactive amber card for model rate limits or timeouts, offering retry (`Enter`) or discard (`Esc`).
- **Keyboard Shortcuts**: Cheat sheet modal (`?` or `/shortcuts`) with a 3-column layout (Compose, Session, Transcript).
- **Session Management**: Full lifecycle support with `/sessions`, `/resume [prefix]`, `/rewind [N]`, `/clear`, and `/title [name]`. `/resume` switches sessions without starting a model turn. After interrupting a task, send a follow-up message to start a new turn from the conversation; the saved paused checkpoint is discarded rather than resumed directly. `/new` returns to an unsaved draft session, and session-scoped commands report `No session yet — send a message first.` until the first message creates it. Switching sessions renders the stored snapshot immediately; the durable-event replay only enriches tool and subagent details — task state, usage, approvals and questions come from the snapshot plus Core's `State`/`PendingRequests`, so the footer metrics never churn — and the transcript re-renders once when the replay settles: Core's replay-complete event ends the freeze, and a watchdog settles a replay that has received nothing for three seconds so a missing completion event cannot leave the view frozen.
- **Context Compaction**: Responses providers use server compaction by default; set `serverCompaction: false` to opt out. Older Lune configs that omit the field are migrated to `true`. `/compact` uses the provider's standalone Responses compaction endpoint; if it explicitly reports the endpoint as unsupported, compaction falls back locally. Automatic compaction sends `context_management` and switches to local compaction when the provider rejects or ignores it at the configured threshold. Local compaction preserves a recent whole-turn tail sized from the current request and capped at 16% of the usable context window, summarizes oversized recent tool results by call ID, and replaces older history with a structured handoff summary. If the summary misses its target but still reduces the request below the hard context budget, Core keeps that best result and reports the target miss separately from a server capability fallback. While manual compaction runs the footer reads `Context: compacting…/<window>`, the terminal title shows `compacting · `, the scroll pill shows `Compacting <dur>`, a second `/compact` or a new message is refused, and `Esc` does not cancel. CLI and TUI report whether manual compaction used the provider or the local summary; provider results do not claim an after-token count that the endpoint did not return.
- **Queued Turns**: A follow-up entered during an active task is offered to Core as a steering input and injected after the current tool-call round, before the next model request. If a final response is already underway, it completes first; the follow-up is then injected before Core finalizes the task, so Plan Review waits for the final follow-up response. Queued cards stay pinned above the composer until injection, and `/cancel-queue` can remove a waiting input before that point. A submission that races task completion falls back to a separate FIFO turn.
- **Workspace Diff**: Read-only colored Git diff modal via `/diff [path]`. It reads unstaged changes and falls back to staged changes when no unstaged diff is available. Git writes use Core tools or an external Git client; there are no `/commit` or `/push` commands.

## Permissions, Modes, and Themes

- Four permission modes: `read-only`, `workspace`, `auto`, `full-access`. CLI/TUI launches default to `full-access`. Open the selector with `/permissions` or set one directly with `/permissions <mode>`; `Shift+Tab` toggles Plan mode instead of cycling permissions.
- `read-only` permits workspace reads and can approve external-directory reads; `workspace` permits workspace reads/writes and asks for external paths, commands, and web; `auto` builds on `workspace` and adds model auto-review, so safe operations run without a prompt while risky operations or a failed review require approval (each auto-review authorizes one invocation only); `full-access` permits registered tools except the permanent extreme-danger command denylist. External file grants are per-session, cover a directory subtree with read or read/write access, survive reopening that session, and do not apply to shell commands.
- Plan mode (`/plan` or `Shift+Tab`) permits only read-only tools. The active permission mode independently governs approval for external reads and read-only network tools; `/permissions` remains available while planning. Exiting Plan mode or choosing implementation keeps the selected permission mode.
- Theme Adaptability (`/theme`): Native background color negotiation via Bubble Tea v2 `tea.RequestBackgroundColor` and `tea.BackgroundColorMsg`, with manual override (`dark`, `light`, `auto`).

## Build and Run

Run the command entry point:

```powershell
go run -trimpath ./cmd
```

Build the native executable and Linux amd64/arm64 executables and sandbox helpers:

```powershell
./scripts/build.ps1
```

The Linux targets are written to `bin/linux-amd64/` and `bin/linux-arm64/`. A direct `go build -trimpath` command still builds only the selected Go target.

## Verify

```powershell
go test -trimpath ./...
```


## Cache diagnostics

The footer's `Cache: … session` is the weighted cumulative cache rate for all usage in the session, while tool categories accumulate across turns and restarts. `/context` shows the latest main-agent request's cached and uncached input, request ID, attempt and locally observed prompt changes. Local changes describe request mutations, not provider cache eviction reasons. Plan and permission toggles still filter visible tools by metadata and may change the tool declarations sent to the provider.

Core keeps fixed rules in Instructions and appends changing runtime guidance as model-only history snapshots. These snapshots never appear as user messages in the transcript. Permissions and approvals remain enforced by Core. The sibling `luneCore` repository includes an opt-in API smoke test (`internal/runtime/cache_api_smoke_test.go`) that runs in an isolated workspace and never copies credentials into reports.
