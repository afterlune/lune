package desktop

import (
	"os/exec"

	"github.com/afterlune/lune/internal/toolchains"
)

// CommandToolchainCandidate is a host PATH lookup result.
type CommandToolchainCandidate = toolchains.Candidate

var commandToolchainNames = toolchains.CommonToolchainNames

func (s *DesktopService) DetectCommandToolchains() []CommandToolchainCandidate {
	return detectCommandToolchains(exec.LookPath)
}

func detectCommandToolchains(lookPath func(string) (string, error)) []CommandToolchainCandidate {
	return toolchains.DetectWith(lookPath)
}

func commandToolchainRoot(name, executable string) string {
	return toolchains.Root(name, executable)
}

func normalizeCommandToolchainPaths(paths []string) ([]string, error) {
	return toolchains.NormalizePaths(paths)
}

func availableCommandToolchainPaths(paths []string) []string {
	return toolchains.AvailablePaths(paths)
}
