package tui

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	core "github.com/afterlune/luneCore"
)

type fakeTUICompactor struct {
	fakeResponder
	compactedTurns int
	compactErr     error
	compactCalls   int
}

func (f *fakeTUICompactor) Compact(context.Context, string) (int, error) {
	f.compactCalls++
	return f.compactedTurns, f.compactErr
}

func TestTUISlashCommandSuggestionsAndUnknownCommand(t *testing.T) {
	client := &fakeResponder{}
	ui := newTUIModel(client, false)
	ui.input.SetValue("/")
	ui.updateCommandSuggestions()
	if len(ui.commandSuggestions) != len(tuiCommands)-1 {
		t.Fatalf("suggestions = %#v, want the existing bare-slash menu size", ui.commandSuggestions)
	}
	if !strings.Contains(strings.Join(ui.commandSuggestions, " "), "/plan") ||
		!strings.Contains(strings.Join(ui.commandSuggestions, " "), "/details") ||
		!strings.Contains(strings.Join(ui.commandSuggestions, " "), "/goal") {
		t.Fatalf("suggestions = %#v, want /plan, /details, and /goal", ui.commandSuggestions)
	}
	ui.input.SetValue("/h")
	ui.updateCommandSuggestions()
	if !strings.Contains(strings.Join(ui.commandSuggestions, " "), "/help") {
		t.Fatalf("suggestions = %#v, want /help for the /h prefix", ui.commandSuggestions)
	}
	ui.input.SetValue("/")
	ui.updateCommandSuggestions()
	if !strings.Contains(ui.View().Content, "/compact") {
		t.Fatalf("view = %q, want command suggestions", ui.View().Content)
	}
	ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	if ui.suggestionIndex != 1 {
		t.Fatalf("selected suggestion = %d, want index 1", ui.suggestionIndex)
	}
	wantCompletion := ui.commandSuggestions[ui.suggestionIndex]
	ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
	if ui.input.Value() != wantCompletion {
		t.Fatalf("Tab completion = %q, want %q", ui.input.Value(), wantCompletion)
	}

	ui.input.SetValue("/not-a-command")
	ui.updateCommandSuggestions()
	ui.submit()
	if client.responses != 0 || len(client.inputs) != 0 {
		t.Fatalf("unknown command was sent as a prompt: responses=%d inputs=%#v", client.responses, client.inputs)
	}
	if !strings.Contains(ui.entries[len(ui.entries)-1].text, "Unknown command") {
		t.Fatalf("last entry = %#v, want unknown-command error", ui.entries[len(ui.entries)-1])
	}
	if !strings.Contains(ui.entries[len(ui.entries)-1].text, "/help") {
		t.Fatalf("unknown-command entry = %#v, want /help in the command list", ui.entries[len(ui.entries)-1])
	}
}

func TestTUIHelpCommandShowsCommandsWithoutStartingTask(t *testing.T) {
	client := &fakeResponder{}
	ui := newTUIModel(client, false)
	ui.input.SetValue("/help")

	updated, command := ui.submit()
	finished := updated.(*tuiModel)
	if command != nil {
		t.Fatalf("/help returned a command: %v", command)
	}
	if client.responses != 0 || len(client.inputs) != 0 {
		t.Fatalf("/help started a task: responses=%d inputs=%#v", client.responses, client.inputs)
	}
	if finished.input.Value() != "" {
		t.Fatalf("input after /help = %q, want cleared input", finished.input.Value())
	}
	if len(finished.entries) == 0 {
		t.Fatal("/help did not add a help entry")
	}
	help := finished.entries[len(finished.entries)-1]
	if help.role != "notice" || help.err {
		t.Fatalf("help entry = %#v, want a non-error notice", help)
	}
	for _, expected := range []string{
		"Available commands:",
		"/autocompact [on|off|threshold|reset]",
		"/context [window <tokens>|reserve <tokens>|reset]",
		"/details [n]",
		"/goal [text|clear]",
		"/help",
		"/keymap [reset]",
		"/rewind <n>",
		"Use /keymap to view or change keyboard shortcuts.",
	} {
		if !strings.Contains(help.text, expected) {
			t.Errorf("help text %q is missing %q", help.text, expected)
		}
	}
}

func TestTUIModelAndEffortPickerCommandsPersistSessionSettings(t *testing.T) {
	client := &fakeResponder{sessions: map[string]core.Session{
		"session-1": {ID: "session-1", Settings: core.SessionSettings{ModelProfile: "lead"}},
	}, last: "session-1"}
	ui := newTUIModel(client, false)
	ui.sessionID = "session-1"
	ui.sessionSettings = core.SessionSettings{ModelProfile: "lead"}
	ui.input.SetValue("/")
	ui.updateCommandSuggestions()
	if !strings.Contains(strings.Join(ui.commandSuggestions, " "), "/model") || !strings.Contains(strings.Join(ui.commandSuggestions, " "), "/effort") {
		t.Fatalf("suggestions = %#v, want model and effort commands", ui.commandSuggestions)
	}

	ui.input.SetValue("/model")
	ui.submit()
	if !ui.modelPicker || !strings.Contains(ui.View().Content, "review-model") {
		t.Fatalf("model picker did not show configured profiles: %q", ui.View().Content)
	}
	ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if ui.modelPicker || ui.sessionSettings.ModelProfile != "review" {
		t.Fatalf("model selection = %#v, picker open=%v", ui.sessionSettings, ui.modelPicker)
	}
	if saved := client.sessions[ui.sessionID]; saved.Settings.ModelProfile != "review" {
		t.Fatalf("persisted model profile = %#v", saved.Settings)
	}

	ui.input.SetValue("/effort high")
	ui.submit()
	if ui.sessionSettings.ReasoningEffort != "high" {
		t.Fatalf("direct effort command settings = %#v", ui.sessionSettings)
	}
	ui.input.SetValue("/effort")
	ui.submit()
	if !ui.effortPicker {
		t.Fatal("bare /effort should open the effort picker")
	}
	for ui.effortIndex > 0 {
		ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}))
	}
	ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if ui.effortPicker || ui.sessionSettings.ReasoningEffort != "" {
		t.Fatalf("default effort selection = %#v, picker open=%v", ui.sessionSettings, ui.effortPicker)
	}
	if saved := client.sessions[ui.sessionID].Settings; saved.ModelProfile != "review" || saved.ReasoningEffort != "" {
		t.Fatalf("persisted default effort settings = %#v", saved)
	}
	footer := ui.renderFooter(120)
	if !strings.Contains(footer, "review") || !strings.Contains(footer, "Default") ||
		!strings.Contains(footer, "Reasoning effort set to Default") || strings.Contains(footer, "tokens —") {
		t.Fatalf("footer should separate settings, status, and available usage: %q", footer)
	}
}

func TestTUIGoalCommandShowsPersistsAndClearsSessionGoal(t *testing.T) {
	client := &fakeResponder{sessions: map[string]core.Session{
		"session-1": {ID: "session-1", Settings: core.SessionSettings{ModelProfile: "lead"}},
	}, last: "session-1"}
	ui := newTUIModel(client, false)
	ui.sessionID = "session-1"
	ui.sessionSettings = core.SessionSettings{ModelProfile: "lead"}

	ui.input.SetValue("/goal")
	ui.submit()
	if len(ui.entries) == 0 || !strings.Contains(ui.entries[len(ui.entries)-1].text, "No task goal is set") {
		t.Fatalf("bare /goal output = %#v", ui.entries)
	}

	ui.input.SetValue("/goal Keep the change focused on observable user behavior.")
	ui.submit()
	if got := ui.sessionSettings.Goal; got != "Keep the change focused on observable user behavior." {
		t.Fatalf("session goal = %q", got)
	}
	if got := client.sessions[ui.sessionID].Settings.Goal; got != ui.sessionSettings.Goal {
		t.Fatalf("persisted goal = %q, want %q", got, ui.sessionSettings.Goal)
	}
	ui.input.SetValue("/goal")
	ui.submit()
	if got := ui.entries[len(ui.entries)-1].text; !strings.Contains(got, ui.sessionSettings.Goal) {
		t.Fatalf("goal display = %q, want saved goal", got)
	}

	ui.pending = true
	ui.input.SetValue("/goal clear")
	ui.submit()
	if ui.sessionSettings.Goal != "" || client.sessions[ui.sessionID].Settings.Goal != "" {
		t.Fatalf("goal was not cleared: session=%#v persisted=%#v", ui.sessionSettings, client.sessions[ui.sessionID].Settings)
	}
	if !strings.Contains(ui.status, "paused task keeps its original goal") {
		t.Fatalf("status = %q, want paused-task behavior", ui.status)
	}
}

func TestTUIShiftTabCyclesPermissionModesAndKeepsDraft(t *testing.T) {
	client := &fakeResponder{}
	ui := newTUIModel(client, false)
	if client.PermissionMode() != core.PermissionWorkspace {
		t.Fatalf("initial mode = %q, want workspace", client.PermissionMode())
	}
	ui.input.SetValue("keep this draft")

	for _, mode := range []core.PermissionMode{
		core.PermissionAsk,
		core.PermissionFullAccess,
		core.PermissionReadOnly,
		core.PermissionWorkspace,
	} {
		ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab, Mod: tea.ModShift}))
		if got := client.PermissionMode(); got != mode {
			t.Fatalf("mode = %q, want %q", got, mode)
		}
		if ui.input.Value() != "keep this draft" {
			t.Fatalf("mode switch cleared input draft: %q", ui.input.Value())
		}
	}
	if !strings.Contains(ui.renderFooter(200), "Workspace") || ui.status != "Ready" || len(ui.entries) != 0 {
		t.Fatalf("mode should stay in footer without adding transcript details: footer=%q status=%q entries=%#v", ui.renderFooter(200), ui.status, ui.entries)
	}

	ui.running = true
	ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab, Mod: tea.ModShift}))
	if client.PermissionMode() != core.PermissionWorkspace {
		t.Fatalf("mode changed while task was running: %q", client.PermissionMode())
	}
}

func TestTUIPlanCommandLocksAndRestoresPermissionMode(t *testing.T) {
	client := &fakeResponder{}
	ui := newTUIModel(client, false)
	initialPermissionMode := client.PermissionMode()

	ui.input.SetValue("/plan")
	updated, command := ui.update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if command != nil {
		t.Fatal("/plan should toggle locally without starting a model request")
	}
	finished := updated.(*tuiModel)
	if !finished.planMode {
		t.Fatalf("plan mode=%v, want enabled", finished.planMode)
	}
	if finished.currentPermissionMode() != core.PermissionReadOnly || client.PermissionMode() != core.PermissionReadOnly || finished.permissionModeBeforePlan != initialPermissionMode {
		t.Fatalf("Plan permission state = active:%q core:%q saved:%q, want read-only with %q saved", finished.currentPermissionMode(), client.PermissionMode(), finished.permissionModeBeforePlan, initialPermissionMode)
	}
	if finished.input.Value() != "" {
		t.Fatalf("/plan input = %q, want cleared", finished.input.Value())
	}

	finished.togglePlanMode()
	if finished.planMode {
		t.Fatalf("second /plan should return to build mode: plan=%v", finished.planMode)
	}
	if finished.currentPermissionMode() != initialPermissionMode || client.PermissionMode() != initialPermissionMode {
		t.Fatalf("permission after Plan exit = active:%q core:%q, want %q", finished.currentPermissionMode(), client.PermissionMode(), initialPermissionMode)
	}
}

func TestTUIPlanModeBlocksPermissionChanges(t *testing.T) {
	client := &fakeResponder{}
	ui := newTUIModel(client, false)
	if err := ui.setPlanMode(true); err != nil {
		t.Fatal(err)
	}

	footer := ui.renderFooter(200)
	if !ui.planMode || client.PermissionMode() != core.PermissionReadOnly {
		t.Fatalf("Plan and permission state = %v/%q, want Plan with Read-only", ui.planMode, client.PermissionMode())
	}
	if !strings.Contains(footer, "/plan") || !strings.Contains(footer, "Read-only") || !strings.Contains(footer, "permissions locked") || strings.Contains(footer, "modes") {
		t.Fatalf("compact footer should show the locked Plan permission: %q", footer)
	}
	_, _ = ui.openPermissionMenu()
	if ui.permissionMenu || !strings.Contains(ui.status, "locked to Read-only") {
		t.Fatalf("permission menu opened during Plan mode: open=%v status=%q", ui.permissionMenu, ui.status)
	}
	if menu := ui.renderPermissionMenu(); !strings.Contains(menu, "Read-only (locked by Plan)") {
		t.Fatalf("permission menu view omitted the Plan lock: %q", menu)
	}

	ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab, Mod: tea.ModShift}))
	if !ui.planMode || ui.currentPermissionMode() != core.PermissionReadOnly || client.PermissionMode() != core.PermissionReadOnly {
		t.Fatalf("Plan/permission state after Shift+Tab: plan=%v active=%q core=%q", ui.planMode, ui.currentPermissionMode(), client.PermissionMode())
	}
	ui.applyPermissionMode(core.PermissionFullAccess)
	if !ui.planMode || client.PermissionMode() != core.PermissionReadOnly {
		t.Fatalf("/permissions changed the active Plan permission: plan=%v core=%q", ui.planMode, client.PermissionMode())
	}
}

func TestTUIPlanModeDoesNotEnableWhenReadOnlyCannotBeSet(t *testing.T) {
	client := &fakeResponder{permissionErr: errors.New("permission store unavailable")}
	ui := newTUIModel(client, false)
	ui.togglePlanMode()
	if ui.planMode || client.PermissionMode() != core.PermissionWorkspace {
		t.Fatalf("failed Plan enable changed state: plan=%v permission=%q", ui.planMode, client.PermissionMode())
	}
}

func TestTUIPermissionCycleDoesNotAppendDetails(t *testing.T) {
	client := &fakeResponder{}
	ui := newTUIModel(client, false)
	initialEntries := len(ui.entries)
	for _, want := range []string{"Ask", "Full access", "Read-only", "Workspace"} {
		ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab, Mod: tea.ModShift}))
		if got := permissionModeLabel(ui.currentPermissionMode()); got != want {
			t.Fatalf("permission mode = %q, want %q", got, want)
		}
		if len(ui.entries) != initialEntries {
			t.Fatalf("permission cycle appended transcript details: %#v", ui.entries[initialEntries:])
		}
		footer := ui.renderFooter(200)
		if !strings.Contains(footer, want) || !strings.Contains(footer, "enter send") || !strings.Contains(footer, "modes") {
			t.Fatalf("permission cycle should retain mode and common hints: %q", footer)
		}
	}
}

func TestTUIEnterRunsHighlightedSlashCommand(t *testing.T) {
	t.Run("runs default highlighted command", func(t *testing.T) {
		client := &fakeTUICompactor{compactedTurns: 2}
		ui := newTUIModel(client, false)
		ui.sessionID = "session-1"
		ui.input.SetValue("/compact")
		ui.updateCommandSuggestions()

		updated, command := ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
		finished := updated.(*tuiModel)
		if command == nil {
			t.Fatal("Enter did not execute the highlighted /compact command")
		}
		runTUICommands(finished, command)
		if client.compactCalls != 1 {
			t.Fatalf("compact calls = %d, want 1", client.compactCalls)
		}
	})

	t.Run("runs command selected with arrows", func(t *testing.T) {
		client := &fakeResumableResponder{pending: true}
		ui := newTUIModel(client, true)
		ui.sessionID = "session-1"
		ui.input.SetValue("/")
		ui.updateCommandSuggestions()
		for ui.commandSuggestions[ui.suggestionIndex] != "/discard" {
			ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
		}

		ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
		if client.discarded != 1 || client.pending {
			t.Fatalf("discarded = %d, pending = %v; want selected /discard to execute", client.discarded, client.pending)
		}
	})
}

func TestTUICompactCommandAndPendingGuard(t *testing.T) {
	client := &fakeTUICompactor{compactedTurns: 2}
	ui := newTUIModel(client, false)
	ui.sessionID = "session-1"
	ui.input.SetValue("/compact")
	updated, command := ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	finished := updated.(*tuiModel)
	if command == nil {
		t.Fatal("/compact did not start an operation")
	}
	runTUICommands(finished, command)
	if client.compactCalls != 1 || !strings.Contains(finished.entries[len(finished.entries)-1].text, "Compacted 2 older") {
		t.Fatalf("compact calls=%d entries=%#v", client.compactCalls, finished.entries)
	}

	client.pending = true
	finished.input.SetValue("/compact")
	_, command = finished.submit()
	if command != nil || client.compactCalls != 1 {
		t.Fatalf("pending task allowed compaction: command=%v calls=%d", command != nil, client.compactCalls)
	}
}

func TestTUIContextCommandShowsPersistsAndResetsOverrides(t *testing.T) {
	defaults := core.ContextDefaults{ContextWindow: 64000, ReserveOutputTokens: 4096}
	client := &fakeResponder{
		contextDefaults: defaults,
		sessions: map[string]core.Session{
			"session-context": {ID: "session-context", Settings: core.SessionSettings{ModelProfile: "lead"}},
		},
	}
	ui := newTUIModel(client, false)
	ui.sessionID = "session-context"
	ui.sessionSettings = core.SessionSettings{ModelProfile: "lead", Goal: "keep the goal", ReasoningEffort: "high"}

	ui.input.SetValue("/context")
	ui.submit()
	view := ui.entries[len(ui.entries)-1].text
	for _, expected := range []string{"64.0k (project default)", "4.1k (project default)", "context window sets its trigger budget", "output reserve leaves room"} {
		if !strings.Contains(strings.ToLower(view), strings.ToLower(expected)) {
			t.Fatalf("context view %q is missing %q", view, expected)
		}
	}

	ui.input.SetValue("/context window 32000")
	ui.submit()
	if ui.sessionSettings.ContextWindow == nil || *ui.sessionSettings.ContextWindow != 32000 || ui.contextWindow != 32000 {
		t.Fatalf("context window override=%v effective=%d", ui.sessionSettings.ContextWindow, ui.contextWindow)
	}
	if !strings.Contains(ui.status, "Automatic compaction can use it") {
		t.Fatalf("context window status = %q, want automatic-compaction status", ui.status)
	}
	ui.input.SetValue("/context reserve 2048")
	ui.submit()
	if ui.sessionSettings.ReserveOutputTokens == nil || *ui.sessionSettings.ReserveOutputTokens != 2048 {
		t.Fatalf("reserve override = %v", ui.sessionSettings.ReserveOutputTokens)
	}
	persisted := client.sessions[ui.sessionID].Settings
	if persisted.Goal != "keep the goal" || persisted.ReasoningEffort != "high" {
		t.Fatalf("context command replaced unrelated session settings: %#v", persisted)
	}

	ui.input.SetValue("/context window reset")
	ui.submit()
	if ui.sessionSettings.ContextWindow != nil || ui.sessionSettings.ReserveOutputTokens == nil || *ui.sessionSettings.ReserveOutputTokens != 2048 || ui.contextWindow != defaults.ContextWindow {
		t.Fatalf("window reset changed unexpected values: settings=%#v effective=%d", ui.sessionSettings, ui.contextWindow)
	}
	ui.input.SetValue("/context reset")
	ui.submit()
	if ui.sessionSettings.ContextWindow != nil || ui.sessionSettings.ReserveOutputTokens != nil {
		t.Fatalf("full context reset left overrides: %#v", ui.sessionSettings)
	}
	if settings := client.sessions[ui.sessionID].Settings; settings.ModelProfile != "lead" || settings.Goal != "keep the goal" || settings.ReasoningEffort != "high" {
		t.Fatalf("reset command replaced unrelated session settings: %#v", settings)
	}

	ui.pending = true
	ui.input.SetValue("/context window 48000")
	ui.submit()
	if !strings.Contains(ui.status, "paused task keeps its original context settings") {
		t.Fatalf("status = %q, want paused-task behavior", ui.status)
	}
}

func TestTUIAutoCompactCommandPersistsSettingsAndShowsRuntimeBehavior(t *testing.T) {
	client := &fakeResponder{
		contextDefaults: core.ContextDefaults{AutoCompact: true, AutoCompactThreshold: 0.75},
		sessions: map[string]core.Session{
			"session-autocompact": {ID: "session-autocompact", Settings: core.SessionSettings{ModelProfile: "lead"}},
		},
	}
	ui := newTUIModel(client, false)
	ui.sessionID = "session-autocompact"
	ui.sessionSettings = core.SessionSettings{ModelProfile: "lead", Goal: "preserve this goal"}

	ui.input.SetValue("/autocompact")
	ui.submit()
	view := ui.entries[len(ui.entries)-1].text
	for _, expected := range []string{"on (project default)", "75% (project default)", "runs before model requests", "set a context window"} {
		if !strings.Contains(strings.ToLower(view), strings.ToLower(expected)) {
			t.Fatalf("auto-compaction view %q is missing %q", view, expected)
		}
	}

	ui.input.SetValue("/autocompact off")
	ui.submit()
	if ui.sessionSettings.AutoCompact == nil || *ui.sessionSettings.AutoCompact || !strings.Contains(ui.status, "applies to future tasks") {
		t.Fatalf("off setting/status = %#v / %q", ui.sessionSettings.AutoCompact, ui.status)
	}
	ui.input.SetValue("/autocompact threshold 0.6")
	ui.submit()
	if ui.sessionSettings.AutoCompactThreshold == nil || *ui.sessionSettings.AutoCompactThreshold != 0.6 {
		t.Fatalf("threshold override = %v", ui.sessionSettings.AutoCompactThreshold)
	}
	ui.input.SetValue("/autocompact threshold 1.1")
	ui.submit()
	if ui.sessionSettings.AutoCompactThreshold == nil || *ui.sessionSettings.AutoCompactThreshold != 0.6 {
		t.Fatalf("invalid threshold changed settings: %v", ui.sessionSettings.AutoCompactThreshold)
	}
	ui.input.SetValue("/autocompact threshold NaN")
	ui.submit()
	if ui.sessionSettings.AutoCompactThreshold == nil || *ui.sessionSettings.AutoCompactThreshold != 0.6 {
		t.Fatalf("non-finite threshold changed settings: %v", ui.sessionSettings.AutoCompactThreshold)
	}
	ui.input.SetValue("/autocompact reset")
	ui.submit()
	if ui.sessionSettings.AutoCompact != nil || ui.sessionSettings.AutoCompactThreshold != nil {
		t.Fatalf("reset left session overrides: %#v", ui.sessionSettings)
	}
	if saved := client.sessions[ui.sessionID].Settings; saved.Goal != "preserve this goal" || saved.ModelProfile != "lead" {
		t.Fatalf("auto-compaction commands replaced unrelated settings: %#v", saved)
	}

	ui.pending = true
	ui.input.SetValue("/autocompact off")
	ui.submit()
	if !strings.Contains(ui.status, "paused task keeps its original settings") {
		t.Fatalf("status = %q, want paused-task behavior", ui.status)
	}
}

func TestTUIThemeCommandSupportsAutoAndResetsSuccessfulInput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings", "ui.json")
	ui := newTUIModel(&fakeResponder{}, false)
	ui.preferences.Theme = themeDark
	ui.preferencesPath = path
	ui.input.SetValue("/theme")
	ui.submit()
	if !strings.Contains(ui.status, "/theme auto") || !strings.Contains(ui.status, "/theme dark") || !strings.Contains(ui.status, "/theme light") {
		t.Fatalf("bare theme command should list supported themes: %q", ui.status)
	}
	if ui.input.Value() != "" {
		t.Fatalf("bare theme command left input behind: %q", ui.input.Value())
	}
	ui.input.SetValue("/theme anime studio")
	ui.submit()
	if ui.preferences.Theme != themeDark || !strings.Contains(ui.status, "Use /theme auto") || ui.input.Value() != "/theme anime studio" {
		t.Fatalf("unsupported theme changed preferences: %#v status=%q", ui.preferences, ui.status)
	}
	ui.input.SetValue("/")
	ui.updateCommandSuggestions()
	if !strings.Contains(strings.Join(ui.commandSuggestions, " "), "/theme") || !strings.Contains(strings.Join(ui.commandSuggestions, " "), "/details") || strings.Contains(strings.Join(ui.commandSuggestions, " "), "/fold") {
		t.Fatalf("theme suggestions = %#v", ui.commandSuggestions)
	}
	ui.input.SetValue("/theme a")
	ui.updateCommandSuggestions()
	if len(ui.commandSuggestions) != 0 {
		t.Fatalf("unknown theme values should not appear as commands: %#v", ui.commandSuggestions)
	}
	ui.input.SetValue("/theme light")
	ui.submit()
	if ui.preferences.Theme != themeLight {
		t.Fatalf("theme = %q, want light", ui.preferences.Theme)
	}
	if ui.input.Value() != "" || len(ui.commandSuggestions) != 0 {
		t.Fatalf("successful theme command left draft or suggestions: %q / %#v", ui.input.Value(), ui.commandSuggestions)
	}
	loaded, err := loadUIPreferences(path)
	if err != nil || loaded.Theme != themeLight {
		t.Fatalf("persisted theme = %#v, %v", loaded, err)
	}

	ui.input.SetValue("/theme auto")
	ui.submit()
	loaded, err = loadUIPreferences(path)
	if err != nil || ui.preferences.Theme != themeAuto || loaded.Theme != themeAuto || ui.input.Value() != "" {
		t.Fatalf("auto theme = %#v, loaded=%#v, input=%q, err=%v", ui.preferences, loaded, ui.input.Value(), err)
	}
}
