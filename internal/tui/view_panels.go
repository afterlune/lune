package tui

import (
	"fmt"
	"math"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

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
	if q.Submitting {
		meta = styleTextMuted().Render(fmt.Sprintf("Submitting answers (%d)…", len(q.Answers)))
	}
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
	if q.Submitting {
		hint = styleTextMuted().Render("Waiting for Core to accept answers…")
	}
	if q.Error != "" {
		hint = lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().WarningAmber)).Render(q.Error) + "\n" + hint
	}

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
