package tui

import (
	"time"

	"charm.land/bubbles/v2/textarea"
	"github.com/afterlune/lune/internal/filemention"
)

// composerState owns the input line and everything typed around it: the
// autocomplete popup, the workspace file scanner behind `@` mentions, prompt
// history navigation, the mouse text selection and the copy feedback row.
//
// Transcript selection (input.selecting) and composer selection
// (input.composerSelecting) are separate on purpose: they select different
// surfaces and must not clear each other.
type composerState struct {
	composer textarea.Model

	autocompleteOpen  bool
	autocompleteMode  AutocompleteMode
	autocompleteItems []AutocompleteItem
	autocompleteIndex int
	autocompleteStart int
	autocompleteEnd   int

	scanner *filemention.Scanner

	history      []string
	historyIndex int
	historyDraft string

	selecting             bool
	composerSelecting     bool
	selectStartX          int
	selectStartY          int
	selectEndX            int
	selectEndY            int
	selectStartDocRow     int
	selectEndDocRow       int
	selectedText          string
	copyFeedback          string
	copyFeedbackError     bool
	copyFeedbackExpiresAt time.Time
	// copiedViaTerminal records that the last copy used an OSC 52 terminal
	// clipboard write instead of a host clipboard bridge.
	copiedViaTerminal bool
	clipboardWriter   func(string) error
}

// scannerFor returns the workspace file scanner, creating it on first use.
func (c *composerState) scannerFor(workspace string) *filemention.Scanner {
	if c.scanner == nil {
		c.scanner = filemention.NewScanner(workspace)
	}
	return c.scanner
}

// clearSelection drops the transcript selection and its copied text.
func (c *composerState) clearSelection() {
	c.selecting = false
	c.composerSelecting = false
	c.composer.ClearSelection()
	c.selectedText = ""
	c.selectStartX, c.selectEndX = 0, 0
	c.selectStartY, c.selectEndY = 0, 0
	c.selectStartDocRow, c.selectEndDocRow = 0, 0
}
