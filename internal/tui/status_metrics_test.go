package tui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	core "github.com/afterlune/luneCore"
	"github.com/charmbracelet/x/ansi"
)

func TestFooterLiveTokenDiagnostics(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	m.usage.estimatedInputTokens = 12400
	m.usage.contextWindow = 200000
	m.usage.totalInputTokens = 15000
	m.usage.cachedTokens = 12750 // 85% cache
	m.usage.totalOutputTokens = 9500
	m.usage.lastTokensPerSec = 48.5

	summary := formatContextAndTokenSummary(m, 120)
	if !strings.Contains(summary, "Context: 12.4k/200k (6.2%)") {
		t.Fatalf("expected used/total ctx in summary, got: %q", summary)
	}
	if !strings.Contains(summary, "Cache: 85%") {
		t.Fatalf("expected cache 85%% in summary, got: %q", summary)
	}
	if !strings.Contains(summary, "48.5 tok/s") {
		t.Fatalf("expected 48.5 tok/s in summary, got: %q", summary)
	}
	if !strings.Contains(summary, "Total: 24.5k tok") {
		t.Fatalf("expected Total: 24.5k tok in summary, got: %q", summary)
	}

	footer := ansi.Strip(renderFooter(m, 120))
	if !strings.Contains(footer, "Context: 12.4k/200k (6.2%)") {
		t.Fatalf("expected footer line 2 to show context metrics, got:\n%s", footer)
	}
	if !strings.Contains(footer, "48.5 tok/s") || !strings.Contains(footer, "Total: 24.5k tok") {
		t.Fatalf("expected footer line 2 to show speed and total, got:\n%s", footer)
	}
}

func TestModelDisplayName(t *testing.T) {
	m := newTUIModel(nil, nil, "sess-1")
	m.currentModel = "claude-3-7-sonnet"

	// When no client or profiles loaded, fallback to currentModel
	if got := m.currentModelDisplayName(); got != "claude-3-7-sonnet" {
		t.Fatalf("expected fallback model name, got: %q", got)
	}
}

func TestContextMetricsUseLatestPromptNotCumulative(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	m.usage.contextWindow = 1000000
	m.usage.lastInputTokens = 42000
	m.usage.totalInputTokens = 794000
	m.usage.totalOutputTokens = 12000
	m.usage.cachedTokens = 754300 // ~95%
	m.usage.lastTokensPerSec = 20.4

	summary := formatContextAndTokenSummary(m, 120)
	if !strings.Contains(summary, "Context: 42k/1M (4.2%)") {
		t.Fatalf("expected Context to use lastInputTokens (42k/1M (4.2%%)), got: %q", summary)
	}
	if !strings.Contains(summary, "Total: 806k tok") {
		t.Fatalf("expected Total to show cumulative sum (806k tok), got: %q", summary)
	}
	if !strings.Contains(summary, "Cache: 95%") {
		t.Fatalf("expected Cache: 95%%, got: %q", summary)
	}
}

func TestEffortPickerPersistsWithoutSwitchingModelProfile(t *testing.T) {
	tmpDir := t.TempDir()
	projectConfig := `{"models":{"providers":{"host":{"protocol":"responses","baseURL":"https://example.invalid","apiKey":"test-key"}},"profiles":{"alpha":{"provider":"host","model":"shared-model","displayName":"Alpha"},"beta":{"provider":"host","model":"shared-model","displayName":"Beta"}},"defaultProfile":"alpha"},"runtime":{"maxParallelAgents":8}}`
	if err := os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte(projectConfig), 0600); err != nil {
		t.Fatal(err)
	}
	client, err := core.New(core.Config{Workspace: tmpDir})
	if err != nil {
		t.Fatalf("failed to create core: %v", err)
	}
	defer client.Close()

	ctx := context.Background()
	sess, err := client.CreateSession(ctx, json.RawMessage("null"))
	if err != nil {
		t.Fatal(err)
	}
	m := newTUIModel(client, nil, sess.ID)

	// Both profiles run the same model ID; the footer must still identify the
	// active profile, not whichever profile happens to sort first.
	m.handleSlashCommand("/model beta")
	if m.currentModelProfile != "beta" {
		t.Fatalf("expected active profile beta, got %q", m.currentModelProfile)
	}
	if got := m.currentModelDisplayName(); got != "Beta" {
		t.Fatalf("expected display name Beta, got %q", got)
	}

	m.handleSlashCommand("/effort")
	for i := 0; i < 5; i++ {
		m.handleKeyPress(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	m.handleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})

	saved, err := client.GetSession(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Settings.ModelProfile != "beta" {
		t.Fatalf("effort change must not switch model profile, got %q", saved.Settings.ModelProfile)
	}
	if saved.Settings.ReasoningEffort != "high" {
		t.Fatalf("expected persisted effort high, got %q", saved.Settings.ReasoningEffort)
	}
	footer := ansi.Strip(renderFooter(m, 300))
	if !strings.Contains(footer, "Beta . high") {
		t.Fatalf("expected footer to show model and effort, got:\n%s", footer)
	}
	if !strings.Contains(footer, "agents 0 active · disabled") {
		t.Fatalf("expected footer to show delegation disabled despite scheduler concurrency, got:\n%s", footer)
	}
}

func TestEffortPickerRendering(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	m.reasoningEffort = "high"
	m.effortPicker = &ActiveEffortPicker{Selected: 4}
	rendered := ansi.Strip(renderEffortPicker(m, 80))
	for _, want := range []string{"Reasoning effort", "5. high", "✓ current", "enter apply"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected effort picker to contain %q, got:\n%s", want, rendered)
		}
	}
}

func TestFooterShowsReasoningEffortBesideModel(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	m.currentModel = "deepseek-flash"
	m.currentModelProfile = "deepseek-flash"
	m.reasoningEffort = "high"
	m.width = 140
	footer := ansi.Strip(renderFooter(m, 140))
	if !strings.Contains(footer, "deepseek-flash . high") {
		t.Fatalf("expected reasoning effort beside the model name, got:\n%s", footer)
	}

	m.reasoningEffort = ""
	footer = ansi.Strip(renderFooter(m, 140))
	if !strings.Contains(footer, "deepseek-flash . max") {
		t.Fatalf("expected max effort label, got:\n%s", footer)
	}
}

func TestEffortCommandOpensInteractivePicker(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")

	// 1. Initial effort is max.
	if got := m.currentReasoningEffort(); got != "max" {
		t.Fatalf("expected max effort initially, got: %q", got)
	}

	// 2. /effort without arguments opens the interactive picker instead of printing usage.
	m.handleSlashCommand("/effort")
	if m.effortPicker == nil {
		t.Fatalf("expected /effort to open the interactive effort picker")
	}
	if m.effortPicker.Selected != 6 {
		t.Fatalf("expected picker to start at current level (max), got %d", m.effortPicker.Selected)
	}
	if len(m.entries) != 0 {
		t.Fatalf("expected no system entry when opening the picker, got: %+v", m.entries)
	}

	// 3. Navigate down from max to "high" (index 4) and apply with Enter.
	for i := 0; i < 5; i++ {
		m.handleKeyPress(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	if m.effortPicker == nil || m.effortPicker.Selected != 4 {
		t.Fatalf("expected picker selection at high, got %+v", m.effortPicker)
	}
	m.handleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.effortPicker != nil {
		t.Fatalf("expected picker to close after applying")
	}
	if m.reasoningEffort != "high" {
		t.Fatalf("expected reasoningEffort 'high', got: %q", m.reasoningEffort)
	}
	last := m.entries[len(m.entries)-1]
	if !strings.Contains(last.Text, "Reasoning effort set to `high`") {
		t.Fatalf("expected set confirmation, got: %q", last.Text)
	}

	// 4. Reopening the picker selects the active level.
	m.handleSlashCommand("/effort")
	if m.effortPicker == nil || m.effortPicker.Selected != 4 {
		t.Fatalf("expected picker to remember active level high, got %+v", m.effortPicker)
	}
	m.handleKeyPress(tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.effortPicker != nil {
		t.Fatalf("expected Esc to close the picker")
	}

	// 5. "default" is no longer an available reasoning effort.
	m.handleSlashCommand("/effort default")
	if m.reasoningEffort != "high" {
		t.Fatalf("invalid effort changed the current value: %q", m.reasoningEffort)
	}
	last = m.entries[len(m.entries)-1]
	if !strings.Contains(last.Text, "Invalid reasoning effort `default`") {
		t.Fatalf("expected default effort to be rejected, got: %q", last.Text)
	}

	// 6. Invalid effort level
	m.handleSlashCommand("/effort super-turbo")
	last = m.entries[len(m.entries)-1]
	if !strings.Contains(last.Text, "Invalid reasoning effort `super-turbo`") {
		t.Fatalf("expected invalid effort error, got: %q", last.Text)
	}
}

func TestEstimateDeltaTokens(t *testing.T) {
	// Empty delta
	if got := estimateDeltaTokens(""); got != 0 {
		t.Fatalf("expected 0 tokens for empty, got %d", got)
	}

	// ASCII English: "Hello world" (11 chars) -> approx 2-5 tokens
	if got := estimateDeltaTokens("Hello world"); got < 2 || got > 5 {
		t.Fatalf("expected 2-5 tokens for 'Hello world', got %d", got)
	}

	// Chinese: "你好世界" (4 chars) -> approx 5-7 tokens (average 1.4 per char)
	if got := estimateDeltaTokens("你好世界"); got < 5 || got > 7 {
		t.Fatalf("expected 5-7 tokens for '你好世界', got %d", got)
	}
}

func TestStreamingGenerationSpeedCalculation(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	m.run.running = true
	// Simulate user submit 5 seconds ago (e.g. prompt overhead, queue, network TTFT)
	m.run.turnStreamStartTime = time.Now().Add(-5 * time.Second)

	// Stream first token arrives now (100 tokens generated in 0.4s)
	m.run.streamFirstTokenTime = time.Now().Add(-400 * time.Millisecond)

	// Usage event arrives with 100 output tokens
	usageEvent := core.Event{
		Kind: core.EventUsage,
		Usage: core.Usage{
			OutputTokens: 100,
		},
	}
	res, _ := m.Update(runtimeEventMsg{event: usageEvent})
	updated := res.(*tuiModel)

	// 100 tokens / 0.4s = 250 tok/s! Should NOT be 100 / 5.4s = 18 tok/s!
	if updated.usage.lastTokensPerSec < 200.0 || updated.usage.lastTokensPerSec > 300.0 {
		t.Fatalf("expected generation speed ~250 tok/s, got %.1f tok/s", updated.usage.lastTokensPerSec)
	}
}
