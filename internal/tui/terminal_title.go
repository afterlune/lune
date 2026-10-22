package tui

import (
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"unicode"

	"github.com/charmbracelet/x/term"
)

const maxTerminalTitleRunes = 240

// synchronizedTerminalFile serializes Bubble Tea rendering and terminal-title
// writes while preserving the file interface Bubble Tea uses to detect a TTY.
// The wrapped file is borrowed from the caller, so Close intentionally does
// not close it.
type synchronizedTerminalFile struct {
	term.File
	mu sync.Mutex
}

func (f *synchronizedTerminalFile) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.File.Write(p)
}

func (f *synchronizedTerminalFile) Close() error {
	return nil
}

type terminalTitleManager struct {
	output io.Writer
	mu     sync.Mutex
	last   string
}

func prepareTerminalOutput(output io.Writer) (io.Writer, *terminalTitleManager) {
	file, ok := output.(term.File)
	if !ok || !term.IsTerminal(file.Fd()) {
		return output, nil
	}

	synchronized := &synchronizedTerminalFile{File: file}
	return synchronized, &terminalTitleManager{output: synchronized}
}

func (m *terminalTitleManager) Set(title string) error {
	if m == nil || m.output == nil {
		return nil
	}
	title = sanitizeTerminalTitle(title)
	if title == "" {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if title == m.last {
		return nil
	}
	if _, err := io.WriteString(m.output, "\x1b]0;"+title+"\x07"); err != nil {
		return err
	}
	m.last = title
	return nil
}

// Notify writes a terminal bell so that a user who switched windows notices
// that the TUI needs attention. Writes share the renderer mutex, so the bell
// never interleaves with Bubble Tea output.
func (m *terminalTitleManager) Notify() error {
	if m == nil || m.output == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	_, err := io.WriteString(m.output, "\a")
	return err
}

func (m *terminalTitleManager) Clear() error {
	if m == nil || m.output == nil {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.last == "" {
		return nil
	}
	if _, err := io.WriteString(m.output, "\x1b]0;\x07"); err != nil {
		return err
	}
	m.last = ""
	return nil
}

// WriteRaw writes a control sequence (for example an OSC 52 clipboard write)
// through the same lock the renderer uses, so it never interleaves with a frame.
func (m *terminalTitleManager) WriteRaw(sequence string) error {
	if m == nil || m.output == nil {
		return errors.New("terminal output unavailable")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	_, err := io.WriteString(m.output, sequence)
	return err
}

func (m *tuiModel) syncTerminalTitle() {
	if m.terminalTitle == nil {
		return
	}
	if err := m.terminalTitle.Set(m.desiredTerminalTitle()); err != nil {
		slog.Debug("failed to update terminal title", "error", err)
	}
}

func sanitizeTerminalTitle(title string) string {
	var cleaned strings.Builder
	cleaned.Grow(min(len(title), maxTerminalTitleRunes))
	chars := 0
	pendingSpace := false

	for _, r := range title {
		if unicode.IsSpace(r) {
			pendingSpace = cleaned.Len() > 0
			continue
		}
		if unicode.IsControl(r) {
			continue
		}
		if pendingSpace && chars < maxTerminalTitleRunes {
			cleaned.WriteByte(' ')
			chars++
		}
		pendingSpace = false
		if chars >= maxTerminalTitleRunes {
			break
		}
		cleaned.WriteRune(r)
		chars++
	}
	return strings.TrimSpace(cleaned.String())
}
