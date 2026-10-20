package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
)

func terminalColorProfileProbeCommands(profile colorprofile.Profile, terminalProgram string) []tea.Cmd {
	if profile <= colorprofile.ASCII || profile == colorprofile.TrueColor || strings.EqualFold(terminalProgram, "Apple_Terminal") {
		return nil
	}
	return []tea.Cmd{
		tea.RequestCapability("RGB"),
		tea.RequestCapability("Tc"),
	}
}
