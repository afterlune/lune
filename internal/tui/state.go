package tui

import (
	"fmt"
	"image/color"
	"strings"
)

type visualState int

const (
	stateIdle visualState = iota
	stateThinking
	stateWorking
	stateApproval
	stateResult
	stateWarning
)

func stateName(state visualState) string {
	switch state {
	case stateThinking:
		return "Thinking"
	case stateWorking:
		return "Working"
	case stateApproval:
		return "Needs approval"
	case stateResult:
		return "Result ready"
	case stateWarning:
		return "Paused"
	default:
		return "Ready"
	}
}

func (m *tuiModel) visualState() visualState {
	if m.pendingApproval != nil {
		return stateApproval
	}
	if m.running {
		for index := len(m.activities) - 1; index >= 0; index-- {
			if m.activities[index].status == "running" {
				return stateWorking
			}
		}
		return stateThinking
	}
	status := strings.ToLower(m.status)
	if m.pending || strings.Contains(status, "failed") || strings.Contains(status, "paused") || strings.Contains(status, "cancelled") {
		return stateWarning
	}
	if strings.Contains(status, "completed") || m.planReview {
		return stateResult
	}
	return stateIdle
}

func backgroundColor(value string) color.Color {
	rgb := parseHexColor(value)
	return color.RGBA{R: rgb[0], G: rgb[1], B: rgb[2], A: 0xff}
}

func parseHexColor(value string) [3]uint8 {
	var out [3]uint8
	if len(value) != 7 || value[0] != '#' {
		return out
	}
	_, _ = fmt.Sscanf(value, "#%02x%02x%02x", &out[0], &out[1], &out[2])
	return out
}
