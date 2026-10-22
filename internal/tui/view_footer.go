package tui

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/afterlune/lune/internal/terminaltext"
	"github.com/charmbracelet/x/ansi"
)

// scrollPillText describes what the background task is doing so the user keeps
// the state visible while reading history above the live tail.
func scrollPillText(m *tuiModel) string {
	const back = "↓ Back to bottom · esc"
	if m == nil {
		return back
	}
	switch {
	case m.unseenCompletion != "":
		return m.unseenCompletion + " · " + back
	case m.running:
		if m.taskStartedAt.IsZero() {
			return "Working · " + back
		}
		return "Working " + formatDuration(time.Since(m.taskStartedAt)) + " · " + back
	case m.hasPausedTask():
		return "Paused · /continue or /discard · " + back
	}
	return back
}

// renderScrollPill renders the floating hint pill when scrolled away from bottom.
func renderScrollPill(m *tuiModel, width int) string {
	text := scrollPillText(m)
	styled := lipgloss.NewStyle().
		Foreground(lipgloss.Color(activeTheme().AccentBlue)).
		Background(lipgloss.Color(activeTheme().CardBg)).
		Padding(0, 2).
		Render(text)
	left := max(0, (width-lipgloss.Width(styled))/2)
	return strings.Repeat(" ", left) + styled
}

// renderComposerBand renders the input box with a soft gray card background covering at least 3 rows.
func renderComposerBand(inputView string, width int) string {
	th := activeTheme()
	lines := strings.Split(inputView, "\n")
	var padded []string
	cardStyle := lipgloss.NewStyle().
		Background(lipgloss.Color(th.CardBg)).
		Foreground(lipgloss.Color(th.TextPrimary))

	// Top padding line
	emptyRow := cardStyle.Render(strings.Repeat(" ", width))
	padded = append(padded, emptyRow)

	// Middle input line(s)
	for _, l := range lines {
		content := " " + l
		pad := max(0, width-lipgloss.Width(content))
		padded = append(padded, renderCardSurface(cardStyle, content+strings.Repeat(" ", pad)))
	}

	// Bottom padding line
	padded = append(padded, emptyRow)

	return strings.Join(padded, "\n")
}

// formatCompactTokens formats token counts into human-readable compact strings (e.g. 500, 12.4k, 200k, 1M).
func formatCompactTokens(n int64) string {
	if n < 0 {
		n = 0
	}
	if n < 1000 {
		return strconv.FormatInt(n, 10)
	}
	if n < 1_000_000 {
		k := float64(n) / 1000.0
		if k >= 100 || math.Mod(k, 1) == 0 {
			return fmt.Sprintf("%.0fk", k)
		}
		str := fmt.Sprintf("%.1f", k)
		return strings.TrimRight(strings.TrimRight(str, "0"), ".") + "k"
	}
	m := float64(n) / 1_000_000.0
	if m >= 100 || math.Mod(m, 1) == 0 {
		return fmt.Sprintf("%.0fM", m)
	}
	str := fmt.Sprintf("%.1f", m)
	return strings.TrimRight(strings.TrimRight(str, "0"), ".") + "M"
}

// formatContextAndTokenSummary produces the 6 live metrics string for the footer:
// 1. Used context
// 2. Total context window
// 3. Percentage
// 4. Cache hit rate (Cache: X%)
// 5. Generation speed (X.X tok/s)
// 6. Cumulative session total tokens (Total: X tok)
func formatContextAndTokenSummary(m *tuiModel, maxWidth int) string {
	usedCtx := m.lastInputTokens
	if usedCtx == 0 {
		usedCtx = m.estimatedInputTokens
	}
	totalCtx := m.contextWindow
	if totalCtx <= 0 {
		totalCtx = 200000 // Standard fallback context window
	}

	pct := 0.0
	if totalCtx > 0 {
		pct = float64(usedCtx) / float64(totalCtx) * 100.0
		if pct > 100.0 {
			pct = 100.0
		}
	}
	ctxStr := fmt.Sprintf("Context: %s/%s (%.1f%%)", formatCompactTokens(usedCtx), formatCompactTokens(totalCtx), pct)

	cachePct := 0.0
	if m.totalInputTokens > 0 {
		cachePct = float64(m.cachedTokens) / float64(m.totalInputTokens) * 100.0
		if cachePct > 100.0 {
			cachePct = 100.0
		}
	}
	cacheStr := fmt.Sprintf("Cache: %.0f%% session", cachePct)

	var speedStr string
	if m.lastTokensPerSec > 0 {
		speedStr = fmt.Sprintf("%.1f tok/s", m.lastTokensPerSec)
	} else {
		speedStr = "0.0 tok/s"
	}

	totalToks := m.totalInputTokens + m.totalOutputTokens
	totalStr := fmt.Sprintf("Total: %s tok", formatCompactTokens(totalToks))

	full := fmt.Sprintf("%s · %s · %s · %s", ctxStr, cacheStr, speedStr, totalStr)
	if maxWidth <= 0 || lipgloss.Width(full) <= maxWidth {
		return full
	}

	// Graceful degradation when footer width is limited
	withoutTotal := fmt.Sprintf("%s · %s · %s", ctxStr, cacheStr, speedStr)
	if lipgloss.Width(withoutTotal) <= maxWidth {
		return withoutTotal
	}

	withoutSpeed := fmt.Sprintf("%s · %s", ctxStr, cacheStr)
	if lipgloss.Width(withoutSpeed) <= maxWidth {
		return withoutSpeed
	}

	if lipgloss.Width(ctxStr) <= maxWidth {
		return ctxStr
	}

	compactCtx := fmt.Sprintf("%s/%s (%.1f%%)", formatCompactTokens(usedCtx), formatCompactTokens(totalCtx), pct)
	if lipgloss.Width(compactCtx) <= maxWidth {
		return compactCtx
	}

	return ansi.Truncate(full, maxWidth, "…")
}

// renderFooter renders the bottom two-line status bar.
func renderFooter(m *tuiModel, width int) string {
	// Right side: Permission / Plan mode
	var rightStr string
	if m.planMode {
		planLabel := stylePlanPurple().Bold(true).Render("Plan mode")
		exitHint := styleTextMuted().Render("(shift+tab to exit)")
		rightStr = planLabel + " " + exitHint
	} else {
		permLabel := styleTextPrimary().Bold(true).Render(string(m.permissionMode))
		planHint := styleTextMuted().Render(" · shift+tab plan")
		rightStr = permLabel + planHint
	}
	rightLen := lipgloss.Width(rightStr)

	// Keep the model and active subagent count visible; workspace and title are secondary.
	modelName := m.currentModelDisplayName()
	modelLabel := ""
	if modelName != "" {
		modelLabel = fmt.Sprintf("%s . %s", modelName, m.currentReasoningEffortLabel())
	}
	maxSubs := m.maxParallelAgents()
	activeSubs := m.activeSubagentsCount()
	var subagentLabel string
	if m.delegationEnabled() {
		subagentLabel = fmt.Sprintf("agents %d active · %d parallel", activeSubs, maxSubs)
	} else {
		subagentLabel = fmt.Sprintf("agents %d active · disabled", activeSubs)
	}
	maxLeftWidth := max(1, width-rightLen-2)
	primaryItems := []string{subagentLabel}
	if modelLabel != "" {
		primaryItems = []string{modelLabel, subagentLabel}
	}
	rawLeft := strings.Join(primaryItems, " · ")
	if lipgloss.Width(rawLeft) > maxLeftWidth {
		compactSubagentLabel := fmt.Sprintf("agents %d/%d", activeSubs, maxSubs)
		if !m.delegationEnabled() {
			compactSubagentLabel = fmt.Sprintf("agents %d · off", activeSubs)
		}
		modelWidth := maxLeftWidth - lipgloss.Width(compactSubagentLabel) - lipgloss.Width(" · ")
		if modelLabel != "" && modelWidth > 0 {
			modelLabel = ansi.Truncate(modelLabel, modelWidth, "…")
			rawLeft = modelLabel + " · " + compactSubagentLabel
		} else {
			rawLeft = compactSubagentLabel
			if lipgloss.Width(rawLeft) > maxLeftWidth {
				rawLeft = fmt.Sprintf("agents %d", activeSubs)
			}
			if lipgloss.Width(rawLeft) > maxLeftWidth {
				rawLeft = ansi.Truncate(fmt.Sprintf("%d", activeSubs), maxLeftWidth, "…")
			}
		}
	}
	remainingWidth := maxLeftWidth - lipgloss.Width(rawLeft)
	optionalItems := []string{m.workspace}
	title := m.sessionTitle
	if title == "" {
		title = deriveSessionTitleFromEntries(m.entries)
	}
	optionalItems = append(optionalItems, title)
	for _, item := range optionalItems {
		if item == "" || remainingWidth <= lipgloss.Width(" · ") {
			continue
		}
		itemWidth := min(lipgloss.Width(item), remainingWidth-lipgloss.Width(" · "))
		if itemWidth <= 0 {
			continue
		}
		shown := ansi.Truncate(item, itemWidth, "…")
		rawLeft += " · " + shown
		remainingWidth -= lipgloss.Width(" · ") + lipgloss.Width(shown)
	}
	leftStr := styleTextMuted().Render(rawLeft)
	if modelLabel != "" && strings.HasPrefix(rawLeft, modelLabel) {
		leftStr = styleTextPrimary().Bold(true).Render(modelLabel) +
			styleTextMuted().Render(strings.TrimPrefix(rawLeft, modelLabel))
	}

	leftLen := lipgloss.Width(leftStr)
	gap := max(1, width-leftLen-rightLen)
	line1 := leftStr + strings.Repeat(" ", gap) + rightStr

	// Line 2: 6 token & context diagnostic metrics (left) + contextual hint / shortcuts (right)
	var line2RightText string
	if m.running {
		line2RightText = "esc to interrupt"
		if m.totalSubagentsCount() > 0 {
			line2RightText += " · /agents"
		}
		if n := len(m.queuedTurns); n > 0 {
			line2RightText += fmt.Sprintf(" · %d queued", n)
		}
	} else if m.hasPausedTask() {
		line2RightText = "task paused · /continue or /discard"
		if n := len(m.queuedTurns); n > 0 {
			line2RightText += fmt.Sprintf(" · %d queued", n)
		}
	} else if n := len(m.queuedTurns); n > 0 {
		line2RightText = fmt.Sprintf("%d queued · /cancel-queue", n)
	} else if m.composer.Value() == "" && len(m.promptHistory) > 0 {
		line2RightText = "esc to edit last message"
	} else if m.totalSubagentsCount() > 0 || m.delegationEnabled() {
		line2RightText = "? for shortcuts · /agents to inspect"
	} else {
		line2RightText = "? for shortcuts"
	}

	line2Right := styleTextMuted().Render(line2RightText)
	right2Len := lipgloss.Width(line2Right)

	maxMetricsWidth := max(10, width-right2Len-2)
	metricsStr := formatContextAndTokenSummary(m, maxMetricsWidth)
	line2Left := styleTextMuted().Render(metricsStr)

	gap2 := max(1, width-lipgloss.Width(line2Left)-right2Len)
	line2 := line2Left + strings.Repeat(" ", gap2) + line2Right

	return line1 + "\n" + line2
}

func fitTerminalText(value string, width int) string {
	if width <= 0 {
		return ""
	}
	return ansi.Truncate(terminaltext.Sanitize(value), width, "…")
}
