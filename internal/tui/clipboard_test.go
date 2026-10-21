package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

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
