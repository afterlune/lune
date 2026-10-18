package desktop

import (
	"errors"
	"log/slog"
)

const (
	defaultWindowWidth  = 1440
	defaultWindowHeight = 920
	minWindowWidth      = 980
	minWindowHeight     = 640
	maxWindowWidth      = 4096
	maxWindowHeight     = 2160
)

// WindowSize returns a valid persisted window size, falling back to the
// default when no usable value has been stored yet.
func (s *DesktopService) WindowSize() (int, int) {
	width, height := defaultWindowWidth, defaultWindowHeight
	if s == nil || s.store == nil {
		return width, height
	}
	restore, err := s.store.DesktopRestore()
	if err != nil {
		return width, height
	}
	return normalizeWindowSize(restore.WindowWidth, restore.WindowHeight)
}

// SaveWindowSize persists the outer window size reported by the frontend.
func (s *DesktopService) SaveWindowSize(width, height int) error {
	if width <= 0 || height <= 0 {
		return errors.New("窗口尺寸无效")
	}
	width, height = normalizeWindowSize(width, height)
	if s == nil || s.store == nil {
		return nil
	}
	return s.store.SaveWindowSize(width, height)
}

func (s *DesktopService) rememberWorkspace(path string) {
	if s == nil || s.store == nil || path == "" {
		return
	}
	if err := s.store.SaveLastWorkspace(path); err != nil {
		slog.Warn("remember desktop workspace", "workspace", path, "error", err)
	}
}

func (s *DesktopService) rememberSession(sessionID string) {
	if s == nil || s.store == nil || sessionID == "" {
		return
	}
	if err := s.store.SaveLastSession(sessionID); err != nil {
		slog.Warn("remember desktop session", "session_id", sessionID, "error", err)
	}
}

// ForgetDesktopLocation clears a stale workspace/session pair so the next
// launch starts from the welcome screen instead of retrying it forever.
func (s *DesktopService) ForgetDesktopLocation() error {
	if s == nil || s.store == nil {
		return nil
	}
	return s.store.ClearDesktopLocation()
}

func (s *DesktopService) clearRememberedLocation() {
	if s == nil || s.store == nil {
		return
	}
	if err := s.store.ClearDesktopLocation(); err != nil {
		slog.Warn("clear desktop restore location", "error", err)
	}
}

func normalizeWindowSize(width, height int) (int, int) {
	if width <= 0 {
		width = defaultWindowWidth
	}
	if height <= 0 {
		height = defaultWindowHeight
	}
	width = max(minWindowWidth, min(width, maxWindowWidth))
	height = max(minWindowHeight, min(height, maxWindowHeight))
	return width, height
}
