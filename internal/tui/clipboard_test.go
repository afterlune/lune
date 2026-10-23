package tui

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// TestClipboardFallsBackToOSC52 covers terminals without a host clipboard bridge.
func TestClipboardFallsBackToOSC52(t *testing.T) {
	m := newTUIModel(nil, nil, "osc52-sess")
	var out bytes.Buffer
	m.terminalTitle = &terminalTitleManager{output: &out}

	if err := m.writeClipboardWith(func(string) error { return errors.New("clipboard unavailable") }, "hello"); err != nil {
		t.Fatalf("OSC 52 fallback failed: %v", err)
	}
	if !m.input.copiedViaTerminal {
		t.Fatalf("expected the OSC 52 path to be recorded")
	}
	want := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte("hello")) + "\x07"
	if got := out.String(); got != want {
		t.Fatalf("OSC 52 sequence = %q, want %q", got, want)
	}

	// A working host clipboard must not emit any terminal sequence.
	out.Reset()
	if err := m.writeClipboardWith(func(string) error { return nil }, "hi"); err != nil {
		t.Fatalf("host clipboard copy failed: %v", err)
	}
	if m.input.copiedViaTerminal || out.Len() != 0 {
		t.Fatalf("host clipboard path wrote terminal output: copiedViaTerminal=%t out=%q", m.input.copiedViaTerminal, out.String())
	}

	// Without a terminal writer the original clipboard error must surface.
	bare := newTUIModel(nil, nil, "osc52-bare")
	if err := bare.writeClipboardWith(func(string) error { return errors.New("no clipboard") }, "x"); err == nil {
		t.Fatalf("expected the clipboard error to surface without a terminal writer")
	}
}

func TestSanitizeClipboardTextRemovesNUL(t *testing.T) {
	if got, want := sanitizeClipboardText("before\x00TOKEN_0\x00after"), "beforeTOKEN_0after"; got != want {
		t.Fatalf("sanitizeClipboardText() = %q, want %q", got, want)
	}
	if got, want := sanitizeClipboardText("计划内容和 `code`"), "计划内容和 `code`"; got != want {
		t.Fatalf("sanitizeClipboardText changed ordinary text: got %q, want %q", got, want)
	}
}

func TestClipboardCopyFeedbackReportsSuccessAndFailure(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	successText := func(count int) string { return fmt.Sprintf("Copied %d chars", count) }
	var copied string
	m.copyTextToClipboardUsing("hello", successText, func(text string) error {
		copied = text
		return nil
	})
	if copied != "hello" || m.input.copyFeedbackError || m.input.copyFeedback != "Copied 5 chars" {
		t.Fatalf("success copy = %q, error=%t, feedback=%q", copied, m.input.copyFeedbackError, m.input.copyFeedback)
	}

	wantErr := errors.New("clipboard unavailable")
	m.copyTextToClipboardUsing("hello", successText, func(string) error { return wantErr })
	if !m.input.copyFeedbackError || !strings.Contains(m.input.copyFeedback, wantErr.Error()) || strings.Contains(m.input.copyFeedback, "Copied") {
		t.Fatalf("failed copy feedback = %q, error=%t", m.input.copyFeedback, m.input.copyFeedbackError)
	}
}

func TestClipboardFeedbackErrorUsesWarningMarkerInModal(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	m.modals.toolPager.open = true
	m.entries = []TranscriptEntry{{Kind: KindTool, ToolName: "read", ToolOutput: "contents"}}
	m.input.copyFeedback = "Copy failed: clipboard unavailable"
	m.input.copyFeedbackError = true
	m.input.copyFeedbackExpiresAt = time.Now().Add(time.Second)
	got := ansi.Strip(renderToolPagerModal(m, 80))
	if !strings.Contains(got, "! Copy failed: clipboard unavailable") || strings.Contains(got, "✓ Copy failed:") {
		t.Fatalf("modal copy feedback = %q, want warning marker rather than success check", got)
	}
}

func TestOlderClipboardTimerDoesNotClearNewFeedback(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	oldExpiry := time.Now().Add(-time.Second)
	newExpiry := time.Now().Add(time.Second)
	m.input.copyFeedback = "Copied newer text"
	m.input.copyFeedbackExpiresAt = newExpiry
	m.input.copyFeedbackError = true

	mod, _ := m.Update(clearCopyFeedbackMsg{at: oldExpiry})
	m = mod.(*tuiModel)
	if m.input.copyFeedback != "Copied newer text" || !m.input.copyFeedbackError || !m.input.copyFeedbackExpiresAt.Equal(newExpiry) {
		t.Fatalf("old timer cleared newer feedback: text=%q error=%t expires=%v", m.input.copyFeedback, m.input.copyFeedbackError, m.input.copyFeedbackExpiresAt)
	}
}
