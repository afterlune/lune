package tui

import (
	"fmt"
	"math"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/afterlune/lune/internal/terminaltext"
	core "github.com/afterlune/luneCore"
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
		descText := terminaltext.Sanitize(item.Description)
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
	draft := q.currentDraft()
	currIdx := q.CurrentIndex
	total := len(q.Request.Questions)
	confirmed := len(q.confirmedAnswers())
	unanswered := total - confirmed
	qItem := q.Request.Questions[currIdx]
	meta := styleTextMuted().Render(fmt.Sprintf("Question %d/%d (%d unanswered)", currIdx+1, total, unanswered))
	if q.Submitting {
		meta = styleTextMuted().Render(fmt.Sprintf("Submitting answers (%d)…", confirmed))
	}
	title := lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().AccentSoft)).Bold(true).Render(terminaltext.Sanitize(qItem.Question))

	optionsRows := renderQuestionOptionRows(qItem.Options, draft.Selected, max(1, max(20, width)-4))

	noneIdx := len(qItem.Options)
	noneMarker := "  "
	if draft.Selected == noneIdx {
		noneMarker = "› "
	}
	noneRaw := fmt.Sprintf("%s%d. None of the above     Type custom answer or press tab for notes", noneMarker, noneIdx+1)
	if draft.Selected == noneIdx {
		pad := max(0, width-lipgloss.Width(noneRaw)-4)
		optionsRows = append(optionsRows, styleSelectedRow().Render(noneRaw+strings.Repeat(" ", pad)))
		customPrompt := "    Custom answer: " + terminaltext.Sanitize(draft.Custom) + "█"
		optionsRows = append(optionsRows, styleTextPrimary().Render(customPrompt))
	} else {
		optionsRows = append(optionsRows, styleTextMuted().Render(noneRaw))
	}

	if draft.NotesOpen || draft.NoteText != "" {
		cursor := ""
		if draft.NotesOpen {
			cursor = "█"
		}
		notesLine := "  Notes: " + terminaltext.Sanitize(draft.NoteText) + cursor
		optionsRows = append(optionsRows, "")
		optionsRows = append(optionsRows, styleTextPrimary().Render(notesLine))
	}

	hintText := "↑/↓ select | tab to add notes | enter to confirm answer | /discard to drop"
	if draft.NotesOpen {
		hintText = "tab to finish notes | enter to confirm answer | esc to close notes"
	} else if len(qItem.Options) > 0 && draft.Selected == len(qItem.Options) {
		hintText = "enter to confirm custom answer | esc to clear | /discard to drop"
	}
	if total > 1 {
		hintText = "←/→ switch question | " + hintText
	}
	hint := styleTextMuted().Render(ansi.Wrap(hintText, max(1, width-4), " "))
	if q.Submitting {
		hint = styleTextMuted().Render("Waiting for Core to accept answers…")
	}
	if q.Error != "" {
		hint = lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().WarningAmber)).Render(terminaltext.Sanitize(q.Error)) + "\n" + hint
	}

	panelContent := meta + "\n" + title + "\n\n" + strings.Join(optionsRows, "\n") + "\n\n" + hint
	cardStyle := panelCardStyle(width)

	return renderCardSurface(cardStyle, panelContent)
}

const minInlineQuestionDescriptionWidth = 24

func renderQuestionOptionRows(options []core.UserQuestionOption, selected, width int) []string {
	if len(options) == 0 {
		return nil
	}
	width = max(1, width)

	labels := make([]string, len(options))
	descriptions := make([]string, len(options))
	prefixWidth := 0
	labelWidth := 0
	hasDescription := false
	for i, option := range options {
		labels[i] = singleLineQuestionText(option.Label)
		if option.Recommended {
			labels[i] += "  [Recommended]"
		}
		descriptions[i] = singleLineQuestionText(option.Description)
		if descriptions[i] != "" {
			hasDescription = true
		}
		labelWidth = max(labelWidth, lipgloss.Width(labels[i]))
		prefixWidth = max(prefixWidth, lipgloss.Width("› "+fmt.Sprintf("%d. ", i+1)))
	}

	descriptionColumn := prefixWidth + labelWidth + 1
	inlineDescriptionWidth := width - descriptionColumn
	inline := hasDescription && inlineDescriptionWidth >= minInlineQuestionDescriptionWidth
	if !inline {
		labelWidth = max(0, width-prefixWidth)
	}

	var rows []string
	for i := range options {
		marker := "  "
		if i == selected {
			marker = "› "
		}
		prefix := marker + fmt.Sprintf("%d. ", i+1)
		prefix += strings.Repeat(" ", max(0, prefixWidth-lipgloss.Width(prefix)))
		label := labels[i]
		description := descriptions[i]

		if inline {
			if description == "" {
				rows = append(rows, renderQuestionOptionLine(prefix, label, "", "", i == selected, width))
				continue
			}
			gap := strings.Repeat(" ", max(1, labelWidth-lipgloss.Width(label)+1))
			descriptionLines := wrapQuestionText(description, inlineDescriptionWidth)
			rows = append(rows, renderQuestionOptionLine(prefix, label, gap, descriptionLines[0], i == selected, width))
			for _, line := range descriptionLines[1:] {
				rows = append(rows, renderQuestionOptionLine("", "", strings.Repeat(" ", descriptionColumn), line, i == selected, width))
			}
			continue
		}

		labelLines := wrapQuestionText(label, labelWidth)
		rows = append(rows, renderQuestionOptionLine(prefix, labelLines[0], "", "", i == selected, width))
		for _, line := range labelLines[1:] {
			rows = append(rows, renderQuestionOptionLine("", line, strings.Repeat(" ", prefixWidth), "", i == selected, width))
		}
		if description == "" {
			continue
		}
		descriptionIndent := strings.Repeat(" ", prefixWidth)
		descriptionLines := wrapQuestionText(description, max(1, width-prefixWidth))
		for _, line := range descriptionLines {
			rows = append(rows, renderQuestionOptionLine("", "", descriptionIndent, line, i == selected, width))
		}
	}
	return rows
}

func singleLineQuestionText(value string) string {
	return strings.Join(strings.Fields(terminaltext.Sanitize(value)), " ")
}

func wrapQuestionText(value string, width int) []string {
	value = terminaltext.Sanitize(value)
	if value == "" {
		return []string{""}
	}
	wrapped := ansi.Wrap(value, max(1, width), " ")
	return strings.Split(wrapped, "\n")
}

func renderQuestionOptionLine(prefix, label, gap, description string, selected bool, width int) string {
	labelText := prefix + label + gap
	lineWidth := lipgloss.Width(labelText + description)
	if selected {
		style := styleSelectedRow()
		var line strings.Builder
		if labelText != "" {
			line.WriteString(style.Render(labelText))
		}
		if description != "" {
			line.WriteString(questionDescriptionStyle(true).Render(description))
		}
		if pad := max(0, width-lineWidth); pad > 0 {
			line.WriteString(style.Render(strings.Repeat(" ", pad)))
		}
		return line.String()
	}

	var line strings.Builder
	if prefix != "" {
		line.WriteString(styleTextPrimary().Render(prefix))
	}
	if label != "" {
		line.WriteString(styleTextPrimary().Bold(true).Render(label))
	}
	if gap != "" {
		line.WriteString(styleTextPrimary().Render(gap))
	}
	if description != "" {
		line.WriteString(questionDescriptionStyle(false).Render(description))
	}
	return line.String()
}

func questionDescriptionStyle(selected bool) lipgloss.Style {
	if selected {
		return styleSelectedRow().Faint(true)
	}
	return styleTextMuted().Faint(true)
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

	footer := styleTextMuted().Render("↑/↓ or 1-7 select · enter apply · esc cancel")
	content := title + "\n\n" + strings.Join(rows, "\n") + "\n\n" + footer

	cardStyle := panelCardStyle(width)
	return renderCardSurface(cardStyle, content)
}

// renderPlanReviewPanel renders the plan implementation options.
func renderPlanReviewPanel(review *ActivePlanReview, width int) string {
	title := styleTextPrimary().Bold(true).Render("Implement this plan?")

	choices := []core.UserQuestionOption{
		{Label: "Yes, implement this plan", Description: "Continue in this conversation"},
		{Label: "Implement in a fresh thread", Description: "Start with only the plan"},
		{Label: "Keep discussing", Description: "Close review without implementing"},
	}
	rows := renderQuestionOptionRows(choices, review.Selected, max(1, width-4))
	footer := styleTextMuted().Render(ansi.Wrap("1-3 / enter select · esc close review", max(1, width-4), " "))
	content := title + "\n\n" + strings.Join(rows, "\n") + "\n\n" + footer

	cardStyle := panelCardStyle(width)
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
	label := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextPrimary)).Bold(true).Render("Task Paused (Checkpoint saved)")
	actions := lipgloss.NewStyle().Foreground(lipgloss.Color(th.AccentBlue)).Render(" · /continue to continue · /discard to drop")
	banner := label + actions
	pad := max(0, (width-lipgloss.Width(banner))/2)
	return strings.Repeat(" ", pad) + banner
}

// renderRecoveryPanel renders the model recovery prompt when a provider times out or rate limits.
func renderRecoveryPanel(recovery *ActiveRecovery, width int) string {
	if recovery == nil {
		return ""
	}
	th := activeTheme()
	cardStyle := panelCardStyle(width)

	var lines []string
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(th.WarningAmber)).
		Render("Model Recovery Required")
	lines = append(lines, title)

	reason := recovery.Reason
	if reason == "" {
		reason = "Model request was interrupted or failed retry deadline."
	}
	lines = append(lines, styleTextPrimary().Render("Reason: "+terminaltext.Sanitize(reason)))

	if !recovery.RetryNotBefore.IsZero() {
		lines = append(lines, "")
		if time.Now().Before(recovery.RetryNotBefore) {
			remaining := time.Until(recovery.RetryNotBefore).Round(time.Second)
			secs := int(remaining.Seconds())
			if secs < 0 {
				secs = 0
			}
			bar := renderCountdownProgressBar(secs, 60, 20)
			countdownLine := fmt.Sprintf("Rate limit cool-down: %s remaining  %s", remaining, bar)
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
	cardStyle := panelCardStyle(width)

	var lines []string
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(th.WarningAmber)).
		Render("Tool Failure Recovery Required")
	lines = append(lines, title)

	toolLine := fmt.Sprintf("Tool: %s", styleTextPrimary().Bold(true).Render(terminaltext.Sanitize(recovery.ToolName)))
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
	cardStyle := panelCardStyle(width)

	var lines []string
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(th.WarningAmber)).
		Render("Tool Approval Required")
	lines = append(lines, title)

	if approval.Request.AutoReviewReason != "" {
		reviewLine := lipgloss.NewStyle().Foreground(lipgloss.Color(th.WarningAmber)).
			Render(fmt.Sprintf("Model Review: %s", terminaltext.Sanitize(approval.Request.AutoReviewReason)))
		lines = append(lines, reviewLine)
	}

	toolLine := fmt.Sprintf("Tool: %s", styleTextPrimary().Bold(true).Render(terminaltext.Sanitize(approval.Request.Tool)))
	lines = append(lines, toolLine)

	summary := formatToolSummary(approval.Request.Tool, string(approval.Request.Arguments), "")
	if summary != "" && summary != approval.Request.Tool {
		lines = append(lines, fmt.Sprintf("Action: %s", styleTextPrimary().Render(terminaltext.Sanitize(summary))))
	}

	if len(approval.Request.Arguments) > 0 && string(approval.Request.Arguments) != "{}" {
		argsStr := sanitizeAndTruncateLine(string(approval.Request.Arguments), max(20, width-16))
		lines = append(lines, styleTextMuted().Render("Arguments: "+argsStr))
	}
	pathGrant := approval.Request.PathGrant
	if pathGrant != nil {
		access := "read-only"
		if pathGrant.Access == "read_write" {
			access = "read and write"
		}
		lines = append(lines, styleTextMuted().Render(fmt.Sprintf("Session grant: %s access", access)))
		safePath := terminaltext.Sanitize(pathGrant.Path)
		wrappedPath := ansi.Wrap("Directory: "+safePath, max(20, width-16), " /\\")
		for _, line := range strings.Split(wrappedPath, "\n") {
			lines = append(lines, styleTextMuted().Render(line))
		}
		lines = append(lines, styleTextMuted().Render("Includes subdirectories for this session"))
	}

	lines = append(lines, "")

	sessionLabel := "2. Allow for this session"
	if pathGrant != nil {
		sessionLabel = "2. Grant for this session"
	}
	options := []string{"1. Allow execution", sessionLabel, "3. Deny"}
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

	hintText := "\n1/y allow once · 2/s allow for session · 3/n/esc deny"
	hint := styleTextMuted().Render(hintText)
	lines = append(lines, hint)

	return renderCardSurface(cardStyle, strings.Join(lines, "\n"))
}
