package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/afterlune/lune/internal/terminaltext"
	tuiMarkdown "github.com/afterlune/lune/internal/tui/markdown"
	publictool "github.com/afterlune/luneCore/tool"
	"github.com/charmbracelet/x/ansi"
)

// renderHeader renders the top left branding.
func renderHeader(workspace string) string {
	prompt := lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().AccentBlue)).Render(">_")
	brand := lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().TextPrimary)).Bold(true).Render(AppName)
	top := fmt.Sprintf("%s %s", prompt, brand)

	cwd := lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().TextMuted)).Render("in " + terminaltext.Sanitize(workspace))
	return top + "\n" + cwd + "\n"
}

// assistantMarkerStyle styles the marker that introduces model narration. It stays
// in the primary text weight so mid-turn prose reads as model output instead of being
// mistaken for the muted, folded reasoning blocks.
func assistantMarkerStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().TextPrimary)).Bold(true)
}

// renderEntry renders a single transcript item according to Lune visual patterns.
func renderEntry(entry TranscriptEntry, width int) string {
	return renderEntryMode(entry, width, false)
}

func renderStreamingEntry(entry TranscriptEntry, width int) string {
	return renderEntryMode(entry, width, true)
}

func renderEntryMode(entry TranscriptEntry, width int, stableTail bool) string {
	switch entry.Kind {
	case KindUser:
		text := terminaltext.Sanitize(strings.TrimSpace(entry.Text))
		prompt := styleTextPrimary().Bold(true).Render("› ")
		content := prompt + styleTextPrimary().Render(text)
		if len(entry.AttachedFiles) > 0 {
			var chips []string
			for _, file := range entry.AttachedFiles {
				chips = append(chips, styleTextMuted().Render("@"+terminaltext.Sanitize(file)))
			}
			content += "\n  " + strings.Join(chips, styleTextMuted().Render("  "))
		}
		return renderSurfaceCard(content, width, 2, 1)

	case KindAssistant:
		bullet := assistantMarkerStyle().Render("• ")
		cleanText := tuiMarkdown.CleanProposedPlanTags(entry.Text)
		var rendered string
		if stableTail {
			rendered = renderMarkdownStable(cleanText, width-4)
		} else {
			rendered = renderMarkdown(cleanText, width-4)
		}
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
			qLine := fmt.Sprintf("  • %s", styleTextPrimary().Render(terminaltext.Sanitize(qa.Question)))
			ansLabel := styleTextMuted().Render("    answer: ")
			ansVal := lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().AccentBlue)).Render(terminaltext.Sanitize(qa.Answer))
			rows = append(rows, qLine, ansLabel+ansVal)
		}
		return strings.Join(rows, "\n")

	case KindPlan:
		cleanPlan := tuiMarkdown.CleanProposedPlanTags(entry.Text)
		if !tuiMarkdown.HasPlanContent(cleanPlan) {
			return ""
		}
		th := activeTheme()
		bullet := lipgloss.NewStyle().Foreground(lipgloss.Color(th.PlanPurple)).Bold(true).Render("• ")
		title := lipgloss.NewStyle().Foreground(lipgloss.Color(th.PlanPurple)).Bold(true).Render("Proposed Plan")
		var rendered string
		if stableTail {
			rendered = renderPlanMarkdownStable(cleanPlan, max(1, width-2))
		} else {
			rendered = renderPlanMarkdown(cleanPlan, max(1, width-2))
		}
		rows := []string{bullet + title, ""}
		for _, line := range strings.Split(strings.TrimRight(rendered, "\n"), "\n") {
			for _, wrapped := range strings.Split(ansi.Hardwrap(line, max(1, width-2), true), "\n") {
				rows = append(rows, "  "+wrapped)
			}
		}
		return strings.Join(rows, "\n")

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

	case KindReasoning:
		if stableTail {
			entry.ReasoningLive = true
		}
		return renderReasoningEntry(entry, width)
	}

	return ""
}

// renderTranscriptRule renders the full-width solid separator that divides task
// turns.
func renderTranscriptRule(width int) string {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().Border)).
		Render(strings.Repeat("─", max(1, width)))
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
	nameStyled := lipgloss.NewStyle().Foreground(lipgloss.Color(th.PlanPurple)).Bold(true).Render(terminaltext.Sanitize(name))
	roleStyled := ""
	if entry.AgentRole != "" {
		roleStyled = styleTextMuted().Render(" (" + terminaltext.Sanitize(entry.AgentRole) + ")")
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
	switch {
	case !entry.SubagentRunning && entry.SubagentDuration > 0:
		durStyled = styleTextMuted().Render(" · " + formatDuration(entry.SubagentDuration))
	case entry.SubagentRunning && !entry.Timestamp.IsZero():
		if elapsed := time.Since(entry.Timestamp); elapsed >= time.Second {
			durStyled = styleTextMuted().Render(" · " + formatDuration(elapsed))
		}
	}

	header := bullet + action + " " + nameStyled + roleStyled + toolsStyled + durStyled
	branch := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Render("└ ")

	if entry.SubagentExpanded && entry.SubagentRunning {
		return header + styleTextMuted().Render(" (live output below; click to collapse)")
	}
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

func renderLiveSubagentOutput(name, agentID, text string, width int) string {
	if name == "" {
		name = agentID
	}
	header := styleTextMuted().Render("Subagent " + terminaltext.Sanitize(name))
	bar := lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().Border)).Render("│ ")
	wrapped := renderMarkdownStable(text, max(20, width-6))
	lines := strings.Split(wrapped, "\n")
	rows := make([]string, 0, len(lines)+1)
	rows = append(rows, header)
	for _, line := range lines {
		rows = append(rows, "  "+bar+line)
	}
	return strings.Join(rows, "\n")
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

	hint := "(click to expand)"
	if entry.ReasoningExpanded {
		hint = "(click to collapse)"
	} else if lineCount > 1 {
		hint = fmt.Sprintf("+ %d lines (click to expand)", lineCount)
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
	var wrapped string
	if entry.ReasoningLive {
		wrapped = renderMarkdownStable(bodyText, contentWidth)
	} else {
		wrapped = renderMarkdown(bodyText, contentWidth)
	}
	lines := strings.Split(wrapped, "\n")

	var outLines []string
	outLines = append(outLines, header)
	for _, l := range lines {
		outLines = append(outLines, "  "+bar+thoughtStyle.Render(l))
	}

	return strings.Join(outLines, "\n")
}

// sanitizeAndTruncateLine removes terminal controls and truncates by display width.
func sanitizeAndTruncateLine(line string, maxWidth int) string {
	line = terminaltext.Sanitize(line)
	if maxWidth <= 0 {
		return ""
	}
	if ansi.StringWidth(line) > maxWidth {
		return ansi.Truncate(line, maxWidth, "…")
	}
	return line
}

// isExploreTool classifies a tool for display only: exploration tools render with
// the quiet read style, everything else with the execution style. Permission and
// Plan-mode decisions live in Core and read tool metadata, never this list.
func isExploreTool(toolName string) bool {
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "read_file", "read_image", "list_dir", "grep", "glob", "find_files", "search_files":
		return true
	default:
		return false
	}
}

// fileChangeCounts returns the added and removed line counts for a file change.
func fileChangeCounts(ch publictool.FileChange) (int, int) {
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
	return adds, dels
}

// fileChangeActionWord returns Added, Deleted, or Edited for a file change.
func fileChangeActionWord(entry TranscriptEntry, ch publictool.FileChange, adds, dels int) string {
	if strings.EqualFold(entry.ToolName, "write_file") || (adds > 0 && dels == 0 && len(ch.Lines) > 0 && ch.Lines[0].OldLine == 0) {
		return "Added"
	}
	if adds == 0 && dels > 0 {
		return "Deleted"
	}
	return "Edited"
}

// renderFileChangeRows renders up to limit numbered diff rows for one file change.
func renderFileChangeRows(ch publictool.FileChange, rowWidth, limit int) []string {
	if limit <= 0 || len(ch.Lines) == 0 {
		return nil
	}
	limit = min(limit, len(ch.Lines))
	th := activeTheme()

	maxLineNo := 1
	for i := 0; i < limit; i++ {
		ln := ch.Lines[i].NewLine
		if ln <= 0 {
			ln = ch.Lines[i].OldLine
		}
		if ln > maxLineNo {
			maxLineNo = ln
		}
	}
	numWidth := len(strconv.Itoa(maxLineNo))

	rows := make([]string, 0, limit)
	for i := 0; i < limit; i++ {
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
		rows = append(rows, "  "+rowStyle.Render(truncated+strings.Repeat(" ", pad)))
	}
	return rows
}

// renderFileChangesCard renders compact Added/Edited/Deleted cards with inline diff line previews.
func renderFileChangesCard(entry TranscriptEntry, width int) string {
	th := activeTheme()
	var blocks []string
	rowWidth := max(20, width-2)

	for _, ch := range entry.ToolChanges {
		adds, dels := fileChangeCounts(ch)
		actionWord := fileChangeActionWord(entry, ch, adds, dels)

		bullet := lipgloss.NewStyle().Foreground(lipgloss.Color(th.SuccessGreen)).Render("• ")
		actionStyled := styleTextPrimary().Bold(true).Render(actionWord)
		pathStyled := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextPrimary)).Underline(true).Render(terminaltext.Sanitize(ch.Path))
		addStyled := lipgloss.NewStyle().Foreground(lipgloss.Color(th.SuccessGreen)).Bold(true).Render(fmt.Sprintf("+%d", adds))
		delStyled := lipgloss.NewStyle().Foreground(lipgloss.Color(th.FailureRed)).Bold(true).Render(fmt.Sprintf("-%d", dels))
		header := fmt.Sprintf("%s%s %s (%s %s)", bullet, actionStyled, pathStyled, addStyled, delStyled)

		previewCount := toolPreviewLineLimit
		if entry.ToolExpanded || len(ch.Lines) <= previewCount {
			previewCount = len(ch.Lines)
		}

		lines := []string{header}
		lines = append(lines, renderFileChangeRows(ch, rowWidth, previewCount)...)

		if len(ch.Lines) > 0 {
			if entry.ToolExpanded && len(ch.Lines) > toolPreviewLineLimit {
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
	action := "Working"
	if thinking {
		action = "Thinking"
	}
	return renderWorkingAction(elapsed, action, subagentsSummary...)
}

func renderWorkingActivity(elapsed time.Duration, activity string, subagentsSummary ...string) string {
	return renderWorkingAction(elapsed, activity, subagentsSummary...)
}

func renderWorkingAction(elapsed time.Duration, action string, subagentsSummary ...string) string {
	bullet := lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().TextMuted)).Render("• ")
	timeStr := formatDuration(elapsed)
	titleText := fmt.Sprintf("%s (%s • esc to interrupt)", action, timeStr)
	if len(subagentsSummary) > 0 && subagentsSummary[0] != "" {
		titleText = fmt.Sprintf("%s (%s · %s • esc to interrupt)", action, timeStr, subagentsSummary[0])
	}
	title := styleTextPrimary().Bold(true).Render(titleText)
	branch := lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().TextMuted)).Render("└ ")
	tip := styleTextMuted().Render("Tip: Use Up/Down to navigate prompt history; Esc to interrupt.")
	return bullet + title + "\n  " + branch + tip
}

// renderQueuedBand pins waiting turns directly above the composer so streaming
// output can never push them out of view.
func renderQueuedBand(m *tuiModel, width int) string {
	if m == nil {
		return ""
	}
	var queued []TranscriptEntry
	for _, entry := range m.entries {
		if entry.Kind == KindQueued {
			queued = append(queued, entry)
		}
	}
	if len(queued) == 0 {
		return ""
	}

	th := activeTheme()
	label := lipgloss.NewStyle().Foreground(lipgloss.Color(th.WarningAmber)).Bold(true).Render("Queued › ")
	hintText := " (/cancel-queue cancels latest waiting task)"

	rows := make([]string, 0, len(queued))
	for i, entry := range queued {
		hint := ""
		if i == len(queued)-1 {
			hint = styleTextMuted().Render(hintText)
		}
		textWidth := max(10, width-lipgloss.Width(label)-lipgloss.Width(hintText))
		text := sanitizeAndTruncateLine(strings.TrimSpace(entry.Text), textWidth)
		rows = append(rows, label+styleTextPrimary().Render(text)+hint)
	}
	return strings.Join(rows, "\n")
}

// maxVisibleAutocomplete is the maximum number of items visible simultaneously in the autocomplete dropdown.
const maxVisibleAutocomplete = 7
