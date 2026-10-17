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
	activeApproval    *ActiveApproval
	activePlanReview  *ActivePlanReview
	autocompleteOpen  bool
	autocompleteItems []AutocompleteItem
	autocompleteIndex int
	scrolledUp        bool
	runningExecution  string
	runtimeCancel     context.CancelFunc
	statusMessage     string
	streamActive      bool
	currentStreamText string
	thinking          bool

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

	m := &tuiModel{
		client:         client,
		runtime:        runtime,
		workspace:      workspace,
		sessionID:      sessionID,
		currentModel:   currentModel,
		permissionMode: permMode,
		savedMode:      permMode,
		composer:       comp,
		viewport:       vp,
		entries:        []TranscriptEntry{},
	}

	m.applyThemeStyles()
	m.checkPendingState(context.Background())
	return m
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
	// - Popup / Question / Approval / Plan review if open
	headerHeight := 3
	gapHeight := 1
	composerBandHeight := m.composer.Height() + 2
	footerHeight := 2

	var modalHeight int
	if m.shortcutsOpen {
		modalHeight = lipgloss.Height(renderShortcutsPanel(contentWidth)) + 1
	} else if m.activeQuestion != nil {
		modalHeight = lipgloss.Height(renderQuestionPanel(m.activeQuestion, contentWidth)) + 1
	} else if m.activeApproval != nil {
		modalHeight = lipgloss.Height(renderApprovalPanel(m.activeApproval, contentWidth)) + 1
	} else if m.activePlanReview != nil {
		modalHeight = lipgloss.Height(renderPlanReviewPanel(m.activePlanReview, contentWidth)) + 1
	} else if m.autocompleteOpen && len(m.autocompleteItems) > 0 {
		modalHeight = len(m.autocompleteItems) + 1
	}

	occupied := headerHeight + gapHeight + composerBandHeight + footerHeight + modalHeight
	vpHeight := max(3, m.height-occupied)

	m.viewport.SetWidth(contentWidth)
	m.viewport.SetHeight(vpHeight)
	m.refreshViewportContent()
}

// refreshViewportContent builds the entire transcript text and updates the viewport.
func (m *tuiModel) refreshViewportContent() {
	contentWidth := max(20, m.width-4)

	// If empty state (no entries, not running), keep viewport completely empty (no central dot matrix)
	if len(m.entries) == 0 && !m.running && m.currentStreamText == "" {
		m.viewport.SetContent("")
		return
	}

	var blocks []string
	for _, entry := range m.entries {
		block := renderEntry(entry, contentWidth)
		if block != "" {
			blocks = append(blocks, block)
		}
	}

	// Active stream delta if any
	if m.currentStreamText != "" {
		streamEntry := TranscriptEntry{
			Kind: KindAssistant,
			Text: m.currentStreamText,
		}
		blocks = append(blocks, renderEntry(streamEntry, contentWidth))
	}

	// Active working indicator
	if m.running {
		elapsed := time.Since(m.taskStartedAt)
		blocks = append(blocks, renderWorking(elapsed, m.thinking))
	}

	fullText := strings.Join(blocks, "\n\n")
	m.viewport.SetContent(fullText)
	if !m.scrolledUp {
		m.viewport.GotoBottom()
	}
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

	// 3. Modal (Shortcuts / Question / Approval / Plan Review / Autocomplete)
	if m.shortcutsOpen {
		sections = append(sections, renderShortcutsPanel(contentWidth))
	} else if m.activeQuestion != nil {
		sections = append(sections, renderQuestionPanel(m.activeQuestion, contentWidth))
	} else if m.activeApproval != nil {
		sections = append(sections, renderApprovalPanel(m.activeApproval, contentWidth))
	} else if m.activePlanReview != nil {
		sections = append(sections, renderPlanReviewPanel(m.activePlanReview, contentWidth))
	} else if m.autocompleteOpen && len(m.autocompleteItems) > 0 {
		sections = append(sections, renderAutocompletePopup(m.autocompleteItems, m.autocompleteIndex, contentWidth))
	}

	// 4. Composer gap (shared with copy feedback, scroll pill, or blank line)
	gapLine := ""
	if m.copyFeedback != "" && time.Now().Before(m.copyFeedbackExpiresAt) {
		styled := lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().AccentBlue)).Render(m.copyFeedback)
		leftPad := max(0, contentWidth-lipgloss.Width(styled))
		gapLine = strings.Repeat(" ", leftPad) + styled
	} else if m.scrolledUp {
		gapLine = renderScrollPill(contentWidth)
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
	if m.client != nil {
		m.currentModel = m.client.ModelForSession(ctx, sessionID)
	}
	rt.SetUserInteractionAvailable(true)
	m.checkPendingState(ctx)
	return nil
}

// checkPendingState queries Core for any pending user questions or approvals.
func (m *tuiModel) checkPendingState(ctx context.Context) {
	if m.runtime == nil {
		return
	}
	reqs, err := m.runtime.PendingRequests(ctx)
	if err == nil {
		for _, req := range reqs {
			if req.Kind == core.PendingUserQuestion && req.UserQuestion != nil {
				m.activeQuestion = &ActiveQuestion{
					Request:  *req.UserQuestion,
					Selected: 0,
				}
				break
			}
			if req.Kind == core.PendingApproval && req.Approval != nil {
				if m.permissionMode == core.PermissionFullAccess {
					_ = m.runtime.ResolveApproval(req.Approval.ID, true)
				} else {
					m.activeApproval = &ActiveApproval{
						Request:  *req.Approval,
						Selected: 0,
					}
				}
			}
		}
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
			Arguments json.RawMessage `json:"arguments"`
			Content   json.RawMessage `json:"content"`
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
			entries = append(entries, TranscriptEntry{
				ID:        fmt.Sprintf("hist-%d", i),
				Kind:      kind,
				Text:      text,
				Timestamp: time.Now(),
			})
		case "function_call":
			args := string(value.Arguments)
			if len(args) > 0 && args[0] == '"' {
				var unquoted string
				if json.Unmarshal(value.Arguments, &unquoted) == nil {
					args = unquoted
				}
			}
			entries = append(entries, TranscriptEntry{
				ID:          fmt.Sprintf("hist-tool-%d", i),
				Kind:        KindTool,
				ToolName:    value.Name,
				ToolCommand: value.Name + " " + args,
				Timestamp:   time.Now(),
			})
		}
	}
	return entries
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
