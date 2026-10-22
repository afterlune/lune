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
	if !m.copiedViaTerminal {
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
	if m.copiedViaTerminal || out.Len() != 0 {
		t.Fatalf("host clipboard path wrote terminal output: copiedViaTerminal=%t out=%q", m.copiedViaTerminal, out.String())
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
	if copied != "hello" || m.copyFeedbackError || m.copyFeedback != "Copied 5 chars" {
		t.Fatalf("success copy = %q, error=%t, feedback=%q", copied, m.copyFeedbackError, m.copyFeedback)
	}

	wantErr := errors.New("clipboard unavailable")
	m.copyTextToClipboardUsing("hello", successText, func(string) error { return wantErr })
	if !m.copyFeedbackError || !strings.Contains(m.copyFeedback, wantErr.Error()) || strings.Contains(m.copyFeedback, "Copied") {
		t.Fatalf("failed copy feedback = %q, error=%t", m.copyFeedback, m.copyFeedbackError)
	}
}

func TestClipboardFeedbackErrorUsesWarningMarkerInModal(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	m.toolPagerOpen = true
	m.entries = []TranscriptEntry{{Kind: KindTool, ToolName: "read", ToolOutput: "contents"}}
	m.copyFeedback = "Copy failed: clipboard unavailable"
	m.copyFeedbackError = true
	m.copyFeedbackExpiresAt = time.Now().Add(time.Second)
	got := ansi.Strip(renderToolPagerModal(m, 80))
	if !strings.Contains(got, "! Copy failed: clipboard unavailable") || strings.Contains(got, "✓ Copy failed:") {
		t.Fatalf("modal copy feedback = %q, want warning marker rather than success check", got)
	}
}

func TestOlderClipboardTimerDoesNotClearNewFeedback(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	oldExpiry := time.Now().Add(-time.Second)
	newExpiry := time.Now().Add(time.Second)
	m.copyFeedback = "Copied newer text"
	m.copyFeedbackExpiresAt = newExpiry
	m.copyFeedbackError = true

	mod, _ := m.Update(clearCopyFeedbackMsg{at: oldExpiry})
	m = mod.(*tuiModel)
	if m.copyFeedback != "Copied newer text" || !m.copyFeedbackError || !m.copyFeedbackExpiresAt.Equal(newExpiry) {
		t.Fatalf("old timer cleared newer feedback: text=%q error=%t expires=%v", m.copyFeedback, m.copyFeedbackError, m.copyFeedbackExpiresAt)
	}
}
