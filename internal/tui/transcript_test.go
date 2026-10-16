package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	core "github.com/afterlune/luneCore"
	publictool "github.com/afterlune/luneCore/tool"
	"github.com/charmbracelet/x/ansi"
)

func TestTranscriptSearchExpandsToolRowsAndPreservesComposerDraft(t *testing.T) {
	ui := newTUIModel(nil, false)
	ui.input.SetValue("keep this draft")
	ui.entries = []tuiEntry{
		{role: "assistant", text: "The file is ready."},
		{role: "tool", activityID: "call-1", text: "Read internal/config/provider.go · done"},
	}
	ui.refreshPanels()
	ui.openTranscriptSearch()
	ui.searchQuery = "provider.go"
	ui.updateTranscriptSearch()
	ui.updateLayout()

	if len(ui.searchMatches) != 1 || !strings.Contains(ui.chat.GetContent(), "Read internal/config/provider.go") {
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
	ui.entries = []tuiEntry{{role: "tool", activityID: "call-1", text: "Read private.txt · done"}}
	ui.refreshPanels()
	ui.openTranscriptSearch()
	if !ui.activityExpanded["call-1"] {
		t.Fatal("search should reveal collapsed tool activity")
	}
	ui.handleTranscriptSearchKey(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	if ui.searchOpen || ui.activityExpanded["call-1"] || !strings.Contains(ui.chat.GetContent(), "Read private.txt") {
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

func TestToolActivityInvocationsStaySeparateAndExpandOutput(t *testing.T) {
	ui := newTUIModel(nil, false)
	ui.runningExecutionID = "execution-1"
	ui.Update(toolActivityMsg{event: core.ToolCall{ExecutionID: "execution-1", ID: "call-1", Name: "read_file", Arguments: []byte(`{"path":"a.go"}`), Started: true}})
	ui.Update(toolActivityMsg{event: core.ToolCall{ExecutionID: "execution-1", ID: "call-1", Name: "read_file", Arguments: []byte(`{"path":"a.go"}`), Duration: 12, Details: &publictool.ExecutionDetails{Output: "first file contents"}}})
	ui.Update(toolActivityMsg{event: core.ToolCall{ExecutionID: "execution-1", ID: "call-2", Name: "web_search", Started: true}})
	ui.entries = append(ui.entries, tuiEntry{role: "assistant", text: "Final answer remains visible."})
	content, _ := ui.transcriptContent()
	if len(ui.entries) != 3 || !strings.Contains(content, "Read a.go") || !strings.Contains(content, "Searched the web") || !strings.Contains(content, "Final answer remains visible.") || strings.Contains(content, "first file contents") {
		t.Fatalf("collapsed activity = %q", content)
	}
	ui.Update(toolActivityMsg{event: core.ToolCall{ExecutionID: "execution-1", ID: "call-1", Name: "read_file", Arguments: []byte(`{"path":"a.go"}`), Duration: 20}})
	content, _ = ui.transcriptContent()
	if !strings.Contains(content, "Read a.go · done") || !strings.Contains(content, "Searched the web · running") {
		t.Fatalf("collapsed activity did not follow the latest event: %q", content)
	}
	ui.activityFocus = true
	ui.toggleFocusedActivity()
	content, _ = ui.transcriptContent()
	if !strings.Contains(content, "Read a.go · done") || !strings.Contains(content, "Searched the web · running") || !strings.Contains(content, "first file contents") {
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

func TestTranscriptWrappingKeepsInlineCodeWordsAndAllCharacters(t *testing.T) {
	token := "internal/cli/paused_test.go"
	text := "Read " + token + " now"
	start := strings.Index(text, token)
	lines := wrapTextSegments(text, 30, [][2]int{{start, start + len(token)}})

	var reconstructed strings.Builder
	codeLines := 0
	for _, line := range lines {
		reconstructed.WriteString(line.text)
		if strings.Contains(line.text, token) {
			codeLines++
		}
	}
	if got := reconstructed.String(); got != text {
		t.Fatalf("wrapped source lost or reordered bytes: got %q want %q", got, text)
	}
	if codeLines != 1 {
		t.Fatalf("fitting code token occupied %d rows: %#v", codeLines, lines)
	}

	longToken := "D:/Git/very-long-folder/core_cli.go"
	longText := "Read " + longToken + " now"
	longStart := strings.Index(longText, longToken)
	longLines := wrapTextSegments(longText, 9, [][2]int{{longStart, longStart + len(longToken)}})
	reconstructed.Reset()
	for _, line := range longLines {
		reconstructed.WriteString(line.text)
	}
	if got := reconstructed.String(); got != longText {
		t.Fatalf("long token split lost characters: got %q want %q; lines=%#v", got, longText, longLines)
	}
}

func TestTranscriptWrappingPreservesUnicodeGraphemesAroundCode(t *testing.T) {
	text := "前缀 👩‍💻 `core_cli.go` 后缀"
	plain := plainConversationText(text)
	token := "core_cli.go"
	start := strings.Index(plain, token)
	lines := wrapTextSegments(plain, 8, [][2]int{{start, start + len(token)}})
	var reconstructed strings.Builder
	for _, line := range lines {
		reconstructed.WriteString(line.text)
	}
	if got := reconstructed.String(); got != plain {
		t.Fatalf("Unicode wrap lost or reordered text: got %q want %q", got, plain)
	}
}

func TestLightTranscriptViewportUsesTheStyledWrapRows(t *testing.T) {
	ui := newTUIModel(nil, false)
	ui.preferences.Theme = themeLight
	ui.applyResolvedTheme()
	ui.Update(tea.WindowSizeMsg{Width: 50, Height: 20})
	ui.entries = []tuiEntry{{role: "assistant", text: "Use `internal/cli/paused_test.go` and `internal/cli/core_cli.go`."}}
	ui.refreshPanels()
	if ui.chat.SoftWrap {
		t.Fatal("light transcript should use its precomputed visual rows")
	}

	source, _ := ui.transcriptContent()
	visual := wrapTextSegments(source, ui.chat.Width(), ui.inlineCodeRanges(source))
	actual := strings.Split(ui.chat.GetContent(), "\n")
	if len(actual) != len(visual) {
		t.Fatalf("viewport rows=%d, styled rows=%d", len(actual), len(visual))
	}
	for index, line := range visual {
		if actual[index] != line.text {
			t.Fatalf("viewport row %d=%q, styled row=%q", index, actual[index], line.text)
		}
	}
	styled := ansi.Strip(ui.styleLightTranscript(ui.chat.View()))
	for _, token := range []string{"internal/cli/paused_test.go", "internal/cli/core_cli.go"} {
		if !strings.Contains(styled, token) {
			t.Fatalf("styled transcript lost inline code token %q: %q", token, styled)
		}
	}
}
