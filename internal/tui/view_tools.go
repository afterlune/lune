package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/afterlune/lune/internal/terminaltext"
)

const toolPreviewLineLimit = 3

// toolOutputPreview returns the visible output window for a tool step.
// Successful finished tools show their first lines, while running or failed
// tools show their last lines so live progress and error summaries stay visible.
func toolOutputPreview(entry TranscriptEntry) (lines []string, hidden, total int) {
	output := strings.TrimSpace(entry.ToolOutput)
	if output == "" {
		return nil, 0, 0
	}
	outLines := strings.Split(output, "\n")
	total = len(outLines)
	if entry.ToolExpanded || total <= toolPreviewLineLimit {
		return outLines, 0, total
	}
	hasNonZeroExit := entry.ToolExitCode != nil && *entry.ToolExitCode != 0
	if entry.ToolRunning || entry.ToolFailed || hasNonZeroExit {
		return outLines[total-toolPreviewLineLimit:], total - toolPreviewLineLimit, total
	}
	return outLines[:toolPreviewLineLimit], total - toolPreviewLineLimit, total
}

// toolDurationBadge renders the muted duration suffix for one tool step.
// Running steps show live elapsed time so long commands are visibly moving;
// finished steps keep the final duration once it is meaningful.
func toolDurationBadge(entry TranscriptEntry) string {
	if entry.ToolRunning {
		if entry.ToolStartedAt.IsZero() {
			return ""
		}
		elapsed := time.Since(entry.ToolStartedAt)
		if elapsed < time.Second {
			return ""
		}
		return styleTextMuted().Render(" (" + formatDuration(elapsed) + ")")
	}
	if entry.ToolDuration >= 500*time.Millisecond {
		return styleTextMuted().Render(" (" + formatDuration(entry.ToolDuration) + ")")
	}
	return ""
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
	header := bullet + action + " " + cmdStyle.Render(commandStr) + toolDurationBadge(entry)

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

	outLines, hidden, total := toolOutputPreview(entry)
	for i, outLine := range outLines {
		line := sanitizeAndTruncateLine(outLine, maxLineLen)
		if i == 0 {
			bodyLines = append(bodyLines, "  "+branch+styleTextMuted().Render(line))
		} else {
			bodyLines = append(bodyLines, "    "+styleTextMuted().Render(line))
		}
	}

	if hidden > 0 {
		expandHint := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).
			Render(fmt.Sprintf("  + %d lines (click to expand · Ctrl+T for full output)", hidden))
		bodyLines = append(bodyLines, expandHint)
	} else if entry.ToolExpanded && total > toolPreviewLineLimit {
		collapseHint := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).
			Render(fmt.Sprintf("  - collapse %d lines (click)", total))
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
			case "read_file", "read_image":
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
		childBlock := renderRoundChildEntry(entry, childWidth)
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
// Tool steps show a compact three-line output preview by default; clicking expands the
// full output through ToolExpanded, and reasoning/subagent steps use their own toggles.
func renderRoundChildEntry(entry TranscriptEntry, width int) string {
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
		wrapped := renderMarkdown(bodyText, max(20, width-4))
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
			rows = append(rows, "  • "+styleTextPrimary().Render(terminaltext.Sanitize(qa.Question)))
			rows = append(rows, "    "+styleTextMuted().Render("answer: ")+lipgloss.NewStyle().Foreground(lipgloss.Color(th.AccentBlue)).Render(terminaltext.Sanitize(qa.Answer)))
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
		nameStyled := lipgloss.NewStyle().Foreground(lipgloss.Color(th.PlanPurple)).Bold(true).Render(terminaltext.Sanitize(name))
		roleStyled := ""
		if entry.AgentRole != "" {
			roleStyled = styleTextMuted().Render(" (" + terminaltext.Sanitize(entry.AgentRole) + ")")
		}
		durStyled := ""
		if !entry.SubagentRunning && entry.SubagentDuration > 0 {
			durStyled = styleTextMuted().Render(" · " + formatDuration(entry.SubagentDuration))
		}
		header := prefix + action + nameStyled + roleStyled + durStyled
		if entry.SubagentExpanded && strings.TrimSpace(entry.Text) != "" {
			rendered := renderMarkdown(strings.TrimSpace(entry.Text), max(20, width-4))
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
			adds, dels := fileChangeCounts(ch)
			actionWord := fileChangeActionWord(entry, ch, adds, dels)
			actionStyled := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Render(actionWord + " ")
			pathStyled := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextPrimary)).Render(terminaltext.Sanitize(ch.Path))
			addStyled := lipgloss.NewStyle().Foreground(lipgloss.Color(th.SuccessGreen)).Render(fmt.Sprintf("+%d", adds))
			delStyled := lipgloss.NewStyle().Foreground(lipgloss.Color(th.FailureRed)).Render(fmt.Sprintf("-%d", dels))
			header := fmt.Sprintf("%s%s%s (%s %s)", prefix, actionStyled, pathStyled, addStyled, delStyled)

			if !entry.ToolExpanded {
				out := []string{header}
				for _, row := range renderFileChangeRows(ch, max(20, width-6), toolPreviewLineLimit) {
					out = append(out, "  "+row)
				}
				if len(ch.Lines) > toolPreviewLineLimit {
					out = append(out, styleTextMuted().Render(
						fmt.Sprintf("  + %d lines (click to expand)", len(ch.Lines)-toolPreviewLineLimit)))
				}
				return strings.Join(out, "\n")
			}

			card := renderFileChangesCard(entry, max(20, width-4))
			cardLines := strings.Split(card, "\n")
			out := []string{header}
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
		case "read_image":
			actionWord = "Viewed"
			targetText = strings.TrimPrefix(targetText, "Viewed image ")
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
		header := prefix + actionStyled + targetStyled + toolDurationBadge(entry)

		output := strings.TrimSpace(entry.ToolOutput)
		branch := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Render("└ ")
		if output == "" {
			if entry.ToolRunning {
				return header + "\n  " + branch + styleTextMuted().Render("Running...")
			}
			if entry.ToolExpanded {
				return header + "\n  " + branch + styleTextMuted().Render("(no output)")
			}
			return header
		}

		outLines, hidden, total := toolOutputPreview(entry)
		maxLineLen := max(20, width-8)
		detailLines := []string{header}
		for i, outLine := range outLines {
			line := sanitizeAndTruncateLine(outLine, maxLineLen)
			if i == 0 {
				detailLines = append(detailLines, "  "+branch+styleTextMuted().Render(line))
			} else {
				detailLines = append(detailLines, "    "+styleTextMuted().Render(line))
			}
		}
		if hidden > 0 {
			detailLines = append(detailLines, styleTextMuted().Render(
				fmt.Sprintf("  + %d lines (click to expand · Ctrl+T for full output)", hidden)))
		} else if entry.ToolExpanded && total > toolPreviewLineLimit {
			detailLines = append(detailLines, styleTextMuted().Render(
				fmt.Sprintf("  - collapse %d lines (click)", total)))
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
