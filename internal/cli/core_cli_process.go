package cli

import (
	"context"
	"fmt"
	core "github.com/afterlune/luneCore"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/afterlune/lune/internal/terminaltext"
)

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	m := int(d.Minutes())
	s := int(d.Seconds()) % 60
	return fmt.Sprintf("%dm %ds", m, s)
}

func handleCoreCLIPS(runtime cliRuntime, stdout, stderr io.Writer) {
	if runtime == nil {
		fmt.Fprintln(stderr, "error: runtime unavailable")
		return
	}
	ctx := context.Background()
	commands, err := runtime.ListCommands(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "error: list processes: %v\n", err)
		return
	}
	if len(commands) == 0 {
		fmt.Fprintln(stdout, "No background processes in current session.")
		return
	}
	fmt.Fprintf(stdout, "Background Processes (%d):\n", len(commands))
	for _, cmd := range commands {
		shortID := cmd.ID
		if len(shortID) > 8 {
			shortID = shortID[:8]
		}
		statusStr := string(cmd.Status)
		if cmd.Background {
			statusStr += " [session service]"
		}
		timeInfo := ""
		if !cmd.FinishedAt.IsZero() {
			timeInfo = fmt.Sprintf(" (duration %s)", formatDuration(cmd.FinishedAt.Sub(cmd.StartedAt)))
		} else if !cmd.StartedAt.IsZero() {
			timeInfo = fmt.Sprintf(" (started %s ago)", formatDuration(time.Since(cmd.StartedAt)))
		}
		exitStr := ""
		if cmd.ExitCode != nil {
			exitStr = fmt.Sprintf(" (exit %d)", *cmd.ExitCode)
		}
		fmt.Fprintf(stdout, "  [%s] %-12s %s%s%s\n", shortID, statusStr, cleanCLIInlineText(cmd.Command), exitStr, timeInfo)
	}
	fmt.Fprintln(stdout, "\nUse '/stop <id|all>' to terminate, '/logs <id>' to inspect output.")
}

func handleCoreCLIStop(runtime cliRuntime, line string, stdout, stderr io.Writer) {
	if runtime == nil {
		fmt.Fprintln(stderr, "error: runtime unavailable")
		return
	}
	parts := strings.Fields(line)
	if len(parts) < 2 {
		fmt.Fprintln(stderr, "Usage: /stop <id|prefix|all>")
		return
	}
	target := strings.ToLower(parts[1])
	ctx := context.Background()
	commands, err := runtime.ListCommands(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "error: list processes: %v\n", err)
		return
	}

	if target == "all" {
		var active []core.CommandInfo
		for _, cmd := range commands {
			if cmd.Status.Active() {
				active = append(active, cmd)
			}
		}
		if len(active) == 0 {
			fmt.Fprintln(stdout, "No active background processes to stop.")
			return
		}
		var stopped []string
		var failures []string
		for _, cmd := range active {
			shortID := cmd.ID
			if len(shortID) > 8 {
				shortID = shortID[:8]
			}
			if _, err := runtime.StopCommand(ctx, cmd.ID); err != nil {
				failures = append(failures, fmt.Sprintf("%s: %v", shortID, err))
				continue
			}
			stopped = append(stopped, shortID)
		}
		fmt.Fprintf(stdout, "Stopped %d of %d active background process(es): %s\n", len(stopped), len(active), strings.Join(stopped, ", "))
		for _, failure := range failures {
			fmt.Fprintf(stderr, "error: stop process [%s]\n", failure)
		}
		return
	}

	var matches []core.CommandInfo
	for _, cmd := range commands {
		if strings.HasPrefix(strings.ToLower(cmd.ID), target) {
			matches = append(matches, cmd)
		}
	}
	if len(matches) == 0 {
		fmt.Fprintf(stderr, "error: process %q not found\n", target)
		return
	}
	if len(matches) > 1 {
		var ids []string
		for _, m := range matches {
			s := m.ID
			if len(s) > 8 {
				s = s[:8]
			}
			ids = append(ids, s)
		}
		fmt.Fprintf(stderr, "error: process ID prefix %q is ambiguous (%d matches: %s)\n", target, len(matches), strings.Join(ids, ", "))
		return
	}
	match := matches[0]
	if _, err := runtime.StopCommand(ctx, match.ID); err != nil {
		fmt.Fprintf(stderr, "error: stop process: %v\n", err)
		return
	}
	shortID := match.ID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	fmt.Fprintf(stdout, "Stopped background process [%s]: %s\n", shortID, cleanCLIInlineText(match.Command))
}

func handleCoreCLILogs(runtime cliRuntime, line string, stdout, stderr io.Writer) {
	if runtime == nil {
		fmt.Fprintln(stderr, "error: runtime unavailable")
		return
	}
	parts := strings.Fields(line)
	if len(parts) < 2 {
		fmt.Fprintln(stderr, "Usage: /logs <id|prefix> [--tail N]")
		return
	}
	target := strings.ToLower(parts[1])
	tailLines := 50
	for i := 2; i < len(parts); i++ {
		if (parts[i] == "--tail" || parts[i] == "-n") && i+1 < len(parts) {
			if n, err := strconv.Atoi(parts[i+1]); err == nil && n > 0 {
				tailLines = n
				i++
			}
		}
	}
	ctx := context.Background()
	commands, err := runtime.ListCommands(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "error: list processes: %v\n", err)
		return
	}
	var matches []core.CommandInfo
	for _, cmd := range commands {
		if strings.HasPrefix(strings.ToLower(cmd.ID), target) {
			matches = append(matches, cmd)
		}
	}
	if len(matches) == 0 {
		fmt.Fprintf(stderr, "error: process %q not found\n", target)
		return
	}
	if len(matches) > 1 {
		var ids []string
		for _, m := range matches {
			s := m.ID
			if len(s) > 8 {
				s = s[:8]
			}
			ids = append(ids, s)
		}
		fmt.Fprintf(stderr, "error: process ID prefix %q is ambiguous (%d matches: %s)\n", target, len(matches), strings.Join(ids, ", "))
		return
	}
	match := matches[0]
	out, err := runtime.ReadCommand(ctx, match.ID, core.CommandReadOptions{})
	if err != nil {
		fmt.Fprintf(stderr, "error: read process output: %v\n", err)
		return
	}
	shortID := match.ID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	content := strings.TrimSpace(out.Output)
	if content == "" {
		fmt.Fprintf(stdout, "Process [%s] (%s) produced no output yet.\n", shortID, cleanCLIInlineText(match.Command))
		return
	}
	rawLines := strings.Split(content, "\n")
	totalLines := len(rawLines)
	if len(rawLines) > tailLines {
		rawLines = rawLines[len(rawLines)-tailLines:]
	}
	fmt.Fprintf(stdout, "Logs for [%s] (%s, %s)", shortID, cleanCLIInlineText(match.Command), match.Status)
	if totalLines > tailLines {
		fmt.Fprintf(stdout, " (showing last %d of %d lines):\n", tailLines, totalLines)
	} else {
		fmt.Fprintln(stdout, ":")
	}
	for _, l := range rawLines {
		fmt.Fprintln(stdout, terminaltext.Sanitize(l))
	}
}
