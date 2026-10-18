package main

import (
	"sync"
	"time"

	"github.com/afterlune/lune/internal/desktop"
)

type windowSizeTracker struct {
	service *desktop.DesktopService
	mu      sync.Mutex
	timer   *time.Timer
	width   int
	height  int
}

func newWindowSizeTracker(service *desktop.DesktopService) *windowSizeTracker {
	return &windowSizeTracker{service: service}
}

func (t *windowSizeTracker) record(width, height int, flush bool) {
	if t == nil || width <= 0 || height <= 0 {
		return
	}
	t.mu.Lock()
	t.width, t.height = width, height
	if t.timer != nil {
		t.timer.Stop()
	}
	delay := 350 * time.Millisecond
	if flush {
		delay = 20 * time.Millisecond
	}
	t.timer = time.AfterFunc(delay, t.save)
	t.mu.Unlock()
}

func (t *windowSizeTracker) save() {
	t.mu.Lock()
	width, height := t.width, t.height
	t.timer = nil
	t.mu.Unlock()
	if width <= 0 || height <= 0 || t.service == nil {
		return
	}
	_ = t.service.SaveWindowSize(width, height)
}
