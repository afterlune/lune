//go:build !windows

package main

import "github.com/afterlune/lune/internal/desktop"

func desktopWindowSizeInterceptor(*desktop.DesktopService) func(uintptr, uint32, uintptr, uintptr) (uintptr, bool) {
	return nil
}
