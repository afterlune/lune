package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	tuiMarkdown "github.com/afterlune/lune/internal/tui/markdown"
	"github.com/charmbracelet/x/ansi"
)

// renderHeader renders the top left branding.
func renderHeader(workspace string) string {
	prompt := lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().AccentBlue)).Render(">_")
	brand := lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().TextPrimary)).Bold(true).Render(AppName)
	top := fmt.Sprintf("%s %s", prompt, brand)

	cwd := lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().TextMuted)).Underline(true).Render(workspace)
	return top + "\n" + cwd + "\n"
}

// renderEntry renders a single transcript item according to LuneAgent visual patterns.
func renderEntry(entry TranscriptEntry, width int) string {
	switch entry.Kind {
	case KindUser:
		text := strings.TrimSpace(entry.Text)
		prefix := lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().TextPrimary)).Bold(true).Render("› ")
		content := prefix + styleTextPrimary().Render(text)
		pad := max(0, width-lipgloss.Width(content))
		cardStyle := lipgloss.NewStyle().Background(lipgloss.Color(activeTheme().CardBg))
		box := renderCardSurface(cardStyle, content+strings.Repeat(" ", pad))
		if len(entry.AttachedFiles) > 0 {
			var chips []string
			for _, file := range entry.AttachedFiles {
				chips = append(chips, "📎 "+file)
			}
			chipLine := "  " + styleTextMuted().Render(strings.Join(chips, "  "))
			return box + "\n" + chipLine
		}
		return box

	case KindAssistant:
		bullet := lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().TextMuted)).Render("• ")
		cleanText := tuiMarkdown.CleanProposedPlanTags(entry.Text)
		rendered := renderMarkdown(cleanText, width-4)
		lines := strings.Split(rendered, "\n")
		var out []string
		for i, line := range lines {
			if i == 0 {
				out = append(out, bullet+line)
			} else {
				out = append(out, "  "+line)
			}
		}
		if entry.TurnDuration >= time.Second {
			worked := styleTextMuted().Render("  Worked for " + formatDuration(entry.TurnDuration))
			out = append(out, "", worked)
		}
		return strings.Join(out, "\n")

	case KindTool:
		return renderToolEntry(entry, width)

	case KindSubagent:
		return renderSubagentEntry(entry, width)

	case KindQuestionAnswered:
		bullet := lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().TextMuted)).Render("• ")
		total := max(entry.QuestionsTotal, len(entry.AnsweredQuestions))
		title := styleTextPrimary().Bold(true).Render(fmt.Sprintf("Questions %d/%d answered", len(entry.AnsweredQuestions), total))
		var rows []string
		rows = append(rows, bullet+title)
		for _, qa := range entry.AnsweredQuestions {
			qLine := fmt.Sprintf("  • %s", styleTextPrimary().Render(qa.Question))
			ansLabel := styleTextMuted().Render("    answer: ")
			ansVal := lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().AccentBlue)).Render(qa.Answer)
			rows = append(rows, qLine, ansLabel+ansVal)
		}
		return strings.Join(rows, "\n")

	case KindPlan:
		th := activeTheme()
		bullet := lipgloss.NewStyle().Foreground(lipgloss.Color(th.PlanPurple)).Bold(true).Render("• ")
		title := lipgloss.NewStyle().Foreground(lipgloss.Color(th.PlanPurple)).Bold(true).Render("Proposed Plan")
		cleanPlan := tuiMarkdown.CleanProposedPlanTags(entry.Text)
		rendered := renderPlanMarkdown(cleanPlan, max(12, width-4))
		return bullet + title + "\n\n" + renderPlanSurface(rendered, max(20, width))

	case KindSystem:
		bullet := lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().TextMuted)).Render("• ")
		rendered := renderMarkdown(entry.Text, width-4)
		lines := strings.Split(rendered, "\n")
		var out []string
		for i, line := range lines {
			if i == 0 {
				out = append(out, bullet+line)
			} else {
				out = append(out, "  "+line)
			}
		}
		return strings.Join(out, "\n")

	case KindQueued:
		icon := lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().WarningAmber)).Render("⏳ ")
		label := lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().WarningAmber)).Bold(true).Render("Queued › ")
		content := styleTextPrimary().Render(strings.TrimSpace(entry.Text))
		hint := styleTextMuted().Render(" (/cancel-queue cancels latest waiting task)")
		row := icon + label + content + hint
		pad := max(0, width-lipgloss.Width(row))
		cardStyle := lipgloss.NewStyle().Background(lipgloss.Color(activeTheme().CardBg))
		box := renderCardSurface(cardStyle, row+strings.Repeat(" ", pad))
		if len(entry.AttachedFiles) > 0 {
			var chips []string
			for _, file := range entry.AttachedFiles {
				chips = append(chips, "📎 "+file)
			}
			chipLine := "  " + styleTextMuted().Render(strings.Join(chips, "  "))
			return box + "\n" + chipLine
		}
		return box

	case KindReasoning:
		return renderReasoningEntry(entry, width)
	}

	return ""
}

// renderSubagentEntry renders a concurrent subagent status card without polluting the lead stream.
func renderSubagentEntry(entry TranscriptEntry, width int) string {
	th := activeTheme()
	var bullet, action string
	if entry.SubagentRunning {
		bullet = lipgloss.NewStyle().Foreground(lipgloss.Color(th.AccentBlue)).Bold(true).Render("⠋ ")
		action = lipgloss.NewStyle().Foreground(lipgloss.Color(th.AccentBlue)).Bold(true).Render("Subagent")
	} else {
		bullet = lipgloss.NewStyle().Foreground(lipgloss.Color(th.SuccessGreen)).Render("• ")
		action = styleTextPrimary().Bold(true).Render("Subagent")
	}

	name := entry.AgentName
	if name == "" {
		name = entry.AgentID
	}
	nameStyled := lipgloss.NewStyle().Foreground(lipgloss.Color(th.PlanPurple)).Bold(true).Render(name)
	roleStyled := ""
	if entry.AgentRole != "" {
		roleStyled = styleTextMuted().Render(" (" + entry.AgentRole + ")")
	}
	toolsStyled := ""
	if entry.SubagentTools > 0 {
		toolsWord := "tools"
		if entry.SubagentTools == 1 {
			toolsWord = "tool"
		}
		toolsStyled = styleTextMuted().Render(fmt.Sprintf(" · %d %s", entry.SubagentTools, toolsWord))
	}
	durStyled := ""
	if !entry.SubagentRunning && entry.SubagentDuration > 0 {
		durStyled = styleTextMuted().Render(" · " + formatDuration(entry.SubagentDuration))
	}

	header := bullet + action + " " + nameStyled + roleStyled + toolsStyled + durStyled
	branch := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Render("└ ")

	if entry.SubagentExpanded && strings.TrimSpace(entry.Text) != "" {
		bar := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Border)).Render("│ ")
		rendered := renderMarkdown(strings.TrimSpace(entry.Text), max(20, width-6))
		var outLines []string
		outLines = append(outLines, header+styleTextMuted().Render(" (click to collapse)"))
		for _, l := range strings.Split(rendered, "\n") {
			outLines = append(outLines, "  "+bar+l)
		}
		return strings.Join(outLines, "\n")
	}

	preview := strings.TrimSpace(entry.SubagentPreview)
	if preview == "" {
		if entry.SubagentRunning {
			if entry.SubagentLastTool != "" {
				preview = fmt.Sprintf("Running %s...", entry.SubagentLastTool)
			} else {
				preview = "Working..."
			}
		} else {
			preview = "Completed"
		}
	}
	preview = sanitizeAndTruncateLine(preview, max(20, width-8))
	collapseHint := ""
	if strings.TrimSpace(entry.Text) != "" {
		collapseHint = styleTextMuted().Render(" (click to expand)")
	}
	return header + "\n  " + branch + styleTextMuted().Render(preview) + collapseHint
}

// renderReasoningEntry renders a model thinking / chain-of-thought block.
func renderReasoningEntry(entry TranscriptEntry, width int) string {
	th := activeTheme()
	bullet := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Render("• ")

	durStr := ""
	if entry.ReasoningDuration > 0 {
		durStr = formatDuration(entry.ReasoningDuration)
		if durStr == "0s" {
			durStr = fmt.Sprintf("%dms", entry.ReasoningDuration.Milliseconds())
		}
	}

	var action string
	if entry.ReasoningLive {
		if durStr != "" {
			action = fmt.Sprintf("Thinking... (%s)", durStr)
		} else {
			action = "Thinking..."
		}
	} else {
		if durStr != "" {
			action = fmt.Sprintf("Thought for %s", durStr)
		} else {
			action = "Thought"
		}
	}

	bodyText := strings.TrimSpace(entry.Text)
	lineCount := 0
	if bodyText != "" {
		lineCount = strings.Count(bodyText, "\n") + 1
	}

	hint := "(ctrl+r to expand)"
	if entry.ReasoningExpanded {
		hint = "(ctrl+r to collapse)"
	} else if lineCount > 1 {
		hint = fmt.Sprintf("+ %d lines (ctrl+r to expand)", lineCount)
	}

	titleText := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Italic(true).Render(action)
	hintText := styleTextMuted().Render(" " + hint)
	header := bullet + titleText + hintText

	if !entry.ReasoningExpanded || bodyText == "" {
		return header
	}

	bar := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Border)).Render("│ ")
	thoughtStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Italic(true)

	contentWidth := max(20, width-4)
	wrapped := renderMarkdown(bodyText, contentWidth)
	lines := strings.Split(wrapped, "\n")

	var outLines []string
	outLines = append(outLines, header)
	for _, l := range lines {
		outLines = append(outLines, "  "+bar+thoughtStyle.Render(l))
	}

	return strings.Join(outLines, "\n")
}

// sanitizeAndTruncateLine expands tabs, strips carriage returns, and truncates safely by display width.
func sanitizeAndTruncateLine(line string, maxWidth int) string {
	line = strings.ReplaceAll(line, "\r", "")
	line = strings.ReplaceAll(line, "\t", "    ")
	if maxWidth <= 0 {
		return ""
	}
	if ansi.StringWidth(line) > maxWidth {
		return ansi.Truncate(line, maxWidth, "…")
	}
	return line
}

func isExploreTool(toolName string) bool {
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "read_file", "list_dir", "grep", "glob", "find_files", "search_files":
		return true
	default:
		return false
	}
}

// renderFileChangesCard renders Codex-style Added/Edited/Deleted cards with inline diff line previews.
func renderFileChangesCard(entry TranscriptEntry, width int) string {
	th := activeTheme()
	var blocks []string
	rowWidth := max(20, width-2)

	for _, ch := range entry.ToolChanges {
		adds, dels := ch.Added, ch.Removed
		if adds == 0 && dels == 0 {
			for _, dl := range ch.Lines {
				switch dl.Kind {
				case "add", "+":
					adds++
				case "delete", "remove", "-":
					dels++
				}
			}
		}

		actionWord := "Edited"
		if strings.EqualFold(entry.ToolName, "write_file") || (adds > 0 && dels == 0 && len(ch.Lines) > 0 && ch.Lines[0].OldLine == 0) {
			actionWord = "Added"
		} else if adds == 0 && dels > 0 {
			actionWord = "Deleted"
		}

		bullet := lipgloss.NewStyle().Foreground(lipgloss.Color(th.SuccessGreen)).Render("• ")
		actionStyled := styleTextPrimary().Bold(true).Render(actionWord)
		pathStyled := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextPrimary)).Underline(true).Render(ch.Path)
		addStyled := lipgloss.NewStyle().Foreground(lipgloss.Color(th.SuccessGreen)).Bold(true).Render(fmt.Sprintf("+%d", adds))
		delStyled := lipgloss.NewStyle().Foreground(lipgloss.Color(th.FailureRed)).Bold(true).Render(fmt.Sprintf("-%d", dels))
		header := fmt.Sprintf("%s%s %s (%s %s)", bullet, actionStyled, pathStyled, addStyled, delStyled)

		var lines []string
		lines = append(lines, header)

		previewCount := 3
		if entry.ToolExpanded || len(ch.Lines) <= previewCount {
			previewCount = len(ch.Lines)
		}

		maxLineNo := 1
		for i := 0; i < previewCount; i++ {
			ln := ch.Lines[i].NewLine
			if ln <= 0 {
				ln = ch.Lines[i].OldLine
			}
			if ln > maxLineNo {
				maxLineNo = ln
			}
		}
		numWidth := len(strconv.Itoa(maxLineNo))

		for i := 0; i < previewCount; i++ {
			dl := ch.Lines[i]
			ln := dl.NewLine
			if ln <= 0 {
				ln = dl.OldLine
			}
			if ln <= 0 {
				ln = i + 1
			}
			prefix := " "
			bgHex := th.CardBg
			fgHex := th.TextPrimary
			switch dl.Kind {
			case "add", "+":
				prefix = "+"
				bgHex = th.DiffAddBg
				fgHex = th.SuccessGreen
			case "delete", "remove", "-":
				prefix = "-"
				bgHex = th.DiffDelBg
				fgHex = th.FailureRed
			}
			rawRow := fmt.Sprintf(" %*d %s%s", numWidth, ln, prefix, dl.Text)
			truncated := sanitizeAndTruncateLine(rawRow, rowWidth)
			pad := max(0, rowWidth-lipgloss.Width(truncated))
			rowStyle := lipgloss.NewStyle().
				Background(lipgloss.Color(bgHex)).
				Foreground(lipgloss.Color(fgHex))
			lines = append(lines, "  "+rowStyle.Render(truncated+strings.Repeat(" ", pad)))
		}

		if len(ch.Lines) > 0 {
			if entry.ToolExpanded && len(ch.Lines) > 3 {
				lines = append(lines, styleTextMuted().Render("  - Hide details"))
			} else {
				lines = append(lines, styleTextMuted().Render("  + Show details"))
			}
		}
		blocks = append(blocks, strings.Join(lines, "\n"))
	}

	return strings.Join(blocks, "\n\n")
}

// renderWorking renders the active working indicator.
func renderWorking(elapsed time.Duration, thinking bool, subagentsSummary ...string) string {
	bullet := lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().TextMuted)).Render("• ")
	timeStr := formatDuration(elapsed)
	action := "Working"
	if thinking {
		action = "Thinking"
	}
	titleText := fmt.Sprintf("%s (%s • esc to interrupt)", action, timeStr)
	if len(subagentsSummary) > 0 && subagentsSummary[0] != "" {
		titleText = fmt.Sprintf("%s (%s · %s • esc to interrupt)", action, timeStr, subagentsSummary[0])
	}
	title := styleTextPrimary().Bold(true).Render(titleText)
	branch := lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().TextMuted)).Render("└ ")
	tip := styleTextMuted().Render("Tip: Use Up/Down to navigate prompt history; Esc to interrupt.")
	return bullet + title + "\n  " + branch + tip
}

// maxVisibleAutocomplete is the maximum number of items visible simultaneously in the autocomplete dropdown.
const maxVisibleAutocomplete = 7
