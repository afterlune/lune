package tui

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestTerminalTitleManagerWritesOSC0DeduplicatesAndClears(t *testing.T) {
	var output bytes.Buffer
	manager := &terminalTitleManager{output: &output}

	if err := manager.Set("总结一下当前项目 | luneAgent"); err != nil {
		t.Fatal(err)
	}
	first := "\x1b]0;总结一下当前项目 | luneAgent\x07"
	if got := output.String(); got != first {
		t.Fatalf("unexpected title sequence: got %q, want %q", got, first)
	}

	if err := manager.Set("总结一下当前项目 | luneAgent"); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); got != first {
		t.Fatalf("unchanged title was written again: %q", got)
	}

	if err := manager.Set("第二个会话 | luneAgent"); err != nil {
		t.Fatal(err)
	}
	if err := manager.Clear(); err != nil {
		t.Fatal(err)
	}
	want := first + "\x1b]0;第二个会话 | luneAgent\x07\x1b]0;\x07"
	if got := output.String(); got != want {
		t.Fatalf("unexpected title update/clear sequence: got %q, want %q", got, want)
	}

	if err := manager.Clear(); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); got != want {
		t.Fatalf("cleared title was written again: %q", got)
	}
}

func TestSanitizeTerminalTitle(t *testing.T) {
	got := sanitizeTerminalTitle("  项目\t | \nA\x1b]0;evil\x07 \u009d B  ")
	if want := "项目 | A]0;evil B"; got != want {
		t.Fatalf("unexpected sanitized title: got %q, want %q", got, want)
	}

	long := sanitizeTerminalTitle(strings.Repeat("界", maxTerminalTitleRunes+5))
	if got := len([]rune(long)); got != maxTerminalTitleRunes {
		t.Fatalf("title was not truncated by rune count: got %d runes, want %d", got, maxTerminalTitleRunes)
	}
}

func TestPrepareTerminalOutputSkipsNonTerminalWriters(t *testing.T) {
	output := &bytes.Buffer{}
	prepared, manager := prepareTerminalOutput(output)
	if manager != nil {
		t.Fatal("non-terminal writer received a terminal title manager")
	}
	if prepared != output {
		t.Fatal("non-terminal output writer was unexpectedly wrapped")
	}
}

func TestUpdateSyncsChangedTerminalTitle(t *testing.T) {
	m := newTUIModel(nil, nil, "sess-1")
	m.workspace = filepath.Join(`D:\Git`, "luneAgent")
	var output bytes.Buffer
	m.terminalTitle = &terminalTitleManager{output: &output}

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	m = updated.(*tuiModel)
	if got, want := output.String(), "\x1b]0;lune | luneAgent\x07"; got != want {
		t.Fatalf("initial title was not synchronized: got %q, want %q", got, want)
	}

	m.sessionTitle = "Refactor Architecture"
	updated, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	m = updated.(*tuiModel)
	want := "\x1b]0;lune | luneAgent\x07\x1b]0;Refactor Architecture | luneAgent\x07"
	if got := output.String(); got != want {
		t.Fatalf("changed title was not synchronized: got %q, want %q", got, want)
	}
}
