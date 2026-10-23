package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/afterlune/lune/internal/filemention"
	"github.com/afterlune/lune/internal/settings"
	core "github.com/afterlune/luneCore"
)

// AutocompleteMode distinguishes between slash command suggestions and @ file mentions.
type AutocompleteMode int

const (
	AutocompleteModeCommand AutocompleteMode = iota
	AutocompleteModeFile
)

type tuiModel struct {
	client       *core.Core
	runtime      *core.SessionRuntime
	workspace    string
	sessionID    string
	sessionTitle string
	// pendingTitle holds a /title value chosen before the draft session exists.
	pendingTitle        string
	currentModel        string
	currentModelProfile string
	// pendingModelProfile holds a model profile chosen before the draft session
	// exists; it is applied when the first message creates the session.
	pendingModelProfile string
	permissionMode      core.PermissionMode
	planMode            bool
	savedMode           core.PermissionMode
	width               int
	height              int
	viewport            viewport.Model
	entries             []TranscriptEntry
	// Input line, autocomplete, prompt history, selection and copy feedback.
	input                  composerState
	terminalTitle          *terminalTitleManager
	attentionInitialized   bool
	attentionDecisionShown bool
	attentionExecutions    map[string]struct{}
	unseenCompletion       string
	roundsCollapsedByUser  bool
	scrolledUp             bool
	activeQuestion         *ActiveQuestion
	activeApproval         *ActiveApproval
	activePlanReview       *ActivePlanReview
	activeRecovery         *ActiveRecovery
	activeToolRecovery     *ActiveToolRecovery
	pausedCheckpoint       *PausedCheckpoint

	// Task run state: flags, Core task snapshot, queue, streaming buffers.
	run runState

	// Rendered transcript blocks and their stale markers.
	view viewCache

	// Token usage, context window, compaction and generation speed.
	usage sessionUsage

	reasoningEffort string

	agentMetrics map[string]*agentRuntimeMetrics

	// Overlays (shortcuts, pickers, sessions, pager, diff) with exclusive focus.
	modals modalState

	settingsStore *settings.Store
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

	workspace := ""
	permMode := core.PermissionWorkspace
	currentModel := ""
	if client != nil {
		workspace = client.Workspace()
		permMode = client.PermissionMode()
		currentModel = client.ModelForSession(context.Background(), sessionID)
	}
	m := &tuiModel{
		client: client, runtime: runtime, workspace: workspace, sessionID: sessionID,
		currentModel: currentModel, currentModelProfile: currentModel,
		permissionMode: permMode, savedMode: permMode,
		input:    composerState{composer: comp, scanner: filemention.NewScanner(workspace)},
		viewport: vp, entries: []TranscriptEntry{},
		view:         viewCache{transcriptDirty: true},
		run:          newRunState(),
		agentMetrics: map[string]*agentRuntimeMetrics{},
	}
	m.ensureAgentMetrics("lead")
	if client != nil && sessionID != "" {
		if saved, err := client.GetSession(context.Background(), sessionID); err == nil {
			m.loadSessionSnapshot(saved)
		}
	}
	if sessionID == "" {
		m.refreshMainContextWindow()
	}
	m.applyThemeStyles()
	if runtime != nil {
		m.beginSessionRestore()
		m.restoreRuntimeState(context.Background())
		m.checkPendingState(context.Background())
	}
	return m
}

// loadSessionSnapshot restores the session-owned transcript, prompt history, and usage state.
func (m *tuiModel) loadSessionSnapshot(saved core.Session) {
	m.sessionTitle = saved.Title
	if strings.TrimSpace(m.sessionTitle) == "" {
		m.sessionTitle = deriveSessionTitleFromEntries(transcriptFromHistory(saved.History))
	}
	m.currentModelProfile = saved.Settings.ModelProfile
	if strings.TrimSpace(m.currentModelProfile) == "" {
		m.currentModelProfile = m.currentModel
	}
	m.reasoningEffort = saved.Settings.ReasoningEffort
	m.usage.applyTotals(saved.SessionUsage)
	m.usage.compacting = false
	m.usage.compactionStartedAt = time.Time{}
	m.usage.clearLastRequest()
	m.usage.contextWindow = 0
	contextWindowConfigured := false
	if saved.Settings.ContextWindow != nil {
		m.usage.contextWindow = *saved.Settings.ContextWindow
		contextWindowConfigured = true
	} else if m.client != nil {
		if projectContext := m.client.ProjectContextDefaults().ContextWindow; projectContext > 0 {
			m.usage.contextWindow = projectContext
			contextWindowConfigured = true
		}
	}
	if last, ok := latestLeadUsageRecord(saved.UsageRequests); ok {
		window := int64(0)
		if !contextWindowConfigured {
			window = last.ContextWindow
		}
		m.usage.applyLeadRequest(last.Usage.InputTokens, last.Usage.InputTokensDetails.CachedTokens,
			last.EstimatedInputTokens, window, last.RequestID, last.RequestAttempt, last.RequestChanges)
	}
	if m.usage.contextWindow <= 0 {
		m.usage.contextWindow = m.modelProfileContextWindow(m.currentModelProfile)
	}
	m.entries = transcriptFromHistory(saved.History)
	if m.entries == nil {
		m.entries = []TranscriptEntry{}
	}
	m.seedAgentMetrics(saved)
	m.input.history = nil
	for _, entry := range m.entries {
		if entry.Kind == KindUser && entry.Text != "" {
			m.input.history = append(m.input.history, entry.Text)
		}
	}
	m.input.historyIndex = len(m.input.history)
	m.input.historyDraft = ""
	m.view.invalidateBlocks()
	m.refreshViewportContent()
}

func (m *tuiModel) refreshMainContextWindow() {
	m.usage.contextWindow = 0
	if m.client != nil {
		if saved, err := m.client.GetSession(context.Background(), m.sessionID); err == nil && saved.Settings.ContextWindow != nil && *saved.Settings.ContextWindow > 0 {
			m.usage.contextWindow = *saved.Settings.ContextWindow
		}
		if m.usage.contextWindow <= 0 {
			m.usage.contextWindow = m.client.ProjectContextDefaults().ContextWindow
		}
	}
	if m.usage.contextWindow <= 0 {
		m.usage.contextWindow = m.modelProfileContextWindow(m.currentModelProfile)
	}
	if main := m.ensureAgentMetrics("lead"); main != nil {
		main.ContextWindow = m.usage.contextWindow
		main.ModelProfile = m.currentModelProfile
		main.Model = m.currentModel
	}
}

// resetSessionState clears data that belongs to the previous session before a new snapshot is applied.
func (m *tuiModel) resetSessionState() {
	m.input.composer.Placeholder = "Ask Lune to do anything"
	m.sessionTitle = ""
	m.currentModel = ""
	m.currentModelProfile = ""
	m.usage.reset()
	m.run.reset()
	m.reasoningEffort = ""
	m.entries = []TranscriptEntry{}
	m.input.history = nil
	m.input.historyIndex = 0
	m.input.historyDraft = ""
	m.activeQuestion = nil
	m.activeApproval = nil
	m.activePlanReview = nil
	m.modals.effort = nil
	m.activeRecovery = nil
	m.activeToolRecovery = nil
	m.pausedCheckpoint = nil
	m.attentionDecisionShown = false
	m.attentionExecutions = nil
	m.unseenCompletion = ""
	m.usage.lastTokensPerSec = 0
	m.agentMetrics = map[string]*agentRuntimeMetrics{}
	m.ensureAgentMetrics("lead")
	m.scrolledUp = false
	m.input.autocompleteOpen = false
	m.modals.close()
	m.modals.memory.pendingAdd = false
	m.modals.memory.pendingCategory = ""
	m.view.invalidateBlocks()
}

// restoreRuntimeState restores the current execution state for the bound session.
func (m *tuiModel) restoreRuntimeState(ctx context.Context) {
	if m.runtime == nil {
		return
	}
	state, err := m.runtime.State(ctx)
	if err != nil {
		return
	}
	m.restoreRuntimeSnapshot(state)
}

// currentReasoningEffort returns the configured reasoning effort for the current session.
func (m *tuiModel) currentReasoningEffort() string {
	if m.reasoningEffort != "" {
		return m.reasoningEffort
	}
	if m.client != nil && m.sessionID != "" {
		if saved, err := m.client.GetSession(context.Background(), m.sessionID); err == nil {
			return saved.Settings.ReasoningEffort
		}
	}
	return ""
}

// maxParallelAgents returns the maximum configured parallel subagent count.
func (m *tuiModel) maxParallelAgents() int {
	if m.client != nil {
		return m.client.MaxParallelAgents()
	}
	return 0
}

func (m *tuiModel) delegationExecution() core.ExecutionOptions {
	switch m.run.taskSnapshot.Status {
	case core.TaskRunning, core.TaskQueued, core.TaskPaused:
		if m.run.taskSnapshot.TaskID != "" {
			return m.run.taskSnapshot.Execution
		}
	}
	if m.client != nil {
		return m.client.ExecutionDefaults()
	}
	return core.ExecutionOptions{}
}

func (m *tuiModel) delegationEnabled() bool {
	limits := m.delegationExecution()
	return limits.MaxTeamChildrenPerAgent > 0 && limits.MaxTeamDepth > 0
}

func (m *tuiModel) delegationLimitsLabel() string {
	limits := m.delegationExecution()
	status := "enabled"
	if !m.delegationEnabled() {
		status = "disabled"
	}
	return fmt.Sprintf("%s (parallel %d; children per agent %d; depth %d)", status,
		m.maxParallelAgents(), limits.MaxTeamChildrenPerAgent, limits.MaxTeamDepth)
}

func (m *tuiModel) getFileScanner() *filemention.Scanner {
	ws := m.workspace
	if ws == "" && m.client != nil {
		ws = m.client.Workspace()
	}
	return m.input.scannerFor(ws)
}

// currentModelDisplayName returns the user-friendly display name of the active model.
// If displayName is configured in the model profile, it returns displayName;
// otherwise it falls back to model name or profile name.
func (m *tuiModel) currentModelDisplayName() string {
	profile := m.currentModelProfile
	cur := m.currentModel
	if cur == "" && m.client != nil {
		cur = m.client.ModelForSession(context.Background(), m.sessionID)
	}
	// Prefer the exact profile ID; model IDs may be shared by several profiles.
	// Example: both "deepseek-flash" and "anthropic-model" can run "deepseek-flash".
	lookup := strings.TrimSpace(profile)
	if lookup == "" {
		lookup = cur
	}
	if lookup == "" {
		return ""
	}
	if m.client != nil {
		profiles := m.client.ListModelProfiles()
		for _, p := range profiles {
			if strings.EqualFold(p.Profile, lookup) {
				return modelProfileLabel(p)
			}
		}
		for _, p := range profiles {
			if strings.EqualFold(p.Model, lookup) {
				return modelProfileLabel(p)
			}
		}
	}
	return lookup
}

// modelProfileLabel resolves the best display label for a model profile.
func modelProfileLabel(p core.ModelOption) string {
	if strings.TrimSpace(p.DisplayName) != "" {
		return p.DisplayName
	}
	if strings.TrimSpace(p.Model) != "" {
		return p.Model
	}
	return p.Profile
}

// currentReasoningEffortLabel returns the effort level shown next to the model name.
func (m *tuiModel) currentReasoningEffortLabel() string {
	level := strings.TrimSpace(m.reasoningEffort)
	if level == "" {
		return "default"
	}
	return level
}

// desiredTerminalTitle computes the expected window/tab title as
// "<state marker><session title> | <workspace directory name>". The marker keeps
// long-running work visible in the tab or window title while the user is
// looking at another window.
func (m *tuiModel) desiredTerminalTitle() string {
	title := strings.TrimSpace(m.sessionTitle)
	if title == "" {
		title = strings.TrimSpace(deriveSessionTitleFromEntries(m.entries))
	}
	hasTitle := title != "" && title != "<untitled>"
	if !hasTitle {
		title = AppName
	}

	workspaceName := workspaceDirectoryName(m.workspace)
	if workspaceName == "" {
		if !hasTitle {
			return m.terminalStatePrefix() + AppName
		}
		workspaceName = AppName
	}
	return m.terminalStatePrefix() + title + " | " + workspaceName
}

// terminalStatePrefix renders the task state as plain words for the terminal title.
// Decisions take precedence over running work because they block the task.
func (m *tuiModel) terminalStatePrefix() string {
	switch {
	case m.needsUserDecision():
		return "waiting · "
	case m.run.running:
		return "running · "
	case m.usage.compacting:
		return "compacting · "
	case m.hasPausedTask():
		return "paused · "
	}
	return ""
}

func workspaceDirectoryName(workspace string) string {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return ""
	}
	cleaned := filepath.Clean(workspace)
	name := filepath.Base(cleaned)
	if name == "." || name == string(filepath.Separator) || filepath.Dir(cleaned) == cleaned {
		return ""
	}
	return name
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

	m.input.composer.SetStyles(styles)
}

func (m *tuiModel) Init() tea.Cmd {
	return tea.Batch(
		textarea.Blink,
		tea.RequestBackgroundColor,
		m.subscribeRuntimeEvents(),
	)
}
