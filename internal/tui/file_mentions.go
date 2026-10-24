package tui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/afterlune/lune/internal/filemention"
)

// fileMentionLimit caps the candidate list the `@` popup renders.
const fileMentionLimit = 50

// fileMentionResultsMsg carries the outcome of an asynchronous workspace scan
// started by an `@` mention. request orders the results so a slow scan cannot
// overwrite the popup of a newer query.
type fileMentionResultsMsg struct {
	request uint64
	query   string
	start   int
	end     int
	items   []AutocompleteItem
}

// searchWorkspaceFiles is the only place the TUI scans the workspace for file
// mentions. Tests replace it to exercise the asynchronous path without I/O.
var searchWorkspaceFiles = func(scanner *filemention.Scanner, query string, limit int) []filemention.File {
	return scanner.Search(query, limit)
}

// refreshFileMentions fills the popup from the cached workspace files and
// returns a background refresh when that cache is missing or stale, so the scan
// never runs on the update goroutine.
func (m *tuiModel) refreshFileMentions(query string, start, end int) tea.Cmd {
	scanner := m.getFileScanner()
	m.input.mentionRequest++
	request := m.input.mentionRequest
	m.setFileMentionItems(fileMentionItems(filemention.FuzzyMatch(scanner.CachedFiles(), query, fileMentionLimit)), start, end)
	if !scanner.Expired() {
		return nil
	}
	return func() tea.Msg {
		files := searchWorkspaceFiles(scanner, query, fileMentionLimit)
		return fileMentionResultsMsg{request: request, query: query, start: start, end: end, items: fileMentionItems(files)}
	}
}

// applyFileMentionResults installs an asynchronous scan, dropping results the
// user has already typed past.
func (m *tuiModel) applyFileMentionResults(msg fileMentionResultsMsg) (tea.Model, tea.Cmd) {
	if msg.request != m.input.mentionRequest {
		return m, nil
	}
	query, _, _, ok := filemention.DetectAtQuery(m.input.composer.Value(), m.cursorOffset())
	if !ok || query != msg.query {
		return m, nil
	}
	m.setFileMentionItems(msg.items, msg.start, msg.end)
	m.updateLayout()
	return m, nil
}

// setFileMentionItems replaces the popup contents; an empty result closes it.
func (m *tuiModel) setFileMentionItems(items []AutocompleteItem, start, end int) {
	if len(items) == 0 {
		m.input.autocompleteOpen = false
		return
	}
	m.input.autocompleteOpen = true
	m.input.autocompleteMode = AutocompleteModeFile
	m.input.autocompleteStart = start
	m.input.autocompleteEnd = end
	m.input.autocompleteIndex = 0
	m.input.autocompleteItems = items
}

// fileMentionItems renders scanner results as popup entries.
func fileMentionItems(files []filemention.File) []AutocompleteItem {
	if len(files) == 0 {
		return nil
	}
	items := make([]AutocompleteItem, 0, len(files))
	for _, match := range files {
		description := filemention.FormatFileSize(match.Size)
		if match.IsModified {
			description += " · modified"
		}
		items = append(items, AutocompleteItem{Command: "@" + match.Path, Description: description})
	}
	return items
}
