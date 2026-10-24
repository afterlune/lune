package desktop

import (
	"errors"
	"fmt"
	"testing"
)

// TestIsCoreBusyError covers the single place that recognises luneCore's
// "task still running" refusal.
func TestIsCoreBusyError(t *testing.T) {
	if !isCoreBusyError(errors.New(coreBusyMarker)) {
		t.Fatal("the plain marker must be recognised")
	}
	wrapped := fmt.Errorf("close workspace: %w", errors.New("cannot close core while a task is running: exec-1"))
	if !isCoreBusyError(wrapped) {
		t.Fatal("a wrapped refusal must be recognised")
	}
	for _, err := range []error{nil, errors.New("core is already closed"), errors.New("permission denied")} {
		if isCoreBusyError(err) {
			t.Fatalf("%v must not be treated as a busy core", err)
		}
	}
}
