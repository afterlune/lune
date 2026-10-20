package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	core "github.com/afterlune/luneCore"
)

func (m *tuiModel) handlePsCommand(parts []string) (tea.Model, tea.Cmd) {
	if m.runtime == nil {
		m.addSystemEntry("Background process management unavailable (no active session runtime).")
		return m, nil
	}

	ctx := context.Background()
	commands, err := m.runtime.ListCommands(ctx)
	if err != nil {
		m.addSystemEntry("Failed to list background processes: " + err.Error())
		return m, nil
	}

	if len(commands) == 0 {
		m.addSystemEntry("No background processes in current session.")
		return m, nil
	}

	var lines []string
	lines = append(lines, fmt.Sprintf("**Background Processes (%d):**", len(commands)))
	for _, cmd := range commands {
		shortID := cmd.ID
		if len(shortID) > 8 {
			shortID = shortID[:8]
		}
		statusStr := string(cmd.Status)
		timeInfo := ""
		if !cmd.FinishedAt.IsZero() {
			timeInfo = fmt.Sprintf(" *(duration %s)*", formatDuration(cmd.FinishedAt.Sub(cmd.StartedAt)))
		} else if !cmd.StartedAt.IsZero() {
			timeInfo = fmt.Sprintf(" *(started %s ago)*", formatDuration(time.Since(cmd.StartedAt)))
		}
		exitStr := ""
		if cmd.ExitCode != nil {
			exitStr = fmt.Sprintf(" `exit %d`", *cmd.ExitCode)
		}
		lines = append(lines, fmt.Sprintf("- `[%s]` **%s**%s · `%s`%s", shortID, statusStr, exitStr, cmd.Command, timeInfo))
	}
	lines = append(lines, "\n*Use `/stop <id|all>` to terminate, `/logs <id>` to inspect output.*")
	m.addSystemEntry(strings.Join(lines, "\n"))
	return m, nil
}

func (m *tuiModel) handleStopCommand(parts []string) (tea.Model, tea.Cmd) {
	if m.runtime == nil {
		m.addSystemEntry("Process termination unavailable (no active session runtime).")
		return m, nil
	}

	if len(parts) < 2 {
		m.addSystemEntry("Usage: `/stop <id|prefix|all>`")
		return m, nil
	}

	target := strings.ToLower(parts[1])
	ctx := context.Background()
	commands, err := m.runtime.ListCommands(ctx)
	if err != nil {
		m.addSystemEntry("Failed to list processes: " + err.Error())
		return m, nil
	}

	if target == "all" {
		var active []core.CommandInfo
		for _, cmd := range commands {
			if cmd.Status.Active() {
				active = append(active, cmd)
			}
		}
		if len(active) == 0 {
			m.addSystemEntry("No active background processes to stop.")
			return m, nil
		}
		var stopped []string
		for _, cmd := range active {
			if _, err := m.runtime.StopCommand(ctx, cmd.ID); err == nil {
				shortID := cmd.ID
				if len(shortID) > 8 {
					shortID = shortID[:8]
				}
				stopped = append(stopped, shortID)
			}
		}
		m.addSystemEntry(fmt.Sprintf("Stopped %d active background process(es): %s", len(stopped), strings.Join(stopped, ", ")))
		return m, nil
	}

	var matches []core.CommandInfo
	for _, cmd := range commands {
		if strings.HasPrefix(strings.ToLower(cmd.ID), target) {
			matches = append(matches, cmd)
		}
	}

	if len(matches) == 0 {
		m.addSystemEntry(fmt.Sprintf("Process %q not found.", target))
		return m, nil
	}

	if len(matches) > 1 {
		var ids []string
		for _, match := range matches {
			s := match.ID
			if len(s) > 8 {
				s = s[:8]
			}
			ids = append(ids, s)
		}
		m.addSystemEntry(fmt.Sprintf("Process ID prefix %q is ambiguous (%d matches: %s).", target, len(matches), strings.Join(ids, ", ")))
		return m, nil
	}

	match := matches[0]
	if _, err := m.runtime.StopCommand(ctx, match.ID); err != nil {
		m.addSystemEntry("Failed to stop process: " + err.Error())
		return m, nil
	}

	shortID := match.ID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	m.addSystemEntry(fmt.Sprintf("Stopped background process `[%s]`: `%s`", shortID, match.Command))
	return m, nil
}

func (m *tuiModel) handleLogsCommand(parts []string) (tea.Model, tea.Cmd) {
	if m.runtime == nil {
		m.addSystemEntry("Process logs unavailable (no active session runtime).")
		return m, nil
	}

	if len(parts) < 2 {
		m.addSystemEntry("Usage: `/logs <id|prefix> [--tail N]`")
		return m, nil
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
	commands, err := m.runtime.ListCommands(ctx)
	if err != nil {
		m.addSystemEntry("Failed to list processes: " + err.Error())
		return m, nil
	}

	var matches []core.CommandInfo
	for _, cmd := range commands {
		if strings.HasPrefix(strings.ToLower(cmd.ID), target) {
			matches = append(matches, cmd)
		}
	}

	if len(matches) == 0 {
		m.addSystemEntry(fmt.Sprintf("Process %q not found.", target))
		return m, nil
	}

	if len(matches) > 1 {
		var ids []string
		for _, match := range matches {
			s := match.ID
			if len(s) > 8 {
				s = s[:8]
			}
			ids = append(ids, s)
		}
		m.addSystemEntry(fmt.Sprintf("Process ID prefix %q is ambiguous (%d matches: %s).", target, len(matches), strings.Join(ids, ", ")))
		return m, nil
	}

	match := matches[0]
	out, err := m.runtime.ReadCommand(ctx, match.ID, core.CommandReadOptions{})
	if err != nil {
		m.addSystemEntry("Failed to read process output: " + err.Error())
		return m, nil
	}

	shortID := match.ID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}

	content := strings.TrimSpace(out.Output)
	if content == "" {
		m.addSystemEntry(fmt.Sprintf("Process `[%s]` (`%s`) produced no output yet.", shortID, match.Command))
		return m, nil
	}

	rawLines := strings.Split(content, "\n")
	totalLines := len(rawLines)
	if len(rawLines) > tailLines {
		rawLines = rawLines[len(rawLines)-tailLines:]
	}

	header := fmt.Sprintf("**Logs for `[%s]` (`%s`, %s):**", shortID, match.Command, match.Status)
	if totalLines > tailLines {
		header += fmt.Sprintf(" *(showing last %d of %d lines)*", tailLines, totalLines)
	}

	logBlock := "```\n" + strings.Join(rawLines, "\n") + "\n```"
	m.addSystemEntry(header + "\n" + logBlock)
	return m, nil
}
