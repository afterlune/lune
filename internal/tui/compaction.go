package tui

import (
	"fmt"
	"time"

	core "github.com/afterlune/luneCore"
)

// handleCompactionEvent keeps the footer honest while Core compacts the context:
// the session totals never change, the context budget switches to the compacted
// estimate as soon as Core reports it, and manual compaction markers survive until
// the next authoritative usage report.
func (m *tuiModel) handleCompactionEvent(details core.ContextCompactionDetails) {
	mode := details.Mode
	if mode == "" {
		mode = "local"
	}
	switch details.Stage {
	case "started":
		m.compacting = true
		m.compactionStartedAt = time.Now()
		main := m.ensureAgentMetrics("lead")
		main.Activity = "Compacting context"
	case "completed":
		m.compacting = false
		if details.AfterTokens > 0 {
			m.estimatedInputTokens = details.AfterTokens
			m.lastInputTokens = 0
			m.lastCachedTokens = 0
		}
		if m.running {
			m.ensureAgentMetrics("lead").Activity = "Thinking"
		}
		m.addSystemEntry(formatCompactionCompleted(details, mode))
	case "fallback":
		m.compacting = false
		m.addSystemEntry("Provider compaction unavailable; fell back to a local summary.")
	case "skipped":
		m.compacting = false
		if details.Err != "" {
			m.addSystemEntry("Automatic compaction skipped: " + details.Err)
		} else {
			m.addSystemEntry("Automatic compaction skipped.")
		}
	case "failed":
		m.compacting = false
		if details.Err != "" {
			m.addSystemEntry("Context compaction failed: " + details.Err)
		} else {
			m.addSystemEntry("Context compaction failed.")
		}
	default:
		return
	}
	m.updateLayout()
}

// formatCompactionCompleted describes a finished compaction without inventing
// numbers Core did not report (server compaction has no after-token count).
func formatCompactionCompleted(details core.ContextCompactionDetails, mode string) string {
	before, after := details.BeforeTokens, details.AfterTokens
	switch {
	case mode == "server" && before > 0:
		return fmt.Sprintf("Context compacted by the provider: was %s tokens.", formatCompactTokens(before))
	case mode == "server":
		return "Context compacted by the provider."
	case before > 0 && after > 0:
		return fmt.Sprintf("Context compacted: %s → %s tokens (local summary).",
			formatCompactTokens(before), formatCompactTokens(after))
	default:
		return "Context compacted (local summary)."
	}
}

// compactionSettingsLabel describes the project's auto-compaction policy in the
// diagnostics panels; it is display-only and never writes configuration.
func (m *tuiModel) compactionSettingsLabel() string {
	if m.client == nil {
		return ""
	}
	defaults := m.client.ProjectContextDefaults()
	if !defaults.AutoCompact {
		return "off"
	}
	ratio := defaults.AutoCompactThreshold
	if ratio <= 0 {
		ratio = core.DefaultAutoCompactThreshold
	}
	window := defaults.ContextWindow
	if window <= 0 {
		window = m.contextWindow
	}
	threshold := compactionThresholdTokens(window, defaults.ReserveOutputTokens, ratio)
	return fmt.Sprintf("on at %.0f%% (%s tokens)", ratio*100, formatTokens(threshold))
}

// compactionThresholdTokens mirrors Core's auto-compaction threshold so the
// diagnostics panels can show when the next automatic pass will start.
func compactionThresholdTokens(window, reserve int64, ratio float64) int64 {
	usable := window - reserve
	if usable <= 0 || ratio <= 0 {
		return 0
	}
	threshold := int64(float64(usable) * ratio)
	if threshold < 1 {
		return 1
	}
	return threshold
}
