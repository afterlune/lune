package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	core "github.com/afterlune/luneCore"
	publictool "github.com/afterlune/luneCore/tool"
	"github.com/charmbracelet/x/ansi"
)

type commandDetailsMsg struct {
	sessionID string
	output    core.CommandOutput
	err       error
}

func (m *tuiModel) readCommandDetails(id string) tea.Cmd {
	reader, ok := m.runtime.(interface {
		ReadCommand(context.Context, string, core.CommandReadOptions) (core.CommandOutput, error)
	})
	if !ok {
		return nil
	}
	sessionID := m.sessionID
	return func() tea.Msg {
		output, err := reader.ReadCommand(context.Background(), id, core.CommandReadOptions{})
		return commandDetailsMsg{sessionID: sessionID, output: output, err: err}
	}
}

func activityKey(event core.ToolCall) string {
	executionID, key := event.ExecutionID, event.InvocationID
	if key == "" {
		key = event.ID
	}
	if event.Details != nil && event.Details.Command != nil {
		command := event.Details.Command.Command
		if command.InvocationID != "" {
			key = command.InvocationID
		}
		if command.ExecutionID != "" {
			executionID = command.ExecutionID
		}
	}
	if executionID != "" {
		return executionID + ":" + key
	}
	return key
}

func toolActivityTitle(activity tuiActivity) string {
	title := activity.summary
	if d := activity.details; d != nil {
		if c := d.Command; c != nil {
			switch c.Command.Status {
			case publictool.CommandRunning, publictool.CommandStopping:
				return "Running " + safeToolText(c.Command.Command)
			case publictool.CommandSucceeded:
				return "Ran " + safeToolText(c.Command.Command)
			case publictool.CommandFailed:
				if c.Command.ExitCode != nil {
					return fmt.Sprintf("Failed (exit %d) %s", *c.Command.ExitCode, safeToolText(c.Command.Command))
				}
				return "Failed " + safeToolText(c.Command.Command)
			default:
				return string(c.Command.Status) + " · " + safeToolText(c.Command.Command)
			}
		}
		if len(d.Changes) > 0 {
			change := d.Changes[0]
			return fmt.Sprintf("Edited %s (+%d -%d)", safeDisplayPath(change.Path), change.Added, change.Removed)
		}
	}
	title += " · " + activity.status
	return title
}

func safeToolText(s string) string {
	// Tools may print cursor movement or OSC sequences. Only printable output
	// enters the transcript, so command output cannot repaint the composer.
	s = ansi.Strip(strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n"))
	return strings.Map(func(r rune) rune {
		if r == '\t' {
			return ' '
		}
		if r < ' ' && r != '\n' || r == 127 {
			return -1
		}
		return r
	}, s)
}

func activityDetailLines(activity tuiActivity) []transcriptLine {
	d := activity.details
	if d == nil {
		return nil
	}
	var lines []transcriptLine
	output := d.Output
	if d.Command != nil {
		output = d.Command.Output
		if d.Command.Truncated {
			lines = append(lines, transcriptLine{text: "Output truncated by runtime", role: "notice"})
		}
	}
	if output != "" {
		for _, s := range strings.Split(strings.TrimRight(safeToolText(output), "\n"), "\n") {
			lines = append(lines, transcriptLine{text: s, role: "tool"})
		}
	}
	for _, change := range d.Changes {
		for _, line := range change.Lines {
			marker, role, number := " ", "tool", line.NewLine
			if line.Kind == "added" {
				marker, role = "+", "diff_added"
			}
			if line.Kind == "removed" {
				marker, role, number = "-", "diff_removed", line.OldLine
			}
			lines = append(lines, transcriptLine{text: fmt.Sprintf("%4d %s %s", number, marker, safeToolText(line.Text)), role: role})
		}
		if change.Truncated {
			lines = append(lines, transcriptLine{text: "Diff preview truncated", role: "notice"})
		}
	}
	return lines
}

func (m *tuiModel) appendToolTranscript(lines []transcriptLine, entry tuiEntry, selected, expanded bool) []transcriptLine {
	var activity tuiActivity
	for _, a := range m.activities {
		if a.callID == entry.activityID {
			activity = a
			break
		}
	}
	role := "tool"
	if activity.status == "failed" {
		role = "error"
	} else if activity.status == "done" {
		role = "tool_done"
	}
	marker := "• "
	if selected {
		marker = "› "
	}
	title := entry.text
	if activity.callID != "" {
		title = toolActivityTitle(activity)
	}
	for i, line := range strings.Split(safeToolText(title), "\n") {
		if i == 0 {
			line = marker + line
		} else {
			line = "  " + line
		}
		lines = append(lines, transcriptLine{text: line, role: role})
	}
	details := activityDetailLines(activity)
	limit := len(details)
	if !expanded {
		limit = min(3, limit)
	}
	for _, line := range details[:limit] {
		line.text = "  │ " + line.text
		lines = append(lines, line)
	}
	if hidden := len(details) - limit; hidden > 0 {
		lines = append(lines, transcriptLine{text: fmt.Sprintf("  └ +%d lines · %s select · %s expand", hidden, m.keyLabel("focus_activity"), m.keyLabel("activity_toggle")), role: "tool"})
	}
	return lines
}
