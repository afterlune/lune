package tui

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/afterlune/lune/core"
	"github.com/charmbracelet/colorprofile"
)

func TestTUISessionRailStaysFixedWithConversationLength(t *testing.T) {
	for _, theme := range []themeName{themeDark, themeLight} {
		t.Run(string(theme), func(t *testing.T) {
			short := newSessionWorkspaceFixture(theme, "short")
			long := newSessionWorkspaceFixture(theme, "long")
			if !short.wideConversation() || !long.wideConversation() {
				t.Fatal("120x30 fixture should use the session workspace layout")
			}
			if short.chat.Width() != long.chat.Width() || short.chat.Height() != long.chat.Height() {
				t.Fatalf("chat viewport moved with transcript length: short=%dx%d long=%dx%d",
					short.chat.Width(), short.chat.Height(), long.chat.Width(), long.chat.Height())
			}
			if !reflect.DeepEqual(sessionRailLines(short), sessionRailLines(long)) {
				t.Fatalf("session rail changed with transcript length:\nshort: %#v\nlong: %#v", sessionRailLines(short), sessionRailLines(long))
			}
			assertSessionRailSeparatorColumn(t, short)
			assertSessionRailSeparatorColumn(t, long)
		})
	}
}

func TestTUIActivityDrawerScrollDoesNotMoveTranscriptOrRail(t *testing.T) {
	for _, theme := range []themeName{themeDark, themeLight} {
		t.Run(string(theme), func(t *testing.T) {
			top := newSessionWorkspaceFixture(theme, "activity-top")
			bottom := newSessionWorkspaceFixture(theme, "activity-bottom")
			if top.activity.TotalLineCount() <= top.activity.Height() {
				t.Fatalf("drawer lines=%d, viewport height=%d; want overflow", top.activity.TotalLineCount(), top.activity.Height())
			}
			if !top.activity.AtTop() || !bottom.activity.AtBottom() {
				t.Fatalf("drawer scroll positions: top=%v bottom=%v", top.activity.AtTop(), bottom.activity.AtBottom())
			}
			if !reflect.DeepEqual(sessionRailLines(top), sessionRailLines(bottom)) {
				t.Fatal("scrolling the Activity/Usage drawer moved the session rail")
			}
			if !strings.Contains(plainTUIView(bottom), "Session ·") {
				t.Fatal("Usage totals are not available at the bottom of the drawer")
			}

			scrolling := newSessionWorkspaceFixture(theme, "activity-top")
			chatOffset, activityOffset := scrolling.chat.YOffset(), scrolling.activity.YOffset()
			scrolling.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
			if scrolling.activity.YOffset() <= activityOffset {
				t.Fatal("Down did not scroll the Activity/Usage drawer")
			}
			if scrolling.chat.YOffset() != chatOffset {
				t.Fatal("scrolling the Activity/Usage drawer also moved the transcript")
			}
			if strings.Index(plainTUIView(top), "Activity & usage") != strings.Index(plainTUIView(scrolling), "Activity & usage") {
				t.Fatal("Activity/Usage drawer heading moved while scrolling its contents")
			}
		})
	}
}

func TestTUIModernWorkspaceChromeUsesRegularWeight(t *testing.T) {
	for _, theme := range []themeName{themeDark, themeLight} {
		t.Run(string(theme), func(t *testing.T) {
			ui := newSessionWorkspaceFixture(theme, "activity-top")
			bodyHeight := ui.availableBodyHeight(ui.width, 1, 2)
			parts := []string{
				ui.renderHeader(ui.width),
				ui.renderSessionRail(ui.sessionRailWidth(), bodyHeight),
				ui.activityDrawerHeading(),
				ui.activityDetailsContent(),
			}
			narrow := newSessionWorkspaceFixture(theme, "narrow")
			narrow.detailsOpen = true
			narrow.updateLayout()
			parts = append(parts, narrow.renderHeader(narrow.width), narrow.renderBody(narrow.width))
			for _, part := range parts {
				for _, line := range parseTUIScreenshotLines(part, colorsFor(theme).Foreground) {
					for _, run := range line {
						if run.bold {
							t.Fatalf("modern workspace chrome contains bold text %q", run.text)
						}
					}
				}
			}
		})
	}
}

func TestTUITeamActivityGroupsAndUpdatesMemberState(t *testing.T) {
	ui := newTUIModel(&fakeResponder{}, false)
	ui.preferences.Theme = themeDark
	ui.sessionID = "session-current"
	ui.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	for _, event := range []core.Event{
		{Kind: core.EventAgentStarted, SessionID: ui.sessionID, TeamRunID: "team-run-1", AgentID: "research", AgentName: "Research", AgentRole: "worker", ModelProfile: "review", Provider: "deepseek", Model: "deepseek-worker", Status: core.TaskRunning},
		{Kind: core.EventAgentStarted, SessionID: ui.sessionID, TeamRunID: "team-run-1", AgentID: "writer", AgentName: "Writer", AgentRole: "worker", ModelProfile: "text", Provider: "anthropic", Model: "claude-writer", Status: core.TaskRunning},
	} {
		ui.Update(coreEventMsg{event: event})
	}
	details := tuiScreenshotANSI.ReplaceAllString(ui.activityDetailsContent(), "")
	for _, expected := range []string{"Team 1", "Research · running", "profile=review", "deepseek/deepseek-worker", "Writer · running", "anthropic/claude-writer"} {
		if !strings.Contains(details, expected) {
			t.Fatalf("team details missing %q: %q", expected, details)
		}
	}
	if strings.Contains(details, "Team 2") {
		t.Fatalf("members from one team run were split into multiple groups: %q", details)
	}

	ui.Update(coreEventMsg{event: core.Event{
		Kind: core.EventAgentFinished, SessionID: ui.sessionID, TeamRunID: "team-run-1",
		AgentID: "research", AgentName: "Research", AgentRole: "worker", Status: core.TaskCompleted,
	}})
	ui.Update(coreEventMsg{event: core.Event{
		Kind: core.EventAgentStarted, SessionID: "other-session", TeamRunID: "team-run-2",
		AgentID: "ignored", AgentName: "Ignored", AgentRole: "worker", Status: core.TaskRunning,
	}})
	ui.Update(coreEventMsg{event: core.Event{
		Kind: core.EventAgentStarted, SessionID: ui.sessionID, TeamRunID: "team-run-2",
		AgentID: "second", AgentName: "Second team", AgentRole: "worker", Status: core.TaskRunning,
	}})
	details = tuiScreenshotANSI.ReplaceAllString(ui.activityDetailsContent(), "")
	if !strings.Contains(details, "Research · done") || !strings.Contains(details, "Team 2") || !strings.Contains(details, "Second team · running") || strings.Contains(details, "Ignored") {
		t.Fatalf("team status update/session filtering failed: %q", details)
	}
}

func TestTUISessionRailClickSwitchesSession(t *testing.T) {
	first := core.Session{ID: "session-1", Title: "Current configuration", History: jsonNull()}
	second := core.Session{ID: "session-2", Title: "Review provider flow", History: jsonNull()}
	client := &fakeResponder{sessions: map[string]core.Session{first.ID: first, second.ID: second}, last: first.ID}
	ui := newTUIModel(client, false)
	ui.preferences.Theme = themeDark
	ui.workspace = client.Workspace()
	ui.sessionID = first.ID
	ui.sessionList = []core.Session{first, second}
	ui.sessionIndex = 0
	ui.Update(tea.WindowSizeMsg{Width: 120, Height: 30})

	bodyHeight := ui.availableBodyHeight(120, 1, 2)
	start, end := ui.sessionRailRange(bodyHeight)
	if end-start < 2 {
		t.Fatal("session rail should display both test sessions")
	}
	clickY := lipgloss.Height(ui.renderHeader(ui.width)) + 1 + sessionRailHeaderRows + (1 - start)
	updated, command := ui.Update(tea.MouseClickMsg{X: 1, Y: clickY, Button: tea.MouseLeft})
	if command != nil {
		t.Fatal("restoring an existing session should not start a background command")
	}
	if got := updated.(*tuiModel).sessionID; got != second.ID {
		t.Fatalf("active session = %q, want %q after clicking the rail", got, second.ID)
	}
}

func TestTUISessionRailCollapsesOnNarrowTerminal(t *testing.T) {
	ui := newSessionWorkspaceFixture(themeDark, "narrow")
	if ui.wideConversation() {
		t.Fatal("80-column fixture should collapse the session rail")
	}
	view := plainTUIView(ui)
	if lipgloss.Width(view) > 80 || lipgloss.Height(view) > 24 {
		t.Fatalf("narrow workspace exceeds terminal size: %dx%d", lipgloss.Width(view), lipgloss.Height(view))
	}
	if strings.Contains(view, "Sessions") || !strings.Contains(view, "‹ Provider configuration ›") || !strings.Contains(view, "/resume sessions") {
		t.Fatalf("narrow workspace should use the header session switcher: %q", view)
	}
	updated, command := ui.Update(tea.MouseClickMsg{X: 12, Y: 0, Button: tea.MouseLeft})
	if command != nil || !updated.(*tuiModel).sessionPicker {
		t.Fatal("clicking the narrow header switcher should open session selection")
	}
}

func TestTUIChatScrollReturnsToBottomAndResumesFollowing(t *testing.T) {
	ui := newTUIModel(&fakeResponder{}, false)
	ui.preferences.Theme = themeDark
	ui.sessionID = "session-current"
	ui.running = true
	ui.status = "Working… Press Esc to interrupt."
	ui.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	ui.entries = []tuiEntry{{role: "assistant", text: strings.TrimSuffix(strings.Repeat("Output line keeps the transcript scrollable.\n", 60), "\n")}}
	ui.refreshPanels()
	if ui.chat.TotalLineCount() <= ui.chat.Height() || !ui.chat.AtBottom() {
		t.Fatalf("initial chat lines=%d height=%d atBottom=%v", ui.chat.TotalLineCount(), ui.chat.Height(), ui.chat.AtBottom())
	}

	ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyPgUp}))
	if ui.chat.AtBottom() {
		t.Fatal("PageUp did not move away from the latest message")
	}
	offset := ui.chat.YOffset()
	ui.entries[0].text += "\nNew streamed line while reading history."
	ui.refreshPanels()
	if ui.chat.AtBottom() || ui.chat.YOffset() != offset {
		t.Fatalf("new output moved a reader from history: bottom=%v offset=%d want=%d", ui.chat.AtBottom(), ui.chat.YOffset(), offset)
	}
	if !strings.Contains(tuiScreenshotANSI.ReplaceAllString(ui.renderFooter(ui.chat.Width()), ""), "back to latest") {
		t.Fatal("scroll-away hint is missing")
	}

	ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnd, Mod: tea.ModAlt}))
	if !ui.chat.AtBottom() {
		t.Fatal("Alt+End did not return to the latest message")
	}
	if strings.Contains(tuiScreenshotANSI.ReplaceAllString(ui.renderFooter(ui.chat.Width()), ""), "back to latest") {
		t.Fatal("scroll-away hint remains after returning to the bottom")
	}
	ui.entries[0].text += "\nNew streamed line after returning."
	ui.refreshPanels()
	if !ui.chat.AtBottom() {
		t.Fatal("new output did not resume auto-follow after returning to the bottom")
	}
}

func TestTUISessionWorkspaceSVGsMatchDeterministicFixtures(t *testing.T) {
	scenarios := []string{"empty", "short", "long", "activity-top", "activity-bottom", "usage-session-top", "usage-session-bottom", "usage-estimated", "narrow", "streaming", "interrupted", "resumed"}
	for _, theme := range []themeName{themeDark, themeLight} {
		for _, scenario := range scenarios {
			theme, scenario := theme, scenario
			name := fmt.Sprintf("%s-%s.svg", theme, scenario)
			t.Run(name, func(t *testing.T) {
				ui := newSessionWorkspaceFixture(theme, scenario)
				got := renderTUIScreenshotSVG(ui.View().Content, ui.width, ui.height, theme)
				assertTUIScreenshotSVG(t, name, got)
			})
		}
	}
}

func TestTUIAnimeThemeSVGsMatchDeterministicFixtures(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	ui := newSessionWorkspaceFixture(themeAnime, "short")
	got := renderTUIScreenshotSVG(ui.View().Content, ui.width, ui.height, themeAnime)
	assertTUIScreenshotSVG(t, "anime-short.svg", got)
}

func newSessionWorkspaceFixture(theme themeName, scenario string) *tuiModel {
	width, height := 120, 30
	if scenario == "narrow" {
		width, height = 80, 24
	}
	ui := newTUIModel(&fakeResponder{}, false)
	ui.preferences.Theme = theme
	ui.colorProfile = colorprofile.TrueColor
	ui.sessionID = "session-current"
	ui.workspace = "D:/work/project"
	ui.sessionList = workspaceFixtureSessions()
	ui.sessionIndex = 0
	applyTextareaTheme(&ui.input, theme)
	ui.Update(tea.WindowSizeMsg{Width: width, Height: height})

	switch scenario {
	case "short":
		ui.entries = []tuiEntry{
			{role: "you", text: "Please review the config loading."},
			{role: "assistant", text: "The merge order is clear and predictable."},
		}
		ui.activities = workspaceFixtureActivities(5)
		ui.sessionUsage = measuredWorkspaceUsage()
	case "long":
		ui.entries = []tuiEntry{
			{role: "you", text: "Please review the config loading."},
			{role: "assistant", text: "I reviewed defaults and project settings across the config flow.\nThe environment overrides are applied after file values.\nProvider fields and optional settings are validated before use.\nThe longer response fills the chat viewport without moving the session rail.\nActivity and Usage open in the bottom drawer."},
		}
		ui.activities = workspaceFixtureActivities(5)
		ui.sessionUsage = measuredWorkspaceUsage()
	case "activity-top", "activity-bottom", "usage-session-top", "usage-session-bottom", "usage-estimated":
		ui.entries = []tuiEntry{{role: "you", text: "Review the recent activity."}, {role: "assistant", text: "Tool events stay in order in the work log."}}
		ui.activities = workspaceFixtureActivities(28)
		ui.teamActivities = workspaceFixtureTeamActivities()
		ui.sessionUsage = measuredWorkspaceUsage()
		ui.usageRequests = workspaceFixtureUsageRequests()
		if scenario == "usage-session-top" || scenario == "usage-session-bottom" {
			ui.sessionUsage = tokenUsageTotals{
				requests: 247, measured: 246, input: 41000, output: 8500, reasoning: 1000, total: 49500, cached: 6000,
			}
			ui.agentUsage = map[string]agentUsageSummary{
				"lead":     {requests: 205, measured: 204, input: 32000, output: 8000, reasoning: 1000, total: 40000, cached: 6000},
				"research": {requests: 42, measured: 42, input: 9000, output: 500, total: 9500},
			}
			ui.agentUsageComplete = true
		}
		ui.contextWindow = 64000
		ui.contextInput = 840
		if scenario == "usage-estimated" {
			ui.contextInput = 12300
			ui.contextEstimated = true
			ui.usageRequests = []tokenUsageRecord{{
				request: 5, usage: core.Usage{OutputTokens: 600, TotalTokens: 600}, available: true,
				agentID: "lead", profile: "lead", provider: "deepseek", model: "deepseek-main",
				contextWindow: 64000, estimatedInputTokens: 12300,
			}}
			ui.agentUsage = map[string]agentUsageSummary{
				"lead": {requests: 5, measured: 5, estimatedInput: 12300, output: 2400, total: 48000},
			}
			ui.sessionUsage = tokenUsageTotals{requests: 5, measured: 5, estimatedInput: 12300, output: 2400, total: 48000}
			ui.taskUsage = tokenUsageTotals{requests: 1, measured: 1, estimatedInput: 12300, output: 600, total: 12000}
		}
		ui.detailsOpen = true
	case "narrow":
		ui.entries = []tuiEntry{
			{role: "you", text: "Review the current task."},
			{role: "assistant", text: "The session rail becomes a compact header switcher."},
		}
		ui.activities = workspaceFixtureActivities(8)
		ui.sessionUsage = measuredWorkspaceUsage()
	case "streaming", "interrupted", "resumed":
		ui.running = true
		ui.status = "Working… Press Esc to interrupt."
		ui.entries = []tuiEntry{{role: "you", text: "Explain the active provider stream."}}
		ui.Update(coreEventMsg{event: core.Event{
			Kind: core.EventTaskStarted, SessionID: ui.sessionID, TaskID: "task-stream", Status: core.TaskRunning,
		}})
		ui.Update(coreEventMsg{event: core.Event{
			Kind: core.EventModelStream, SessionID: ui.sessionID, TaskID: "task-stream", AgentID: "lead",
			Stream: &core.ModelStreamEvent{Type: "text_delta", ItemID: "response-stream", Delta: "The response appears as text deltas arrive, keeping the workspace layout steady."},
		}})
		ui.flushModelStreamEvents()
		if scenario == "interrupted" {
			ui.finishModelStream("", false)
			ui.running = false
			ui.status = "Task paused. Use /continue to resume or type a new message to branch."
		} else if scenario == "resumed" {
			ui.finishModelStream("", false)
			ui.running = true
			ui.status = "Working… Press Esc to interrupt."
			ui.Update(coreEventMsg{event: core.Event{
				Kind: core.EventTaskStarted, SessionID: ui.sessionID, TaskID: "task-resumed", Status: core.TaskRunning,
			}})
			ui.Update(coreEventMsg{event: core.Event{
				Kind: core.EventModelStream, SessionID: ui.sessionID, TaskID: "task-resumed", AgentID: "lead",
				Stream: &core.ModelStreamEvent{Type: "text_delta", ItemID: "response-resumed", Delta: "The full answer appears after resuming the interrupted turn."},
			}})
			ui.flushModelStreamEvents()
			ui.Update(coreEventMsg{event: core.Event{
				Kind: core.EventModelStream, SessionID: ui.sessionID, TaskID: "task-resumed", AgentID: "lead",
				Stream: &core.ModelStreamEvent{Type: "completed", ItemID: "response-resumed"},
			}})
			ui.flushModelStreamEvents()
			ui.Update(coreEventMsg{event: core.Event{
				Kind: core.EventTaskFinished, SessionID: ui.sessionID, TaskID: "task-resumed", Status: core.TaskCompleted,
			}})
			ui.finishModelStream("The full answer appears after resuming the interrupted turn.", true)
			ui.running = false
			ui.status = "Completed."
		}
	case "empty":
	default:
		panic("unknown session workspace fixture: " + scenario)
	}

	if ui.modelStreamFirstTokenRecorded {
		ui.modelStreamFirstTokenLatency = 240 * time.Millisecond
	}
	ui.refreshPanels()
	switch scenario {
	case "activity-top":
		ui.activity.GotoTop()
	case "activity-bottom", "usage-session-bottom":
		ui.activity.GotoBottom()
	case "usage-session-top":
		ui.activity.GotoTop()
	case "usage-estimated":
		ui.activity.GotoBottom()
	}
	return ui
}

func workspaceFixtureSessions() []core.Session {
	return []core.Session{
		{ID: "session-current", Title: "Provider configuration"},
		{ID: "session-search", Title: "Add search index"},
		{ID: "session-tests", Title: "Fix CI failure"},
		{ID: "session-docs", Title: "Polish documentation"},
	}
}

func workspaceFixtureActivities(count int) []tuiActivity {
	activities := make([]tuiActivity, count)
	for index := range activities {
		status := "done"
		if index == count-1 {
			status = "running"
		}
		activities[index] = tuiActivity{
			callID:  fmt.Sprintf("call-%02d", index+1),
			summary: fmt.Sprintf("Reading internal/config/section-%02d/provider-settings.go", index+1),
			status:  status,
		}
	}
	return activities
}

func workspaceFixtureTeamActivities() []tuiTeamActivity {
	return []tuiTeamActivity{
		{teamRunID: "fixture-team-1", agentID: "research", name: "Research", profile: "review", provider: "deepseek", model: "deepseek-worker", status: "done"},
		{teamRunID: "fixture-team-1", agentID: "reviewer", name: "Reviewer", profile: "review", provider: "anthropic", model: "claude-review", status: "running"},
	}
}

func measuredWorkspaceUsage() tokenUsageTotals {
	return tokenUsageTotals{requests: 4, measured: 4, input: 1200, output: 300, reasoning: 600, total: 2100, cached: 400}
}

func workspaceFixtureUsageRequests() []tokenUsageRecord {
	return []tokenUsageRecord{
		{
			request: 3, available: true, agentID: "lead", profile: "lead", provider: "deepseek", model: "deepseek-main",
			usage: core.Usage{InputTokens: 840, OutputTokens: 210, TotalTokens: 1050, InputTokensDetails: core.TokenInputDetails{CachedTokens: 240}},
		},
		{
			request: 4, available: true, agentID: "research", profile: "review", provider: "anthropic", model: "claude-review",
			usage: core.Usage{InputTokens: 360, OutputTokens: 90, TotalTokens: 450, InputTokensDetails: core.TokenInputDetails{CachedTokens: 160}},
		},
	}
}

func sessionRailLines(ui *tuiModel) []string {
	plain := plainTUIView(ui)
	lines := strings.Split(strings.TrimRight(plain, "\n"), "\n")
	start := lipgloss.Height(ui.renderHeader(max(1, ui.width))) + 1
	bodyHeight := ui.availableBodyHeight(max(1, ui.width), 1, 2)
	rail := make([]string, bodyHeight)
	for row := range rail {
		if start+row < len(lines) {
			rail[row] = terminalCellSlice(lines[start+row], 0, ui.sessionRailWidth())
		}
	}
	return rail
}

func assertSessionRailSeparatorColumn(t *testing.T, ui *tuiModel) {
	t.Helper()
	lines := strings.Split(strings.TrimRight(plainTUIView(ui), "\n"), "\n")
	bodyStart := lipgloss.Height(ui.renderHeader(max(1, ui.width))) + 1
	bodyHeight := ui.availableBodyHeight(max(1, ui.width), 1, 2)
	separatorColumn := ui.sessionRailWidth()
	for row := bodyStart; row < bodyStart+bodyHeight; row++ {
		if row >= len(lines) {
			t.Fatalf("workspace body row %d is missing from view with %d lines", row, len(lines))
		}
		if got := terminalCellSlice(lines[row], separatorColumn, 1); got != "│" {
			t.Fatalf("session rail separator moved at row %d: got %q at column %d", row, got, separatorColumn)
		}
	}
}

func plainTUIView(ui *tuiModel) string {
	return tuiScreenshotANSI.ReplaceAllString(ui.View().Content, "")
}

func terminalCellSlice(line string, start, width int) string {
	var output strings.Builder
	column := 0
	for _, character := range line {
		characterWidth := lipgloss.Width(string(character))
		if column >= start && column < start+width {
			output.WriteRune(character)
		}
		column += characterWidth
	}
	return strings.TrimRight(output.String(), " ")
}

func jsonNull() json.RawMessage { return json.RawMessage("null") }
