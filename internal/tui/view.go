package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// renderHeader renders the top left LuneAgent branding.
func renderHeader(workspace string) string {
	prompt := lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().AccentBlue)).Render(">_")
	brand := lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().TextPrimary)).Bold(true).Render(AppName)
	version := lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().TextMuted)).Render(fmt.Sprintf("(%s)", AppVersion))
	top := fmt.Sprintf("%s %s %s", prompt, brand, version)

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
		return lipgloss.NewStyle().
			Background(lipgloss.Color(activeTheme().CardBg)).
			Render(content + strings.Repeat(" ", pad))

	case KindAssistant:
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

	case KindTool:
		return renderToolEntry(entry, width)

	case KindQuestionAnswered:
		bullet := lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().TextMuted)).Render("• ")
		title := styleTextPrimary().Bold(true).Render("Question answered")
		qLine := fmt.Sprintf("  • %s", styleTextPrimary().Render(entry.QuestionText))
		ansLabel := styleTextMuted().Render("    answer: ")
		ansVal := lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().AccentBlue)).Render(entry.AnswerText)
		return bullet + title + "\n" + qLine + "\n" + ansLabel + ansVal

	case KindPlan:
		bullet := lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().TextMuted)).Render("• ")
		title := styleTextPrimary().Bold(true).Render("Proposed Plan")
		cardStyle := lipgloss.NewStyle().
			Background(lipgloss.Color(activeTheme().CardBg)).
			Foreground(lipgloss.Color(activeTheme().TextPrimary)).
			Padding(1, 2)
		body := cardStyle.Width(max(20, width-4)).Render(RenderMarkdown(entry.Text, width-8))
		return bullet + title + "\n\n" + body

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
		return lipgloss.NewStyle().
			Background(lipgloss.Color(activeTheme().CardBg)).
			Render(row + strings.Repeat(" ", pad))

	case KindDiff:
		return renderDiffEntry(entry, width)

	case KindReasoning:
		return renderReasoningEntry(entry, width)
	}

	return ""
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

	hint := "(ctrl+r to expand)"
	if entry.ReasoningExpanded {
		hint = "(ctrl+r to collapse)"
	}

	titleText := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Italic(true).Render(action)
	hintText := styleTextMuted().Render(" " + hint)
	header := bullet + titleText + hintText

	if !entry.ReasoningExpanded {
		return header
	}

	bodyText := strings.TrimSpace(entry.Text)
	if bodyText == "" {
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

// renderDiffEntry formats Git unified diff outputs with syntax highlighting.
func renderDiffEntry(entry TranscriptEntry, width int) string {
	bullet := lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().AccentBlue)).Render("• ")
	title := styleTextPrimary().Bold(true).Render("Workspace Diff")

	lines := strings.Split(entry.Text, "\n")
	var formatted []string

	styleAdd := lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().SuccessGreen))
	styleDel := lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().FailureRed))
	styleHunk := lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().AccentBlue)).Bold(true)
	styleFile := lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().TextPrimary)).Bold(true)
	styleNormal := styleTextMuted()

	for _, line := range lines {
		if strings.HasPrefix(line, "diff --git") {
			formatted = append(formatted, "\n  "+styleFile.Render(line))
		} else if strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---") {
			formatted = append(formatted, "  "+styleNormal.Render(line))
		} else if strings.HasPrefix(line, "@@") {
			formatted = append(formatted, "  "+styleHunk.Render(line))
		} else if strings.HasPrefix(line, "+") {
			formatted = append(formatted, "  "+styleAdd.Render(line))
		} else if strings.HasPrefix(line, "-") {
			formatted = append(formatted, "  "+styleDel.Render(line))
		} else {
			formatted = append(formatted, "  "+styleNormal.Render(line))
		}
	}

	return bullet + title + "\n" + strings.Join(formatted, "\n")
}

// renderToolEntry renders tool executions (Ran, Failed, Explored).
func renderToolEntry(entry TranscriptEntry, width int) string {
	isFail := entry.ToolExitCode != nil && *entry.ToolExitCode != 0

	var bullet, action string
	if isFail {
		bullet = lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().FailureRed)).Render("• ")
		action = lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().FailureRed)).Bold(true).
			Render(fmt.Sprintf("Failed (exit %d)", *entry.ToolExitCode))
	} else if strings.EqualFold(entry.ToolName, "read_file") ||
		strings.EqualFold(entry.ToolName, "list_dir") ||
		strings.EqualFold(entry.ToolName, "grep") ||
		strings.EqualFold(entry.ToolName, "glob") ||
		strings.EqualFold(entry.ToolName, "find_files") {
		bullet = lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().TextMuted)).Render("• ")
		action = lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().TextPrimary)).Bold(true).Render("Explored")
	} else {
		bullet = lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().SuccessGreen)).Render("• ")
		action = lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().TextPrimary)).Bold(true).Render("Ran")
	}

	commandStr := entry.ToolCommand
	if commandStr == "" {
		commandStr = entry.ToolName + " " + entry.ToolArgs
	}
	cmdStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().AccentBlue))
	header := bullet + action + " " + cmdStyle.Render(commandStr)

	// Branch indicator
	branch := lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().TextMuted)).Render("└ ")

	// Output preview
	output := strings.TrimSpace(entry.ToolOutput)
	if output == "" {
		output = "(no output)"
	}
	outLines := strings.Split(output, "\n")
	previewCount := 3
	if entry.ToolExpanded || len(outLines) <= previewCount {
		previewCount = len(outLines)
	}

	maxLineLen := max(20, width-8)
	var bodyLines []string
	for i := 0; i < previewCount; i++ {
		line := outLines[i]
		if len(line) > maxLineLen {
			line = line[:maxLineLen] + "…"
		}
		if i == 0 {
			bodyLines = append(bodyLines, "  "+branch+styleTextMuted().Render(line))
		} else {
			bodyLines = append(bodyLines, "    "+styleTextMuted().Render(line))
		}
	}

	if len(outLines) > previewCount && !entry.ToolExpanded {
		remaining := len(outLines) - previewCount
		expandHint := lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().TextMuted)).
			Render(fmt.Sprintf("  + %d lines (ctrl+t to expand)", remaining))
		bodyLines = append(bodyLines, expandHint)
	} else if entry.ToolExpanded && len(outLines) > 3 {
		collapseHint := lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().TextMuted)).
			Render(fmt.Sprintf("  - collapse %d lines (ctrl+t)", len(outLines)))
		bodyLines = append(bodyLines, collapseHint)
	}

	return header + "\n" + strings.Join(bodyLines, "\n")
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
func renderWorking(elapsed time.Duration, thinking bool) string {
	bullet := lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().TextMuted)).Render("• ")
	timeStr := formatDuration(elapsed)
	action := "Working"
	if thinking {
		action = "Thinking"
	}
	title := styleTextPrimary().Bold(true).Render(fmt.Sprintf("%s (%s • esc to interrupt)", action, timeStr))
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
	leftCol := 16
	for _, item := range items {
		leftCol = max(leftCol, lipgloss.Width(item.Command)+2)
	}
	maxLeft := max(24, width*3/5)
	leftCol = min(leftCol, maxLeft)

	for i := startIdx; i < endIdx; i++ {
		item := items[i]
		marker := "  "
		if i == selected {
			marker = "› "
		}
		cmdText := item.Command + strings.Repeat(" ", max(1, leftCol-lipgloss.Width(item.Command)))
		descText := item.Description
		line := marker + cmdText + descText

		if i == selected {
			pad := max(0, width-lipgloss.Width(line))
			rows = append(rows, styleSelectedRow().Render(line+strings.Repeat(" ", pad)))
		} else {
			cmdStyled := lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().TextPrimary)).Render(cmdText)
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
	noneRaw := fmt.Sprintf("%s%d. None of the above     Optionally, add details in notes (tab)", noneMarker, noneIdx+1)
	if q.Selected == noneIdx {
		pad := max(0, width-lipgloss.Width(noneRaw)-4)
		optionsRows = append(optionsRows, styleSelectedRow().Render(noneRaw+strings.Repeat(" ", pad)))
	} else {
		optionsRows = append(optionsRows, styleTextMuted().Render(noneRaw))
	}

	hint := styleTextMuted().Render("tab to add notes | enter to submit answer | esc to interrupt")

	panelContent := meta + "\n" + title + "\n\n" + strings.Join(optionsRows, "\n") + "\n\n" + hint
	cardStyle := lipgloss.NewStyle().
		Background(lipgloss.Color(activeTheme().CardBg)).
		Foreground(lipgloss.Color(activeTheme().TextPrimary)).
		Padding(1, 2)

	return cardStyle.Width(max(20, width)).Render(panelContent)
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

	footer := styleTextMuted().Render("enter select · esc back")
	content := title + "\n\n" + strings.Join(rows, "\n") + "\n\n" + footer

	cardStyle := lipgloss.NewStyle().
		Background(lipgloss.Color(activeTheme().CardBg)).
		Foreground(lipgloss.Color(activeTheme().TextPrimary)).
		Padding(1, 2)
	return cardStyle.Width(max(20, width)).Render(content)
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

	if !recovery.RetryNotBefore.IsZero() && time.Now().Before(recovery.RetryNotBefore) {
		remaining := time.Until(recovery.RetryNotBefore).Round(time.Second)
		lines = append(lines, styleTextMuted().Render(fmt.Sprintf("Retry cool-down: %s remaining", remaining)))
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
	lines = append(lines, styleTextMuted().Render("enter select · esc discard"))

	return cardStyle.Render(strings.Join(lines, "\n"))
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
		argsStr := recovery.Arguments
		if len(argsStr) > 100 {
			argsStr = argsStr[:97] + "..."
		}
		lines = append(lines, styleTextMuted().Render("Arguments: "+argsStr))
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
	lines = append(lines, styleTextMuted().Render("enter select · esc skip"))

	return cardStyle.Render(strings.Join(lines, "\n"))
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

	if len(approval.Request.Arguments) > 0 {
		argsStr := string(approval.Request.Arguments)
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

	hint := styleTextMuted().Render("\nenter confirm · esc deny")
	lines = append(lines, hint)

	return cardStyle.Render(strings.Join(lines, "\n"))
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
				currentBadge = " [active]"
			}
			title := s.Title
			if title == "" {
				title = "<untitled>"
			}
			timeStr := s.UpdatedAt.Format("Jan 02 15:04")

			availWidth := max(20, width-4)
			idColWidth := 14
			timeColWidth := 14
			badgeWidth := len(currentBadge)
			titleWidth := max(10, availWidth-idColWidth-timeColWidth-badgeWidth-len(marker)-2)

			truncTitle := truncateString(title, titleWidth)

			if i == m.sessionsIndex {
				raw := fmt.Sprintf("%s%-12s  %-*s  %s%s", marker, shortID, titleWidth, truncTitle, timeStr, currentBadge)
				pad := max(0, availWidth-lipgloss.Width(raw))
				lines = append(lines, styleSelectedRow().Render(raw+strings.Repeat(" ", pad)))
			} else {
				idStyled := lipgloss.NewStyle().Foreground(lipgloss.Color(th.AccentBlue)).Render(fmt.Sprintf("%-12s", shortID))
				titleStyled := styleTextPrimary().Render(fmt.Sprintf("%-*s", titleWidth, truncTitle))
				timeStyled := styleTextMuted().Render(timeStr)
				badgeStyled := ""
				if currentBadge != "" {
					badgeStyled = " " + lipgloss.NewStyle().Foreground(lipgloss.Color(th.SuccessGreen)).Bold(true).Render("[active]")
				}
				lines = append(lines, fmt.Sprintf("%s%s  %s  %s%s", marker, idStyled, titleStyled, timeStyled, badgeStyled))
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

	return cardStyle.Render(strings.Join(lines, "\n"))
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
		return cardStyle.Render(styleTextMuted().Render("No tool executions in current session."))
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
	if entry.ToolExitCode != nil && *entry.ToolExitCode != 0 {
		statusBadge = lipgloss.NewStyle().Foreground(lipgloss.Color(th.FailureRed)).Bold(true).
			Render(fmt.Sprintf("[Failed (exit %d)]", *entry.ToolExitCode))
	} else if strings.EqualFold(entry.ToolName, "read_file") ||
		strings.EqualFold(entry.ToolName, "list_dir") ||
		strings.EqualFold(entry.ToolName, "grep") ||
		strings.EqualFold(entry.ToolName, "glob") ||
		strings.EqualFold(entry.ToolName, "find_files") {
		statusBadge = lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Bold(true).Render("[Explored]")
	} else {
		statusBadge = lipgloss.NewStyle().Foreground(lipgloss.Color(th.SuccessGreen)).Bold(true).Render("[Ran]")
	}

	// Tool command text
	cmdStr := entry.ToolCommand
	if cmdStr == "" {
		cmdStr = entry.ToolName
		if entry.ToolArgs != "" {
			cmdStr += " " + entry.ToolArgs
		}
	}
	availWidth := max(20, width-4)
	truncCmd := truncateString(cmdStr, max(10, availWidth-lipgloss.Width(statusBadge)-3))
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
			lineContent := rawLines[i]
			maxContentWidth := max(10, availWidth-numWidth-4)
			if len(lineContent) > maxContentWidth {
				lineContent = lineContent[:maxContentWidth-1] + "…"
			}
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

	return cardStyle.Render(strings.Join(lines, "\n"))
}

// renderDiffModal renders the floating workspace diff viewer modal.
func renderDiffModal(m *tuiModel, width int) string {
	th := activeTheme()
	cardStyle := lipgloss.NewStyle().
		Background(lipgloss.Color(th.CardBg)).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(th.Border)).
		Padding(1, 2).
		Width(width)

	if len(m.diffFiles) == 0 {
		emptyLine := styleTextMuted().Italic(true).Render("No git changes detected in workspace.")
		hint := styleTextMuted().Render("esc/q/ctrl+d close")
		return cardStyle.Render(emptyLine + "\n\n" + hint)
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

	// Header line
	titleLeft := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(th.AccentBlue)).
		Render("Workspace Diff Pager") +
		styleTextMuted().Render(fmt.Sprintf(" (File %d of %d)", fileNum, totalFiles))

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
			lineContent := file.Lines[i]
			maxContentWidth := max(10, availWidth-numWidth-4)
			if len(lineContent) > maxContentWidth {
				lineContent = lineContent[:maxContentWidth-1] + "…"
			}

			var contentStyled string
			if strings.HasPrefix(lineContent, "diff --git") || strings.HasPrefix(lineContent, "index ") {
				contentStyled = lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextPrimary)).Bold(true).Render(lineContent)
			} else if strings.HasPrefix(lineContent, "---") {
				contentStyled = lipgloss.NewStyle().Foreground(lipgloss.Color(th.FailureRed)).Bold(true).Render(lineContent)
			} else if strings.HasPrefix(lineContent, "+++") {
				contentStyled = lipgloss.NewStyle().Foreground(lipgloss.Color(th.SuccessGreen)).Bold(true).Render(lineContent)
			} else if strings.HasPrefix(lineContent, "@@") {
				contentStyled = lipgloss.NewStyle().Foreground(lipgloss.Color(th.AccentBlue)).Bold(true).Render(lineContent)
			} else if strings.HasPrefix(lineContent, "+") {
				contentStyled = lipgloss.NewStyle().Foreground(lipgloss.Color(th.SuccessGreen)).Render(lineContent)
			} else if strings.HasPrefix(lineContent, "-") {
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
		hint := styleTextMuted().Render("↑/↓ scroll · pgup/pgdn · [/] or tab switch file · c copy · esc/q/ctrl+d close")
		lines = append(lines, hint)
	}

	return cardStyle.Render(strings.Join(lines, "\n"))
}

// renderShortcutsPanel renders the keyboard shortcut cheat sheet in a 3-column layout.
func renderShortcutsPanel(width int) string {
	title := styleTextPrimary().Bold(true).Render("Keyboard Shortcuts")

	col1Title := styleTextPrimary().Bold(true).Underline(true).Render("Compose")
	col1Items := []struct{ Key, Desc string }{
		{"/", "Commands"},
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
		Padding(1, 2)

	return cardStyle.Width(max(20, width)).Render(panelContent)
}

// renderScrollPill renders the floating hint pill when scrolled away from bottom.
func renderScrollPill(width int) string {
	text := "New activity · ↓ Back to bottom · esc"
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
	lines := strings.Split(inputView, "\n")
	var padded []string
	cardStyle := lipgloss.NewStyle().Background(lipgloss.Color(activeTheme().CardBg))

	// Top padding line
	emptyRow := cardStyle.Render(strings.Repeat(" ", width))
	padded = append(padded, emptyRow)

	// Middle input line(s)
	for _, l := range lines {
		content := " " + l
		pad := max(0, width-lipgloss.Width(content))
		padded = append(padded, cardStyle.Render(content+strings.Repeat(" ", pad)))
	}

	// Bottom padding line
	padded = append(padded, emptyRow)

	return strings.Join(padded, "\n")
}

// renderFooter renders the bottom two-line status bar.
func renderFooter(m *tuiModel, width int) string {
	// Left side: Model · Workspace · Title
	var leftItems []string
	modelName := m.currentModel
	if modelName == "" && m.client != nil {
		modelName = m.client.ModelForSession(context.Background(), m.sessionID)
	}
	if modelName != "" {
		leftItems = append(leftItems, modelName)
	}
	if m.workspace != "" {
		leftItems = append(leftItems, m.workspace)
	}
	if m.sessionTitle != "" {
		leftItems = append(leftItems, m.sessionTitle)
	}
	leftStr := styleTextMuted().Render(strings.Join(leftItems, " · "))

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

	leftLen := lipgloss.Width(leftStr)
	rightLen := lipgloss.Width(rightStr)
	gap := max(1, width-leftLen-rightLen)
	line1 := leftStr + strings.Repeat(" ", gap) + rightStr

	// Line 2: ? for shortcuts
	line2 := styleTextMuted().Render("? for shortcuts")

	return line1 + "\n" + line2
}

func fitTerminalText(value string, width int) string {
	if width <= 0 {
		return ""
	}
	return ansi.Truncate(value, width, "…")
}
