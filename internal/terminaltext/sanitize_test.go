package terminaltext

import "testing"

func TestSanitizeRemovesTerminalControls(t *testing.T) {
	input := "before\x1b[2J\x1b]0;title\a\u009b31m\u009d52;c;payload\u009c\rline\tend\n"
	want := "before[2J]0;title31m52;c;payloadline    end\n"
	if got := Sanitize(input); got != want {
		t.Fatalf("Sanitize() = %q, want %q", got, want)
	}
}

func TestSanitizeHandlesControlSequencesSplitAcrossChunks(t *testing.T) {
	first := Sanitize("text\x1b[")
	second := Sanitize("2Jstill safe")
	if got, want := first+second, "text[2Jstill safe"; got != want {
		t.Fatalf("sanitized chunks = %q, want %q", got, want)
	}
}
