package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// renderHeader renders the top left Codex branding.
func renderHeader(workspace string) string {
	prompt := lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().AccentBlue)).Render(">_")
	brand := lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().TextPrimary)).Bold(true).Render("OpenAI Codex")
	version := lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().TextMuted)).Render("(v0.163.0)")
	top := fmt.Sprintf("%s %s %s", prompt, brand, version)

	cwd := lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().TextMuted)).Underline(true).Render(workspace)
	return top + "\n" + cwd + "\n"
}

// renderEntry renders a single transcript item according to Codex visual patterns.
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
		title := styleTextPrimary().Bold(true).Render("Questions 1/1 answered")
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
	}

	return ""
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
	tip := styleTextMuted().Render("Tip: When the composer is empty, press Esc to step back and edit your last message; Enter confirms.")
	return bullet + title + "\n  " + branch + tip
}

// renderAutocompletePopup renders the floating slash command suggestions above the composer.
func renderAutocompletePopup(items []AutocompleteItem, selected int, width int) string {
	if len(items) == 0 {
		return ""
	}
	var rows []string
	leftCol := 16
	for _, item := range items {
		leftCol = max(leftCol, lipgloss.Width(item.Command)+2)
	}
	leftCol = min(leftCol, 24)

	for i, item := range items {
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
	qItem := q.Request.Questions[0]
	meta := styleTextMuted().Render("Question 1/1 (1 unanswered)")
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

// renderApprovalPanel renders a permission approval modal matching Codex's aesthetic.
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
