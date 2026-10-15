package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestTranscriptSearchExpandsToolRowsAndPreservesComposerDraft(t *testing.T) {
	ui := newTUIModel(nil, false)
	ui.input.SetValue("keep this draft")
	ui.entries = []tuiEntry{
		{role: "assistant", text: "The file is ready."},
		{role: "tool", activityID: "call-1", text: "Reading internal/config/provider.go · done"},
	}
	ui.refreshPanels()
	ui.openTranscriptSearch()
	ui.searchQuery = "provider.go"
	ui.updateTranscriptSearch()
	ui.updateLayout()

	if len(ui.searchMatches) != 1 || !strings.Contains(ui.chat.GetContent(), "Reading internal/config/provider.go") {
		t.Fatalf("search matches=%v content=%q", ui.searchMatches, ui.chat.GetContent())
	}
	if ui.input.Value() != "keep this draft" {
		t.Fatalf("search changed composer draft: %q", ui.input.Value())
	}
	ui.handleTranscriptSearchKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if ui.searchOpen || len(ui.searchMatches) != 1 || !ui.activityExpanded["call-1"] {
		t.Fatalf("Enter should keep the match visible: open=%v matches=%v expanded=%v", ui.searchOpen, ui.searchMatches, ui.activityExpanded)
	}
}

func TestTranscriptSearchEscapeRestoresCollapsedActivity(t *testing.T) {
	ui := newTUIModel(nil, false)
	ui.entries = []tuiEntry{{role: "tool", activityID: "call-1", text: "Reading private.txt · done"}}
	ui.refreshPanels()
	ui.openTranscriptSearch()
	if !ui.activityExpanded["call-1"] {
		t.Fatal("search should reveal collapsed tool activity")
	}
	ui.handleTranscriptSearchKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	if ui.searchOpen || ui.activityExpanded["call-1"] || !strings.Contains(ui.chat.GetContent(), "Reading private.txt") {
		t.Fatalf("Esc should restore prior transcript disclosure state: open=%v expanded=%v content=%q", ui.searchOpen, ui.activityExpanded, ui.chat.GetContent())
	}
}

func TestTranscriptMouseSelectionMapsUnicodeCellsAndCopies(t *testing.T) {
	ui := newTUIModel(nil, false)
	ui.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	ui.entries = []tuiEntry{{role: "assistant", text: "Hello 世界 👋"}}
	ui.refreshPanels()
	ui.preferences.CopyOnSelect = "never"

	ui.Update(tea.MouseClickMsg{X: 7, Y: 0, Button: tea.MouseLeft})
	ui.Update(tea.MouseMotionMsg{X: 9, Y: 0, Button: tea.MouseLeft})
	ui.Update(tea.MouseReleaseMsg{X: 9, Y: 0, Button: tea.MouseLeft})
	if got := ui.selectedTranscriptText(); got != "世界" {
		t.Fatalf("drag selected %q, want two CJK graphemes", got)
	}

	var copied string
	previousWriter := writeClipboard
	writeClipboard = func(value string) error { copied = value; return nil }
	t.Cleanup(func() { writeClipboard = previousWriter })
	if !ui.copyTranscriptSelection(false) || copied != "世界" {
		t.Fatalf("manual transcript copy = %q, active=%v", copied, ui.transcriptSelection.active)
	}
}

func TestTranscriptSelectionCopyPolicyAndClearPreference(t *testing.T) {
	ui := newTUIModel(nil, false)
	t.Setenv("TERM_PROGRAM", "Windows_Terminal")
	ui.preferences.CopyOnSelect = "auto"
	if ui.autoCopySelectionEnabled() {
		t.Fatal("auto mode should defer copy in a terminal with native selection shortcuts")
	}
	t.Setenv("TMUX", "1")
	if !ui.autoCopySelectionEnabled() {
		t.Fatal("auto mode should copy selections inside a terminal multiplexer")
	}

	ui.entries = []tuiEntry{{role: "assistant", text: "copy me"}}
	ui.refreshPanels()
	ui.transcriptSelection = transcriptSelection{start: 0, end: 7, active: true}
	ui.preferences.ClearSelectionOnCopy = true
	previousWriter := writeClipboard
	writeClipboard = func(string) error { return nil }
	t.Cleanup(func() { writeClipboard = previousWriter })
	if !ui.copyTranscriptSelection(false) || ui.transcriptSelection.active {
		t.Fatal("copy should clear the selected transcript when configured")
	}
}

func TestTranscriptAutoCopyMatchesCodexTerminalPolicy(t *testing.T) {
	for _, test := range []struct {
		name, terminal, version string
		copy                    bool
	}{
		{name: "known Ghostty forwards shortcut", terminal: "ghostty", version: "1.2.0", copy: false},
		{name: "older Ghostty copies", terminal: "ghostty", version: "1.1.9", copy: true},
		{name: "unknown Ghostty version copies", terminal: "ghostty", version: "unknown", copy: true},
		{name: "WezTerm copies", terminal: "WezTerm", version: "2026.1", copy: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("TMUX", "")
			t.Setenv("STY", "")
			t.Setenv("ZELLIJ", "")
			t.Setenv("WT_SESSION", "")
			t.Setenv("TERM_PROGRAM", test.terminal)
			t.Setenv("TERM_PROGRAM_VERSION", test.version)
			ui := newTUIModel(nil, false)
			ui.preferences.CopyOnSelect = "auto"
			if got := ui.autoCopySelectionEnabled(); got != test.copy {
				t.Fatalf("auto copy=%v, want %v for %s %s", got, test.copy, test.terminal, test.version)
			}
		})
	}
}

func TestCopySelectionCommandPersistsPolicy(t *testing.T) {
	path := t.TempDir() + "/ui.json"
	ui := newTUIModel(nil, false)
	ui.preferencesPath = path
	ui.input.SetValue("/copyselect always")
	ui.submit()
	ui.input.SetValue("/copyselect clear on")
	ui.submit()
	loaded, err := loadUIPreferences(path)
	if err != nil || loaded.CopyOnSelect != "always" || !loaded.ClearSelectionOnCopy {
		t.Fatalf("copy selection preferences = %#v, err=%v", loaded, err)
	}
}

func TestCollapsedToolActivityExpandsWithoutPayload(t *testing.T) {
	ui := newTUIModel(nil, false)
	ui.entries = []tuiEntry{
		{role: "tool", activityID: "call-1", text: "Reading a.go · done"},
		{role: "tool", activityID: "call-2", text: "Searching project files · running"},
	}
	content, _ := ui.transcriptContent()
	if strings.Contains(content, "Tool activity (2)") || !strings.Contains(content, "Reading a.go") || !strings.Contains(content, "Searching project files") {
		t.Fatalf("collapsed activity = %q", content)
	}
	ui.activityFocus = true
	ui.toggleFocusedActivity()
	content, _ = ui.transcriptContent()
	if !strings.Contains(content, "Reading a.go · done") || !strings.Contains(content, "Searching project files · running") {
		t.Fatalf("expanded activity = %q", content)
	}
}

func TestTranscriptKeepsReadOffsetAndCountsUnreadEntries(t *testing.T) {
	ui := newTUIModel(nil, false)
	ui.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	ui.entries = []tuiEntry{{role: "assistant", text: strings.Repeat("long response line\n", 24)}}
	ui.refreshPanels()
	ui.chat.SetYOffset(2)
	ui.entries = append(ui.entries, tuiEntry{role: "assistant", text: "new response"})
	ui.refreshPanels()
	if ui.chat.YOffset() != 2 || ui.unreadEntries != 1 {
		t.Fatalf("read offset=%d, unread=%d; want offset 2 and one unread entry", ui.chat.YOffset(), ui.unreadEntries)
	}
	ui.chat.GotoBottom()
	ui.unreadEntries = 0
	ui.refreshPanels()
	if ui.unreadEntries != 0 {
		t.Fatalf("following latest should clear unread count, got %d", ui.unreadEntries)
	}
}
