package tui

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
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
		cleanText := cleanProposedPlanTags(entry.Text)
		rendered := RenderMarkdown(cleanText, width-4)
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
		bar := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Border)).Render("│ ")
		cleanPlan := cleanProposedPlanTags(entry.Text)
		rendered := RenderMarkdown(cleanPlan, max(20, width-6))
		var outLines []string
		outLines = append(outLines, bullet+title)
		for _, line := range strings.Split(rendered, "\n") {
			outLines = append(outLines, "  "+bar+line)
		}
		return strings.Join(outLines, "\n")

	case KindSystem:
		bullet := lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().TextMuted)).Render("• ")
		rendered := RenderMarkdown(entry.Text, width-4)
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
		hint := styleTextMuted().Render(" (/cancel-queue to cancel)")
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
		rendered := RenderMarkdown(strings.TrimSpace(entry.Text), max(20, width-6))
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
	wrapped := RenderMarkdown(bodyText, contentWidth)
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

// renderToolEntry renders tool executions (Running, Ran, Failed, Explored, Added/Edited).
func renderToolEntry(entry TranscriptEntry, width int) string {
	th := activeTheme()
	hasNonZeroExit := entry.ToolExitCode != nil && *entry.ToolExitCode != 0
	isFail := entry.ToolFailed || hasNonZeroExit

	if !entry.ToolRunning && !isFail && len(entry.ToolChanges) > 0 {
		return renderFileChangesCard(entry, width)
	}

	var bullet, action string
	if entry.ToolRunning {
		bullet = lipgloss.NewStyle().Foreground(lipgloss.Color(th.AccentBlue)).Bold(true).Render("⠋ ")
		action = lipgloss.NewStyle().Foreground(lipgloss.Color(th.AccentBlue)).Bold(true).Render("Running")
	} else if isFail {
		bullet = lipgloss.NewStyle().Foreground(lipgloss.Color(th.FailureRed)).Render("• ")
		if hasNonZeroExit {
			action = lipgloss.NewStyle().Foreground(lipgloss.Color(th.FailureRed)).Bold(true).
				Render(fmt.Sprintf("Failed (exit %d)", *entry.ToolExitCode))
		} else {
			action = lipgloss.NewStyle().Foreground(lipgloss.Color(th.FailureRed)).Bold(true).
				Render("Failed")
		}
	} else if isExploreTool(entry.ToolName) {
		bullet = lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Render("• ")
		action = lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextPrimary)).Bold(true).Render("Explored")
	} else {
		bullet = lipgloss.NewStyle().Foreground(lipgloss.Color(th.SuccessGreen)).Render("• ")
		action = lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextPrimary)).Bold(true).Render("Ran")
	}

	commandStr := strings.TrimSpace(entry.ToolCommand)
	if commandStr == "" || commandStr == entry.ToolName || commandStr == entry.ToolName+" "+entry.ToolArgs {
		commandStr = formatToolSummary(entry.ToolName, entry.ToolArgs, "")
	}
	commandStr = sanitizeAndTruncateLine(commandStr, max(20, width-18))
	cmdStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.AccentBlue))
	durBadge := ""
	if !entry.ToolRunning && entry.ToolDuration >= 500*time.Millisecond {
		durBadge = styleTextMuted().Render(" (" + formatDuration(entry.ToolDuration) + ")")
	}
	header := bullet + action + " " + cmdStyle.Render(commandStr) + durBadge

	// Branch indicator
	branch := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Render("└ ")

	var bodyLines []string
	maxLineLen := max(20, width-8)

	// Output preview
	output := strings.TrimSpace(entry.ToolOutput)
	if output == "" {
		if entry.ToolRunning {
			bodyLines = append(bodyLines, "  "+branch+styleTextMuted().Render("Running..."))
		} else {
			bodyLines = append(bodyLines, "  "+branch+styleTextMuted().Render("(no output)"))
			bodyLines = append(bodyLines, styleTextMuted().Render("  + Show details"))
		}
		return header + "\n" + strings.Join(bodyLines, "\n")
	}

	outLines := strings.Split(output, "\n")
	previewCount := 3
	if entry.ToolExpanded || len(outLines) <= previewCount {
		previewCount = len(outLines)
	}

	// Show the tail (last 3 lines) when collapsed so error summaries, tracebacks,
	// and final results are immediately visible just like Codex.
	startIdx := 0
	if !entry.ToolExpanded && len(outLines) > previewCount {
		startIdx = len(outLines) - previewCount
	}

	for i := 0; i < previewCount; i++ {
		line := sanitizeAndTruncateLine(outLines[startIdx+i], maxLineLen)
		if i == 0 {
			bodyLines = append(bodyLines, "  "+branch+styleTextMuted().Render(line))
		} else {
			bodyLines = append(bodyLines, "    "+styleTextMuted().Render(line))
		}
	}

	if len(outLines) > previewCount && !entry.ToolExpanded {
		remaining := len(outLines) - previewCount
		expandHint := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).
			Render(fmt.Sprintf("  + %d lines (ctrl+t to expand)", remaining))
		bodyLines = append(bodyLines, expandHint)
	} else if entry.ToolExpanded && len(outLines) > 3 {
		collapseHint := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).
			Render(fmt.Sprintf("  - collapse %d lines (ctrl+t)", len(outLines)))
		bodyLines = append(bodyLines, collapseHint)
	}

	return header + "\n" + strings.Join(bodyLines, "\n")
}

// isCoreRoundKind returns true for primary execution step kinds (Tool, Reasoning).
func isCoreRoundKind(kind EntryKind) bool {
	return kind == KindTool || kind == KindReasoning
}

// isRoundCandidateKind returns true for kinds that can belong to a tool round.
func isRoundCandidateKind(kind EntryKind) bool {
	return isCoreRoundKind(kind) || kind == KindQuestionAnswered
}

// isRoundMember checks whether entries[idx] belongs to a contiguous tool round.
// KindQuestionAnswered joins a round when adjacent to at least one Tool/Reasoning/Subagent step,
// while standalone KindQuestionAnswered entries render as standalone cards.
func isRoundMember(entries []TranscriptEntry, idx int) bool {
	if idx < 0 || idx >= len(entries) || !isRoundCandidateKind(entries[idx].Kind) {
		return false
	}
	if isCoreRoundKind(entries[idx].Kind) {
		return true
	}
	for i := idx - 1; i >= 0 && isRoundCandidateKind(entries[i].Kind); i-- {
		if isCoreRoundKind(entries[i].Kind) {
			return true
		}
	}
	for i := idx + 1; i < len(entries) && isRoundCandidateKind(entries[i].Kind); i++ {
		if isCoreRoundKind(entries[i].Kind) {
			return true
		}
	}
	return false
}

// formatThoughtDuration formats reasoning durations with sub-10s tenths (e.g. "1.6s") like Grok Build.
func formatThoughtDuration(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	if d < time.Second {
		ms := d.Milliseconds()
		if ms <= 0 {
			return "0.1s"
		}
		return fmt.Sprintf("%dms", ms)
	}
	if d < 10*time.Second {
		tenths := int(d.Round(100*time.Millisecond).Milliseconds()) / 100
		if tenths%10 == 0 {
			return fmt.Sprintf("%ds", tenths/10)
		}
		return fmt.Sprintf("%.1fs", float64(tenths)/10.0)
	}
	return formatDuration(d)
}

// summarizeToolRound aggregates a slice of round steps into a Grok Build summary string:
// e.g. "Explored 2 directories, 23 files, called 1 tool, ran 2 commands".
func summarizeToolRound(entries []TranscriptEntry) string {
	var (
		dirs            int
		files           int
		searches        int
		edits           int
		cmds            int
		otherTools      int
		subagents       int
		questions       int
		thoughts        int
		failedCount     int
		totalThoughtDur time.Duration
	)

	for _, e := range entries {
		switch e.Kind {
		case KindReasoning:
			thoughts++
			totalThoughtDur += e.ReasoningDuration
		case KindSubagent:
			subagents++
		case KindQuestionAnswered:
			if len(e.AnsweredQuestions) > 0 {
				questions += len(e.AnsweredQuestions)
			} else {
				questions++
			}
		case KindTool:
			hasNonZeroExit := e.ToolExitCode != nil && *e.ToolExitCode != 0
			if e.ToolFailed || hasNonZeroExit {
				failedCount++
			}
			name := strings.ToLower(strings.TrimSpace(e.ToolName))
			if len(e.ToolChanges) > 0 {
				edits += len(e.ToolChanges)
				continue
			}
			switch name {
			case "list_dir":
				dirs++
			case "read_file":
				files++
			case "grep", "glob", "find_files", "search_files":
				searches++
			case "write_file", "edit_file", "replace_file_content", "apply_patch":
				edits++
			case "command", "run_command", "bash", "exec_command", "shell":
				cmds++
			case "ask_user_question", "ask_question", "ask_user":
				questions++
			case "delegate_team":
				subagents++
			default:
				otherTools++
			}
		}
	}

	plural := func(n int, singular, pluralForm string) string {
		if n == 1 {
			return fmt.Sprintf("1 %s", singular)
		}
		return fmt.Sprintf("%d %s", n, pluralForm)
	}

	var parts []string
	if dirs > 0 || files > 0 || searches > 0 {
		var exp []string
		if dirs > 0 {
			exp = append(exp, plural(dirs, "directory", "directories"))
		}
		if files > 0 {
			exp = append(exp, plural(files, "file", "files"))
		}
		if searches > 0 {
			exp = append(exp, plural(searches, "search", "searches"))
		}
		parts = append(parts, "Explored "+strings.Join(exp, ", "))
	}
	if edits > 0 {
		verb := "edited "
		if len(parts) == 0 {
			verb = "Edited "
		}
		parts = append(parts, verb+plural(edits, "file", "files"))
	}
	if otherTools > 0 {
		verb := "called "
		if len(parts) == 0 {
			verb = "Called "
		}
		parts = append(parts, verb+plural(otherTools, "tool", "tools"))
	}
	if cmds > 0 {
		verb := "ran "
		if len(parts) == 0 {
			verb = "Ran "
		}
		parts = append(parts, verb+plural(cmds, "command", "commands"))
	}
	if subagents > 0 {
		verb := "ran "
		if len(parts) == 0 {
			verb = "Ran "
		}
		parts = append(parts, verb+plural(subagents, "subagent", "subagents"))
	}
	if questions > 0 {
		verb := "asked "
		if len(parts) == 0 {
			verb = "Asked "
		}
		parts = append(parts, verb+plural(questions, "question", "questions"))
	}

	summary := strings.Join(parts, ", ")
	if summary == "" {
		if thoughts > 0 {
			if dur := formatThoughtDuration(totalThoughtDur); dur != "" {
				summary = "Thought for " + dur
			} else {
				summary = "Thought"
			}
		} else {
			summary = fmt.Sprintf("Executed %d step(s)", len(entries))
		}
	}
	if failedCount > 0 {
		summary += fmt.Sprintf(" (%d failed)", failedCount)
	}
	return summary
}

// renderCollapsedToolRound renders a completed tool round as a single-line Grok Build bar.
func renderCollapsedToolRound(entries []TranscriptEntry, width int) string {
	th := activeTheme()
	summary := summarizeToolRound(entries)
	chevron := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Bold(true).Render("› ")
	summaryStyled := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Render(
		sanitizeAndTruncateLine(summary, max(20, width-4)),
	)
	content := chevron + summaryStyled
	pad := max(0, width-lipgloss.Width(content))
	cardStyle := lipgloss.NewStyle().Background(lipgloss.Color(th.CardBg))
	return renderCardSurface(cardStyle, content+strings.Repeat(" ", pad))
}

// renderExpandedToolRound renders an expanded or live tool round with a vertical `│ ` tree
// and returns the rendered string along with relative line ranges (header + each child item).
func renderExpandedToolRound(entries []TranscriptEntry, startIdx int, live bool, width int) (string, []entryLineRange) {
	th := activeTheme()
	summary := summarizeToolRound(entries)

	var icon string
	if live {
		icon = lipgloss.NewStyle().Foreground(lipgloss.Color(th.AccentBlue)).Bold(true).Render("⠋ ")
	} else {
		icon = lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextPrimary)).Bold(true).Render("⌄ ")
	}
	summaryStyled := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextPrimary)).Render(
		sanitizeAndTruncateLine(summary, max(20, width-4)),
	)
	headerContent := icon + summaryStyled
	pad := max(0, width-lipgloss.Width(headerContent))
	cardStyle := lipgloss.NewStyle().Background(lipgloss.Color(th.CardBg))
	headerLine := renderCardSurface(cardStyle, headerContent+strings.Repeat(" ", pad))

	var allLines []string
	allLines = append(allLines, headerLine)

	ranges := make([]entryLineRange, 0, len(entries)+1)
	ranges = append(ranges, entryLineRange{
		EntryIndex:    startIdx,
		StartLine:     0,
		EndLine:       0,
		IsRoundHeader: true,
	})

	bar := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Border)).Render("│ ")
	childWidth := max(20, width-4)

	for k, entry := range entries {
		childBlock := renderRoundChildEntry(entry, live, childWidth)
		childLines := strings.Split(childBlock, "\n")
		childStart := len(allLines)
		for _, cl := range childLines {
			allLines = append(allLines, bar+cl)
		}
		childEnd := len(allLines) - 1
		ranges = append(ranges, entryLineRange{
			EntryIndex:    startIdx + k,
			StartLine:     childStart,
			EndLine:       childEnd,
			IsRoundHeader: false,
		})
	}

	return strings.Join(allLines, "\n"), ranges
}

// renderRoundChildEntry renders an individual step inside an expanded tool round tree.
// Compact 1-line header (`› Read go.mod`, `› Thought for 1.6s`, `› Ran git status --short`)
// when collapsed, or inline details when clicked (`ToolExpanded` / `ReasoningExpanded` / `SubagentExpanded`) or running.
func renderRoundChildEntry(entry TranscriptEntry, liveRound bool, width int) string {
	th := activeTheme()
	mutedChevron := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Render("› ")
	openChevron := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextPrimary)).Bold(true).Render("⌄ ")
	runSpinner := lipgloss.NewStyle().Foreground(lipgloss.Color(th.AccentBlue)).Bold(true).Render("⠋ ")

	switch entry.Kind {
	case KindReasoning:
		durStr := formatThoughtDuration(entry.ReasoningDuration)
		var label string
		if entry.ReasoningLive {
			if durStr != "" {
				label = fmt.Sprintf("Thinking... (%s)", durStr)
			} else {
				label = "Thinking..."
			}
		} else {
			if durStr != "" {
				label = fmt.Sprintf("Thought for %s", durStr)
			} else {
				label = "Thought"
			}
		}
		prefix := mutedChevron
		if entry.ReasoningLive {
			prefix = runSpinner
		} else if entry.ReasoningExpanded {
			prefix = openChevron
		}
		header := prefix + lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Italic(true).Render(label)
		bodyText := strings.TrimSpace(entry.Text)
		if (!entry.ReasoningExpanded && !entry.ReasoningLive) || bodyText == "" {
			return header
		}
		thoughtStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Italic(true)
		wrapped := RenderMarkdown(bodyText, max(20, width-4))
		var out []string
		out = append(out, header)
		for _, l := range strings.Split(wrapped, "\n") {
			out = append(out, "  "+thoughtStyle.Render(l))
		}
		return strings.Join(out, "\n")

	case KindQuestionAnswered:
		qSummary := ""
		if len(entry.AnsweredQuestions) > 0 {
			qSummary = strings.TrimSpace(entry.AnsweredQuestions[0].Question)
			if len(entry.AnsweredQuestions) > 1 {
				qSummary = fmt.Sprintf("%s (+%d more)", qSummary, len(entry.AnsweredQuestions)-1)
			}
		}
		qSummary = sanitizeAndTruncateLine(qSummary, max(20, width-10))
		prefix := mutedChevron
		if entry.ToolExpanded {
			prefix = openChevron
		}
		action := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Render("Ask ")
		header := prefix + action + styleTextPrimary().Render(qSummary)
		if !entry.ToolExpanded {
			return header
		}
		var rows []string
		rows = append(rows, header)
		for _, qa := range entry.AnsweredQuestions {
			rows = append(rows, "  • "+styleTextPrimary().Render(qa.Question))
			rows = append(rows, "    "+styleTextMuted().Render("answer: ")+lipgloss.NewStyle().Foreground(lipgloss.Color(th.AccentBlue)).Render(qa.Answer))
		}
		return strings.Join(rows, "\n")

	case KindSubagent:
		name := entry.AgentName
		if name == "" {
			name = entry.AgentID
		}
		prefix := mutedChevron
		if entry.SubagentRunning {
			prefix = runSpinner
		} else if entry.SubagentExpanded {
			prefix = openChevron
		}
		action := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Render("Subagent ")
		nameStyled := lipgloss.NewStyle().Foreground(lipgloss.Color(th.PlanPurple)).Bold(true).Render(name)
		roleStyled := ""
		if entry.AgentRole != "" {
			roleStyled = styleTextMuted().Render(" (" + entry.AgentRole + ")")
		}
		durStyled := ""
		if !entry.SubagentRunning && entry.SubagentDuration > 0 {
			durStyled = styleTextMuted().Render(" · " + formatDuration(entry.SubagentDuration))
		}
		header := prefix + action + nameStyled + roleStyled + durStyled
		if entry.SubagentExpanded && strings.TrimSpace(entry.Text) != "" {
			rendered := RenderMarkdown(strings.TrimSpace(entry.Text), max(20, width-4))
			var out []string
			out = append(out, header)
			for _, l := range strings.Split(rendered, "\n") {
				out = append(out, "  "+l)
			}
			return strings.Join(out, "\n")
		}
		if entry.SubagentRunning && strings.TrimSpace(entry.SubagentPreview) != "" {
			preview := sanitizeAndTruncateLine(strings.TrimSpace(entry.SubagentPreview), max(20, width-6))
			return header + "\n  " + styleTextMuted().Render(preview)
		}
		return header

	case KindTool:
		hasNonZeroExit := entry.ToolExitCode != nil && *entry.ToolExitCode != 0
		isFail := entry.ToolFailed || hasNonZeroExit

		prefix := mutedChevron
		if entry.ToolRunning {
			prefix = runSpinner
		} else if entry.ToolExpanded {
			prefix = openChevron
		}

		// File changes (Added / Edited / Deleted)
		if !entry.ToolRunning && !isFail && len(entry.ToolChanges) > 0 {
			ch := entry.ToolChanges[0]
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
			actionStyled := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Render(actionWord + " ")
			pathStyled := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextPrimary)).Render(ch.Path)
			addStyled := lipgloss.NewStyle().Foreground(lipgloss.Color(th.SuccessGreen)).Render(fmt.Sprintf("+%d", adds))
			delStyled := lipgloss.NewStyle().Foreground(lipgloss.Color(th.FailureRed)).Render(fmt.Sprintf("-%d", dels))
			header := fmt.Sprintf("%s%s%s (%s %s)", prefix, actionStyled, pathStyled, addStyled, delStyled)
			if !entry.ToolExpanded {
				return header
			}
			card := renderFileChangesCard(entry, max(20, width-4))
			cardLines := strings.Split(card, "\n")
			var out []string
			out = append(out, header)
			for i := 1; i < len(cardLines); i++ {
				out = append(out, "  "+cardLines[i])
			}
			return strings.Join(out, "\n")
		}

		commandStr := strings.TrimSpace(entry.ToolCommand)
		if commandStr == "" || commandStr == entry.ToolName || commandStr == entry.ToolName+" "+entry.ToolArgs {
			commandStr = formatToolSummary(entry.ToolName, entry.ToolArgs, "")
		}

		actionWord := "Ran"
		targetText := commandStr
		switch strings.ToLower(strings.TrimSpace(entry.ToolName)) {
		case "read_file":
			actionWord = "Read"
			targetText = strings.TrimPrefix(targetText, "Read ")
		case "list_dir":
			actionWord = "Listed"
			targetText = strings.TrimPrefix(strings.TrimPrefix(targetText, "List "), "list_dir ")
		case "grep", "search_files":
			actionWord = "Searched"
			targetText = strings.TrimPrefix(targetText, "Search ")
		case "glob", "find_files":
			actionWord = "Found"
			targetText = strings.TrimPrefix(targetText, "Find ")
		case "write_file":
			actionWord = "Added"
			targetText = strings.TrimPrefix(targetText, "Write ")
		case "edit_file", "replace_file_content":
			actionWord = "Edited"
			targetText = strings.TrimPrefix(targetText, "Edit ")
		case "command", "run_command", "bash", "exec_command", "shell":
			actionWord = "Ran"
		default:
			if !isExploreTool(entry.ToolName) {
				actionWord = "Called"
			}
		}

		if entry.ToolRunning {
			actionWord = "Running " + actionWord
		} else if isFail {
			if hasNonZeroExit {
				actionWord = fmt.Sprintf("Failed (exit %d)", *entry.ToolExitCode)
			} else {
				actionWord = "Failed"
			}
		}

		targetText = sanitizeAndTruncateLine(targetText, max(20, width-len(actionWord)-10))
		var actionStyled string
		if isFail {
			actionStyled = lipgloss.NewStyle().Foreground(lipgloss.Color(th.FailureRed)).Bold(true).Render(actionWord + " ")
		} else if entry.ToolRunning {
			actionStyled = lipgloss.NewStyle().Foreground(lipgloss.Color(th.AccentBlue)).Bold(true).Render(actionWord + " ")
		} else {
			actionStyled = lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Render(actionWord + " ")
		}
		targetStyled := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextPrimary)).Render(targetText)
		durBadge := ""
		if !entry.ToolRunning && entry.ToolDuration >= 500*time.Millisecond {
			durBadge = styleTextMuted().Render(" (" + formatDuration(entry.ToolDuration) + ")")
		}
		header := prefix + actionStyled + targetStyled + durBadge

		// Show details below the child header when expanded by user, or while actively running / failed in a live round
		showDetails := entry.ToolExpanded || entry.ToolRunning || (liveRound && isFail)
		if !showDetails {
			return header
		}

		output := strings.TrimSpace(entry.ToolOutput)
		branch := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Render("└ ")
		if output == "" {
			if entry.ToolRunning {
				return header + "\n  " + branch + styleTextMuted().Render("Running...")
			}
			return header + "\n  " + branch + styleTextMuted().Render("(no output)")
		}

		outLines := strings.Split(output, "\n")
		previewCount := len(outLines)
		startLineIdx := 0
		if !entry.ToolExpanded && len(outLines) > 3 {
			previewCount = 3
			startLineIdx = len(outLines) - 3
		}
		maxLineLen := max(20, width-8)
		var detailLines []string
		detailLines = append(detailLines, header)
		for i := 0; i < previewCount; i++ {
			line := sanitizeAndTruncateLine(outLines[startLineIdx+i], maxLineLen)
			if i == 0 {
				detailLines = append(detailLines, "  "+branch+styleTextMuted().Render(line))
			} else {
				detailLines = append(detailLines, "    "+styleTextMuted().Render(line))
			}
		}
		return strings.Join(detailLines, "\n")
	}

	return renderEntry(entry, width)
}

// formatDuration formats elapsed duration into natural human-readable units:
// - "12s" for seconds
// - "1m 24s" or "16m 40s" for minutes and seconds
// - "1h 5m 2s" for hours
func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	totalSecs := int(d.Seconds())
	if totalSecs <= 0 {
		return "0s"
	}
	if totalSecs < 60 {
		return fmt.Sprintf("%ds", totalSecs)
	}
	hours := totalSecs / 3600
	minutes := (totalSecs % 3600) / 60
	seconds := totalSecs % 60
	if hours > 0 {
		if seconds > 0 {
			return fmt.Sprintf("%dh %dm %ds", hours, minutes, seconds)
		}
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	if seconds > 0 {
		return fmt.Sprintf("%dm %ds", minutes, seconds)
	}
	return fmt.Sprintf("%dm", minutes)
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

// renderAutocompletePopup renders the floating slash command suggestions above the composer.
func renderAutocompletePopup(items []AutocompleteItem, selected int, width int) string {
	if len(items) == 0 {
		return ""
	}
	total := len(items)
	visible := min(total, maxVisibleAutocomplete)

	startIdx := 0
	if total > visible {
		startIdx = selected - visible/2
		if startIdx < 0 {
			startIdx = 0
		}
		if startIdx+visible > total {
			startIdx = total - visible
		}
	}
	endIdx := startIdx + visible

	var rows []string
	maxLeft := max(24, width*3/5)
	leftCol := 16
	for _, item := range items {
		leftCol = max(leftCol, min(maxLeft, lipgloss.Width(item.Command)+2))
	}

	for i := startIdx; i < endIdx; i++ {
		item := items[i]
		marker := "  "
		if i == selected {
			marker = "› "
		}
		displayCmd := fitTerminalText(item.Command, maxLeft-2)
		cmdText := displayCmd + strings.Repeat(" ", max(1, leftCol-lipgloss.Width(displayCmd)))
		descText := item.Description
		line := marker + cmdText + descText
		if lipgloss.Width(line) > width {
			line = ansi.Truncate(line, width, "…")
		}

		if i == selected {
			pad := max(0, width-lipgloss.Width(line))
			rows = append(rows, styleSelectedRow().Render(line+strings.Repeat(" ", pad)))
		} else {
			var cmdStyled string
			if strings.HasPrefix(item.Command, "@") {
				rawCmd := strings.TrimRight(cmdText, " ")
				spaces := cmdText[len(rawCmd):]
				if lastSlash := strings.LastIndex(rawCmd, "/"); lastSlash != -1 {
					dirPart := rawCmd[:lastSlash+1]
					filePart := rawCmd[lastSlash+1:]
					cmdStyled = styleTextMuted().Render(dirPart) +
						lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().AccentBlue)).Bold(true).Render(filePart) +
						spaces
				} else {
					cmdStyled = lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().AccentBlue)).Bold(true).Render(rawCmd) + spaces
				}
			} else {
				cmdStyled = lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().TextPrimary)).Render(cmdText)
			}
			descStyled := styleTextMuted().Render(descText)
			rows = append(rows, marker+cmdStyled+descStyled)
		}
	}
	return strings.Join(rows, "\n")
}

// renderQuestionPanel renders the clarification question card at the bottom.
func renderQuestionPanel(q *ActiveQuestion, width int) string {
	if len(q.Request.Questions) == 0 {
		return ""
	}
	currIdx := q.CurrentIndex
	if currIdx >= len(q.Request.Questions) {
		currIdx = len(q.Request.Questions) - 1
	}
	total := len(q.Request.Questions)
	unanswered := total - len(q.Answers)
	if unanswered < 1 {
		unanswered = 1
	}
	qItem := q.Request.Questions[currIdx]
	meta := styleTextMuted().Render(fmt.Sprintf("Question %d/%d (%d unanswered)", currIdx+1, total, unanswered))
	title := lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().AccentBlue)).Bold(true).Render(qItem.Question)

	var optionsRows []string
	for i, opt := range qItem.Options {
		num := fmt.Sprintf("%d. ", i+1)
		label := opt.Label
		desc := opt.Description
		marker := "  "
		if i == q.Selected {
			marker = "› "
		}
		raw := fmt.Sprintf("%s%s%-24s %s", marker, num, label, desc)
		if i == q.Selected {
			pad := max(0, width-lipgloss.Width(raw)-4)
			optionsRows = append(optionsRows, styleSelectedRow().Render(raw+strings.Repeat(" ", pad)))
		} else {
			numStyled := styleTextPrimary().Render(marker + num)
			lblStyled := styleTextPrimary().Bold(true).Render(fmt.Sprintf("%-24s ", label))
			descStyled := styleTextMuted().Render(desc)
			optionsRows = append(optionsRows, numStyled+lblStyled+descStyled)
		}
	}

	noneIdx := len(qItem.Options)
	noneMarker := "  "
	if q.Selected == noneIdx {
		noneMarker = "› "
	}
	noneRaw := fmt.Sprintf("%s%d. None of the above     Type custom answer or press tab for notes", noneMarker, noneIdx+1)
	if q.Selected == noneIdx {
		pad := max(0, width-lipgloss.Width(noneRaw)-4)
		optionsRows = append(optionsRows, styleSelectedRow().Render(noneRaw+strings.Repeat(" ", pad)))
		customPrompt := "    Custom answer: " + q.Custom + "█"
		optionsRows = append(optionsRows, lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().AccentBlue)).Render(customPrompt))
	} else {
		optionsRows = append(optionsRows, styleTextMuted().Render(noneRaw))
	}

	if q.NotesOpen || q.NoteText != "" {
		cursor := ""
		if q.NotesOpen {
			cursor = "█"
		}
		notesLine := "  Notes: " + q.NoteText + cursor
		optionsRows = append(optionsRows, "")
		optionsRows = append(optionsRows, lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().AccentBlue)).Render(notesLine))
	}

	hint := styleTextMuted().Render("tab to add notes | enter to submit answer | esc to interrupt")

	panelContent := meta + "\n" + title + "\n\n" + strings.Join(optionsRows, "\n") + "\n\n" + hint
	cardStyle := lipgloss.NewStyle().
		Background(lipgloss.Color(activeTheme().CardBg)).
		Foreground(lipgloss.Color(activeTheme().TextPrimary)).
		Padding(1, 2).
		Width(max(20, width))

	return renderCardSurface(cardStyle, panelContent)
}

// renderEffortPicker renders the interactive reasoning-effort selector.
func renderEffortPicker(m *tuiModel, width int) string {
	title := styleTextPrimary().Bold(true).Render("Reasoning effort")
	current := strings.TrimSpace(m.reasoningEffort)

	options := reasoningEffortOptions()
	var rows []string
	for i, opt := range options {
		marker := "  "
		if i == m.effortPicker.Selected {
			marker = "› "
		}
		label := fmt.Sprintf("%d. %-8s", i+1, opt.Label)
		suffix := ""
		if opt.Value == current {
			suffix = "  ✓ current"
		}
		raw := fmt.Sprintf("%s%-42s %s%s", marker, label, opt.Description, suffix)
		if i == m.effortPicker.Selected {
			pad := max(0, width-lipgloss.Width(raw)-4)
			rows = append(rows, styleSelectedRow().Render(raw+strings.Repeat(" ", pad)))
		} else {
			labelStyled := styleTextPrimary().Render(marker + label)
			descStyled := styleTextMuted().Render(opt.Description + suffix)
			rows = append(rows, labelStyled+descStyled)
		}
	}

	footer := styleTextMuted().Render("↑/↓ or 1-8 select · enter apply · esc cancel")
	content := title + "\n\n" + strings.Join(rows, "\n") + "\n\n" + footer

	cardStyle := lipgloss.NewStyle().
		Background(lipgloss.Color(activeTheme().CardBg)).
		Foreground(lipgloss.Color(activeTheme().TextPrimary)).
		Padding(1, 2).
		Width(max(20, width))
	return renderCardSurface(cardStyle, content)
}

// renderPlanReviewPanel renders the plan implementation options.
func renderPlanReviewPanel(review *ActivePlanReview, width int) string {
	title := styleTextPrimary().Bold(true).Render("Implement this plan?")

	choices := []struct {
		Label string
		Desc  string
	}{
		{"1. Yes, implement this plan", "Switch to Default and start coding"},
		{"2. Yes, clear context and implement", "Start a fresh thread"},
		{"3. No, stay in Plan mode", "Continue planning with the model"},
	}

	var rows []string
	for i, c := range choices {
		marker := "  "
		if i == review.Selected {
			marker = "› "
		}
		raw := fmt.Sprintf("%s%-36s %s", marker, c.Label, c.Desc)
		if i == review.Selected {
			pad := max(0, width-lipgloss.Width(raw)-4)
			rows = append(rows, styleSelectedRow().Render(raw+strings.Repeat(" ", pad)))
		} else {
			lblStyled := styleTextPrimary().Render(marker + fmt.Sprintf("%-36s ", c.Label))
			descStyled := styleTextMuted().Render(c.Desc)
			rows = append(rows, lblStyled+descStyled)
		}
	}

	footer := styleTextMuted().Render("1-3 / enter select · esc stay in Plan mode")
	content := title + "\n\n" + strings.Join(rows, "\n") + "\n\n" + footer

	cardStyle := lipgloss.NewStyle().
		Background(lipgloss.Color(activeTheme().CardBg)).
		Foreground(lipgloss.Color(activeTheme().TextPrimary)).
		Padding(1, 2).
		Width(max(20, width))
	return renderCardSurface(cardStyle, content)
}

// renderCountdownProgressBar draws an ASCII progress bar representing cool-down completion.
func renderCountdownProgressBar(remainingSecs, maxSecs, width int) string {
	if width <= 0 {
		return ""
	}
	if maxSecs <= 0 {
		maxSecs = 60
	}
	ratio := float64(remainingSecs) / float64(maxSecs)
	if ratio > 1.0 {
		ratio = 1.0
	}
	if ratio < 0.0 {
		ratio = 0.0
	}
	elapsedRatio := 1.0 - ratio
	elapsedWidth := int(math.Round(elapsedRatio * float64(width)))
	if elapsedWidth > width {
		elapsedWidth = width
	}
	remainingWidth := width - elapsedWidth
	return "[" + strings.Repeat("█", elapsedWidth) + strings.Repeat("░", remainingWidth) + "]"
}

// renderPausedBanner renders an actionable checkpoint status banner in the composer gap.
func renderPausedBanner(width int) string {
	th := activeTheme()
	icon := lipgloss.NewStyle().Foreground(lipgloss.Color(th.WarningAmber)).Bold(true).Render("⏸ ")
	label := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextPrimary)).Bold(true).Render("Task Paused (Checkpoint saved)")
	actions := lipgloss.NewStyle().Foreground(lipgloss.Color(th.AccentBlue)).Render(" · /resume to continue · /discard to drop")
	banner := icon + label + actions
	pad := max(0, (width-lipgloss.Width(banner))/2)
	return strings.Repeat(" ", pad) + banner
}

// renderRecoveryPanel renders the model recovery prompt when a provider times out or rate limits.
func renderRecoveryPanel(recovery *ActiveRecovery, width int) string {
	if recovery == nil {
		return ""
	}
	th := activeTheme()
	cardStyle := lipgloss.NewStyle().
		Background(lipgloss.Color(th.CardBg)).
		Foreground(lipgloss.Color(th.TextPrimary)).
		Padding(1, 2).
		Width(max(20, width))

	var lines []string
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(th.WarningAmber)).
		Render("Model Recovery Required")
	lines = append(lines, title)

	reason := recovery.Reason
	if reason == "" {
		reason = "Model request was interrupted or failed retry deadline."
	}
	lines = append(lines, styleTextPrimary().Render("Reason: "+reason))

	if !recovery.RetryNotBefore.IsZero() {
		lines = append(lines, "")
		if time.Now().Before(recovery.RetryNotBefore) {
			remaining := time.Until(recovery.RetryNotBefore).Round(time.Second)
			secs := int(remaining.Seconds())
			if secs < 0 {
				secs = 0
			}
			bar := renderCountdownProgressBar(secs, 60, 20)
			countdownLine := fmt.Sprintf("⏳ Rate limit cool-down: %s remaining  %s", remaining, bar)
			lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color(th.WarningAmber)).Bold(true).Render(countdownLine))
			lines = append(lines, styleTextMuted().Render("   Provider rate limit active. Execution checkpoint is safely preserved."))
		} else {
			readyLine := lipgloss.NewStyle().Foreground(lipgloss.Color(th.SuccessGreen)).Bold(true).
				Render("✓ Cool-down elapsed — Ready to resume execution")
			lines = append(lines, readyLine)
		}
	}

	lines = append(lines, "")

	options := []struct {
		Label string
		Desc  string
	}{
		{"1. Retry model request", "Resume the paused task and retry model call"},
		{"2. Discard paused task", "Cancel current execution and return to prompt"},
	}

	for i, opt := range options {
		marker := "  "
		if i == recovery.Selected {
			marker = "› "
		}
		raw := fmt.Sprintf("%s%-28s %s", marker, opt.Label, opt.Desc)
		if i == recovery.Selected {
			pad := max(0, width-lipgloss.Width(raw)-4)
			lines = append(lines, styleSelectedRow().Render(raw+strings.Repeat(" ", pad)))
		} else {
			lblStyled := styleTextPrimary().Render(marker + fmt.Sprintf("%-28s ", opt.Label))
			descStyled := styleTextMuted().Render(opt.Desc)
			lines = append(lines, lblStyled+descStyled)
		}
	}

	lines = append(lines, "")
	lines = append(lines, styleTextMuted().Render("enter select · esc / 2 discard · 1 retry · r / d quick action"))

	return renderCardSurface(cardStyle, strings.Join(lines, "\n"))
}

// renderToolRecoveryPanel renders the tool recovery modal when a tool fails and needs retry or skip.
func renderToolRecoveryPanel(recovery *ActiveToolRecovery, width int) string {
	if recovery == nil {
		return ""
	}
	th := activeTheme()
	cardStyle := lipgloss.NewStyle().
		Background(lipgloss.Color(th.CardBg)).
		Foreground(lipgloss.Color(th.TextPrimary)).
		Padding(1, 2).
		Width(max(20, width))

	var lines []string
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(th.WarningAmber)).
		Render("Tool Failure Recovery Required")
	lines = append(lines, title)

	toolLine := fmt.Sprintf("Tool: %s", styleTextPrimary().Bold(true).Render(recovery.ToolName))
	lines = append(lines, toolLine)

	if recovery.Arguments != "" && recovery.Arguments != "null" {
		argsStr := sanitizeAndTruncateLine(recovery.Arguments, max(20, width-16))
		lines = append(lines, styleTextMuted().Render("Arguments: "+argsStr))
	}

	if recovery.ErrorOutput != "" {
		lines = append(lines, "")
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color(th.FailureRed)).Bold(true).Render("Failure Details:"))
		errLines := strings.Split(strings.TrimSpace(recovery.ErrorOutput), "\n")
		maxErrLines := 4
		for i, el := range errLines {
			if i >= maxErrLines {
				lines = append(lines, styleTextMuted().Render(fmt.Sprintf("  ... (+%d more lines)", len(errLines)-maxErrLines)))
				break
			}
			lines = append(lines, styleTextPrimary().Render("  "+sanitizeAndTruncateLine(el, max(20, width-8))))
		}
	}

	lines = append(lines, "")

	options := []struct {
		Label string
		Desc  string
	}{
		{"1. Retry tool execution", "Attempt executing the tool again"},
		{"2. Skip this tool call", "Bypass execution and continue session"},
	}

	for i, opt := range options {
		marker := "  "
		if i == recovery.Selected {
			marker = "› "
		}
		raw := fmt.Sprintf("%s%-28s %s", marker, opt.Label, opt.Desc)
		if i == recovery.Selected {
			pad := max(0, width-lipgloss.Width(raw)-4)
			lines = append(lines, styleSelectedRow().Render(raw+strings.Repeat(" ", pad)))
		} else {
			lblStyled := styleTextPrimary().Render(marker + fmt.Sprintf("%-28s ", opt.Label))
			descStyled := styleTextMuted().Render(opt.Desc)
			lines = append(lines, lblStyled+descStyled)
		}
	}

	lines = append(lines, "")
	lines = append(lines, styleTextMuted().Render("enter select · esc / 2 skip · 1 retry · r / s quick action"))

	return renderCardSurface(cardStyle, strings.Join(lines, "\n"))
}

// renderApprovalPanel renders a permission approval modal.
func renderApprovalPanel(approval *ActiveApproval, width int) string {
	th := activeTheme()
	cardStyle := lipgloss.NewStyle().
		Background(lipgloss.Color(th.CardBg)).
		Foreground(lipgloss.Color(th.TextPrimary)).
		Padding(1, 2).
		Width(max(20, width))

	var lines []string
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(th.WarningAmber)).
		Render("Tool Approval Required")
	lines = append(lines, title)

	toolLine := fmt.Sprintf("Tool: %s", styleTextPrimary().Bold(true).Render(approval.Request.Tool))
	lines = append(lines, toolLine)

	summary := formatToolSummary(approval.Request.Tool, string(approval.Request.Arguments), "")
	if summary != "" && summary != approval.Request.Tool {
		lines = append(lines, fmt.Sprintf("Action: %s", styleTextPrimary().Render(summary)))
	}

	if len(approval.Request.Arguments) > 0 && string(approval.Request.Arguments) != "{}" {
		argsStr := sanitizeAndTruncateLine(string(approval.Request.Arguments), max(20, width-16))
		lines = append(lines, styleTextMuted().Render("Arguments: "+argsStr))
	}

	lines = append(lines, "")

	options := []string{"1. Allow execution", "2. Deny"}
	for i, opt := range options {
		marker := "  "
		if i == approval.Selected {
			marker = "› "
			raw := marker + opt
			pad := max(0, width-lipgloss.Width(raw)-4)
			lines = append(lines, styleSelectedRow().Render(raw+strings.Repeat(" ", pad)))
		} else {
			lines = append(lines, styleTextPrimary().Render(marker+opt))
		}
	}

	hint := styleTextMuted().Render("\n1/y/enter allow · 2/n/esc deny")
	lines = append(lines, hint)

	return renderCardSurface(cardStyle, strings.Join(lines, "\n"))
}

func truncateString(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return string(runes[:maxLen])
	}
	return string(runes[:maxLen-3]) + "..."
}

// formatRelativeTime converts a timestamp into a human-friendly relative string (e.g. "just now", "5m ago", "2h ago", "yesterday", "3d ago").
func formatRelativeTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := time.Since(t)
	if d < 0 {
		return "just now"
	}
	if d < time.Minute {
		return "just now"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	days := int(d.Hours() / 24)
	if days == 1 {
		return "yesterday"
	}
	if days < 30 {
		return fmt.Sprintf("%dd ago", days)
	}
	return t.Format("Jan 02")
}

// renderSessionsModal renders the interactive floating session switcher modal.
func renderSessionsModal(m *tuiModel, width int) string {
	th := activeTheme()
	cardStyle := lipgloss.NewStyle().
		Background(lipgloss.Color(th.CardBg)).
		Foreground(lipgloss.Color(th.TextPrimary)).
		Padding(1, 2).
		Width(max(20, width))

	var lines []string

	total := len(m.sessionsItems)
	filteredCount := len(m.sessionsFiltered)
	countInfo := ""
	if m.sessionsFilter != "" {
		countInfo = fmt.Sprintf(" (%d/%d)", filteredCount, total)
	} else if total > 0 {
		countInfo = fmt.Sprintf(" (%d)", total)
	}

	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(th.AccentBlue)).
		Render("Switch Session") + styleTextMuted().Render(countInfo)
	lines = append(lines, title)

	filterLabel := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Render("Filter: ")
	var filterVal string
	if m.sessionsFilter == "" {
		filterVal = lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Italic(true).Render("type to filter...")
	} else {
		filterVal = lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextPrimary)).Bold(true).Render(m.sessionsFilter)
	}
	lines = append(lines, filterLabel+filterVal)
	lines = append(lines, "")

	if filteredCount == 0 {
		lines = append(lines, "  "+styleTextMuted().Render("No matching sessions found."))
	} else {
		maxVisible := 8
		startIdx := 0
		if m.sessionsIndex >= maxVisible {
			startIdx = m.sessionsIndex - maxVisible + 1
		}
		endIdx := min(filteredCount, startIdx+maxVisible)

		availWidth := max(20, width-4)
		// marker (2) + shortID (12) + 2 spaces + titleWidth + 2 spaces + time (10) = 28 + titleWidth
		baseTitleWidth := max(10, availWidth-28)

		for i := startIdx; i < endIdx; i++ {
			s := m.sessionsFiltered[i]
			marker := "  "
			if i == m.sessionsIndex {
				marker = "› "
			}
			shortID := s.ID
			if len(shortID) > 12 {
				shortID = shortID[:12]
			}
			currentBadge := ""
			if s.ID == m.sessionID {
				currentBadge = " • active"
			}
			title := sessionDisplayTitle(s)
			timeStr := formatRelativeTime(s.UpdatedAt)

			rowTitleWidth := max(8, baseTitleWidth-lipgloss.Width(currentBadge))
			truncTitle := sanitizeAndTruncateLine(title, rowTitleWidth)
			titlePad := max(0, rowTitleWidth-lipgloss.Width(truncTitle))
			paddedTitle := truncTitle + strings.Repeat(" ", titlePad)

			if i == m.sessionsIndex {
				raw := fmt.Sprintf("%s%-12s  %s%s  %-10s", marker, shortID, paddedTitle, currentBadge, timeStr)
				pad := max(0, availWidth-lipgloss.Width(raw))
				lines = append(lines, styleSelectedRow().Render(raw+strings.Repeat(" ", pad)))
			} else {
				idStyled := lipgloss.NewStyle().Foreground(lipgloss.Color(th.AccentBlue)).Render(fmt.Sprintf("%-12s", shortID))
				titleStyled := styleTextPrimary().Render(paddedTitle)
				timeStyled := styleTextMuted().Render(fmt.Sprintf("%-10s", timeStr))
				badgeStyled := ""
				if currentBadge != "" {
					badgeStyled = lipgloss.NewStyle().Foreground(lipgloss.Color(th.SuccessGreen)).Bold(true).Render(currentBadge)
				}
				lines = append(lines, fmt.Sprintf("%s%s  %s%s  %s", marker, idStyled, titleStyled, badgeStyled, timeStyled))
			}
		}
	}

	if m.sessionsConfirmDelete {
		lines = append(lines, "")
		warn := lipgloss.NewStyle().Foreground(lipgloss.Color(th.WarningAmber)).Bold(true).
			Render(fmt.Sprintf("Delete session %s? (y to confirm · n/esc to cancel)", truncateString(m.sessionsDeletingID, 16)))
		lines = append(lines, "  "+warn)
	}

	lines = append(lines, "")
	lines = append(lines, styleTextMuted().Render("↑/↓ navigate · enter select · ctrl+d/del delete · esc close"))

	return renderCardSurface(cardStyle, strings.Join(lines, "\n"))
}

// renderToolPagerModal renders the dedicated floating pager modal for inspecting full tool output.
func renderToolPagerModal(m *tuiModel, width int) string {
	th := activeTheme()
	cardStyle := lipgloss.NewStyle().
		Background(lipgloss.Color(th.CardBg)).
		Foreground(lipgloss.Color(th.TextPrimary)).
		Padding(1, 2).
		Width(max(20, width))

	indices := m.getToolEntryIndices()
	if len(indices) == 0 {
		return renderCardSurface(cardStyle, styleTextMuted().Render("No tool executions in current session."))
	}

	idx := m.toolPagerIndex
	if idx < 0 {
		idx = 0
	} else if idx >= len(indices) {
		idx = len(indices) - 1
	}

	entry := m.entries[indices[idx]]
	toolNum := idx + 1
	totalTools := len(indices)

	// Status badge
	var statusBadge string
	if entry.ToolRunning {
		statusBadge = lipgloss.NewStyle().Foreground(lipgloss.Color(th.AccentBlue)).Bold(true).Render("[Running]")
	} else if entry.ToolFailed || (entry.ToolExitCode != nil && *entry.ToolExitCode != 0) {
		if entry.ToolExitCode != nil && *entry.ToolExitCode != 0 {
			statusBadge = lipgloss.NewStyle().Foreground(lipgloss.Color(th.FailureRed)).Bold(true).
				Render(fmt.Sprintf("[Failed (exit %d)]", *entry.ToolExitCode))
		} else {
			statusBadge = lipgloss.NewStyle().Foreground(lipgloss.Color(th.FailureRed)).Bold(true).Render("[Failed]")
		}
	} else if isExploreTool(entry.ToolName) {
		statusBadge = lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Bold(true).Render("[Explored]")
	} else {
		statusBadge = lipgloss.NewStyle().Foreground(lipgloss.Color(th.SuccessGreen)).Bold(true).Render("[Ran]")
	}

	// Tool command text
	cmdStr := entry.ToolCommand
	if cmdStr == "" {
		cmdStr = formatToolSummary(entry.ToolName, entry.ToolArgs, m.workspace)
	}
	availWidth := max(20, width-4)
	truncCmd := sanitizeAndTruncateLine(cmdStr, max(10, availWidth-lipgloss.Width(statusBadge)-3))
	cmdStyled := lipgloss.NewStyle().Foreground(lipgloss.Color(th.AccentBlue)).Render(truncCmd)

	output := entry.ToolOutput
	trimmedOut := strings.TrimRight(output, "\r\n")
	var rawLines []string
	if strings.TrimSpace(trimmedOut) == "" {
		rawLines = []string{}
	} else {
		rawLines = strings.Split(trimmedOut, "\n")
	}
	totalLines := len(rawLines)

	visibleLines := m.toolPagerVisibleLines()
	maxScroll := max(0, totalLines-visibleLines)
	scrollY := m.toolPagerScrollY
	if scrollY > maxScroll {
		scrollY = maxScroll
	}
	if scrollY < 0 {
		scrollY = 0
	}

	// Header line
	titleLeft := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(th.AccentBlue)).
		Render("Tool Output Pager") +
		styleTextMuted().Render(fmt.Sprintf(" (Tool %d of %d)", toolNum, totalTools))

	var scrollInfo string
	if totalLines > 0 {
		endLineNum := min(totalLines, scrollY+visibleLines)
		scrollInfo = styleTextMuted().Render(fmt.Sprintf("Lines %d-%d of %d", scrollY+1, endLineNum, totalLines))
	} else {
		scrollInfo = styleTextMuted().Render("0 lines")
	}

	headerGap := max(1, availWidth-lipgloss.Width(titleLeft)-lipgloss.Width(scrollInfo))
	headerLine := titleLeft + strings.Repeat(" ", headerGap) + scrollInfo

	toolHeaderLine := statusBadge + " " + cmdStyled

	var lines []string
	lines = append(lines, headerLine)
	lines = append(lines, toolHeaderLine)
	lines = append(lines, "")

	if totalLines == 0 {
		lines = append(lines, "  "+styleTextMuted().Italic(true).Render("(no output)"))
	} else {
		endIdx := min(totalLines, scrollY+visibleLines)
		numWidth := len(strconv.Itoa(totalLines))
		if numWidth < 2 {
			numWidth = 2
		}
		for i := scrollY; i < endIdx; i++ {
			lineNumStr := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).
				Render(fmt.Sprintf("%*d │ ", numWidth, i+1))
			maxContentWidth := max(10, availWidth-numWidth-4)
			lineContent := sanitizeAndTruncateLine(rawLines[i], maxContentWidth)
			contentStyled := styleTextPrimary().Render(lineContent)
			lines = append(lines, lineNumStr+contentStyled)
		}
	}

	lines = append(lines, "")

	// Footer hint or copy feedback
	if m.copyFeedback != "" && time.Now().Before(m.copyFeedbackExpiresAt) {
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color(th.SuccessGreen)).Bold(true).Render("✓ "+m.copyFeedback))
	} else {
		hint := styleTextMuted().Render("↑/↓ scroll · pgup/pgdn · [/] switch tool · c copy · esc/q/ctrl+t close")
		lines = append(lines, hint)
	}

	return renderCardSurface(cardStyle, strings.Join(lines, "\n"))
}

// renderDiffModal renders the floating workspace diff viewer modal.
func renderDiffModal(m *tuiModel, width int) string {
	th := activeTheme()
	cardStyle := lipgloss.NewStyle().
		Background(lipgloss.Color(th.CardBg)).
		Foreground(lipgloss.Color(th.TextPrimary)).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(th.Border)).
		Padding(1, 2).
		Width(width)

	if len(m.diffFiles) == 0 {
		emptyLine := styleTextMuted().Italic(true).Render("No git changes detected in workspace.")
		hint := styleTextMuted().Render("esc/q/ctrl+d close")
		return renderCardSurface(cardStyle, emptyLine+"\n\n"+hint)
	}

	idx := m.diffFileIndex
	if idx < 0 {
		idx = 0
	} else if idx >= len(m.diffFiles) {
		idx = len(m.diffFiles) - 1
	}

	file := m.diffFiles[idx]
	fileNum := idx + 1
	totalFiles := len(m.diffFiles)

	availWidth := max(20, width-4)

	// Header line with total workspace stats
	totAdds, totDels := computeDiffStats(m.diffFiles)
	titleLeft := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(th.AccentBlue)).
		Render("Workspace Diff Pager") +
		styleTextMuted().Render(fmt.Sprintf(" (File %d of %d · %d files +%d/-%d)", fileNum, totalFiles, totalFiles, totAdds, totDels))

	totalLines := len(file.Lines)
	visibleLines := m.diffModalVisibleLines()
	maxScroll := max(0, totalLines-visibleLines)
	scrollY := m.diffScrollY
	if scrollY > maxScroll {
		scrollY = maxScroll
	}
	if scrollY < 0 {
		scrollY = 0
	}

	var scrollInfo string
	if totalLines > 0 {
		endLineNum := min(totalLines, scrollY+visibleLines)
		scrollInfo = styleTextMuted().Render(fmt.Sprintf("Lines %d-%d of %d", scrollY+1, endLineNum, totalLines))
	} else {
		scrollInfo = styleTextMuted().Render("0 lines")
	}

	headerGap := max(1, availWidth-lipgloss.Width(titleLeft)-lipgloss.Width(scrollInfo))
	headerLine := titleLeft + strings.Repeat(" ", headerGap) + scrollInfo

	// Multi-file tab bar
	tabBar := renderDiffTabBar(m.diffFiles, idx, availWidth)

	// File info line with path, additions (+) and deletions (-)
	pathStyled := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(th.TextPrimary)).Render(file.Path)
	statsStyled := fmt.Sprintf(" %s %s",
		lipgloss.NewStyle().Foreground(lipgloss.Color(th.SuccessGreen)).Bold(true).Render(fmt.Sprintf("+%d", file.Additions)),
		lipgloss.NewStyle().Foreground(lipgloss.Color(th.FailureRed)).Bold(true).Render(fmt.Sprintf("-%d", file.Deletions)),
	)
	if file.IsBinary {
		statsStyled += " " + lipgloss.NewStyle().Foreground(lipgloss.Color(th.WarningAmber)).Render("[binary]")
	}

	var lines []string
	lines = append(lines, headerLine)
	if tabBar != "" {
		lines = append(lines, tabBar)
	}
	lines = append(lines, pathStyled+statsStyled)
	lines = append(lines, "")

	if totalLines == 0 {
		lines = append(lines, "  "+styleTextMuted().Italic(true).Render("(no changes)"))
	} else {
		endIdx := min(totalLines, scrollY+visibleLines)
		numWidth := len(strconv.Itoa(totalLines))
		if numWidth < 2 {
			numWidth = 2
		}
		for i := scrollY; i < endIdx; i++ {
			lineNumStr := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).
				Render(fmt.Sprintf("%*d │ ", numWidth, i+1))
			rawLine := file.Lines[i]
			maxContentWidth := max(10, availWidth-numWidth-4)
			lineContent := sanitizeAndTruncateLine(rawLine, maxContentWidth)

			var contentStyled string
			if strings.HasPrefix(rawLine, "diff --git") || strings.HasPrefix(rawLine, "index ") {
				contentStyled = lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextPrimary)).Bold(true).Render(lineContent)
			} else if strings.HasPrefix(rawLine, "---") {
				contentStyled = lipgloss.NewStyle().Foreground(lipgloss.Color(th.FailureRed)).Bold(true).Render(lineContent)
			} else if strings.HasPrefix(rawLine, "+++") {
				contentStyled = lipgloss.NewStyle().Foreground(lipgloss.Color(th.SuccessGreen)).Bold(true).Render(lineContent)
			} else if strings.HasPrefix(rawLine, "@@") {
				contentStyled = lipgloss.NewStyle().Foreground(lipgloss.Color(th.AccentBlue)).Bold(true).Render(lineContent)
			} else if strings.HasPrefix(rawLine, "+") {
				contentStyled = lipgloss.NewStyle().Foreground(lipgloss.Color(th.SuccessGreen)).Render(lineContent)
			} else if strings.HasPrefix(rawLine, "-") {
				contentStyled = lipgloss.NewStyle().Foreground(lipgloss.Color(th.FailureRed)).Render(lineContent)
			} else {
				contentStyled = styleTextPrimary().Render(lineContent)
			}

			lines = append(lines, lineNumStr+contentStyled)
		}
	}

	lines = append(lines, "")

	// Footer hint or copy feedback
	if m.copyFeedback != "" && time.Now().Before(m.copyFeedbackExpiresAt) {
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color(th.SuccessGreen)).Bold(true).Render("✓ "+m.copyFeedback))
	} else {
		hint := styleTextMuted().Render("1-9 jump · [/] or tab switch · c copy · esc/q close")
		lines = append(lines, hint)
	}

	return renderCardSurface(cardStyle, strings.Join(lines, "\n"))
}

// renderShortcutsPanel renders the keyboard shortcut cheat sheet in a 3-column layout.
func renderShortcutsPanel(width int) string {
	title := styleTextPrimary().Bold(true).Render("Keyboard Shortcuts")

	col1Title := styleTextPrimary().Bold(true).Underline(true).Render("Compose")
	col1Items := []struct{ Key, Desc string }{
		{"/", "Commands"},
		{"Esc", "Edit last message"},
		{"/clear", "Clear view"},
		{"Up / Down", "Prompt history"},
		{"Shift+Enter", "New line"},
		{"Ctrl+C", "Interrupt / Quit"},
	}

	col2Title := styleTextPrimary().Bold(true).Underline(true).Render("Session")
	col2Items := []struct{ Key, Desc string }{
		{"Enter", "Send message"},
		{"Ctrl+O", "Switch session"},
		{"Shift+Tab", "Cycle permissions"},
		{"/model", "Switch model"},
		{"/status", "Show status"},
		{"/sessions", "List sessions"},
	}

	col3Title := styleTextPrimary().Bold(true).Underline(true).Render("Transcript")
	col3Items := []struct{ Key, Desc string }{
		{"Mouse Drag", "Select & auto-copy"},
		{"Scroll", "History scroll"},
		{"Ctrl+T", "Tool output pager"},
		{"Ctrl+D", "Workspace diff viewer"},
		{"Ctrl+R", "Toggle reasoning"},
		{"Ctrl+C", "Copy selection"},
		{"Esc", "Clear selection"},
	}

	formatCol := func(colTitle string, items []struct{ Key, Desc string }) string {
		lines := []string{colTitle}
		for _, it := range items {
			k := lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().AccentBlue)).Bold(true).Render(it.Key)
			pad := max(1, 12-lipgloss.Width(k))
			d := styleTextPrimary().Render(it.Desc)
			lines = append(lines, k+strings.Repeat(" ", pad)+d)
		}
		return strings.Join(lines, "\n")
	}

	col1 := formatCol(col1Title, col1Items)
	col2 := formatCol(col2Title, col2Items)
	col3 := formatCol(col3Title, col3Items)

	var colsContent string
	if width >= 94 {
		colsContent = lipgloss.JoinHorizontal(lipgloss.Top, col1, "   ", col2, "   ", col3)
	} else {
		colsContent = col1 + "\n\n" + col2 + "\n\n" + col3
	}

	hint := styleTextMuted().Render("esc or ? to close")
	panelContent := title + "\n\n" + colsContent + "\n\n" + hint

	cardStyle := lipgloss.NewStyle().
		Background(lipgloss.Color(activeTheme().CardBg)).
		Foreground(lipgloss.Color(activeTheme().TextPrimary)).
		Padding(1, 2).
		Width(max(20, width))

	return renderCardSurface(cardStyle, panelContent)
}

// renderScrollPill renders the floating hint pill when scrolled away from bottom.
func renderScrollPill(width int) string {
	text := "↓ Back to bottom · esc"
	styled := lipgloss.NewStyle().
		Foreground(lipgloss.Color(activeTheme().AccentBlue)).
		Background(lipgloss.Color(activeTheme().CardBg)).
		Padding(0, 2).
		Render(text)
	left := max(0, (width-lipgloss.Width(styled))/2)
	return strings.Repeat(" ", left) + styled
}

// renderComposerBand renders the input box with a soft gray card background covering at least 3 rows.
func renderComposerBand(inputView string, width int) string {
	th := activeTheme()
	lines := strings.Split(inputView, "\n")
	var padded []string
	cardStyle := lipgloss.NewStyle().
		Background(lipgloss.Color(th.CardBg)).
		Foreground(lipgloss.Color(th.TextPrimary))

	// Top padding line
	emptyRow := cardStyle.Render(strings.Repeat(" ", width))
	padded = append(padded, emptyRow)

	// Middle input line(s)
	for _, l := range lines {
		content := " " + l
		pad := max(0, width-lipgloss.Width(content))
		padded = append(padded, renderCardSurface(cardStyle, content+strings.Repeat(" ", pad)))
	}

	// Bottom padding line
	padded = append(padded, emptyRow)

	return strings.Join(padded, "\n")
}

// formatCompactTokens formats token counts into human-readable compact strings (e.g. 500, 12.4k, 200k, 1M).
func formatCompactTokens(n int64) string {
	if n < 0 {
		n = 0
	}
	if n < 1000 {
		return strconv.FormatInt(n, 10)
	}
	if n < 1_000_000 {
		k := float64(n) / 1000.0
		if k >= 100 || math.Mod(k, 1) == 0 {
			return fmt.Sprintf("%.0fk", k)
		}
		str := fmt.Sprintf("%.1f", k)
		return strings.TrimRight(strings.TrimRight(str, "0"), ".") + "k"
	}
	m := float64(n) / 1_000_000.0
	if m >= 100 || math.Mod(m, 1) == 0 {
		return fmt.Sprintf("%.0fM", m)
	}
	str := fmt.Sprintf("%.1f", m)
	return strings.TrimRight(strings.TrimRight(str, "0"), ".") + "M"
}

// formatContextAndTokenSummary produces the 6 live metrics string for the footer:
// 1. Used context
// 2. Total context window
// 3. Percentage
// 4. Cache hit rate (Cache: X%)
// 5. Generation speed (X.X tok/s)
// 6. Cumulative session total tokens (Total: X tok)
func formatContextAndTokenSummary(m *tuiModel, maxWidth int) string {
	usedCtx := m.lastInputTokens
	if usedCtx == 0 {
		usedCtx = m.estimatedInputTokens
	}
	totalCtx := m.contextWindow
	if totalCtx <= 0 {
		totalCtx = 200000 // Standard fallback context window
	}

	pct := 0.0
	if totalCtx > 0 {
		pct = float64(usedCtx) / float64(totalCtx) * 100.0
		if pct > 100.0 {
			pct = 100.0
		}
	}
	ctxStr := fmt.Sprintf("Context: %s/%s (%.1f%%)", formatCompactTokens(usedCtx), formatCompactTokens(totalCtx), pct)

	cachePct := 0.0
	if m.totalInputTokens > 0 {
		cachePct = float64(m.cachedTokens) / float64(m.totalInputTokens) * 100.0
		if cachePct > 100.0 {
			cachePct = 100.0
		}
	}
	cacheStr := fmt.Sprintf("Cache: %.0f%%", cachePct)

	var speedStr string
	if m.lastTokensPerSec > 0 {
		speedStr = fmt.Sprintf("%.1f tok/s", m.lastTokensPerSec)
	} else {
		speedStr = "0.0 tok/s"
	}

	totalToks := m.totalInputTokens + m.totalOutputTokens
	totalStr := fmt.Sprintf("Total: %s tok", formatCompactTokens(totalToks))

	full := fmt.Sprintf("%s · %s · %s · %s", ctxStr, cacheStr, speedStr, totalStr)
	if maxWidth <= 0 || lipgloss.Width(full) <= maxWidth {
		return full
	}

	// Graceful degradation when footer width is limited
	withoutTotal := fmt.Sprintf("%s · %s · %s", ctxStr, cacheStr, speedStr)
	if lipgloss.Width(withoutTotal) <= maxWidth {
		return withoutTotal
	}

	withoutSpeed := fmt.Sprintf("%s · %s", ctxStr, cacheStr)
	if lipgloss.Width(withoutSpeed) <= maxWidth {
		return withoutSpeed
	}

	if lipgloss.Width(ctxStr) <= maxWidth {
		return ctxStr
	}

	compactCtx := fmt.Sprintf("%s/%s (%.1f%%)", formatCompactTokens(usedCtx), formatCompactTokens(totalCtx), pct)
	if lipgloss.Width(compactCtx) <= maxWidth {
		return compactCtx
	}

	return ansi.Truncate(full, maxWidth, "…")
}

// renderFooter renders the bottom two-line status bar.
func renderFooter(m *tuiModel, width int) string {
	// Right side: Permission / Plan mode
	var rightStr string
	if m.planMode {
		planLabel := stylePlanPurple().Bold(true).Render("Plan mode")
		cycleHint := styleTextMuted().Render("(shift+tab to cycle)")
		rightStr = planLabel + " " + cycleHint
	} else {
		permLabel := styleTextPrimary().Bold(true).Render(string(m.permissionMode))
		cycleHint := styleTextMuted().Render("(shift+tab to cycle)")
		rightStr = permLabel + " " + cycleHint
	}
	rightLen := lipgloss.Width(rightStr)

	// Keep the model and active subagent count visible; workspace and title are secondary.
	modelName := m.currentModelDisplayName()
	modelLabel := ""
	if modelName != "" {
		modelLabel = fmt.Sprintf("%s . %s", modelName, m.currentReasoningEffortLabel())
	}
	maxSubs := m.maxParallelAgents()
	activeSubs := m.activeSubagentsCount()
	subagentLabel := fmt.Sprintf("🤖 %d active · %d max", activeSubs, maxSubs)
	maxLeftWidth := max(1, width-rightLen-2)
	primaryItems := []string{subagentLabel}
	if modelLabel != "" {
		primaryItems = []string{modelLabel, subagentLabel}
	}
	rawLeft := strings.Join(primaryItems, " · ")
	if lipgloss.Width(rawLeft) > maxLeftWidth {
		compactSubagentLabel := fmt.Sprintf("🤖 %d/%d", activeSubs, maxSubs)
		modelWidth := maxLeftWidth - lipgloss.Width(compactSubagentLabel) - lipgloss.Width(" · ")
		if modelLabel != "" && modelWidth > 0 {
			modelLabel = ansi.Truncate(modelLabel, modelWidth, "…")
			rawLeft = modelLabel + " · " + compactSubagentLabel
		} else {
			rawLeft = compactSubagentLabel
			if lipgloss.Width(rawLeft) > maxLeftWidth {
				rawLeft = fmt.Sprintf("🤖 %d", activeSubs)
			}
			if lipgloss.Width(rawLeft) > maxLeftWidth {
				rawLeft = ansi.Truncate(fmt.Sprintf("%d", activeSubs), maxLeftWidth, "…")
			}
		}
	}
	remainingWidth := maxLeftWidth - lipgloss.Width(rawLeft)
	optionalItems := []string{m.workspace}
	title := m.sessionTitle
	if title == "" {
		title = deriveSessionTitleFromEntries(m.entries)
	}
	optionalItems = append(optionalItems, title)
	for _, item := range optionalItems {
		if item == "" || remainingWidth <= lipgloss.Width(" · ") {
			continue
		}
		itemWidth := min(lipgloss.Width(item), remainingWidth-lipgloss.Width(" · "))
		if itemWidth <= 0 {
			continue
		}
		shown := ansi.Truncate(item, itemWidth, "…")
		rawLeft += " · " + shown
		remainingWidth -= lipgloss.Width(" · ") + lipgloss.Width(shown)
	}
	leftStr := styleTextMuted().Render(rawLeft)

	leftLen := lipgloss.Width(leftStr)
	gap := max(1, width-leftLen-rightLen)
	line1 := leftStr + strings.Repeat(" ", gap) + rightStr

	// Line 2: 6 token & context diagnostic metrics (left) + contextual hint / shortcuts (right)
	var line2RightText string
	if m.running {
		if m.totalSubagentsCount() > 0 {
			line2RightText = "esc to interrupt · /agents"
		} else {
			line2RightText = "esc to interrupt"
		}
	} else if m.hasPausedTask() {
		line2RightText = "⏸ task paused · /resume or /discard"
	} else if m.composer.Value() == "" && len(m.promptHistory) > 0 {
		line2RightText = "esc to edit last message"
	} else if m.totalSubagentsCount() > 0 || m.maxParallelAgents() > 0 {
		line2RightText = "? for shortcuts · /agents to inspect"
	} else {
		line2RightText = "? for shortcuts"
	}

	line2Right := styleTextMuted().Render(line2RightText)
	right2Len := lipgloss.Width(line2Right)

	maxMetricsWidth := max(10, width-right2Len-2)
	metricsStr := formatContextAndTokenSummary(m, maxMetricsWidth)
	line2Left := styleTextMuted().Render(metricsStr)

	gap2 := max(1, width-lipgloss.Width(line2Left)-right2Len)
	line2 := line2Left + strings.Repeat(" ", gap2) + line2Right

	return line1 + "\n" + line2
}

func fitTerminalText(value string, width int) string {
	if width <= 0 {
		return ""
	}
	return ansi.Truncate(value, width, "…")
}
