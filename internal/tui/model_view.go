package tui

import (
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	tuiMarkdown "github.com/afterlune/lune/internal/tui/markdown"
	"github.com/charmbracelet/x/ansi"
)

// updateLayout recalculates viewport and composer dimensions based on terminal window size.
func (m *tuiModel) updateLayout() {
	if m.width <= 0 || m.height <= 0 {
		return
	}

	if m.width < 24 || m.height < 10 {
		return
	}
	contentWidth := max(20, m.width-4)
	m.refreshLiveContent(contentWidth)
	// renderComposerBand reserves one leading column for the card surface.
	m.input.composer.SetWidth(contentWidth - 1)

	// Measure bottom components height:
	// - Header: ~2-3 lines
	// - Blank line gap before composer: 1 line
	// - Composer band: composer height (1 to 5) + 2 padding rows
	// - Footer: 2 lines
	// - Popup / Question / Approval / Plan review / Recovery if open
	headerHeight := 3
	gapHeight := 1
	composerBandHeight := m.input.composer.Height() + 2
	footerHeight := 2

	queuedHeight := 0
	if band := renderQueuedBand(m, contentWidth); band != "" {
		queuedHeight = lipgloss.Height(band)
	}

	baseOccupied := headerHeight + gapHeight + composerBandHeight + footerHeight + queuedHeight
	if m.view.liveContent != "" {
		baseOccupied += lipgloss.Height(m.view.liveContent) + 1
	}
	m.overlayMaxRows = max(0, m.height-baseOccupied-2)
	m.layoutReady = true
	_, modalHeight := m.overlaySection(contentWidth)
	occupied := baseOccupied + modalHeight
	vpHeight := max(1, m.height-occupied)

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

	entriesChanged := (len(m.entries) != m.view.cachedEntryCount)
	needRefresh := widthChanged || entriesChanged || m.view.cachedDirty || m.view.transcriptDirty
	if needRefresh {
		m.refreshViewportContent()
	}
}

// overlaySection returns the one overlay the layout must reserve space for, its
// rendered text and the rows it occupies. Modal overlays come from modalState; the
// memory input prompt, the MCP authentication panel, the task cards and the
// autocomplete popup follow in the same priority order so the reserved height and
// the rendered text can never disagree.
func (m *tuiModel) overlaySection(contentWidth int) (string, int) {
	active := m.modals.active()
	var view string
	switch {
	case active == modalShortcuts:
		view = renderShortcutsPanel(contentWidth)
	case active == modalAgents:
		view = m.renderAgentsPanel(contentWidth)
	case active == modalSessions:
		view = renderSessionsModal(m, contentWidth)
	case active == modalMemoryClear:
		view = renderMemoryClearConfirm(m, contentWidth)
	case active == modalMemoryPicker:
		view = renderMemoryPicker(m, contentWidth)
	case active == modalMCPPicker:
		view = renderMCPServerPicker(m, contentWidth)
	case m.modals.memory.pendingAdd:
		view = renderMemoryInputPrompt(m, contentWidth)
	case active == modalTheme:
		view = renderThemePicker(m, contentWidth)
	case active == modalModel:
		view = renderModelPicker(m, contentWidth)
	case active == modalPermission:
		view = renderPermissionPicker(m, contentWidth)
	case active == modalProcess:
		view = renderProcessPicker(m, contentWidth)
	case m.modals.mcp.authName != "":
		view = renderMCPAuthPanel(m, contentWidth)
	case active == modalToolPager:
		view = renderToolPagerModal(m, contentWidth)
	case active == modalDiff:
		view = renderDiffModal(m, contentWidth)
	case m.activeCardKind() != cardNone:
		switch m.activeCardKind() {
		case cardRecovery:
			view = renderRecoveryPanel(m.activeRecovery, contentWidth)
		case cardToolRecovery:
			view = renderToolRecoveryPanel(m.activeToolRecovery, contentWidth)
		case cardApproval:
			view = renderApprovalPanel(m.activeApproval, contentWidth)
		case cardQuestion:
			view = renderQuestionPanel(m.activeQuestion, contentWidth)
		case cardEffort:
			view = renderEffortPicker(m, contentWidth)
		case cardPlanReview:
			view = renderPlanReviewPanel(m.activePlanReview, contentWidth)
		}
	case m.input.autocompleteOpen && len(m.input.autocompleteItems) > 0:
		// The popup sizes itself by its visible candidates plus the hint row,
		// not by its rendered height.
		return renderAutocompletePopup(m.input.autocompleteItems, m.input.autocompleteIndex, contentWidth),
			min(len(m.input.autocompleteItems), maxVisibleAutocomplete) + 1
	default:
		return "", 0
	}
	if m.layoutReady {
		view = fitOverlayHeight(view, m.overlayMaxRows)
	}
	// The extra row is the fixed hint slot above the panel, in addition to the
	// composer gap accounted for by updateLayout.
	return view, lipgloss.Height(view) + 1
}

func fitOverlayHeight(view string, maxRows int) string {
	if view == "" || maxRows <= 0 {
		return ""
	}
	lines := strings.Split(view, "\n")
	if len(lines) <= maxRows {
		return view
	}
	selected := -1
	for i, line := range lines {
		if strings.Contains(ansi.Strip(line), "›") {
			selected = i
			break
		}
	}
	if selected < 0 {
		return strings.Join(lines[:maxRows], "\n")
	}
	if maxRows < 4 {
		priority := []int{selected}
		if maxRows >= 2 && selected != 0 {
			priority = append(priority, 0)
		}
		if maxRows >= 3 && selected != len(lines)-1 {
			priority = append(priority, len(lines)-1)
		}
		for i := 0; len(priority) < maxRows && i < len(lines); i++ {
			if !slices.Contains(priority, i) {
				priority = append(priority, i)
			}
		}
		slices.Sort(priority)
		result := make([]string, 0, len(priority))
		for _, index := range priority[:min(maxRows, len(priority))] {
			result = append(result, lines[index])
		}
		return strings.Join(result, "\n")
	}
	keep := map[int]struct{}{0: {}, 1: {}, selected: {}, len(lines) - 1: {}}
	for distance := 1; len(keep) < maxRows; distance++ {
		added := false
		for _, index := range []int{selected - distance, selected + distance} {
			if index >= 2 && index < len(lines)-1 {
				if _, exists := keep[index]; !exists {
					keep[index] = struct{}{}
					added = true
					if len(keep) == maxRows {
						break
					}
				}
			}
		}
		if !added || distance > len(lines) {
			break
		}
	}
	indices := make([]int, 0, len(keep))
	for index := range keep {
		indices = append(indices, index)
	}
	slices.Sort(indices)
	if len(indices) > maxRows {
		indices = indices[:maxRows]
	}
	result := make([]string, 0, len(indices))
	previous := -2
	for _, index := range indices {
		if index > previous+1 && len(result) < maxRows {
			result = append(result, "…")
		}
		if len(result) >= maxRows {
			break
		}
		result = append(result, lines[index])
		previous = index
	}
	if len(result) > maxRows {
		result = result[:maxRows]
	}
	return strings.Join(result, "\n")
}

// refreshViewportContent builds the entire transcript text and updates the viewport.
func (m *tuiModel) refreshViewportContent() {
	contentWidth := max(20, m.width-4)
	m.refreshLiveContent(contentWidth)
	if !m.view.cachedDirty && !m.view.transcriptDirty && m.view.cachedWidth == contentWidth &&
		m.view.cachedThemeBg == activeTheme().CardBg && len(m.entries) == m.view.cachedEntryCount {
		return
	}
	viewportWidth := max(1, m.viewport.Width()-m.viewport.Style.GetHorizontalFrameSize())
	if m.viewport.LeftGutterFunc != nil {
		viewportWidth = max(1, viewportWidth-ansi.StringWidth(m.viewport.LeftGutterFunc(viewport.GutterContext{})))
	}

	// If empty state (no entries, not running), keep viewport completely empty (no central dot matrix)
	if len(m.entries) == 0 && !m.run.running && m.run.currentStreamText == "" && m.run.currentReasoningText == "" {
		m.view.cachedBlocks = nil
		m.view.cachedEntryCount = 0
		m.view.cachedDirty = false
		m.view.dirtyEntries = nil
		m.view.transcriptDirty = false
		m.view.entryLineRanges = nil
		m.view.transcriptLines = nil
		m.viewport.SoftWrap = false
		m.viewport.SetContent("")
		return
	}

	curThemeBg := activeTheme().CardBg
	rebuildEntryCache := m.view.cachedDirty || m.view.cachedWidth != contentWidth || m.view.cachedThemeBg != curThemeBg || len(m.entries) < m.view.cachedEntryCount
	if rebuildEntryCache {
		m.viewport.SoftWrap = false
		m.view.cachedBlocks = make([]string, len(m.entries))
		for i, entry := range m.entries {
			if !isRoundMember(m.entries, i) {
				m.view.cachedBlocks[i] = renderEntry(entry, contentWidth)
			}
		}
		m.view.cachedEntryCount = len(m.entries)
		m.view.cachedWidth = contentWidth
		m.view.cachedThemeBg = curThemeBg
		m.view.cachedDirty = false
		m.view.dirtyEntries = nil
	} else {
		cachedEntryCount := m.view.cachedEntryCount
		if len(m.entries) > cachedEntryCount {
			for i := cachedEntryCount; i < len(m.entries); i++ {
				if !isRoundMember(m.entries, i) {
					m.view.cachedBlocks = append(m.view.cachedBlocks, renderEntry(m.entries[i], contentWidth))
				} else {
					m.view.cachedBlocks = append(m.view.cachedBlocks, "")
				}
			}
			m.view.cachedEntryCount = len(m.entries)
		}
		for index := range m.view.dirtyEntries {
			if index >= cachedEntryCount || index >= len(m.entries) {
				continue
			}
			if isRoundMember(m.entries, index) {
				m.view.cachedBlocks[index] = ""
			} else {
				m.view.cachedBlocks[index] = renderEntry(m.entries[index], contentWidth)
			}
		}
		m.view.dirtyEntries = nil
	}

	m.view.entryLineRanges = make([]entryLineRange, 0, len(m.entries)+4)
	blocks := make([]string, 0, len(m.entries)+3)
	currentLine := 0
	pendingRule := false

	// Blocks are joined with a blank line. appendBlock owns that accounting so
	// entryLineRanges stays exact after separators are inserted.
	appendBlock := func(block string) {
		if block == "" {
			return
		}
		if len(blocks) > 0 {
			currentLine += 2 // "\n\n" separator adds 1 blank line + moves to next line
		}
		currentLine += strings.Count(block, "\n")
		blocks = append(blocks, block)
	}
	nextBlockStart := func() int {
		if len(blocks) > 0 {
			return currentLine + 2
		}
		return currentLine
	}
	// flushRule emits the pending horizontal rule in front of real content and
	// never leaves a dangling rule at the end of the transcript.
	flushRule := func() {
		if !pendingRule {
			return
		}
		pendingRule = false
		if len(blocks) == 0 {
			return
		}
		appendBlock(renderTranscriptRule(contentWidth))
	}
	appendRendered := func(block string, relRanges []entryLineRange) {
		if block == "" {
			return
		}
		flushRule()
		start := nextBlockStart()
		for _, rr := range relRanges {
			m.view.entryLineRanges = append(m.view.entryLineRanges, entryLineRange{
				EntryIndex:    rr.EntryIndex,
				StartLine:     start + rr.StartLine,
				EndLine:       start + rr.EndLine,
				IsRoundHeader: rr.IsRoundHeader,
				IsRoundFooter: rr.IsRoundFooter,
			})
		}
		appendBlock(block)
	}

	for i := 0; i < len(m.entries); {
		if m.entries[i].Kind == KindQueued {
			// Queued turns stay pinned above the composer instead of scrolling
			// away with the transcript.
			i++
			continue
		}
		if isRoundMember(m.entries, i) {
			end := i + 1
			for end < len(m.entries) && isRoundMember(m.entries, end) {
				end++
			}
			roundSlice := m.entries[i:end]
			live := m.roundLive(i, end)

			if m.roundExpanded(i, live) {
				block, relRanges := renderExpandedToolRound(roundSlice, i, live, contentWidth)
				appendRendered(block, relRanges)
			} else {
				block := renderCollapsedToolRound(roundSlice, contentWidth)
				if block != "" {
					appendRendered(block, []entryLineRange{{
						EntryIndex:    i,
						EndLine:       strings.Count(block, "\n"),
						IsRoundHeader: true,
					}})
				}
			}
			i = end
			continue
		}

		block := m.view.cachedBlocks[i]
		if block == "" {
			block = renderEntry(m.entries[i], contentWidth)
			m.view.cachedBlocks[i] = block
		}
		if block == "" {
			i++
			continue
		}
		// Every user turn starts a new task, so it gets its own solid rule.
		if m.entries[i].Kind == KindUser && len(blocks) > 0 {
			pendingRule = true
		}
		appendRendered(block, []entryLineRange{{
			EntryIndex: i,
			EndLine:    strings.Count(block, "\n"),
		}})
		i++
	}

	if !m.viewport.SoftWrap {
		m.viewport.SoftWrap = transcriptNeedsSoftWrap(blocks, viewportWidth)
	}
	fullText := strings.Join(blocks, "\n\n")
	if fullText == "" {
		m.view.transcriptLines = nil
	} else {
		m.view.transcriptLines = strings.Split(fullText, "\n")
	}
	m.viewport.SetContent(fullText)
	if !m.scrolledUp {
		m.viewport.GotoBottom()
	}
	m.view.transcriptDirty = false
}

func (m *tuiModel) refreshLiveContent(width int) {
	if !m.view.liveDirty && m.view.liveContent != "" && width == m.view.cachedWidth {
		return
	}
	m.run.ensureLiveTails()
	if m.scrolledUp {
		m.view.liveContent = ""
		m.view.liveDirty = false
		return
	}
	var blocks []string
	if m.run.currentReasoningText != "" {
		blocks = append(blocks, renderStreamingEntry(TranscriptEntry{
			Kind: KindReasoning, Text: m.run.reasoningTail.RenderInput(),
			ReasoningDuration: time.Since(m.run.reasoningStartTime),
			ReasoningExpanded: !m.run.reasoningLiveCollapsed, ReasoningLive: true,
		}, width))
	}
	if m.run.currentStreamText != "" {
		appendAssistant := func(text string) {
			if strings.TrimSpace(text) != "" {
				blocks = append(blocks, renderStreamingEntry(TranscriptEntry{Kind: KindAssistant, Text: text}, width))
			}
		}
		if m.run.planFound {
			appendAssistant(m.run.planBeforeTail.RenderInput())
			plan := m.run.planTail.RenderInput()
			if tuiMarkdown.HasPlanContent(tuiMarkdown.CleanProposedPlanTags(plan)) {
				blocks = append(blocks, renderStreamingEntry(TranscriptEntry{Kind: KindPlan, Text: plan}, width))
			}
			appendAssistant(m.run.planAfterTail.RenderInput())
		} else {
			appendAssistant(m.run.assistantTail.RenderInput())
		}
	}
	if m.run.running {
		elapsed := time.Since(m.run.taskStartedAt)
		if m.run.taskActivity != "" {
			blocks = append(blocks, renderWorkingActivity(elapsed, m.run.taskActivity, m.subagentsStatusSummary()))
		} else {
			blocks = append(blocks, renderWorking(elapsed, m.run.thinking, m.subagentsStatusSummary()))
		}
	}
	for _, agentID := range m.run.subagentStreamOrder {
		stream := m.run.subagentStreams[agentID]
		if stream == nil || stream.tail == nil {
			continue
		}
		blocks = append(blocks, renderLiveSubagentOutput(stream.agentName, agentID, stream.tail.RenderInput(), width))
	}
	content := strings.Join(blocks, "\n\n")
	const maxLiveRows = 16
	lines := strings.Split(content, "\n")
	if len(lines) > maxLiveRows {
		lines = append([]string{"…"}, lines[len(lines)-maxLiveRows+1:]...)
	}
	m.view.liveContent = strings.Join(lines, "\n")
	m.view.liveDirty = false
}

func transcriptNeedsSoftWrap(blocks []string, width int) bool {
	if width <= 0 {
		return true
	}
	for _, block := range blocks {
		for start := 0; start < len(block); {
			relativeEnd := strings.IndexByte(block[start:], '\n')
			end := len(block)
			if relativeEnd >= 0 {
				end = start + relativeEnd
			}
			if ansi.StringWidth(block[start:end]) > width {
				return true
			}
			if relativeEnd < 0 {
				break
			}
			start = end + 1
		}
	}
	return false
}

func (m *tuiModel) hasSelection() bool {
	return m.input.selectStartX != m.input.selectEndX || m.input.selectStartDocRow != m.input.selectEndDocRow || (m.input.selectStartY != m.input.selectEndY && m.input.selectStartDocRow == 0 && m.input.selectEndDocRow == 0)
}

func (m *tuiModel) normalizedDocSelection() (r1, c1, r2, c2 int) {
	r1 = m.input.selectStartDocRow
	c1 = m.input.selectStartX
	r2 = m.input.selectEndDocRow
	c2 = m.input.selectEndX
	if r1 == 0 && r2 == 0 && m.input.selectStartY != m.input.selectEndY {
		headerHeight := 3
		r1 = m.viewport.YOffset() + max(0, m.input.selectStartY-headerHeight)
		r2 = m.viewport.YOffset() + max(0, m.input.selectEndY-headerHeight)
	}
	if r1 > r2 || (r1 == r2 && c1 > c2) {
		r1, r2 = r2, r1
		c1, c2 = c2, c1
	}
	return r1, c1, r2, c2
}

func (m *tuiModel) normalizedSelection() (r1, c1, r2, c2 int) {
	headerHeight := 3
	r1 = m.input.selectStartY - headerHeight
	c1 = m.input.selectStartX
	r2 = m.input.selectEndY - headerHeight
	c2 = m.input.selectEndX
	if r1 > r2 || (r1 == r2 && c1 > c2) {
		r1, r2 = r2, r1
		c1, c2 = c2, c1
	}
	return r1, c1, r2, c2
}

func (m *tuiModel) viewportAllLines() []string {
	if m.view.transcriptLines != nil {
		return m.view.transcriptLines
	}
	totalLines := m.viewport.TotalLineCount()
	if totalLines <= 0 {
		return nil
	}
	vpCopy := m.viewport
	vpCopy.SetHeight(totalLines)
	vpCopy.SetYOffset(0)
	return strings.Split(vpCopy.View(), "\n")
}

func (m *tuiModel) extractSelectedText() string {
	if !m.hasSelection() {
		return ""
	}
	r1, c1, r2, c2 := m.normalizedDocSelection()
	lines := m.viewportAllLines()
	if len(lines) == 0 {
		lines = strings.Split(m.viewport.View(), "\n")
		r1, c1, r2, c2 = m.normalizedSelection()
	}
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
		} else if r > r1 && r < r2 {
			selectedLines = append(selectedLines, "")
		}
	}
	return strings.Join(selectedLines, "\n")
}

func (m *tuiModel) renderViewportWithSelection() string {
	viewStr := m.viewport.View()
	if !m.hasSelection() {
		return viewStr
	}

	docR1, c1, docR2, c2 := m.normalizedDocSelection()
	lines := strings.Split(viewStr, "\n")
	styleSel := lipgloss.NewStyle().
		Background(lipgloss.Color(activeTheme().SelectBg)).
		Foreground(lipgloss.Color(activeTheme().SelectFg))

	yOffset := m.viewport.YOffset()

	for r := 0; r < len(lines); r++ {
		docR := yOffset + r
		if docR < docR1 || docR > docR2 {
			continue
		}
		rawLine := lines[r]
		lineWidth := ansi.StringWidth(rawLine)
		if lineWidth == 0 {
			continue
		}

		startCol := 0
		endCol := lineWidth

		if docR == docR1 && docR == docR2 {
			startCol = max(0, min(c1, lineWidth))
			endCol = max(0, min(c2, lineWidth))
		} else if docR == docR1 {
			startCol = max(0, min(c1, lineWidth))
		} else if docR == docR2 {
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
	if m.width > 0 && m.height > 0 && (m.width < 24 || m.height < 10) {
		width := max(1, m.width)
		message := ansi.Truncate("Terminal too small; resize to continue", width, "…")
		return tea.NewView(message)
	}
	contentWidth := max(20, m.width-4)
	m.refreshLiveContent(contentWidth)

	var sections []string

	// 1. Top Header
	sections = append(sections, renderHeader(m.workspace))

	// 2. Viewport (Transcript / Empty State) with mouse selection overlay
	sections = append(sections, m.renderViewportWithSelection())
	if m.view.liveContent != "" {
		sections = append(sections, m.view.liveContent)
	}

	// 2b. Pinned queue band: waiting turns never scroll away with the transcript.
	if band := renderQueuedBand(m, contentWidth); band != "" {
		sections = append(sections, band)
	}

	// Keep permanent gaps above and below the review. Changing the hint must
	// never move the review choices or the composer.
	copyFeedbackActive := m.input.copyFeedback != "" && time.Now().Before(m.input.copyFeedbackExpiresAt)
	planReviewGap := m.activePlanReview != nil
	gapLine := ""
	if copyFeedbackActive {
		color := activeTheme().AccentBlue
		if m.input.copyFeedbackError {
			color = activeTheme().WarningAmber
		}
		styled := lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(m.input.copyFeedback)
		leftPad := max(0, contentWidth-lipgloss.Width(styled))
		gapLine = strings.Repeat(" ", leftPad) + styled
	} else if m.scrolledUp {
		gapLine = renderScrollPill(m, contentWidth)
	} else if m.hasPausedTask() && !m.run.running {
		gapLine = renderPausedBanner(contentWidth)
	}
	if planReviewGap {
		sections = append(sections, gapLine)
	}

	// 3. The one overlay that owns the screen (modal, task card or autocomplete).
	if overlay, _ := m.overlaySection(contentWidth); overlay != "" {
		sections = append(sections, overlay)
	}

	// 4. Composer gap (shared with copy feedback, scroll pill, paused task banner, or blank line)
	if planReviewGap {
		sections = append(sections, "")
	} else {
		sections = append(sections, gapLine)
	}

	// 5. Composer
	sections = append(sections, renderComposerBand(m.input.composer.View(), contentWidth))

	// 6. Footer
	sections = append(sections, renderFooter(m, contentWidth))

	content := strings.Join(sections, "\n")

	view := tea.NewView(content)
	view.AltScreen = true
	view.MouseMode = tea.MouseModeCellMotion
	return view
}
