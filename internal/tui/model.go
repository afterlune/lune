package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	core "github.com/afterlune/luneCore"
	"github.com/charmbracelet/x/ansi"
)

// AutocompleteMode distinguishes between slash command suggestions and @ file mentions.
type AutocompleteMode int

const (
	AutocompleteModeCommand AutocompleteMode = iota
	AutocompleteModeFile
)

type tuiModel struct {
	client            *core.Core
	runtime           *core.SessionRuntime
	workspace         string
	sessionID         string
	sessionTitle      string
	currentModel      string
	permissionMode    core.PermissionMode
	planMode          bool
	savedMode         core.PermissionMode
	width             int
	height            int
	composer          textarea.Model
	viewport          viewport.Model
	entries           []TranscriptEntry
	running           bool
	taskStartedAt     time.Time
	activeQuestion    *ActiveQuestion
	activeApproval     *ActiveApproval
	activePlanReview   *ActivePlanReview
	activeRecovery     *ActiveRecovery
	activeToolRecovery *ActiveToolRecovery
	pausedCheckpoint    *PausedCheckpoint
	activePlanExecution *ActivePlanExecution
	queuedTurns         []QueuedItem
	autocompleteOpen  bool
	autocompleteMode  AutocompleteMode
	autocompleteItems []AutocompleteItem
	autocompleteIndex int
	autocompleteStart int
	autocompleteEnd   int
	fileScanner       *WorkspaceFileScanner
	scrolledUp        bool
	runningExecution  string
	runtimeCancel     context.CancelFunc
	statusMessage     string
	streamActive           bool
	currentStreamText      string
	currentReasoningText   string
	reasoningStartTime     time.Time
	reasoningLiveCollapsed bool
	thinking               bool

	// Viewport transcript cache
	cachedBlocks     []string
	cachedEntryCount int
	cachedWidth      int
	cachedThemeBg    string
	cachedDirty      bool
	transcriptDirty  bool
	entryLineRanges  []entryLineRange

	// Token usage and context window statistics
	totalInputTokens     int64
	totalOutputTokens    int64
	cachedTokens         int64
	reasoningTokens      int64
	contextWindow        int64
	estimatedInputTokens int64
	seededSessionUsage   bool

	// Mouse text selection and copy feedback
	isSelecting           bool
	selectStartX          int
	selectStartY          int
	selectEndX            int
	selectEndY            int
	selectedText          string
	copyFeedback          string
	copyFeedbackExpiresAt time.Time
	shortcutsOpen         bool
	sessionsModalOpen     bool
	sessionsItems         []core.Session
	sessionsFiltered      []core.Session
	sessionsFilter        string
	sessionsIndex         int
	sessionsConfirmDelete bool
	sessionsDeletingID    string

	// Tool output pager modal
	toolPagerOpen    bool
	toolPagerIndex   int
	toolPagerScrollY int

	// Diff viewer modal
	diffModalOpen bool
	diffFiles     []DiffFile
	diffFileIndex int
	diffScrollY   int

	// Prompt history navigation
	promptHistory []string
	historyIndex  int
	historyDraft  string
}

func newTUIModel(client *core.Core, runtime *core.SessionRuntime, sessionID string) *tuiModel {
	comp := textarea.New()
	comp.Placeholder = "Ask Lune to do anything"
	comp.Prompt = "› "
	comp.ShowLineNumbers = false
	comp.DynamicHeight = true
	comp.MinHeight = 1
	comp.MaxHeight = 5
	comp.SetVirtualCursor(true)
	comp.Focus()

	vp := viewport.New(viewport.WithWidth(80), viewport.WithHeight(20))
	vp.SoftWrap = true

	currentModel := ""
	workspace := ""
	permMode := core.PermissionWorkspace
	if client != nil {
		workspace = client.Workspace()
		permMode = client.PermissionMode()
		currentModel = client.ModelForSession(context.Background(), sessionID)
	}

	var entries []TranscriptEntry
	title := ""
	var totalIn, totalOut, cached, reasoning int64
	if client != nil && sessionID != "" {
		if saved, err := client.GetSession(context.Background(), sessionID); err == nil {
			title = saved.Title
			totalIn = saved.SessionUsage.Input
			totalOut = saved.SessionUsage.Output
			cached = saved.SessionUsage.Cached
			reasoning = saved.SessionUsage.Reasoning
			entries = transcriptFromHistory(saved.History)
		}
	}
	if entries == nil {
		entries = []TranscriptEntry{}
	}
	if strings.TrimSpace(title) == "" {
		title = deriveSessionTitleFromEntries(entries)
	}

	var historyPrompts []string
	for _, entry := range entries {
		if entry.Kind == KindUser && entry.Text != "" {
			historyPrompts = append(historyPrompts, entry.Text)
		}
	}

	m := &tuiModel{
		client:             client,
		runtime:            runtime,
		workspace:          workspace,
		sessionID:          sessionID,
		sessionTitle:       title,
		currentModel:       currentModel,
		permissionMode:     permMode,
		savedMode:          permMode,
		totalInputTokens:   totalIn,
		totalOutputTokens:  totalOut,
		cachedTokens:       cached,
		reasoningTokens:    reasoning,
		seededSessionUsage: totalIn > 0 || totalOut > 0 || cached > 0 || reasoning > 0,
		composer:           comp,
		viewport:           vp,
		entries:            entries,
		promptHistory:          historyPrompts,
		historyIndex:           len(historyPrompts),
		fileScanner:            NewWorkspaceFileScanner(workspace),
		reasoningLiveCollapsed: true,
		transcriptDirty:        true,
	}

	m.applyThemeStyles()
	m.checkPendingState(context.Background())
	return m
}

func (m *tuiModel) invalidateEntryCache() {
	m.cachedDirty = true
	m.transcriptDirty = true
}

func (m *tuiModel) getFileScanner() *WorkspaceFileScanner {
	if m.fileScanner == nil {
		ws := m.workspace
		if ws == "" && m.client != nil {
			ws = m.client.Workspace()
		}
		m.fileScanner = NewWorkspaceFileScanner(ws)
	}
	return m.fileScanner
}

func (m *tuiModel) applyThemeStyles() {
	th := activeTheme()
	bg := lipgloss.Color(th.CardBg)

	styles := textarea.DefaultLightStyles()
	styles.Focused.Base = lipgloss.NewStyle().Background(bg)
	styles.Focused.CursorLine = lipgloss.NewStyle().Background(bg)
	styles.Focused.Prompt = lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextPrimary)).Background(bg).Bold(true)
	styles.Focused.Placeholder = lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Background(bg)
	styles.Focused.Text = lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextPrimary)).Background(bg)
	styles.Focused.EndOfBuffer = lipgloss.NewStyle().Background(bg)

	// High-contrast, clearly visible block cursor
	styles.Cursor.Color = lipgloss.Color(th.AccentBlue)
	styles.Cursor.Shape = tea.CursorBlock
	styles.Cursor.Blink = false

	styles.Blurred.Base = lipgloss.NewStyle().Background(bg)
	styles.Blurred.CursorLine = lipgloss.NewStyle().Background(bg)
	styles.Blurred.Prompt = lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Background(bg)
	styles.Blurred.Placeholder = lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Background(bg)
	styles.Blurred.Text = lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextPrimary)).Background(bg)
	styles.Blurred.EndOfBuffer = lipgloss.NewStyle().Background(bg)

	m.composer.SetStyles(styles)
}

func (m *tuiModel) Init() tea.Cmd {
	return tea.Batch(
		textarea.Blink,
		tea.RequestBackgroundColor,
		m.subscribeRuntimeEvents(),
	)
}

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

	var modalHeight int
	if m.shortcutsOpen {
		modalHeight = lipgloss.Height(renderShortcutsPanel(contentWidth)) + 1
	} else if m.sessionsModalOpen {
		modalHeight = lipgloss.Height(renderSessionsModal(m, contentWidth)) + 1
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
	} else if m.activeApproval != nil {
		modalHeight = lipgloss.Height(renderApprovalPanel(m.activeApproval, contentWidth)) + 1
	} else if m.activePlanReview != nil {
		modalHeight = lipgloss.Height(renderPlanReviewPanel(m.activePlanReview, contentWidth)) + 1
	} else if m.autocompleteOpen && len(m.autocompleteItems) > 0 {
		visibleCount := min(len(m.autocompleteItems), maxVisibleAutocomplete)
		modalHeight = visibleCount + 1
	}

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
			blocks = append(blocks, renderWorking(elapsed, m.thinking))
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

	// 3. Modal (Shortcuts / Sessions / Recovery / Question / Approval / Plan Review / Autocomplete)
	if m.shortcutsOpen {
		sections = append(sections, renderShortcutsPanel(contentWidth))
	} else if m.sessionsModalOpen {
		sections = append(sections, renderSessionsModal(m, contentWidth))
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
	} else if m.activeApproval != nil {
		sections = append(sections, renderApprovalPanel(m.activeApproval, contentWidth))
	} else if m.activePlanReview != nil {
		sections = append(sections, renderPlanReviewPanel(m.activePlanReview, contentWidth))
	} else if m.autocompleteOpen && len(m.autocompleteItems) > 0 {
		sections = append(sections, renderAutocompletePopup(m.autocompleteItems, m.autocompleteIndex, contentWidth))
	}

	// 4. Composer gap (shared with copy feedback, scroll pill, paused task banner, or blank line)
	gapLine := ""
	if m.copyFeedback != "" && time.Now().Before(m.copyFeedbackExpiresAt) {
		styled := lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().AccentBlue)).Render(m.copyFeedback)
		leftPad := max(0, contentWidth-lipgloss.Width(styled))
		gapLine = strings.Repeat(" ", leftPad) + styled
	} else if m.scrolledUp {
		gapLine = renderScrollPill(contentWidth)
	} else if m.hasPausedTask() && !m.running {
		gapLine = renderPausedBanner(contentWidth)
	} else if m.hasActivePlan() {
		gapLine = renderPlanProgressBanner(m.activePlanExecution, contentWidth)
	}
	sections = append(sections, gapLine)

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

// bindSessionRuntime switches the active runtime and session context cleanly.
func (m *tuiModel) bindSessionRuntime(ctx context.Context, sessionID string) error {
	if sessionID == "" {
		return fmt.Errorf("session id is required")
	}
	if m.runtimeCancel != nil {
		m.runtimeCancel()
		m.runtimeCancel = nil
	}
	if m.runtime != nil {
		m.runtime.SetUserInteractionAvailable(false)
	}
	rt, err := m.client.OpenSessionRuntime(ctx, sessionID)
	if err != nil {
		return err
	}
	m.runtime = rt
	m.sessionID = sessionID
	m.queuedTurns = nil
	if m.client != nil {
		m.currentModel = m.client.ModelForSession(ctx, sessionID)
		if saved, err := m.client.GetSession(ctx, sessionID); err == nil {
			m.sessionTitle = saved.Title
			m.totalInputTokens = saved.SessionUsage.Input
			m.totalOutputTokens = saved.SessionUsage.Output
			m.cachedTokens = saved.SessionUsage.Cached
			m.reasoningTokens = saved.SessionUsage.Reasoning
			m.seededSessionUsage = saved.SessionUsage.Input > 0 || saved.SessionUsage.Output > 0 || saved.SessionUsage.Cached > 0 || saved.SessionUsage.Reasoning > 0
			m.entries = transcriptFromHistory(saved.History)
			if m.entries == nil {
				m.entries = []TranscriptEntry{}
			}
			if strings.TrimSpace(m.sessionTitle) == "" {
				m.sessionTitle = deriveSessionTitleFromEntries(m.entries)
			}
			m.promptHistory = nil
			for _, entry := range m.entries {
				if entry.Kind == KindUser && entry.Text != "" {
					m.promptHistory = append(m.promptHistory, entry.Text)
				}
			}
			m.historyIndex = len(m.promptHistory)
			m.historyDraft = ""
			m.invalidateEntryCache()
			m.refreshViewportContent()
		}
	}
	rt.SetUserInteractionAvailable(true)
	m.checkPendingState(ctx)
	if state, err := rt.State(ctx); err == nil && state.Task.Status == core.TaskPaused {
		m.pausedCheckpoint = &PausedCheckpoint{
			ExecutionID: state.Task.ExecutionID,
			PausedAt:    time.Now(),
			Reason:      "Paused in session",
		}
	} else {
		m.pausedCheckpoint = nil
	}
	return nil
}

// hasPausedTask reports whether the current session has an interrupted checkpoint or pending recovery.
func (m *tuiModel) hasPausedTask() bool {
	return m.pausedCheckpoint != nil || m.activeRecovery != nil || m.activeToolRecovery != nil
}

// hasActivePlan reports whether an approved plan execution is currently active.
func (m *tuiModel) hasActivePlan() bool {
	return m.activePlanExecution != nil && !m.activePlanExecution.Completed
}

// checkPendingState queries Core for any pending user questions or approvals.
func (m *tuiModel) checkPendingState(ctx context.Context) {
	if m.runtime == nil {
		return
	}
	reqs, err := m.runtime.PendingRequests(ctx)
	if err != nil {
		return
	}
	var foundQuestion *core.UserQuestionRequest
	var foundApproval *core.ApprovalRequest
	var foundRecovery *core.PendingModelRecovery
	var foundToolRecovery *core.ToolRecoveryRequest
	for _, req := range reqs {
		if req.Kind == core.PendingUserQuestion && req.UserQuestion != nil && foundQuestion == nil {
			foundQuestion = req.UserQuestion
		}
		if req.Kind == core.PendingApproval && req.Approval != nil && foundApproval == nil {
			foundApproval = req.Approval
		}
		if req.Kind == core.PendingModelRecoveryRequest && req.ModelRecovery != nil && foundRecovery == nil {
			foundRecovery = req.ModelRecovery
		}
		if req.Kind == core.PendingToolRecovery && req.ToolRecovery != nil && foundToolRecovery == nil {
			foundToolRecovery = req.ToolRecovery
		}
	}

	if foundQuestion != nil {
		if m.activeQuestion == nil || m.activeQuestion.Request.ID != foundQuestion.ID {
			m.activeQuestion = &ActiveQuestion{
				Request:  *foundQuestion,
				Selected: 0,
			}
		}
	} else {
		m.activeQuestion = nil
	}

	if foundApproval != nil {
		if m.permissionMode == core.PermissionFullAccess {
			_ = m.runtime.ResolveApproval(foundApproval.ID, true)
		} else {
			if m.activeApproval == nil || m.activeApproval.Request.ID != foundApproval.ID {
				m.activeApproval = &ActiveApproval{
					Request:  *foundApproval,
					Selected: 0,
				}
			}
		}
	} else {
		m.activeApproval = nil
	}

	if foundRecovery != nil {
		if m.activeRecovery == nil || m.activeRecovery.CallID != foundRecovery.CallID {
			reason := string(foundRecovery.Failure.Category)
			if foundRecovery.Failure.StatusCode > 0 {
				reason = fmt.Sprintf("%s (status=%d, attempts=%d)", foundRecovery.Failure.Category, foundRecovery.Failure.StatusCode, foundRecovery.Failure.Attempts)
			}
			m.activeRecovery = &ActiveRecovery{
				RunID:          foundRecovery.RunID,
				ExecutionID:    foundRecovery.ExecutionID,
				InvocationID:   foundRecovery.InvocationID,
				CallID:         foundRecovery.CallID,
				Reason:         reason,
				RetryNotBefore: foundRecovery.Failure.RetryNotBefore,
				Selected:       0,
			}
		}
	} else {
		m.activeRecovery = nil
	}

	if foundToolRecovery != nil {
		if m.activeToolRecovery == nil || m.activeToolRecovery.InvocationID != foundToolRecovery.InvocationID {
			errOutput := ""
			for i := len(m.entries) - 1; i >= 0; i-- {
				if m.entries[i].Kind == KindTool && (m.entries[i].ToolCallID == foundToolRecovery.CallID || m.entries[i].ToolName == foundToolRecovery.Tool) {
					errOutput = m.entries[i].ToolOutput
					break
				}
			}
			m.activeToolRecovery = &ActiveToolRecovery{
				InvocationID: foundToolRecovery.InvocationID,
				CallID:       foundToolRecovery.CallID,
				ToolName:     foundToolRecovery.Tool,
				Arguments:    string(foundToolRecovery.Arguments),
				ErrorOutput:  errOutput,
				Selected:     0,
			}
		}
	} else {
		m.activeToolRecovery = nil
	}
}

// transcriptFromHistory restores conversation transcript entries from stored JSON history.
func transcriptFromHistory(history json.RawMessage) []TranscriptEntry {
	if len(history) == 0 || string(history) == "null" {
		return nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(history, &items); err != nil {
		return nil
	}
	var entries []TranscriptEntry
	for i, item := range items {
		var value struct {
			Type      string          `json:"type"`
			Role      string          `json:"role"`
			Name      string          `json:"name"`
			CallID    string          `json:"call_id"`
			Arguments json.RawMessage `json:"arguments"`
			Content   json.RawMessage `json:"content"`
			Output    json.RawMessage `json:"output"`
		}
		if err := json.Unmarshal(item, &value); err != nil {
			continue
		}
		switch value.Type {
		case "message":
			text := messageContentText(value.Content)
			if text == "" {
				continue
			}
			kind := KindAssistant
			if value.Role == "user" {
				kind = KindUser
			}
			if kind == KindAssistant && strings.Contains(text, "<proposed_plan>") {
				planText, surrounding := extractProposedPlan(text)
				if surrounding != "" {
					entries = append(entries, TranscriptEntry{
						ID:        fmt.Sprintf("hist-%d", i),
						Kind:      KindAssistant,
						Text:      surrounding,
						Timestamp: time.Now(),
					})
				}
				if planText != "" {
					entries = append(entries, TranscriptEntry{
						ID:        fmt.Sprintf("hist-plan-%d", i),
						Kind:      KindPlan,
						Text:      planText,
						Timestamp: time.Now(),
					})
				}
				continue
			}
			entries = append(entries, TranscriptEntry{
				ID:        fmt.Sprintf("hist-%d", i),
				Kind:      kind,
				Text:      text,
				Timestamp: time.Now(),
			})
		case "function_call", "custom_tool_call":
			if strings.EqualFold(value.Name, "select_tool_category") {
				continue
			}
			args := string(value.Arguments)
			if len(args) > 0 && args[0] == '"' {
				var unquoted string
				if json.Unmarshal(value.Arguments, &unquoted) == nil {
					args = unquoted
				}
			}
			callID := value.CallID
			if callID == "" {
				callID = fmt.Sprintf("hist-tool-%d", i)
			}
			entries = append(entries, TranscriptEntry{
				ID:          fmt.Sprintf("hist-tool-%d", i),
				Kind:        KindTool,
				ToolCallID:  callID,
				ToolName:    value.Name,
				ToolArgs:    args,
				ToolCommand: formatToolSummary(value.Name, args, ""),
				Timestamp:   time.Now(),
			})
		case "function_call_output", "custom_tool_call_output":
			outText := extractToolOutputString(value.Output)
			found := false
			if value.CallID != "" {
				for j := len(entries) - 1; j >= 0; j-- {
					if entries[j].Kind == KindTool && entries[j].ToolCallID == value.CallID {
						entries[j].ToolOutput = outText
						found = true
						break
					}
				}
			}
			if !found && len(entries) > 0 {
				for j := len(entries) - 1; j >= 0; j-- {
					if entries[j].Kind == KindTool && entries[j].ToolOutput == "" {
						entries[j].ToolOutput = outText
						break
					}
				}
			}
		}
	}
	return entries
}

func deriveSessionTitleFromEntries(entries []TranscriptEntry) string {
	for _, entry := range entries {
		if entry.Kind == KindUser && strings.TrimSpace(entry.Text) != "" {
			return deriveSessionTitleText(entry.Text)
		}
	}
	return ""
}

func deriveSessionTitleText(text string) string {
	line := strings.TrimSpace(text)
	if idx := strings.IndexAny(line, "\r\n"); idx >= 0 {
		line = strings.TrimSpace(line[:idx])
	}
	if line == "" {
		return ""
	}
	return truncateString(line, 40)
}

func sessionDisplayTitle(s core.Session) string {
	if t := strings.TrimSpace(s.Title); t != "" {
		return t
	}
	if len(s.History) > 0 && string(s.History) != "null" {
		if t := deriveSessionTitleFromEntries(transcriptFromHistory(s.History)); t != "" {
			return t
		}
	}
	return "<untitled>"
}

func extractResponseText(response json.RawMessage) string {
	if len(response) == 0 || string(response) == "null" {
		return ""
	}
	var envelope struct {
		Output []json.RawMessage `json:"output"`
	}
	if err := json.Unmarshal(response, &envelope); err != nil {
		return ""
	}
	var parts []string
	for _, item := range envelope.Output {
		var value struct {
			Type    string          `json:"type"`
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		}
		if err := json.Unmarshal(item, &value); err != nil {
			continue
		}
		if value.Type == "message" && (value.Role == "" || value.Role == "assistant") {
			if text := strings.TrimSpace(messageContentText(value.Content)); text != "" {
				parts = append(parts, text)
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

func extractToolOutputString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var str string
	if err := json.Unmarshal(raw, &str); err == nil {
		return str
	}
	var parts []json.RawMessage
	if err := json.Unmarshal(raw, &parts); err == nil {
		var b strings.Builder
		for _, p := range parts {
			var part struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}
			if json.Unmarshal(p, &part) == nil && part.Text != "" {
				b.WriteString(part.Text)
			}
		}
		if b.Len() > 0 {
			return b.String()
		}
	}
	var obj struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil && obj.Text != "" {
		return obj.Text
	}
	return string(raw)
}

func messageContentText(content json.RawMessage) string {
	if len(content) == 0 {
		return ""
	}
	var text string
	if json.Unmarshal(content, &text) == nil {
		return text
	}
	var parts []json.RawMessage
	if json.Unmarshal(content, &parts) != nil {
		return ""
	}
	var out strings.Builder
	for _, raw := range parts {
		var part struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(raw, &part) == nil && (part.Type == "input_text" || part.Type == "output_text") {
			out.WriteString(part.Text)
		}
	}
	return out.String()
}
