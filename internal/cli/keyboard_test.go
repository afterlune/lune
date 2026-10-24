package cli

import (
	"testing"
	"time"

	"github.com/eiannone/keyboard"
)

func restoreKeyboardSeams(t *testing.T) {
	t.Helper()
	originalStat, originalOpen, originalClose := stdinIsCharacterDevice, openCLIKeys, closeCLIKeys
	t.Cleanup(func() {
		stdinIsCharacterDevice, openCLIKeys, closeCLIKeys = originalStat, originalOpen, originalClose
	})
}

// TestKeyboardWatcherDeliversKeysAndStopsOnce covers E2: the watcher forwards
// keys, closes its channel on stop, and only closes the shared keyboard once.
func TestKeyboardWatcherDeliversKeysAndStopsOnce(t *testing.T) {
	restoreKeyboardSeams(t)
	stdinIsCharacterDevice = func() (bool, error) { return true, nil }
	keys := make(chan keyboard.KeyEvent, 2)
	openCLIKeys = func(int) (<-chan keyboard.KeyEvent, error) { return keys, nil }
	closed := 0
	closeCLIKeys = func() error {
		closed++
		// The real keyboard closes its key channel on Close; mirror that so the
		// watcher goroutine can finish.
		close(keys)
		return nil
	}

	events, stop, err := startKeyboardInputWatcher()
	if err != nil {
		t.Fatalf("startKeyboardInputWatcher: %v", err)
	}
	keys <- keyboard.KeyEvent{Key: keyboard.KeyEsc}
	select {
	case event := <-events:
		if !event.escape {
			t.Fatalf("escape event was not forwarded: %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("the watcher did not forward a pending key")
	}

	stop()
	if closed != 1 {
		t.Fatalf("keyboard close calls = %d, want 1", closed)
	}
	if _, ok := <-events; ok {
		t.Fatal("the event channel must close when the watcher stops")
	}
	stop()
	if closed != 1 {
		t.Fatalf("keyboard close calls = %d after a repeated stop, want 1", closed)
	}
}

// TestKeyboardWatcherSkipsNonInteractiveStdin keeps piped input working: no
// keyboard state is touched when stdin is not a terminal.
func TestKeyboardWatcherSkipsNonInteractiveStdin(t *testing.T) {
	restoreKeyboardSeams(t)
	stdinIsCharacterDevice = func() (bool, error) { return false, nil }
	opened := false
	openCLIKeys = func(int) (<-chan keyboard.KeyEvent, error) {
		opened = true
		return make(chan keyboard.KeyEvent), nil
	}
	closed := 0
	closeCLIKeys = func() error {
		closed++
		return nil
	}

	events, stop, err := startKeyboardInputWatcher()
	if err != nil {
		t.Fatalf("startKeyboardInputWatcher: %v", err)
	}
	if events != nil || opened || closed != 0 {
		t.Fatalf("piped stdin must not open the keyboard: events=%v opened=%v closed=%d", events != nil, opened, closed)
	}
	stop()
	if closed != 0 {
		t.Fatalf("keyboard close calls = %d, want 0", closed)
	}
}
