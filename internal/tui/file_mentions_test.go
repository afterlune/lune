package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"github.com/afterlune/lune/internal/filemention"
)

// settleCommands runs cmd to completion, feeds every message keep accepts back
// into Update, and returns the resulting model. Commands that only schedule
// unrelated work (cursor blink, clock ticks) are dropped.
func settleCommands(t *testing.T, m *tuiModel, cmd tea.Cmd, keep func(tea.Msg) bool) *tuiModel {
	t.Helper()
	if cmd == nil {
		return m
	}
	done := make(chan []tea.Msg, 1)
	go func() { done <- collectMessages(cmd) }()

	var messages []tea.Msg
	select {
	case messages = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("asynchronous command did not settle")
	}
	for _, msg := range messages {
		if keep != nil && !keep(msg) {
			continue
		}
		next, _ := m.Update(msg)
		m = next.(*tuiModel)
	}
	return m
}

// collectMessages flattens a batch into the messages its commands produced.
func collectMessages(cmd tea.Cmd) []tea.Msg {
	msg := cmd()
	if msg == nil {
		return nil
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		var messages []tea.Msg
		for _, inner := range batch {
			messages = append(messages, collectMessages(inner)...)
		}
		return messages
	}
	return []tea.Msg{msg}
}

func isMentionResults(msg tea.Msg) bool {
	_, ok := msg.(fileMentionResultsMsg)
	return ok
}

func isReviewMaterials(msg tea.Msg) bool {
	_, ok := msg.(reviewMaterialsMsg)
	return ok
}

func isDiffLoaded(msg tea.Msg) bool {
	_, ok := msg.(diffLoadedMsg)
	return ok
}

// TestFileMentionSearchRunsOffTheUpdateGoroutine is the regression guard for the
// synchronous workspace walk that used to block every keystroke behind "@".
func TestFileMentionSearchRunsOffTheUpdateGoroutine(t *testing.T) {
	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, "internal", "tui"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "internal", "tui", "tui.go"), []byte("package tui"), 0o644); err != nil {
		t.Fatal(err)
	}

	scans := 0
	original := searchWorkspaceFiles
	searchWorkspaceFiles = func(scanner *filemention.Scanner, query string, limit int) []filemention.File {
		scans++
		return scanner.Search(query, limit)
	}
	t.Cleanup(func() { searchWorkspaceFiles = original })

	m := newTUIModel(nil, nil, "mention-session")
	m.workspace = workspace
	m.input.scanner = filemention.NewScanner(workspace)
	m.input.composer = textarea.New()
	m.input.composer.Focus()
	m.input.composer.SetValue("@")
	m.input.composer.CursorEnd()

	mod, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: 't', Text: "t"}))
	m = mod.(*tuiModel)
	if scans != 0 {
		t.Fatalf("the key handler scanned the workspace %d times; the scan must run in a command", scans)
	}
	if cmd == nil {
		t.Fatal("expected a background scan command for a cold cache")
	}
	if m.input.autocompleteOpen {
		t.Fatal("the popup must stay closed until the background scan reports")
	}

	m = settleCommands(t, m, cmd, isMentionResults)
	if scans != 1 {
		t.Fatalf("expected exactly one background scan, got %d", scans)
	}
	if !m.input.autocompleteOpen || m.input.autocompleteMode != AutocompleteModeFile {
		t.Fatalf("expected the file popup after the scan: open=%v mode=%v", m.input.autocompleteOpen, m.input.autocompleteMode)
	}
	if len(m.input.autocompleteItems) != 1 || m.input.autocompleteItems[0].Command != "@internal/tui/tui.go" {
		t.Fatalf("unexpected popup items: %#v", m.input.autocompleteItems)
	}
}

// TestFileMentionUsesWarmCacheWithoutScanning keeps typing responsive once the
// cache is populated: no further scan is started while the cache is fresh.
func TestFileMentionUsesWarmCacheWithoutScanning(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "main.go"), []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}

	scans := 0
	original := searchWorkspaceFiles
	searchWorkspaceFiles = func(scanner *filemention.Scanner, query string, limit int) []filemention.File {
		scans++
		return scanner.Search(query, limit)
	}
	t.Cleanup(func() { searchWorkspaceFiles = original })

	m := newTUIModel(nil, nil, "warm-cache-session")
	m.workspace = workspace
	m.input.scanner = filemention.NewScanner(workspace)
	m.input.composer = textarea.New()
	m.input.composer.Focus()
	m.input.composer.SetValue("@")
	m.input.composer.CursorEnd()

	mod, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: 'm', Text: "m"}))
	m = mod.(*tuiModel)
	m = settleCommands(t, m, cmd, isMentionResults)
	if scans != 1 || !m.input.autocompleteOpen {
		t.Fatalf("expected one cold scan and an open popup, scans=%d open=%v", scans, m.input.autocompleteOpen)
	}

	mod, cmd = m.Update(tea.KeyPressMsg(tea.Key{Code: 'a', Text: "a"}))
	m = mod.(*tuiModel)
	if cmd != nil {
		if messages := collectMessages(cmd); len(messages) > 0 {
			for _, msg := range messages {
				if _, ok := msg.(fileMentionResultsMsg); ok {
					t.Fatalf("a warm cache must not start another scan: %#v", msg)
				}
			}
		}
	}
	if scans != 1 {
		t.Fatalf("warm cache triggered an extra scan: %d", scans)
	}
}

// TestFileMentionDropsStaleResults covers the ordering guard: a slow scan for an
// older query must not replace the popup the user is looking at.
func TestFileMentionDropsStaleResults(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "alpha.go"), []byte("package alpha"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := newTUIModel(nil, nil, "stale-session")
	m.workspace = workspace
	m.input.scanner = filemention.NewScanner(workspace)
	m.input.composer = textarea.New()
	m.input.composer.Focus()
	m.input.composer.SetValue("@a")
	m.input.composer.CursorEnd()

	stale := fileMentionResultsMsg{
		request: m.input.mentionRequest,
		query:   "a",
		start:   1,
		end:     len("@a"),
		items:   []AutocompleteItem{{Command: "@stale.go"}},
	}
	// A newer keystroke advances the request counter and closes the popup.
	m.input.mentionRequest++
	m.input.autocompleteOpen = false

	mod, _ := m.Update(stale)
	m = mod.(*tuiModel)
	if m.input.autocompleteOpen {
		t.Fatalf("stale results reopened the popup: %#v", m.input.autocompleteItems)
	}
}

// TestFileMentionResultsCloseOnEmptyMatch keeps the original "no match closes
// the popup" behavior on the asynchronous path.
func TestFileMentionResultsCloseOnEmptyMatch(t *testing.T) {
	m := newTUIModel(nil, nil, "empty-session")
	m.input.composer = textarea.New()
	m.input.composer.Focus()
	m.input.composer.SetValue("@zzz")
	m.input.composer.CursorEnd()
	m.input.autocompleteOpen = true
	m.input.autocompleteItems = []AutocompleteItem{{Command: "@old.go"}}

	mod, _ := m.Update(fileMentionResultsMsg{
		request: m.input.mentionRequest,
		query:   "zzz",
		start:   1,
		end:     4,
	})
	m = mod.(*tuiModel)
	if m.input.autocompleteOpen {
		t.Fatal("empty results must close the popup")
	}
}

// TestDiffLoadedMessageOpensViewer covers the message half of the asynchronous
// diff load.
func TestDiffLoadedMessageOpensViewer(t *testing.T) {
	m := newTUIModel(nil, nil, "diff-session")
	mod, _ := m.Update(diffLoadedMsg{files: []DiffFile{{Path: "main.go", Additions: 1, Lines: []string{"+x"}}}})
	m = mod.(*tuiModel)
	if !m.modals.diff.open || len(m.modals.diff.files) != 1 {
		t.Fatalf("expected the diff viewer to open: open=%v files=%d", m.modals.diff.open, len(m.modals.diff.files))
	}
}

// TestDiffLoadFailureIsReported keeps a failed git call visible instead of
// leaving the interface without feedback.
func TestDiffLoadFailureIsReported(t *testing.T) {
	m := newTUIModel(nil, nil, "diff-error-session")
	mod, _ := m.Update(diffLoadedMsg{err: errWorkspaceGitTimeout})
	m = mod.(*tuiModel)
	if m.modals.diff.open {
		t.Fatal("a failed diff load must not open the viewer")
	}
	last := m.entries[len(m.entries)-1].Text
	if !strings.Contains(last, "Failed to run git diff") {
		t.Fatalf("expected the git failure in the transcript, got %q", last)
	}
}

// TestReviewMaterialsMessageReportsEmptyWorkspace covers the message half of the
// asynchronous review collection.
func TestReviewMaterialsMessageReportsEmptyWorkspace(t *testing.T) {
	m := newTUIModel(nil, nil, "review-session")
	mod, _ := m.Update(reviewMaterialsMsg{changed: false})
	m = mod.(*tuiModel)
	last := m.entries[len(m.entries)-1].Text
	if !strings.Contains(last, "No text changes to review") {
		t.Fatalf("expected the no-changes notice, got %q", last)
	}
}
