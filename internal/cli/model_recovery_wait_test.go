package cli

import (
	"strings"
	"testing"
	"time"

	core "github.com/afterlune/luneCore"
)

// TestModelRecoveryWaitCanBeCancelledWithEscape covers D1: the retry window no
// longer blocks the prompt, and Esc leaves the task paused instead of recovering
// it behind the user's back.
func TestModelRecoveryWaitCanBeCancelledWithEscape(t *testing.T) {
	modelRecovery := &core.PendingModelRecovery{
		ExecutionID: "execution-paused",
		Failure:     core.ModelFailure{RetryNotBefore: time.Now().Add(2 * time.Second)},
	}
	client := &fakeCLICore{pending: true, pendingRequests: []core.PendingRequest{{Kind: core.PendingModelRecoveryRequest, ModelRecovery: modelRecovery}}}

	keyEvents := make(chan cliInputEvent, 1)
	watchKeys := func() (<-chan cliInputEvent, func(), error) { return keyEvents, func() {}, nil }
	go func() {
		// The wait starts after the [r]etry line is consumed.
		time.Sleep(100 * time.Millisecond)
		keyEvents <- cliInputEvent{escape: true}
	}()

	start := time.Now()
	stdout, _ := runCLI(t, client, "/continue\nr\n/exit\n", watchKeys)
	elapsed := time.Since(start)

	if client.resumes != 0 {
		t.Fatalf("resumes = %d, want the cancelled retry to leave the task paused", client.resumes)
	}
	if elapsed >= 2*time.Second {
		t.Fatalf("the retry wait blocked for %s; Esc must cancel it", elapsed)
	}
	if !strings.Contains(stdout, "Retry cancelled") {
		t.Fatalf("stdout = %q, want a cancellation notice", stdout)
	}
}
