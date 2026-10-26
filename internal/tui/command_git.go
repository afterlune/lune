package tui

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/afterlune/lune/internal/workspacereview"
	core "github.com/afterlune/luneCore"
)

// workspaceGitTimeout bounds every git call the diff viewer runs. A hung git
// must never leave the interface without an answer.
const workspaceGitTimeout = 5 * time.Second

// errWorkspaceGitTimeout reports a git call abandoned because it outlived
// workspaceGitTimeout.
var errWorkspaceGitTimeout = errors.New("git timed out")

// diffLoadedMsg carries the result of the asynchronous workspace diff.
type diffLoadedMsg struct {
	targetPath string
	files      []DiffFile
	err        error
}

// reviewMaterialsMsg carries the workspace review materials collected off the
// update goroutine.
type reviewMaterialsMsg struct {
	targetPath        string
	materials         string
	changed           bool
	bindingGeneration uint64
	err               error
}

// diffModalVisibleLines calculates the visible line capacity of the diff modal.
func (m *tuiModel) diffModalVisibleLines() int {
	if m.height <= 0 {
		return 10
	}
	lines := m.height - 12
	if lines > 20 {
		lines = 20
	}
	if lines < 5 {
		lines = 5
	}
	return lines
}

// openDiffModal starts loading the workspace diff. Git runs in a command so the
// key that opened the viewer never waits for it.
func (m *tuiModel) openDiffModal(targetPath string) (tea.Model, tea.Cmd) {
	workspace := m.workspace
	if workspace == "" && m.client != nil {
		workspace = m.client.Workspace()
	}
	m.input.autocompleteOpen = false
	return m, func() tea.Msg {
		files, err := loadWorkspaceDiff(context.Background(), workspace, targetPath)
		return diffLoadedMsg{targetPath: targetPath, files: files, err: err}
	}
}

// handleDiffLoaded opens the floating diff viewer, or reports why there is
// nothing to show.
func (m *tuiModel) handleDiffLoaded(msg diffLoadedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.addSystemEntry("Failed to run git diff: " + msg.err.Error())
		m.updateLayout()
		return m, nil
	}
	if len(msg.files) == 0 {
		if msg.targetPath != "" {
			m.addSystemEntry(fmt.Sprintf("No changes found for: %s", msg.targetPath))
		} else {
			m.addSystemEntry("No git changes detected in workspace.")
		}
		m.updateLayout()
		return m, nil
	}

	m.modals.open()
	m.modals.diff.open = true
	m.modals.diff.files = msg.files
	m.modals.diff.fileIndex = 0
	m.modals.diff.scrollY = 0

	m.updateLayout()
	return m, nil
}

// loadWorkspaceDiff reads the working tree diff and falls back to the staged
// diff when the working tree is clean.
func loadWorkspaceDiff(ctx context.Context, workspace, targetPath string) ([]DiffFile, error) {
	args := []string{"diff"}
	if targetPath != "" {
		args = append(args, "--", targetPath)
	}
	out, err := runWorkspaceGit(ctx, workspace, args...)
	if err != nil {
		return nil, err
	}
	diffText := strings.TrimSpace(out)
	if diffText == "" && targetPath == "" {
		stagedOut, stagedErr := runWorkspaceGit(ctx, workspace, "diff", "--cached")
		if stagedErr == nil {
			diffText = strings.TrimSpace(stagedOut)
		}
	}
	if diffText == "" {
		return nil, nil
	}
	return parseGitDiff(diffText), nil
}

// runWorkspaceGit runs one git command bounded by workspaceGitTimeout.
func runWorkspaceGit(ctx context.Context, workspace string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, workspaceGitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = workspace
	out, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("git %s: %w after %s", strings.Join(args, " "), errWorkspaceGitTimeout, workspaceGitTimeout)
		}
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// handleReviewCommand starts collecting the workspace review materials.
func (m *tuiModel) handleReviewCommand(parts []string) (tea.Model, tea.Cmd) {
	if m.taskBusy() {
		m.addSystemEntry("Workspace review is unavailable while a task is running or queued.")
		return m, nil
	}
	workspace := m.workspace
	if strings.TrimSpace(workspace) == "" && m.client != nil {
		workspace = m.client.Workspace()
	}
	if workspace == "" {
		m.addSystemEntry("Code review is unavailable because the workspace path is empty.")
		return m, nil
	}
	targetPath := ""
	if len(parts) > 1 {
		targetPath = strings.Join(parts[1:], " ")
	}
	bindingGeneration := m.bindingGeneration
	return m, func() tea.Msg {
		materials, changed, err := workspacereview.CollectMaterials(workspace, targetPath)
		return reviewMaterialsMsg{targetPath: targetPath, materials: materials, changed: changed, bindingGeneration: bindingGeneration, err: err}
	}
}

// handleReviewMaterials submits the review turn once the materials are ready.
func (m *tuiModel) handleReviewMaterials(msg reviewMaterialsMsg) (tea.Model, tea.Cmd) {
	if !m.matchesBinding(msg.bindingGeneration) {
		return m, nil
	}
	if msg.err != nil {
		m.addSystemEntry("Could not prepare workspace review: " + msg.err.Error())
		m.updateLayout()
		return m, nil
	}
	if !msg.changed {
		m.addSystemEntry("No text changes to review. Git-ignored and binary files are excluded.")
		m.updateLayout()
		return m, nil
	}
	if m.runtime == nil {
		m.addSystemEntry("Code review requires an active session runtime.")
		m.updateLayout()
		return m, nil
	}
	prompt := workspacereview.Prompt(msg.materials, msg.targetPath)
	return m.submitTurnWithOptions(prompt, core.PermissionReadOnly, false, false)
}
