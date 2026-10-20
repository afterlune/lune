package tui

import (
	"context"
	"fmt"
	"io"
	"os"
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
	client                 *core.Core
	runtime                *core.SessionRuntime
	workspace              string
	sessionID              string
	sessionTitle           string
	currentModel           string
	currentModelProfile    string
	permissionMode         core.PermissionMode
	planMode               bool
	savedMode              core.PermissionMode
	width                  int
	height                 int
	composer               textarea.Model
	viewport               viewport.Model
	entries                []TranscriptEntry
	running                bool
	taskStartedAt          time.Time
	activeQuestion         *ActiveQuestion
	activeApproval         *ActiveApproval
	activePlanReview       *ActivePlanReview
	effortPicker           *ActiveEffortPicker
	activeRecovery         *ActiveRecovery
	activeToolRecovery     *ActiveToolRecovery
	pausedCheckpoint       *PausedCheckpoint
	queuedTurns            []QueuedItem
	autocompleteOpen       bool
	autocompleteMode       AutocompleteMode
	autocompleteItems      []AutocompleteItem
	autocompleteIndex      int
	autocompleteStart      int
	autocompleteEnd        int
	fileScanner            *WorkspaceFileScanner
	scrolledUp             bool
	runningExecution       string
	runtimeCancel          context.CancelFunc
	awaitingTurns          map[string]uint64
	waitGeneration         uint64
	taskSnapshot           core.TaskState
	questionSubmissions    map[questionWaitKey]questionSubmissionState
	terminalEvents         map[string]core.Event
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
	lastInputTokens      int64
	estimatedInputTokens int64
	seededSessionUsage   bool

	reasoningEffort string

	// Streaming and generation diagnostics
	turnStreamStartTime    time.Time
	streamFirstTokenTime   time.Time
	streamGenerationTokens int64
	lastTokensPerSec       float64
	agentMetrics           map[string]*agentRuntimeMetrics
	agentsPanelOpen        bool
	agentsPanelSelected    int
	agentsPanelDetail      bool
	agentsPanelDetailID    string

	// Terminal output writer for escape sequences
	output io.Writer

	// Mouse text selection and copy feedback
	isSelecting            bool
	composerMouseSelecting bool
	selectStartX           int
	selectStartY           int
	selectEndX             int
	selectEndY             int
	selectedText           string
	copyFeedback           string
	copyFeedbackExpiresAt  time.Time
	shortcutsOpen          bool
	sessionsModalOpen      bool
	sessionsItems          []core.Session
	sessionsFiltered       []core.Session
	sessionsFilter         string
	sessionsIndex          int
	sessionsConfirmDelete  bool
	sessionsDeletingID     string

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
		composer: comp, viewport: vp, entries: []TranscriptEntry{},
		fileScanner: NewWorkspaceFileScanner(workspace), reasoningLiveCollapsed: true,
		transcriptDirty: true, output: os.Stdout,
		awaitingTurns: map[string]uint64{}, terminalEvents: map[string]core.Event{},
		agentMetrics: map[string]*agentRuntimeMetrics{},
	}
	m.ensureAgentMetrics("lead")
	if client != nil && sessionID != "" {
		if saved, err := client.GetSession(context.Background(), sessionID); err == nil {
			m.loadSessionSnapshot(saved)
		}
	}
	m.applyThemeStyles()
	if runtime != nil {
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
	m.totalInputTokens = saved.SessionUsage.Input
	m.totalOutputTokens = saved.SessionUsage.Output
	m.cachedTokens = saved.SessionUsage.Cached
	m.reasoningTokens = saved.SessionUsage.Reasoning
	m.seededSessionUsage = saved.SessionUsage.Input > 0 || saved.SessionUsage.Output > 0 || saved.SessionUsage.Cached > 0 || saved.SessionUsage.Reasoning > 0
	m.lastInputTokens = 0
	m.estimatedInputTokens = 0
	m.contextWindow = 0
	contextWindowConfigured := false
	if saved.Settings.ContextWindow != nil {
		m.contextWindow = *saved.Settings.ContextWindow
		contextWindowConfigured = true
	} else if m.client != nil {
		if projectContext := m.client.ProjectContextDefaults().ContextWindow; projectContext > 0 {
			m.contextWindow = projectContext
			contextWindowConfigured = true
		}
	}
	if last, ok := latestLeadUsageRecord(saved.UsageRequests); ok {
		m.lastInputTokens = last.Usage.InputTokens
		m.estimatedInputTokens = last.EstimatedInputTokens
		if !contextWindowConfigured {
			m.contextWindow = last.ContextWindow
		}
	}
	if m.contextWindow <= 0 {
		m.contextWindow = m.modelProfileContextWindow(m.currentModelProfile)
	}
	m.entries = transcriptFromHistory(saved.History)
	if m.entries == nil {
		m.entries = []TranscriptEntry{}
	}
	m.seedAgentMetrics(saved)
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

func (m *tuiModel) refreshMainContextWindow() {
	m.contextWindow = 0
	if m.client != nil {
		if saved, err := m.client.GetSession(context.Background(), m.sessionID); err == nil && saved.Settings.ContextWindow != nil && *saved.Settings.ContextWindow > 0 {
			m.contextWindow = *saved.Settings.ContextWindow
		}
		if m.contextWindow <= 0 {
			m.contextWindow = m.client.ProjectContextDefaults().ContextWindow
		}
	}
	if m.contextWindow <= 0 {
		m.contextWindow = m.modelProfileContextWindow(m.currentModelProfile)
	}
	if main := m.ensureAgentMetrics("lead"); main != nil {
		main.ContextWindow = m.contextWindow
		main.ModelProfile = m.currentModelProfile
		main.Model = m.currentModel
	}
}

// resetSessionState clears data that belongs to the previous session before a new snapshot is applied.
func (m *tuiModel) resetSessionState() {
	m.sessionTitle = ""
	m.currentModel = ""
	m.currentModelProfile = ""
	m.totalInputTokens = 0
	m.totalOutputTokens = 0
	m.cachedTokens = 0
	m.reasoningTokens = 0
	m.contextWindow = 0
	m.lastInputTokens = 0
	m.estimatedInputTokens = 0
	m.seededSessionUsage = false
	m.reasoningEffort = ""
	m.running = false
	m.runningExecution = ""
	m.taskStartedAt = time.Time{}
	m.queuedTurns = nil
	m.entries = []TranscriptEntry{}
	m.promptHistory = nil
	m.historyIndex = 0
	m.historyDraft = ""
	m.activeQuestion = nil
	m.activeApproval = nil
	m.activePlanReview = nil
	m.effortPicker = nil
	m.activeRecovery = nil
	m.activeToolRecovery = nil
	m.pausedCheckpoint = nil
	m.currentStreamText = ""
	m.currentReasoningText = ""
	m.reasoningStartTime = time.Time{}
	m.thinking = false
	m.streamFirstTokenTime = time.Time{}
	m.turnStreamStartTime = time.Time{}
	m.streamGenerationTokens = 0
	m.lastTokensPerSec = 0
	m.agentMetrics = map[string]*agentRuntimeMetrics{}
	m.ensureAgentMetrics("lead")
	m.agentsPanelOpen = false
	m.agentsPanelSelected = 0
	m.agentsPanelDetail = false
	m.agentsPanelDetailID = ""
	m.scrolledUp = false
	m.autocompleteOpen = false
	m.shortcutsOpen = false
	m.sessionsModalOpen = false
	m.sessionsConfirmDelete = false
	m.sessionsDeletingID = ""
	m.toolPagerOpen = false
	m.diffModalOpen = false
	m.awaitingTurns = map[string]uint64{}
	m.taskSnapshot = core.TaskState{}
	m.questionSubmissions = nil
	m.terminalEvents = map[string]core.Event{}
	m.invalidateEntryCache()
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
	switch m.taskSnapshot.Status {
	case core.TaskRunning, core.TaskQueued, core.TaskPaused:
		if m.taskSnapshot.TaskID != "" {
			return m.taskSnapshot.Execution
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

// desiredTerminalTitle computes the expected window/tab title:
// displays "lune" initially/for untitled sessions, and "<title> | lune" when titled.
func (m *tuiModel) desiredTerminalTitle() string {
	title := strings.TrimSpace(m.sessionTitle)
	if title == "" {
		title = strings.TrimSpace(deriveSessionTitleFromEntries(m.entries))
	}
	if title == "" || title == "<untitled>" {
		return AppName
	}
	return title + " | " + AppName
}

// syncTerminalTitle writes the OSC 0/2 escape sequence to update the terminal window/tab title.
func (m *tuiModel) syncTerminalTitle() {
	out := m.output
	if out == nil {
		out = os.Stdout
	}
	_, _ = io.WriteString(out, ansi.SetWindowTitle(m.desiredTerminalTitle()))
}

// updateTerminalTitleCmd returns a Bubbletea command that synchronizes the terminal title.
func (m *tuiModel) updateTerminalTitleCmd() tea.Cmd {
	return func() tea.Msg {
		m.syncTerminalTitle()
		return nil
	}
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
		m.updateTerminalTitleCmd(),
	)
}
