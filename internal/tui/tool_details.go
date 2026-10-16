package tui

import (
	"context"
	"fmt"
	"regexp"
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

func toolActivityTitle(activity tuiActivity, workspace ...string) string {
	workspaceRoot := ""
	if len(workspace) > 0 {
		workspaceRoot = workspace[0]
	}
	if d := activity.details; d != nil {
		if c := d.Command; c != nil {
			status := c.Command.Status
			if status == "" {
				status = publictool.CommandStatus(activity.status)
			}
			return commandActivityTitle(c.Command.Command, status, c.Command.ExitCode)
		}
		if len(d.Changes) > 0 {
			change := d.Changes[0]
			title := fmt.Sprintf("Edited %s (+%d -%d)", displayToolPath(change.Path, workspaceRoot), change.Added, change.Removed)
			return activityStatusSuffix(title, activity.status)
		}
	}
	if strings.HasPrefix(activity.summary, "Run ") {
		command := strings.TrimPrefix(activity.summary, "Run ")
		return commandActivityTitle(command, publictool.CommandStatus(activity.status), nil)
	}
	return activityStatusSuffix(activity.summary, activity.status)
}

func activityStatusSuffix(title, status string) string {
	switch status {
	case "failed", "cancelled", "timed_out", "interrupted", "paused":
		return title + " · " + strings.ReplaceAll(status, "_", " ")
	default:
		return title
	}
}

func commandActivityTitle(command string, status publictool.CommandStatus, exitCode *int) string {
	command = safeToolText(command)
	switch status {
	case publictool.CommandRunning, publictool.CommandStopping:
		return "Run " + command
	case publictool.CommandSucceeded, publictool.CommandStatus("done"):
		return "Ran " + command
	case publictool.CommandFailed:
		result := "failed"
		if exitCode != nil {
			result += fmt.Sprintf(" (exit %d)", *exitCode)
		}
		return "Ran " + command + " · " + result
	case publictool.CommandCancelled:
		return "Ran " + command + " · cancelled"
	case publictool.CommandTimedOut:
		return "Ran " + command + " · timed out"
	case publictool.CommandInterrupted:
		return "Ran " + command + " · interrupted"
	default:
		return "Run " + command
	}
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
			if internalToolMetadataLine(s) {
				continue
			}
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

var internalToolMetadata = regexp.MustCompile(`^\[next_start_line=\d+;\s*more=(?:true|false);\s*line_truncated=(?:true|false)\]$`)

func internalToolMetadataLine(line string) bool {
	return internalToolMetadata.MatchString(strings.TrimSpace(line))
}

func (m *tuiModel) appendToolActivityGroup(lines []transcriptLine, group toolActivityGroup, selected, expanded bool) []transcriptLine {
	activities := m.toolActivitiesForGroup(group)
	if m.lightLayout() {
		var activity tuiActivity
		hasActivity := false
		if len(activities) > 0 {
			activity, hasActivity = activities[len(activities)-1], true
		}
		marker := "•"
		if selected {
			marker = "›"
		} else if hasActivity && activity.status == "running" {
			marker = titleSpinner[m.windowTitleFrame%len(titleSpinner)]
		} else if hasActivity && activity.status == "failed" {
			marker = "×"
		}
		width := max(1, m.chat.Width())
		cacheable := hasActivity && !selected && activity.status != "running"
		block := ""
		if cacheable && activity.cachedRender != "" && activity.cachedWidth == width && activity.cachedOpen == expanded {
			block = activity.cachedRender
		} else {
			title := "Tool activity"
			var output []string
			if hasActivity {
				title = safeToolText(toolActivityTitle(activity, m.workspace))
				for _, detail := range activityDetailLines(activity) {
					if detail.text != "" {
						output = append(output, detail.text)
					}
				}
			} else if len(group.entries) > 0 {
				index := group.entries[len(group.entries)-1]
				if index >= 0 && index < len(m.entries) {
					title = safeToolText(m.entries[index].text)
				}
			}
			block = ansi.Strip(renderLightToolActivity(title, output, expanded, width, marker))
			if cacheable {
				for index := range m.activities {
					if m.activities[index].callID == activity.callID {
						m.activities[index].cachedRender = block
						m.activities[index].cachedWidth = width
						m.activities[index].cachedOpen = expanded
						break
					}
				}
			}
		}
		block = strings.ReplaceAll(block, "ctrl+t", m.keyLabel("activity_toggle_latest"))
		for _, line := range strings.Split(block, "\n") {
			role := "tool"
			if hasActivity {
				role = activityTranscriptRole(activity)
			}
			lines = append(lines, transcriptLine{text: line, role: role})
		}
		return lines
	}
	if !expanded {
		role := "tool"
		marker := "· "
		if latest, ok := m.latestToolActivity(group); ok {
			role = activityTranscriptRole(latest)
			if latest.status == "running" {
				marker = titleSpinner[m.windowTitleFrame%len(titleSpinner)] + " "
			} else {
				marker = activityBadge(latest.status) + " "
			}
		}
		if selected {
			marker = "› "
		}
		lines = append(lines, transcriptLine{text: marker + m.collapsedToolGroupLabel(group), role: role})
		return lines
	}
	if len(activities) == 0 {
		for position, entryIndex := range group.entries {
			if entryIndex < 0 || entryIndex >= len(m.entries) {
				continue
			}
			marker := "  "
			if position == 0 && selected {
				marker = "› "
			}
			lines = append(lines, transcriptLine{text: marker + safeToolText(m.entries[entryIndex].text), role: "tool"})
		}
		return lines
	}
	for index, activity := range activities {
		marker := "  "
		if index == 0 && selected {
			marker = "› "
		} else if index == 0 {
			marker = "• "
		}
		role := activityTranscriptRole(activity)
		for lineIndex, title := range strings.Split(safeToolText(toolActivityTitle(activity, m.workspace)), "\n") {
			if lineIndex > 0 {
				title = "  " + title
			} else {
				title = marker + title
			}
			lines = append(lines, transcriptLine{text: title, role: role})
		}
		for _, detail := range activityDetailLines(activity) {
			detail.text = "  │ " + detail.text
			lines = append(lines, detail)
		}
	}
	return lines
}

func activityTranscriptRole(activity tuiActivity) string {
	switch activity.status {
	case "failed":
		return "error"
	case "done":
		return "tool_done"
	default:
		return "tool"
	}
}
