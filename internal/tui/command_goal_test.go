package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	core "github.com/afterlune/luneCore"
)

func newTestCoreClient(t *testing.T) *core.Core {
	t.Helper()
	tmpDir := t.TempDir()
	projectConfig := `{"models":{"providers":{"host":{"protocol":"responses","baseURL":"https://example.invalid","apiKey":"test-key"}},"profiles":{"primary":{"provider":"host","model":"test-model"}},"defaultProfile":"primary"},"runtime":{}}`
	if err := os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte(projectConfig), 0600); err != nil {
		t.Fatal(err)
	}
	client, err := core.New(core.Config{Workspace: tmpDir, Memory: core.MemoryConfig{Disabled: true}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = client.Close()
	})
	return client
}

func TestTUIGoalPlanModeRestrictions(t *testing.T) {
	client := newTestCoreClient(t)
	m := newTUIModel(client, nil, "test-sess")
	m.planMode = true

	// Creating a goal in plan mode should be blocked.
	m.handleSlashCommand("/goal Do something")
	lastSys := m.entries[len(m.entries)-1].Text
	if !strings.Contains(lastSys, "Exit Plan mode before starting or changing a goal.") {
		t.Fatalf("expected plan mode warning, got: %s", lastSys)
	}

	// But showing or pausing or clearing goal shouldn't trigger the plan mode blocker error
	m.handleSlashCommand("/goal")
	lastSys = m.entries[len(m.entries)-1].Text
	if strings.Contains(lastSys, "Exit Plan mode") {
		t.Fatalf("unexpected plan mode warning on /goal: %s", lastSys)
	}
}

func TestTUIGoalEditPrefill(t *testing.T) {
	client := newTestCoreClient(t)
	m := newTUIModel(client, nil, "test-sess")
	m.run.goal = &core.Goal{
		ID:        "goal-1",
		Status:    core.GoalPaused,
		Objective: "Refactor database models",
	}

	m.handleSlashCommand("/goal edit")
	composerVal := m.input.composer.Value()
	expected := "/goal edit Refactor database models"
	if composerVal != expected {
		t.Fatalf("composer value = %q, want %q", composerVal, expected)
	}

	// Active goal should disallow editing
	m.run.goal.Status = core.GoalActive
	m.handleSlashCommand("/goal edit")
	lastSys := m.entries[len(m.entries)-1].Text
	if !strings.Contains(lastSys, "Pause the goal before editing it.") {
		t.Fatalf("expected pause warning, got: %s", lastSys)
	}
}

func TestTUIGoalFooter(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	if footer := m.goalFooter(); footer != "" {
		t.Fatalf("expected empty footer when no goal, got: %s", footer)
	}

	budget := int64(100000)
	m.run.goal = &core.Goal{
		ID:          "goal-1",
		Status:      core.GoalActive,
		TokensUsed:  15000,
		TokenBudget: &budget,
	}

	footer := m.goalFooter()
	if !strings.Contains(footer, "goal active") || !strings.Contains(footer, "15k/100k") || !strings.Contains(footer, "/goal") {
		t.Fatalf("unexpected footer: %s", footer)
	}

	// When running and task matches goal ID
	m.run.running = true
	m.run.taskSnapshot.GoalID = "goal-1"
	footer = m.goalFooter()
	if !strings.Contains(footer, "esc to pause") {
		t.Fatalf("expected 'esc to pause' in running footer, got: %s", footer)
	}
}
