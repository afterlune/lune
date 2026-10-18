//go:build windows

package main

import (
	"math"
	"unsafe"

	"github.com/afterlune/lune/internal/desktop"
	"golang.org/x/sys/windows"
)

const (
	wmSize           = 0x0005
	wmExitSizeMove   = 0x0232
	gaRoot           = 2
	minTrackedWidth  = 320
	minTrackedHeight = 240
)

type windowRect struct {
	left, top, right, bottom int32
}

var (
	user32              = windows.NewLazySystemDLL("user32.dll")
	procGetWindowRect   = user32.NewProc("GetWindowRect")
	procGetAncestor     = user32.NewProc("GetAncestor")
	procGetDpiForWindow = user32.NewProc("GetDpiForWindow")
)

func desktopWindowSizeInterceptor(service *desktop.DesktopService) func(uintptr, uint32, uintptr, uintptr) (uintptr, bool) {
	tracker := newWindowSizeTracker(service)
	return func(hwnd uintptr, msg uint32, _, _ uintptr) (uintptr, bool) {
		if msg != wmSize && msg != wmExitSizeMove {
			return 0, false
		}
		root, _, _ := procGetAncestor.Call(hwnd, gaRoot)
		if root != hwnd {
			return 0, false
		}
		var rect windowRect
		ok, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&rect)))
		if ok == 0 {
			return 0, false
		}
		dpi, _, _ := procGetDpiForWindow.Call(hwnd)
		if dpi == 0 {
			return 0, false
		}
		// Wails window options are in DIPs, while GetWindowRect returns physical
		// pixels. Store DIPs so a 125%/150% display does not grow the window on
		// every launch.
		width := int(math.Round(float64(rect.right-rect.left) * 96 / float64(dpi)))
		height := int(math.Round(float64(rect.bottom-rect.top) * 96 / float64(dpi)))
		if width < minTrackedWidth || height < minTrackedHeight {
			return 0, false
		}
		tracker.record(width, height, msg == wmExitSizeMove)
		return 0, false
	}
}
