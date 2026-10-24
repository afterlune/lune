// Package slashcmd is the single source of truth for the slash commands the TUI
// and the line-oriented CLI expose. It carries metadata only: dispatch, modal
// handling and subcommand rules stay in each frontend.
package slashcmd

import "strings"

// Target marks the frontends that list a command in autocomplete and help output.
type Target uint8

const (
	// TargetTUI marks commands the Bubble Tea interface lists.
	TargetTUI Target = 1 << iota
	// TargetCLI marks commands the line-oriented interface lists.
	TargetCLI
)

// Command is one slash command entry. Usage and Description feed the CLI help
// table, Summary feeds TUI completion and /help; the two frontends word their
// help differently, so both texts are kept instead of forcing one wording.
type Command struct {
	// Name includes the leading slash, e.g. "/model".
	Name string
	// Summary is the one-line TUI description.
	Summary string
	// Usage is the CLI help usage column, e.g. "/model [profile]".
	Usage string
	// Description is the CLI help description.
	Description string

	Targets Target

	// AvailableDuringTask keeps the command listed and runnable while a task is
	// running. Subcommand-level limits are enforced by the frontend.
	AvailableDuringTask bool

	// NeedsSession marks commands the CLI refuses until the first message creates
	// a session.
	NeedsSession bool
	// DraftOK keeps a NeedsSession command usable in a TUI draft session, either
	// because it defers the change until the session exists or because it simply
	// has nothing to act on yet.
	DraftOK bool
}

// In reports whether the command is listed for the given frontend.
func (c Command) In(target Target) bool {
	return c.Targets&target != 0
}

// registry keeps the TUI order: it is the order users see while typing, so it
// groups related commands instead of sorting them alphabetically.
var registry = []Command{
	{Name: "/new", Summary: "start a new chat during a conversation", Usage: "/new", Description: "Start a fresh draft session (saved with your first message).", Targets: TargetTUI | TargetCLI},
	{Name: "/clear", Summary: "clear transcript history from the current view", Usage: "/clear", Description: "Clear terminal screen.", Targets: TargetTUI | TargetCLI},
	{Name: "/plan", Summary: "toggle Plan mode (read-only exploration)", Usage: "/plan", Description: "Toggle Plan mode (read-only exploration).", Targets: TargetTUI | TargetCLI},
	{Name: "/goal", Summary: "view or manage a persistent goal: objective, edit, budget, pause, resume, clear", Usage: "/goal [objective|subcommand]", Description: "Manage a persistent goal; optional --tokens N counts uncached input plus output, not money.", Targets: TargetTUI | TargetCLI, AvailableDuringTask: true},
	{Name: "/permissions", Summary: "choose a permission mode: read-only, workspace, auto, full-access", Usage: "/permissions [mode]", Description: "Show or set mode: read-only, workspace, auto, full-access.", Targets: TargetTUI | TargetCLI, AvailableDuringTask: true},
	{Name: "/model", Summary: "choose an active model profile", Usage: "/model [profile]", Description: "View or change active model profile.", Targets: TargetTUI | TargetCLI, AvailableDuringTask: true, NeedsSession: true, DraftOK: true},
	{Name: "/effort", Summary: "choose the reasoning effort level interactively", Usage: "/effort [level]", Description: "View or set model reasoning effort: default, none, minimal, low, medium, high, xhigh, max.", Targets: TargetTUI | TargetCLI, AvailableDuringTask: true, NeedsSession: true, DraftOK: true},
	{Name: "/theme", Summary: "choose a persisted theme: auto, dark, light", Targets: TargetTUI, AvailableDuringTask: true},
	{Name: "/memory", Summary: "view and manage workspace long-term memories", Usage: "/memory [list|add|remove|clear]", Description: "View and manage workspace long-term memories.", Targets: TargetTUI | TargetCLI, AvailableDuringTask: true},
	{Name: "/agents", Summary: "inspect live agents and their context, cache, speed, and usage", Usage: "/agents [id]", Description: "Inspect live agents and subagents in this session.", Targets: TargetTUI | TargetCLI, AvailableDuringTask: true, NeedsSession: true},
	{Name: "/ps", Summary: "list background processes and daemon commands in session", Usage: "/ps", Description: "List background processes and daemon commands.", Targets: TargetTUI | TargetCLI, AvailableDuringTask: true, NeedsSession: true},
	{Name: "/stop", Summary: "choose a running process to stop, or use /stop <id|all>", Usage: "/stop [id|all]", Description: "Stop running background processes.", Targets: TargetTUI | TargetCLI, AvailableDuringTask: true, NeedsSession: true},
	{Name: "/logs", Summary: "choose a process to inspect, or use /logs <id>", Usage: "/logs [id|prefix] [--tail N]", Description: "View logs of a background process.", Targets: TargetTUI | TargetCLI, AvailableDuringTask: true, NeedsSession: true},
	{Name: "/sessions", Summary: "list or search recent sessions in this workspace", Usage: "/sessions [query]", Description: "List or search recent sessions in this workspace.", Targets: TargetTUI | TargetCLI, AvailableDuringTask: true},
	{Name: "/resume", Summary: "switch to a saved session or open the session switcher", Usage: "/resume [id|prefix]", Description: "Switch to a saved session; list recent sessions when no ID is given.", Targets: TargetTUI | TargetCLI},
	{Name: "/drop", Summary: "choose a saved session to delete with confirmation", Usage: "/drop [id|prefix]", Description: "Delete a saved session.", Targets: TargetTUI | TargetCLI},
	{Name: "/detail", Summary: "open tool output pager modal: /detail [index]", Usage: "/detail [index]", Description: "View full output of a tool call.", Targets: TargetTUI | TargetCLI, AvailableDuringTask: true, NeedsSession: true, DraftOK: true},
	{Name: "/title", Summary: "view or set custom title for this session", Usage: "/title [name]", Description: "View or set custom title for this session.", Targets: TargetTUI | TargetCLI, AvailableDuringTask: true, NeedsSession: true, DraftOK: true},
	{Name: "/export", Summary: "export session transcript: /export [md|html|jsonl] [file]", Usage: "/export [md|html|jsonl] [path]", Description: "Export session conversation transcript to a file.", Targets: TargetTUI | TargetCLI, NeedsSession: true},
	{Name: "/context", Summary: "show context window budget, system rules, and capabilities", Usage: "/context", Description: "Show context window budget, system rules, and capabilities.", Targets: TargetTUI | TargetCLI, AvailableDuringTask: true},
	{Name: "/diff", Summary: "inspect git code changes in workspace modal (Ctrl+D)", Usage: "/diff [path]", Description: "Inspect git code changes in workspace.", Targets: TargetTUI | TargetCLI, AvailableDuringTask: true},
	{Name: "/review", Summary: "run a read-only model review of workspace changes", Usage: "/review [path]", Description: "Run a read-only model review of workspace changes in this session.", Targets: TargetTUI | TargetCLI, NeedsSession: true, DraftOK: true},
	{Name: "/mcp", Summary: "list and interactively manage external MCP servers", Usage: "/mcp [list|enable|disable|auth]", Description: "Manage external MCP servers.", Targets: TargetTUI | TargetCLI, AvailableDuringTask: true},
	{Name: "/cancel-queue", Summary: "cancel the most recently queued task", Targets: TargetTUI, AvailableDuringTask: true},
	{Name: "/compact", Summary: "compact older conversation history", Usage: "/compact", Description: "Compact older conversation history.", Targets: TargetTUI | TargetCLI, NeedsSession: true},
	{Name: "/fork", Summary: "fork the current chat", Usage: "/fork", Description: "Fork the current session.", Targets: TargetTUI | TargetCLI, NeedsSession: true},
	{Name: "/rewind", Summary: "rewind session history: /rewind [N] or /rewind preview [N]", Usage: "/rewind [N|preview [N]]", Description: "Rewind conversation or preview turns to be removed.", Targets: TargetTUI | TargetCLI, NeedsSession: true},
	{Name: "/status", Summary: "show session, model and token status", Usage: "/status", Description: "Show session, model, and token status.", Targets: TargetTUI | TargetCLI, AvailableDuringTask: true},
	{Name: "/continue", Summary: "continue the paused task in this session", Usage: "/continue", Description: "Continue the paused task in this session.", Targets: TargetTUI | TargetCLI, NeedsSession: true},
	{Name: "/discard", Summary: "discard the paused task in this session", Usage: "/discard", Description: "Discard the paused task in this session.", Targets: TargetTUI | TargetCLI, NeedsSession: true},
	{Name: "/shortcuts", Summary: "display keyboard shortcuts help", Targets: TargetTUI, AvailableDuringTask: true},
	{Name: "/thought", Summary: "toggle visibility of latest thinking/reasoning block", Targets: TargetTUI, AvailableDuringTask: true},
	{Name: "/help", Summary: "list all available slash commands", Usage: "/help", Description: "Show available commands.", Targets: TargetTUI | TargetCLI, AvailableDuringTask: true},
	{Name: "/exit", Summary: "exit the chat session", Usage: "/exit", Description: "Exit the chat.", Targets: TargetTUI | TargetCLI},
}

// All returns every registered command in registry order.
func All() []Command {
	return registry
}

// For returns the commands listed for one frontend, in registry order.
func For(target Target) []Command {
	commands := make([]Command, 0, len(registry))
	for _, command := range registry {
		if command.In(target) {
			commands = append(commands, command)
		}
	}
	return commands
}

// Lookup finds the command a raw input line starts with. Leading spaces and case
// are ignored so "/Model" and "  /model gpt" both resolve.
func Lookup(text string) (Command, bool) {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return Command{}, false
	}
	return Find(fields[0])
}

// Find resolves one command token, ignoring case.
func Find(name string) (Command, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, command := range registry {
		if command.Name == name {
			return command, true
		}
	}
	return Command{}, false
}
