package tui

import (
	"encoding/base64"
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

func writeClipboardTextWith(text string, writeAll func(string) error) (string, error) {
	text = sanitizeClipboardText(text)
	return text, writeAll(text)
}

func (m *tuiModel) copyTextToClipboard(text string) tea.Cmd {
	return m.copyTextToClipboardAs(text, func(count int) string {
		if m.copiedViaTerminal {
			return fmt.Sprintf("Copied %d chars to terminal clipboard", count)
		}
		return fmt.Sprintf("Copied %d chars to host clipboard", count)
	})
}

// writeClipboard prefers the host clipboard bridge and falls back to an OSC 52
// escape sequence so copying works on terminals without xclip/xsel/wl-copy.
func (m *tuiModel) writeClipboard(text string) error {
	native := m.clipboardWriter
	if native == nil {
		native = clipboard.WriteAll
	}
	return m.writeClipboardWith(native, text)
}

// writeClipboardWith runs the host clipboard bridge and falls back to OSC 52.
func (m *tuiModel) writeClipboardWith(native func(string) error, text string) error {
	m.copiedViaTerminal = false
	nativeErr := native(text)
	if nativeErr == nil {
		return nil
	}
	if err := m.terminalTitle.WriteRaw(osc52Sequence(text)); err != nil {
		return nativeErr
	}
	m.copiedViaTerminal = true
	return nil
}

// osc52Sequence encodes text as an OSC 52 clipboard write.
func osc52Sequence(text string) string {
	return "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(text)) + "\x07"
}

func (m *tuiModel) copyTextToClipboardAs(text string, successMessage func(int) string) tea.Cmd {
	writeAll := m.clipboardWriter
	if writeAll == nil {
		writeAll = m.writeClipboard
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
