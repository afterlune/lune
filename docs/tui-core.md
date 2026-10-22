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

## Questions, Approvals, and Execution Records

- **User Questions (`ask_user`)**: Interactive card in the transcript with radio-style choices and free text. Navigated via `Up`/`Down`, confirmed via `Enter`. The complete batch remains visible while Core accepts the answers; failures retain choices and notes for retry. Exact wait identities and task-attempt generations prevent delayed pause notifications from reopening an answered batch.
- **Agent Status**: The footer shows `disabled` when effective delegation limits prohibit children; otherwise it shows active workers and scheduling concurrency (`parallel`). `/status` and `/context` distinguish parallel capacity, cumulative children per agent, and recursion depth. `runtime.maxParallelAgents: 0` inherits the scheduler default, while `runtime.maxTeamChildrenPerAgent: 0` with no positive host/task override disables delegation.
- **Tool Execution Records**: Chronological tool cards and every step inside a live or manually expanded tool round show a 3-line preview and a live elapsed badge while a step runs. Successful output previews its first 3 lines; running or failed output previews its last 3 lines so live progress and error summaries stay visible. Click a card or step to expand the full output inline, or run `/detail` to open the full-output pager. An expanded round also ends with a `⌃ Show less` row that collapses the block from the bottom, while `Ctrl+O` expands or collapses every round in the transcript without touching per-step expansion. File edits preview their first 3 diff rows and expand to the complete diff.
- **Model Narration vs Reasoning**: Mid-turn model text renders as primary-color prose behind a bold `•` marker, matching the final answer, while reasoning stays muted italic (`Thought for Xs`, folded by default). Only registered slash commands are dispatched; any other input that merely starts with `/` (for example a pasted absolute path) is submitted as a normal prompt.
- **Long-task Observability**: The terminal title carries the task state as plain words (`running`, `waiting`, `paused`) instead of icons, so background work stays visible from the tab strip. The scrolled-away pill, the footer status badges, the pinned queue band and the checkpoint banner use plain words too: colored emoji stay out of the terminal canvas, while typographic symbols such as `✓`, `!` and `↓` remain. A terminal bell rings once when a decision card appears and once when a turn that ran at least 10 seconds finishes; the scrolled-away pill also shows the running, paused, finished, failed, or cancelled state.
- **Tool Approvals**: Interactive card when permission mode requires user consent before running shell commands or modifying files.
- **Plan Review**: Interactive card triggered when a `<proposed_plan>` completes in Plan mode, offering same-context or new-context implementation. Approved plans remain archived; execution status comes from Core, without inferred step completion. The plan body itself uses a muted vertical rail from the purple `Proposed Plan` marker to the last body line instead of a filled card surface, and full-width rules separate task turns and delimit the plan block; the review option card keeps its card background.
- **Model Recovery (`ActiveRecovery`)**: Interactive amber card for model rate limits or timeouts, offering retry (`Enter`) or discard (`Esc`).
- **Keyboard Shortcuts**: Cheat sheet modal (`?` or `/shortcuts`) with a 3-column layout (Compose, Session, Transcript).
- **Session Management**: Full lifecycle support with `/sessions`, `/resume [prefix]`, `/rewind [N]`, `/clear`, and `/title [name]`. `/resume` switches sessions without starting a model turn; `/continue` resumes a paused checkpoint. `/new` returns to an unsaved draft session, and session-scoped commands report `No session yet — send a message first.` until the first message creates it. Switching sessions renders the stored snapshot immediately; the durable-event replay only enriches tool and subagent details — task state, usage, approvals and questions come from the snapshot plus Core's `State`/`PendingRequests`, so the footer metrics never churn — and the transcript re-renders once when the replay settles (200 ms quiet, or at once when live output or a new turn starts).
- **Context Compaction**: `/compact` summarizes everything older than the latest four user turns and keeps those turns verbatim. While it runs the footer reads `Context: compacting…/<window>`, the terminal title shows `compacting · `, the scroll pill shows `Compacting <dur>`, a second `/compact` or a new message is refused, and `Esc` does not cancel (Core has no cancel path; a cancel would leave the session paused). Automatic compaction follows Core's threshold (shown as `Auto-compaction` in `/context` and `/status`): started passes show up as lead-agent activity, completed passes print `Context compacted: 621k → 92k tokens (local summary).` and install Core's after-token estimate into the footer at once, while fallback/skipped/failed passes keep one explicit transcript line. Manual compaction has no token count, so the footer shows `Context: compacted (was 621k)/<window>` without a percentage until the next message's usage report replaces it.
- **Queued Turns**: Continuous prompt entry while tasks are running, with FIFO queued cards pinned directly above the composer so streaming output can never scroll them away; `/cancel-queue` removes a queued turn and `Esc` interrupts the active task.
- **Workspace Diff**: Read-only colored Git diff modal via `/diff [path]`. It reads unstaged changes and falls back to staged changes when no unstaged diff is available. Git writes use Core tools or an external Git client; there are no `/commit` or `/push` commands.

## Permissions, Modes, and Themes

- Four permission modes: `read-only`, `workspace` (default), `ask`, `full-access`. Open the selector with `/permissions` or set one directly with `/permissions <mode>`; `Shift+Tab` toggles Plan mode instead of cycling permissions.
- `read-only` permits workspace reads and can approve external-directory reads; `ask` prompts for every operation except workspace reads; `workspace` permits workspace reads/writes and asks for external paths, commands, and web; `full-access` permits registered tools except the permanent extreme-danger command denylist. External file grants are per-session, cover a directory subtree with read or read/write access, survive reopening that session, and do not apply to shell commands.
- Plan mode (`/plan` or `Shift+Tab`): Locks permissions to `read-only` while planning. Restores the previous permission mode when Plan mode ends or implementation is chosen.
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

The footer's `Cache: … session` is the weighted cumulative cache rate for all usage in the session. `/context` shows the latest main-agent request's cached and uncached input, request ID, attempt and locally observed prompt changes. Local changes describe request mutations, not provider cache eviction reasons.

工具类别在会话内累计发现，跨任务及重启保留。页脚为全部会话用量的累计缓存率；`/context` 显示最近主 agent 请求的输入缓存明细、请求身份和本地变化。Plan 与权限切换仍按工具元数据过滤能力，因此可能引起必要的工具声明变化。

Core keeps fixed rules in Instructions and appends changing runtime guidance as model-only history snapshots. These snapshots never appear as user messages in the transcript. Permissions and approvals remain enforced by Core. The sibling luneCore README documents the bounded opt-in API smoke test; it uses an isolated workspace and never copies credentials into reports.
