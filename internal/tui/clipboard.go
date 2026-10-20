package tui

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/atotto/clipboard"
)

func sanitizeClipboardText(text string) string {
	return strings.ReplaceAll(text, "\x00", "")
}

func writeClipboardText(text string) (string, error) {
	return writeClipboardTextWith(text, clipboard.WriteAll)
}

func writeClipboardTextWith(text string, writeAll func(string) error) (string, error) {
	text = sanitizeClipboardText(text)
	return text, writeAll(text)
}

func (m *tuiModel) copyTextToClipboard(text string) tea.Cmd {
	return m.copyTextToClipboardAs(text, func(count int) string {
		return fmt.Sprintf("Copied %d chars to host clipboard", count)
	})
}

func (m *tuiModel) copyTextToClipboardAs(text string, successMessage func(int) string) tea.Cmd {
	writeAll := m.clipboardWriter
	if writeAll == nil {
		writeAll = clipboard.WriteAll
	}
	return m.copyTextToClipboardUsing(text, successMessage, writeAll)
}

func (m *tuiModel) copyTextToClipboardUsing(text string, successMessage func(int) string, writeAll func(string) error) tea.Cmd {
	if text == "" {
		return nil
	}
	cleaned, err := writeClipboardTextWith(text, writeAll)
	m.copyFeedbackError = err != nil
	if err != nil {
		m.copyFeedback = "Copy failed: " + err.Error()
	} else {
		m.copyFeedback = successMessage(utf8.RuneCountInString(cleaned))
	}
	m.copyFeedbackExpiresAt = time.Now().Add(3 * time.Second)
	expiresAt := m.copyFeedbackExpiresAt
	return tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
		return clearCopyFeedbackMsg{at: expiresAt}
	})
}
