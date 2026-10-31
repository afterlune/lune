package tui

import (
	"context"

	core "github.com/afterlune/luneCore"
)

// modalKind identifies the single overlay that owns the keyboard. Cards that
// belong to a running task (approval, question, plan review, recovery) are not
// modals: they are part of the transcript interaction and stay in place when an
// overlay opens.
type modalKind uint8

const (
	modalNone modalKind = iota
	modalShortcuts
	modalAgents
	modalSessions
	modalMCPPicker
	modalTheme
	modalModel
	modalPermission
	modalProcess
	modalToolPager
	modalDiff
)

// modalState owns every overlay the model can show at once: at most one of them
// is open, and opening another one closes the previous. Each overlay keeps its
// own payload; a set payload or open flag is what makes that overlay active.
type modalState struct {
	shortcuts bool
	sessions  sessionsModalState
	mcp       mcpModalState
	process   processModalState
	agents    agentsModalState
	toolPager pagerModalState
	diff      diffModalState

	theme      *ActiveThemePicker
	model      *ActiveModelPicker
	permission *ActivePermissionPicker
}

type sessionsModalState struct {
	open          bool
	items         []core.Session
	filtered      []core.Session
	filter        string
	index         int
	confirmDelete bool
	deletingID    string
	deleteMode    bool
}


type mcpModalState struct {
	picker         *ActiveMCPServerPicker
	authName       string
	authCancel     context.CancelFunc
	authGeneration uint64
	authRunner     func(context.Context, string) error
}

type processModalState struct {
	open     bool
	mode     processPickerMode
	items    []core.CommandInfo
	selected int
}

type agentsModalState struct {
	open     bool
	selected int
	detail   bool
	detailID string
}

type pagerModalState struct {
	open    bool
	index   int
	scrollY int
}

type diffModalState struct {
	open      bool
	files     []DiffFile
	fileIndex int
	scrollY   int
}

// active returns the overlay that currently owns the keyboard, in the order the
// layout renders them. The effort picker is deliberately absent: the layout
// renders it after the task cards so a pending approval or question stays
// visible. A running MCP authentication is a status panel, not an overlay, and is
// rendered in place without taking the keyboard.
func (s *modalState) active() modalKind {
	switch {
	case s.shortcuts:
		return modalShortcuts
	case s.agents.open:
		return modalAgents
	case s.sessions.open:
		return modalSessions
	case s.mcp.picker != nil:
		return modalMCPPicker
	case s.theme != nil:
		return modalTheme
	case s.model != nil:
		return modalModel
	case s.permission != nil:
		return modalPermission
	case s.process.open:
		return modalProcess
	case s.toolPager.open:
		return modalToolPager
	case s.diff.open:
		return modalDiff
	}
	return modalNone
}

// open closes every other overlay so the caller becomes the only one on screen.
// Callers assign their own payload right after calling it.
func (s *modalState) open() {
	s.close()
}

// close closes whichever overlay is open and drops its transient payload. Task
// cards, the autocomplete popup, the memory input mode and a running MCP
// authentication are owned elsewhere and survive.
func (s *modalState) close() {
	s.shortcuts = false
	s.sessions = sessionsModalState{}
	s.mcp.picker = nil
	s.process = processModalState{}
	s.agents = agentsModalState{}
	s.toolPager = pagerModalState{}
	s.diff = diffModalState{}
	s.theme = nil
	s.model = nil
	s.permission = nil
}
