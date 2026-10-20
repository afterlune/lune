package tui

import (
	"fmt"
	"testing"

	"github.com/charmbracelet/colorprofile"
)

func TestTerminalColorProfileProbeCommands(t *testing.T) {
	tests := []struct {
		name         string
		profile      colorprofile.Profile
		terminal     string
		wantCommands []string
	}{
		{
			name:         "probe limited VS Code terminal",
			profile:      colorprofile.ANSI256,
			terminal:     "vscode",
			wantCommands: []string{"RGB", "Tc"},
		},
		{
			name:     "skip terminals already reporting truecolor",
			profile:  colorprofile.TrueColor,
			terminal: "vscode",
		},
		{
			name:     "respect terminals with color disabled",
			profile:  colorprofile.ASCII,
			terminal: "vscode",
		},
		{
			name:     "skip Apple Terminal capability probe",
			profile:  colorprofile.ANSI,
			terminal: "Apple_Terminal",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			commands := terminalColorProfileProbeCommands(test.profile, test.terminal)
			if len(commands) != len(test.wantCommands) {
				t.Fatalf("got %d capability commands, want %d", len(commands), len(test.wantCommands))
			}
			for i, command := range commands {
				if got := fmt.Sprint(command()); got != test.wantCommands[i] {
					t.Errorf("command %d = %q, want %q", i, got, test.wantCommands[i])
				}
			}
		})
	}
}
