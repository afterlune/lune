package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/afterlune/lune/internal/terminaltext"
)

// renderSessionsModal renders the interactive floating session switcher modal.
func renderSessionsModal(m *tuiModel, width int) string {
	th := activeTheme()
	cardStyle := panelCardStyle(width)

	var lines []string

	total := len(m.modals.sessions.items)
	filteredCount := len(m.modals.sessions.filtered)
	countInfo := ""
	if m.modals.sessions.filter != "" {
		countInfo = fmt.Sprintf(" (%d/%d)", filteredCount, total)
	} else if total > 0 {
		countInfo = fmt.Sprintf(" (%d)", total)
	}

	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(th.AccentBlue)).
		Render("Switch Session") + styleTextMuted().Render(countInfo)
	lines = append(lines, title)

	filterLabel := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Render("Filter: ")
	var filterVal string
	if m.modals.sessions.filter == "" {
		filterVal = lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Italic(true).Render("type to filter...")
	} else {
		filterVal = lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextPrimary)).Bold(true).Render(m.modals.sessions.filter)
	}
	lines = append(lines, filterLabel+filterVal)
	lines = append(lines, "")

	if filteredCount == 0 {
		lines = append(lines, "  "+styleTextMuted().Render("No matching sessions found."))
	} else {
		maxVisible := 8
		startIdx := 0
		if m.modals.sessions.index >= maxVisible {
			startIdx = m.modals.sessions.index - maxVisible + 1
		}
		endIdx := min(filteredCount, startIdx+maxVisible)

		availWidth := max(20, width-4)
		// marker (2) + shortID (12) + 2 spaces + titleWidth + 2 spaces + time (10) = 28 + titleWidth
		baseTitleWidth := max(10, availWidth-28)

		for i := startIdx; i < endIdx; i++ {
			s := m.modals.sessions.filtered[i]
			marker := "  "
			if i == m.modals.sessions.index {
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

			if i == m.modals.sessions.index {
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

	if m.modals.sessions.confirmDelete {
		lines = append(lines, "")
		warn := lipgloss.NewStyle().Foreground(lipgloss.Color(th.WarningAmber)).Bold(true).
			Render(fmt.Sprintf("Delete session %s? (y to confirm · n/esc to cancel)", truncateString(m.modals.sessions.deletingID, 16)))
		lines = append(lines, "  "+warn)
	}

	lines = append(lines, "")
	hint := "↑/↓ navigate · enter select · ctrl+d/del delete · esc close"
	if m.modals.sessions.deleteMode {
		hint = "↑/↓ navigate · enter choose session to delete · y confirm · n/esc cancel · esc close"
	}
	lines = append(lines, styleTextMuted().Render(hint))

	return renderCardSurface(cardStyle, strings.Join(lines, "\n"))
}

func renderThemePicker(m *tuiModel, width int) string {
	th := activeTheme()
	cardStyle := panelCardStyle(width)
	lines := []string{lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(th.AccentBlue)).Render("Theme")}
	for i, mode := range themeModes {
		label := string(mode)
		if mode == CurrentThemeMode() {
			label += " · current"
		}
		if i == m.modals.theme.Selected {
			lines = append(lines, styleSelectedRow().Render("› "+label))
		} else {
			lines = append(lines, "  "+label)
		}
	}
	lines = append(lines, "", styleTextMuted().Render("↑/↓ navigate · enter apply · esc cancel"))
	return renderCardSurface(cardStyle, strings.Join(lines, "\n"))
}

func renderModelPicker(m *tuiModel, width int) string {
	th := activeTheme()
	cardStyle := panelCardStyle(width)
	lines := []string{lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(th.AccentBlue)).Render(fmt.Sprintf("Model profile (%d)", len(m.modals.model.Items)))}
	visible := 8
	start := 0
	if m.modals.model.Selected >= visible {
		start = m.modals.model.Selected - visible + 1
	}
	end := min(len(m.modals.model.Items), start+visible)
	for i := start; i < end; i++ {
		item := m.modals.model.Items[i]
		modelName := item.Model
		if item.DisplayName != "" && item.DisplayName != item.Model {
			modelName = item.DisplayName + " · " + item.Model
		}
		label := fmt.Sprintf("%s · %s · %s", item.Profile, item.Provider, modelName)
		if strings.EqualFold(item.Profile, m.currentModelProfile) {
			label += " · current"
		}
		label = sanitizeAndTruncateLine(label, max(12, width-8))
		if i == m.modals.model.Selected {
			lines = append(lines, styleSelectedRow().Render("› "+label))
		} else {
			lines = append(lines, "  "+label)
		}
	}
	lines = append(lines, "", styleTextMuted().Render("↑/↓ navigate · enter apply · esc cancel"))
	return renderCardSurface(cardStyle, strings.Join(lines, "\n"))
}

func renderPermissionPicker(m *tuiModel, width int) string {
	th := activeTheme()
	cardStyle := panelCardStyle(width)
	lines := []string{lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(th.AccentBlue)).Render("Permission mode")}
	for i, mode := range permissionModes {
		label := string(mode)
		if mode == m.permissionMode {
			label += " · current"
		}
		if i == m.modals.permission.Selected {
			lines = append(lines, styleSelectedRow().Render("› "+label))
		} else {
			lines = append(lines, "  "+label)
		}
	}
	lines = append(lines, "", styleTextMuted().Render("↑/↓ navigate · enter apply · esc cancel"))
	return renderCardSurface(cardStyle, strings.Join(lines, "\n"))
}

func renderMemoryInputPrompt(m *tuiModel, width int) string {
	cardStyle := panelCardStyle(width)
	category := "preference"
	if m.modals.memory.pendingCategory != "" {
		category = m.modals.memory.pendingCategory
	}
	content := fmt.Sprintf("Add workspace memory (%s)\nType the memory in the composer. Enter saves · Esc cancels.", category)
	return renderCardSurface(cardStyle, content)
}

func renderMemoryPicker(m *tuiModel, width int) string {
	th := activeTheme()
	cardStyle := panelCardStyle(width)
	lines := []string{lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(th.AccentBlue)).Render("Remove workspace memory")}
	visible := 8
	start := 0
	if m.modals.memory.picker.Selected >= visible {
		start = m.modals.memory.picker.Selected - visible + 1
	}
	end := min(len(m.modals.memory.picker.Items), start+visible)
	for i := start; i < end; i++ {
		item := m.modals.memory.picker.Items[i]
		id := item.ID
		if len(id) > 8 {
			id = id[:8]
		}
		label := fmt.Sprintf("[%s] %s · %s", id, item.Category, sanitizeAndTruncateLine(item.Content, max(10, width-36)))
		if i == m.modals.memory.picker.Selected {
			lines = append(lines, styleSelectedRow().Render("› "+label))
		} else {
			lines = append(lines, "  "+label)
		}
	}
	lines = append(lines, "", styleTextMuted().Render("↑/↓ navigate · enter remove · esc cancel"))
	return renderCardSurface(cardStyle, strings.Join(lines, "\n"))
}

func renderMemoryClearConfirm(m *tuiModel, width int) string {
	th := activeTheme()
	cardStyle := panelCardStyle(width)
	content := lipgloss.NewStyle().Foreground(lipgloss.Color(th.WarningAmber)).Bold(true).
		Render("Clear all workspace memories?") + "\n" + styleTextMuted().Render("y confirm · n/Esc cancel")
	return renderCardSurface(cardStyle, content)
}

func renderMCPServerPicker(m *tuiModel, width int) string {
	th := activeTheme()
	cardStyle := panelCardStyle(width)
	title := map[string]string{"enable": "Enable MCP server", "disable": "Disable MCP server", "auth": "Authorize MCP server"}[m.modals.mcp.picker.Action]
	lines := []string{lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(th.AccentBlue)).Render(title)}
	visible := 8
	start := 0
	if m.modals.mcp.picker.Selected >= visible {
		start = m.modals.mcp.picker.Selected - visible + 1
	}
	end := min(len(m.modals.mcp.picker.Names), start+visible)
	for i := start; i < end; i++ {
		name := sanitizeAndTruncateLine(m.modals.mcp.picker.Names[i], max(10, width-8))
		if i == m.modals.mcp.picker.Selected {
			lines = append(lines, styleSelectedRow().Render("› "+name))
		} else {
			lines = append(lines, "  "+name)
		}
	}
	lines = append(lines, "", styleTextMuted().Render("↑/↓ navigate · enter select · esc cancel"))
	return renderCardSurface(cardStyle, strings.Join(lines, "\n"))
}

func renderProcessPicker(m *tuiModel, width int) string {
	th := activeTheme()
	cardStyle := panelCardStyle(width)
	title := "Select process logs"
	if m.modals.process.mode == processPickerStop {
		title = "Select process to stop"
	}
	lines := []string{lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(th.AccentBlue)).Render(fmt.Sprintf("%s (%d)", title, len(m.modals.process.items)))}
	visible := 8
	start := 0
	if m.modals.process.selected >= visible {
		start = m.modals.process.selected - visible + 1
	}
	end := min(len(m.modals.process.items), start+visible)
	for i := start; i < end; i++ {
		command := m.modals.process.items[i]
		id := command.ID
		if len(id) > 8 {
			id = id[:8]
		}
		row := fmt.Sprintf("[%s] %s · %s", id, command.Status, sanitizeAndTruncateLine(command.Command, max(10, width-30)))
		if i == m.modals.process.selected {
			lines = append(lines, styleSelectedRow().Render("› "+row))
		} else {
			lines = append(lines, "  "+row)
		}
	}
	lines = append(lines, "", styleTextMuted().Render("↑/↓ navigate · enter select · esc cancel"))
	return renderCardSurface(cardStyle, strings.Join(lines, "\n"))
}

func renderMCPAuthPanel(m *tuiModel, width int) string {
	cardStyle := panelCardStyle(width)
	content := fmt.Sprintf("Authorizing MCP server `%s`\nComplete the browser flow. Press Esc to cancel.", terminaltext.Sanitize(m.modals.mcp.authName))
	return renderCardSurface(cardStyle, content)
}

// renderToolPagerModal renders the dedicated floating pager modal for inspecting full tool output.
func renderToolPagerModal(m *tuiModel, width int) string {
	th := activeTheme()
	cardStyle := panelCardStyle(width)

	indices := m.getToolEntryIndices()
	if len(indices) == 0 {
		return renderCardSurface(cardStyle, styleTextMuted().Render("No tool executions in current session."))
	}

	idx := m.modals.toolPager.index
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
	scrollY := m.modals.toolPager.scrollY
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
	if m.input.copyFeedback != "" && time.Now().Before(m.input.copyFeedbackExpiresAt) {
		mark, color := "✓", th.SuccessGreen
		if m.input.copyFeedbackError {
			mark, color = "!", th.WarningAmber
		}
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Bold(true).Render(mark+" "+m.input.copyFeedback))
	} else {
		hint := styleTextMuted().Render("↑/↓ scroll · pgup/pgdn · [/] switch tool · c copy · esc/q close")
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

	if len(m.modals.diff.files) == 0 {
		emptyLine := styleTextMuted().Italic(true).Render("No git changes detected in workspace.")
		hint := styleTextMuted().Render("esc/q/ctrl+d close")
		return renderCardSurface(cardStyle, emptyLine+"\n\n"+hint)
	}

	idx := m.modals.diff.fileIndex
	if idx < 0 {
		idx = 0
	} else if idx >= len(m.modals.diff.files) {
		idx = len(m.modals.diff.files) - 1
	}

	file := m.modals.diff.files[idx]
	fileNum := idx + 1
	totalFiles := len(m.modals.diff.files)

	availWidth := max(20, width-4)

	// Header line with total workspace stats
	totAdds, totDels := computeDiffStats(m.modals.diff.files)
	titleLeft := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(th.AccentBlue)).
		Render("Workspace Diff Pager") +
		styleTextMuted().Render(fmt.Sprintf(" (File %d of %d · %d files +%d/-%d)", fileNum, totalFiles, totalFiles, totAdds, totDels))

	totalLines := len(file.Lines)
	visibleLines := m.diffModalVisibleLines()
	maxScroll := max(0, totalLines-visibleLines)
	scrollY := m.modals.diff.scrollY
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
	tabBar := renderDiffTabBar(m.modals.diff.files, idx, availWidth)

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
	if m.input.copyFeedback != "" && time.Now().Before(m.input.copyFeedbackExpiresAt) {
		mark, color := "✓", th.SuccessGreen
		if m.input.copyFeedbackError {
			mark, color = "!", th.WarningAmber
		}
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Bold(true).Render(mark+" "+m.input.copyFeedback))
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
		{"Mouse Drag", "Select input text"},
		{"Shift+←/→", "Select input text"},
		{"Ctrl+G", "Select all input"},
		{"Ctrl+C", "Copy / stop / quit"},
	}

	col2Title := styleTextPrimary().Bold(true).Underline(true).Render("Session")
	col2Items := []struct{ Key, Desc string }{
		{"Enter", "Send message"},
		{"/resume", "Switch session"},
		{"Shift+Tab", "Toggle Plan mode"},
		{"/permissions", "Switch permission mode"},
		{"/model", "Switch model"},
		{"/status", "Show status"},
		{"/sessions", "List sessions"},
	}

	col3Title := styleTextPrimary().Bold(true).Underline(true).Render("Transcript")
	col3Items := []struct{ Key, Desc string }{
		{"Mouse Drag", "Select & auto-copy"},
		{"Scroll", "History scroll"},
		{"Ctrl+O", "Expand/collapse all rounds"},
		{"Ctrl+D", "Workspace diff viewer"},
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

	cardStyle := panelCardStyle(width)

	return renderCardSurface(cardStyle, panelContent)
}
