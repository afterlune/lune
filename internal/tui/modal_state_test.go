package tui

import (
	"strings"
	"testing"

	core "github.com/afterlune/luneCore"
	"github.com/charmbracelet/x/ansi"
)

// TestModalStateKeepsOneOverlayOpen covers the invariant that replaced the three
// hand-written priority chains: opening an overlay closes every other one, so key
// routing and layout always agree on a single owner.
func TestModalStateKeepsOneOverlay(t *testing.T) {
	m := newTUIModel(nil, nil, "modal-session")

	openers := []struct {
		kind modalKind
		open func()
	}{
		{modalShortcuts, func() { m.modals.shortcuts = true }},
		{modalAgents, func() { m.modals.agents.open = true }},
		{modalSessions, func() { m.modals.sessions.open = true }},
		{modalMemoryClear, func() { m.modals.memory.clearConfirm = true }},
		{modalMemoryPicker, func() { m.modals.memory.picker = &ActiveMemoryPicker{} }},
		{modalMCPPicker, func() { m.modals.mcp.picker = &ActiveMCPServerPicker{} }},
		{modalTheme, func() { m.modals.theme = &ActiveThemePicker{} }},
		{modalModel, func() { m.modals.model = &ActiveModelPicker{} }},
		{modalPermission, func() { m.modals.permission = &ActivePermissionPicker{} }},
		{modalProcess, func() { m.modals.process.open = true }},
		{modalToolPager, func() { m.modals.toolPager.open = true }},
		{modalDiff, func() { m.modals.diff.open = true }},
	}

	for i, opener := range openers {
		if i > 0 {
			// Every overlay is opened through the same path in production code.
			m.modals.open()
		}
		opener.open()
		if got := m.modals.active(); got != opener.kind {
			t.Fatalf("active overlay = %v, want %v", got, opener.kind)
		}
	}

	m.modals.open()
	if got := m.modals.active(); got != modalNone {
		t.Fatalf("open() must clear the previous overlay, active = %v", got)
	}
	for _, opener := range openers {
		opener.open()
		m.modals.close()
		if got := m.modals.active(); got != modalNone {
			t.Fatalf("close() must clear the overlay, active = %v", got)
		}
	}
}

// TestModalOpenClosesEveryOtherOverlay covers the payload carried by each opening
// path: after open() only the caller's overlay survives.
func TestModalOpenClosesEveryOtherOverlay(t *testing.T) {
	m := newTUIModel(nil, nil, "modal-session")

	m.modals.shortcuts = true
	m.modals.diff.open = true
	m.modals.theme = &ActiveThemePicker{}

	m.modals.open()
	m.modals.toolPager.open = true

	if got := m.modals.active(); got != modalToolPager {
		t.Fatalf("active overlay = %v, want the tool pager", got)
	}
	if m.modals.shortcuts || m.modals.diff.open || m.modals.theme != nil {
		t.Fatalf("open() must close every other overlay: shortcuts=%v diff=%v theme=%v",
			m.modals.shortcuts, m.modals.diff.open, m.modals.theme != nil)
	}
}

// TestQuestionCardOutranksEffortPicker covers the preserved priority: a pending
// user question keeps the screen (and the keyboard) over the effort picker, which
// sits later in the overlay order.
func TestQuestionCardOutranksEffortPicker(t *testing.T) {
	m := newTUIModel(nil, nil, "modal-session")
	m.width, m.height = 120, 40
	m.effortPicker = &ActiveEffortPicker{Selected: 1}
	m.activeQuestion = &ActiveQuestion{
		Request: core.UserQuestionRequest{Questions: []core.UserQuestionItem{{
			Question: "Which file should I edit?",
			Options:  []core.UserQuestionOption{{Label: "main.go"}, {Label: "docs.md"}},
		}}},
	}

	overlay, height := m.overlaySection(100)
	if height <= 0 {
		t.Fatal("layout must reserve rows for the active card")
	}
	plain := ansi.Strip(overlay)
	if !strings.Contains(plain, "Which file should I edit?") {
		t.Fatalf("the question card must win over the effort picker, got %q", plain)
	}
	if strings.Contains(plain, "Reasoning effort") {
		t.Fatalf("the effort picker must stay hidden behind the question card, got %q", plain)
	}
}
