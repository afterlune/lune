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
}

func newTUIModel(client *core.Core, runtime *core.SessionRuntime, sessionID string) *tuiModel {
	comp := textarea.New()
	comp.Placeholder = "Ask Codex to do anything"
	comp.Prompt = "› "
	comp.ShowLineNumbers = false
	comp.DynamicHeight = true
	comp.MinHeight = 1
	comp.MaxHeight = 5
	comp.SetVirtualCursor(false)
	comp.Focus()

	vp := viewport.New(viewport.WithWidth(80), viewport.WithHeight(20))
	vp.SoftWrap = true

	m := &tuiModel{
		client:         client,
		runtime:        runtime,
		workspace:      client.Workspace(),
		sessionID:      sessionID,
		permissionMode: client.PermissionMode(),
		savedMode:      client.PermissionMode(),
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
	styles := textarea.DefaultLightStyles()
	styles.Focused.Prompt = lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextPrimary)).Bold(true)
	styles.Focused.Placeholder = lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted))
	styles.Focused.Text = lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextPrimary))
	styles.Blurred.Prompt = lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted))
	styles.Blurred.Placeholder = lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted))
	styles.Blurred.Text = lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextPrimary))
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
	// - Composer: composer height (1 to 5) + 1
	// - Footer: 2 lines
	// - Popup / Question / Approval / Plan review if open
	headerHeight := 3
	composerHeight := m.composer.Height() + 1
	footerHeight := 2

	var modalHeight int
	if m.activeQuestion != nil {
		modalHeight = lipgloss.Height(renderQuestionPanel(m.activeQuestion, contentWidth)) + 1
	} else if m.activeApproval != nil {
		modalHeight = lipgloss.Height(renderApprovalPanel(m.activeApproval, contentWidth)) + 1
	} else if m.activePlanReview != nil {
		modalHeight = lipgloss.Height(renderPlanReviewPanel(m.activePlanReview, contentWidth)) + 1
	} else if m.autocompleteOpen && len(m.autocompleteItems) > 0 {
		modalHeight = len(m.autocompleteItems) + 1
	}

	occupied := headerHeight + composerHeight + footerHeight + modalHeight
	vpHeight := max(3, m.height-occupied)

	m.viewport.SetWidth(contentWidth)
	m.viewport.SetHeight(vpHeight)
	m.refreshViewportContent()
}

// refreshViewportContent builds the entire transcript text and updates the viewport.
func (m *tuiModel) refreshViewportContent() {
	contentWidth := max(20, m.width-4)

	// If empty state (no entries, not running), render the centered Braille logo
	if len(m.entries) == 0 && !m.running && m.currentStreamText == "" {
		logo := RenderLogoEmptyState(contentWidth, m.viewport.Height())
		m.viewport.SetContent(logo)
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
		blocks = append(blocks, renderWorking(elapsed))
	}

	fullText := strings.Join(blocks, "\n\n")
	m.viewport.SetContent(fullText)
	if !m.scrolledUp {
		m.viewport.GotoBottom()
	}
}

// View implements tea.Model and renders the complete screen frame.
func (m *tuiModel) View() tea.View {
	contentWidth := max(20, m.width-4)

	var sections []string

	// 1. Top Header
	sections = append(sections, renderHeader(m.workspace))

	// 2. Viewport (Transcript / Empty State)
	sections = append(sections, m.viewport.View())

	// 3. Scroll pill if scrolled up
	if m.scrolledUp {
		sections = append(sections, renderScrollPill(contentWidth))
	}

	// 4. Modal (Question / Approval / Plan Review / Autocomplete)
	if m.activeQuestion != nil {
		sections = append(sections, renderQuestionPanel(m.activeQuestion, contentWidth))
	} else if m.activeApproval != nil {
		sections = append(sections, renderApprovalPanel(m.activeApproval, contentWidth))
	} else if m.activePlanReview != nil {
		sections = append(sections, renderPlanReviewPanel(m.activePlanReview, contentWidth))
	} else if m.autocompleteOpen && len(m.autocompleteItems) > 0 {
		sections = append(sections, renderAutocompletePopup(m.autocompleteItems, m.autocompleteIndex, contentWidth))
	}

	// 5. Composer
	sections = append(sections, renderComposerBand(m.composer.View(), contentWidth))

	// 6. Footer
	sections = append(sections, renderFooter(m, contentWidth))

	content := strings.Join(sections, "\n")

	view := tea.NewView(content)
	view.AltScreen = true
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
