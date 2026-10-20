package tui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// updateLayout recalculates viewport and composer dimensions based on terminal window size.
func (m *tuiModel) updateLayout() {
	if m.width <= 0 || m.height <= 0 {
		return
	}

	contentWidth := max(20, m.width-4)
	m.composer.SetWidth(contentWidth)

	// Measure bottom components height:
	// - Header: ~2-3 lines
	// - Blank line gap before composer: 1 line
	// - Composer band: composer height (1 to 5) + 2 padding rows
	// - Footer: 2 lines
	// - Popup / Question / Approval / Plan review / Recovery if open
	headerHeight := 3
	gapHeight := 1
	composerBandHeight := m.composer.Height() + 2
	footerHeight := 2

	modalHeight := m.modalHeight(contentWidth)

	occupied := headerHeight + gapHeight + composerBandHeight + footerHeight + modalHeight
	vpHeight := max(3, m.height-occupied)

	widthChanged := (contentWidth != m.viewport.Width())
	heightChanged := (vpHeight != m.viewport.Height())

	if widthChanged {
		m.viewport.SetWidth(contentWidth)
	}
	if heightChanged {
		m.viewport.SetHeight(vpHeight)
		if !m.scrolledUp {
			m.viewport.GotoBottom()
		}
	}

	entriesChanged := (len(m.entries) != m.cachedEntryCount)
	needRefresh := widthChanged || entriesChanged || m.cachedDirty || m.transcriptDirty
	if needRefresh {
		m.refreshViewportContent()
	}
}

func (m *tuiModel) modalHeight(contentWidth int) int {
	var modalHeight int
	if m.shortcutsOpen {
		modalHeight = lipgloss.Height(renderShortcutsPanel(contentWidth)) + 1
	} else if m.agentsPanelOpen {
		modalHeight = lipgloss.Height(m.renderAgentsPanel(contentWidth)) + 1
	} else if m.sessionsModalOpen {
		modalHeight = lipgloss.Height(renderSessionsModal(m, contentWidth)) + 1
	} else if m.memoryClearConfirm {
		modalHeight = lipgloss.Height(renderMemoryClearConfirm(m, contentWidth)) + 1
	} else if m.memoryPicker != nil {
		modalHeight = lipgloss.Height(renderMemoryPicker(m, contentWidth)) + 1
	} else if m.mcpServerPicker != nil {
		modalHeight = lipgloss.Height(renderMCPServerPicker(m, contentWidth)) + 1
	} else if m.pendingMemoryAdd {
		modalHeight = lipgloss.Height(renderMemoryInputPrompt(m, contentWidth)) + 1
	} else if m.themePicker != nil {
		modalHeight = lipgloss.Height(renderThemePicker(m, contentWidth)) + 1
	} else if m.modelPicker != nil {
		modalHeight = lipgloss.Height(renderModelPicker(m, contentWidth)) + 1
	} else if m.permissionPicker != nil {
		modalHeight = lipgloss.Height(renderPermissionPicker(m, contentWidth)) + 1
	} else if m.processPickerOpen {
		modalHeight = lipgloss.Height(renderProcessPicker(m, contentWidth)) + 1
	} else if m.mcpAuthName != "" {
		modalHeight = lipgloss.Height(renderMCPAuthPanel(m, contentWidth)) + 1
	} else if m.toolPagerOpen {
		modalHeight = lipgloss.Height(renderToolPagerModal(m, contentWidth)) + 1
	} else if m.diffModalOpen {
		modalHeight = lipgloss.Height(renderDiffModal(m, contentWidth)) + 1
	} else if m.activeRecovery != nil {
		modalHeight = lipgloss.Height(renderRecoveryPanel(m.activeRecovery, contentWidth)) + 1
	} else if m.activeToolRecovery != nil {
		modalHeight = lipgloss.Height(renderToolRecoveryPanel(m.activeToolRecovery, contentWidth)) + 1
	} else if m.activeQuestion != nil {
		modalHeight = lipgloss.Height(renderQuestionPanel(m.activeQuestion, contentWidth)) + 1
	} else if m.effortPicker != nil {
		modalHeight = lipgloss.Height(renderEffortPicker(m, contentWidth)) + 1
	} else if m.activeApproval != nil {
		modalHeight = lipgloss.Height(renderApprovalPanel(m.activeApproval, contentWidth)) + 1
	} else if m.activePlanReview != nil {
		modalHeight = lipgloss.Height(renderPlanReviewPanel(m.activePlanReview, contentWidth)) + 1
	} else if m.autocompleteOpen && len(m.autocompleteItems) > 0 {
		visibleCount := min(len(m.autocompleteItems), maxVisibleAutocomplete)
		modalHeight = visibleCount + 1
	}
	return modalHeight
}

// refreshViewportContent builds the entire transcript text and updates the viewport.
func (m *tuiModel) refreshViewportContent() {
	contentWidth := max(20, m.width-4)

	// If empty state (no entries, not running), keep viewport completely empty (no central dot matrix)
	if len(m.entries) == 0 && !m.running && m.currentStreamText == "" && m.currentReasoningText == "" {
		m.cachedBlocks = nil
		m.cachedEntryCount = 0
		m.cachedDirty = false
		m.transcriptDirty = false
		m.entryLineRanges = nil
		m.viewport.SetContent("")
		return
	}

	curThemeBg := activeTheme().CardBg
	if m.cachedDirty || m.cachedWidth != contentWidth || m.cachedThemeBg != curThemeBg || len(m.entries) < m.cachedEntryCount {
		m.cachedBlocks = make([]string, len(m.entries))
		for i, entry := range m.entries {
			if !isRoundMember(m.entries, i) {
				m.cachedBlocks[i] = renderEntry(entry, contentWidth)
			}
		}
		m.cachedEntryCount = len(m.entries)
		m.cachedWidth = contentWidth
		m.cachedThemeBg = curThemeBg
		m.cachedDirty = false
	} else if len(m.entries) > m.cachedEntryCount {
		for i := m.cachedEntryCount; i < len(m.entries); i++ {
			if !isRoundMember(m.entries, i) {
				m.cachedBlocks = append(m.cachedBlocks, renderEntry(m.entries[i], contentWidth))
			} else {
				m.cachedBlocks = append(m.cachedBlocks, "")
			}
		}
		m.cachedEntryCount = len(m.entries)
	}

	m.entryLineRanges = make([]entryLineRange, 0, len(m.entries)+4)
	blocks := make([]string, 0, len(m.entries)+3)
	currentLine := 0
	for i := 0; i < len(m.entries); {
		if isRoundMember(m.entries, i) {
			end := i + 1
			for end < len(m.entries) && isRoundMember(m.entries, end) {
				end++
			}
			roundSlice := m.entries[i:end]
			hasRunningStep := false
			for _, re := range roundSlice {
				if re.ToolRunning || re.SubagentRunning || re.ReasoningLive {
					hasRunningStep = true
					break
				}
			}
			live := hasRunningStep || (end == len(m.entries) && m.running && strings.TrimSpace(m.currentStreamText) == "")

			if live || m.entries[i].RoundExpanded {
				block, relRanges := renderExpandedToolRound(roundSlice, i, live, contentWidth)
				if block != "" {
					if len(blocks) > 0 {
						currentLine += 2
					}
					for _, rr := range relRanges {
						m.entryLineRanges = append(m.entryLineRanges, entryLineRange{
							EntryIndex:    rr.EntryIndex,
							StartLine:     currentLine + rr.StartLine,
							EndLine:       currentLine + rr.EndLine,
							IsRoundHeader: rr.IsRoundHeader,
						})
					}
					lineCount := strings.Count(block, "\n") + 1
					currentLine += lineCount - 1
					blocks = append(blocks, block)
				}
			} else {
				block := renderCollapsedToolRound(roundSlice, contentWidth)
				if block != "" {
					if len(blocks) > 0 {
						currentLine += 2
					}
					lineCount := strings.Count(block, "\n") + 1
					m.entryLineRanges = append(m.entryLineRanges, entryLineRange{
						EntryIndex:    i,
						StartLine:     currentLine,
						EndLine:       currentLine + lineCount - 1,
						IsRoundHeader: true,
					})
					currentLine += lineCount - 1
					blocks = append(blocks, block)
				}
			}
			i = end
			continue
		}

		block := m.cachedBlocks[i]
		if block == "" {
			block = renderEntry(m.entries[i], contentWidth)
			m.cachedBlocks[i] = block
		}
		if block == "" {
			i++
			continue
		}
		if len(blocks) > 0 {
			currentLine += 2 // "\n\n" separator adds 1 blank line + moves to next line
		}
		lineCount := strings.Count(block, "\n") + 1
		m.entryLineRanges = append(m.entryLineRanges, entryLineRange{
			EntryIndex: i,
			StartLine:  currentLine,
			EndLine:    currentLine + lineCount - 1,
		})
		currentLine += lineCount - 1
		blocks = append(blocks, block)
		i++
	}

	if m.currentReasoningText != "" || m.currentStreamText != "" || m.running {
		if m.currentReasoningText != "" {
			elapsed := time.Since(m.reasoningStartTime)
			liveReasoning := TranscriptEntry{
				Kind:              KindReasoning,
				Text:              m.currentReasoningText,
				ReasoningDuration: elapsed,
				ReasoningExpanded: !m.reasoningLiveCollapsed,
				ReasoningLive:     true,
			}
			blocks = append(blocks, renderEntry(liveReasoning, contentWidth))
		}
		if m.currentStreamText != "" {
			streamEntry := TranscriptEntry{
				Kind: KindAssistant,
				Text: stripProposedPlan(m.currentStreamText),
			}
			if strings.TrimSpace(streamEntry.Text) != "" {
				blocks = append(blocks, renderEntry(streamEntry, contentWidth))
			}
		}
		if m.running {
			elapsed := time.Since(m.taskStartedAt)
			blocks = append(blocks, renderWorking(elapsed, m.thinking, m.subagentsStatusSummary()))
		}
	}

	fullText := strings.Join(blocks, "\n\n")
	m.viewport.SetContent(fullText)
	if !m.scrolledUp {
		m.viewport.GotoBottom()
	}
	m.transcriptDirty = false
}

func (m *tuiModel) hasSelection() bool {
	return m.selectStartX != m.selectEndX || m.selectStartY != m.selectEndY
}

func (m *tuiModel) normalizedSelection() (r1, c1, r2, c2 int) {
	headerHeight := 3
	r1 = m.selectStartY - headerHeight
	c1 = m.selectStartX
	r2 = m.selectEndY - headerHeight
	c2 = m.selectEndX
	if r1 > r2 || (r1 == r2 && c1 > c2) {
		r1, r2 = r2, r1
		c1, c2 = c2, c1
	}
	return r1, c1, r2, c2
}

func (m *tuiModel) extractSelectedText() string {
	if !m.hasSelection() {
		return ""
	}
	r1, c1, r2, c2 := m.normalizedSelection()
	lines := strings.Split(m.viewport.View(), "\n")
	var selectedLines []string

	for r := r1; r <= r2 && r < len(lines); r++ {
		if r < 0 {
			continue
		}
		rawLine := lines[r]
		lineWidth := ansi.StringWidth(rawLine)
		if lineWidth == 0 {
			if r1 < r2 {
				selectedLines = append(selectedLines, "")
			}
			continue
		}

		startCol := 0
		endCol := lineWidth

		if r == r1 && r == r2 {
			startCol = max(0, min(c1, lineWidth))
			endCol = max(0, min(c2, lineWidth))
		} else if r == r1 {
			startCol = max(0, min(c1, lineWidth))
		} else if r == r2 {
			endCol = max(0, min(c2, lineWidth))
		}

		if startCol < endCol {
			selPiece := ansi.Strip(ansi.Cut(rawLine, startCol, endCol))
			selectedLines = append(selectedLines, strings.TrimRight(selPiece, " "))
		}
	}
	return strings.Join(selectedLines, "\n")
}

func (m *tuiModel) renderViewportWithSelection() string {
	viewStr := m.viewport.View()
	if !m.hasSelection() {
		return viewStr
	}

	r1, c1, r2, c2 := m.normalizedSelection()
	lines := strings.Split(viewStr, "\n")
	styleSel := lipgloss.NewStyle().
		Background(lipgloss.Color(activeTheme().SelectBg)).
		Foreground(lipgloss.Color(activeTheme().SelectFg))

	for r := r1; r <= r2 && r < len(lines); r++ {
		if r < 0 {
			continue
		}
		rawLine := lines[r]
		lineWidth := ansi.StringWidth(rawLine)
		if lineWidth == 0 {
			continue
		}

		startCol := 0
		endCol := lineWidth

		if r == r1 && r == r2 {
			startCol = max(0, min(c1, lineWidth))
			endCol = max(0, min(c2, lineWidth))
		} else if r == r1 {
			startCol = max(0, min(c1, lineWidth))
		} else if r == r2 {
			endCol = max(0, min(c2, lineWidth))
		}

		if startCol >= endCol {
			continue
		}

		prefix := ansi.Cut(rawLine, 0, startCol)
		selText := ansi.Strip(ansi.Cut(rawLine, startCol, endCol))
		suffix := ansi.Cut(rawLine, endCol, lineWidth)

		lines[r] = prefix + styleSel.Render(selText) + suffix
	}

	return strings.Join(lines, "\n")
}

// View implements tea.Model and renders the complete screen frame.
func (m *tuiModel) View() tea.View {
	contentWidth := max(20, m.width-4)

	var sections []string

	// 1. Top Header
	sections = append(sections, renderHeader(m.workspace))

	// 2. Viewport (Transcript / Empty State) with mouse selection overlay
	sections = append(sections, m.renderViewportWithSelection())

	// Keep the scroll control between plan content and its review choices.
	copyFeedbackActive := m.copyFeedback != "" && time.Now().Before(m.copyFeedbackExpiresAt)
	moveScrollPillBeforePlan := m.activePlanReview != nil && m.scrolledUp && !copyFeedbackActive
	gapLine := ""
	if copyFeedbackActive {
		color := activeTheme().AccentBlue
		if m.copyFeedbackError {
			color = activeTheme().WarningAmber
		}
		styled := lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(m.copyFeedback)
		leftPad := max(0, contentWidth-lipgloss.Width(styled))
		gapLine = strings.Repeat(" ", leftPad) + styled
	} else if m.scrolledUp {
		gapLine = renderScrollPill(contentWidth)
	} else if m.hasPausedTask() && !m.running {
		gapLine = renderPausedBanner(contentWidth)
	}
	if moveScrollPillBeforePlan {
		sections = append(sections, gapLine)
	}

	// 3. Modal (Shortcuts / Sessions / Recovery / Question / Approval / Plan Review / Autocomplete)
	if m.shortcutsOpen {
		sections = append(sections, renderShortcutsPanel(contentWidth))
	} else if m.agentsPanelOpen {
		sections = append(sections, m.renderAgentsPanel(contentWidth))
	} else if m.sessionsModalOpen {
		sections = append(sections, renderSessionsModal(m, contentWidth))
	} else if m.memoryClearConfirm {
		sections = append(sections, renderMemoryClearConfirm(m, contentWidth))
	} else if m.memoryPicker != nil {
		sections = append(sections, renderMemoryPicker(m, contentWidth))
	} else if m.mcpServerPicker != nil {
		sections = append(sections, renderMCPServerPicker(m, contentWidth))
	} else if m.pendingMemoryAdd {
		sections = append(sections, renderMemoryInputPrompt(m, contentWidth))
	} else if m.themePicker != nil {
		sections = append(sections, renderThemePicker(m, contentWidth))
	} else if m.modelPicker != nil {
		sections = append(sections, renderModelPicker(m, contentWidth))
	} else if m.permissionPicker != nil {
		sections = append(sections, renderPermissionPicker(m, contentWidth))
	} else if m.processPickerOpen {
		sections = append(sections, renderProcessPicker(m, contentWidth))
	} else if m.mcpAuthName != "" {
		sections = append(sections, renderMCPAuthPanel(m, contentWidth))
	} else if m.toolPagerOpen {
		sections = append(sections, renderToolPagerModal(m, contentWidth))
	} else if m.diffModalOpen {
		sections = append(sections, renderDiffModal(m, contentWidth))
	} else if m.activeRecovery != nil {
		sections = append(sections, renderRecoveryPanel(m.activeRecovery, contentWidth))
	} else if m.activeToolRecovery != nil {
		sections = append(sections, renderToolRecoveryPanel(m.activeToolRecovery, contentWidth))
	} else if m.activeQuestion != nil {
		sections = append(sections, renderQuestionPanel(m.activeQuestion, contentWidth))
	} else if m.effortPicker != nil {
		sections = append(sections, renderEffortPicker(m, contentWidth))
	} else if m.activeApproval != nil {
		sections = append(sections, renderApprovalPanel(m.activeApproval, contentWidth))
	} else if m.activePlanReview != nil {
		sections = append(sections, renderPlanReviewPanel(m.activePlanReview, contentWidth))
	} else if m.autocompleteOpen && len(m.autocompleteItems) > 0 {
		sections = append(sections, renderAutocompletePopup(m.autocompleteItems, m.autocompleteIndex, contentWidth))
	}

	// 4. Composer gap (shared with copy feedback, scroll pill, paused task banner, or blank line)
	if !moveScrollPillBeforePlan {
		sections = append(sections, gapLine)
	}

	// 5. Composer
	sections = append(sections, renderComposerBand(m.composer.View(), contentWidth))

	// 6. Footer
	sections = append(sections, renderFooter(m, contentWidth))

	content := strings.Join(sections, "\n")

	view := tea.NewView(content)
	view.AltScreen = true
	view.MouseMode = tea.MouseModeCellMotion
	return view
}
