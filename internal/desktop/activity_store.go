package desktop

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	_ "modernc.org/sqlite"
)

const maxStoredToolActivityBytes = 128 << 10

// ExecutionActivityView is the desktop-owned transcript of one Core execution.
// Tool arguments and details are deliberately kept out of Core's runtime log.
type ExecutionActivityView struct {
	ExecutionID      string             `json:"executionId"`
	UserMessageIndex int                `json:"userMessageIndex"`
	Status           string             `json:"status"`
	QueuePosition    int64              `json:"queuePosition,omitempty"`
	MessageText      string             `json:"messageText,omitempty"`
	MessageImages    []string           `json:"messageImages,omitempty"`
	StartedAt        time.Time          `json:"startedAt"`
	FinishedAt       *time.Time         `json:"finishedAt,omitempty"`
	Tools            []ToolActivityView `json:"tools"`
}

type ToolActivityView struct {
	ID           string                    `json:"id"`
	InvocationID string                    `json:"invocationId"`
	AgentID      string                    `json:"agentId,omitempty"`
	Name         string                    `json:"name"`
	Arguments    json.RawMessage           `json:"arguments,omitempty"`
	Details      *ToolExecutionDetailsView `json:"details,omitempty"`
	Status       string                    `json:"status"`
	DurationMs   int64                     `json:"durationMs,omitempty"`
	Error        string                    `json:"error,omitempty"`
	Truncated    bool                      `json:"truncated,omitempty"`
}

type ToolExecutionDetailsView struct {
	Output    string                 `json:"output,omitempty"`
	Command   *ActivityCommandOutput `json:"command,omitempty"`
	Changes   []ActivityFileChange   `json:"changes,omitempty"`
	Truncated bool                   `json:"truncated,omitempty"`
}

type ActivityCommandOutput struct {
	Text      string `json:"text"`
	Output    string `json:"output,omitempty"`
	Status    string `json:"status,omitempty"`
	ExitCode  *int   `json:"exitCode,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
	Error     string `json:"error,omitempty"`
}

type ActivityFileChange struct {
	Path      string             `json:"path"`
	Added     int                `json:"added"`
	Removed   int                `json:"removed"`
	Lines     []ActivityDiffLine `json:"lines,omitempty"`
	Truncated bool               `json:"truncated,omitempty"`
}

type ActivityDiffLine struct {
	Kind    string `json:"kind"`
	OldLine int    `json:"oldLine,omitempty"`
	NewLine int    `json:"newLine,omitempty"`
	Text    string `json:"text"`
}

type ActivityStore struct {
	db *sql.DB
}

type DesktopSessionRecord struct {
	ID             string
	Mode           string
	Workspace      string
	StateDir       string
	WorktreePath   string
	RepositoryRoot string
	Title          string
	UpdatedAt      time.Time
	Status         string
}

func OpenActivityStore(stateDir string) (*ActivityStore, error) {
	if strings.TrimSpace(stateDir) == "" {
		return nil, errors.New("activity store directory is required")
	}
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		return nil, fmt.Errorf("create activity store directory: %w", err)
	}
	db, err := sql.Open("sqlite", filepath.Join(stateDir, "desktop-activities.db"))
	if err != nil {
		return nil, fmt.Errorf("open desktop activity database: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err := db.PingContext(context.Background()); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("connect desktop activity database: %w", err)
	}
	if _, err := db.Exec(`
		PRAGMA busy_timeout = 5000;
		PRAGMA journal_mode = WAL;
		CREATE TABLE IF NOT EXISTS execution_activity_runs (
			session_id TEXT NOT NULL,
			execution_id TEXT NOT NULL,
			user_message_index INTEGER NOT NULL DEFAULT -1,
			status TEXT NOT NULL,
			started_at INTEGER NOT NULL,
			finished_at INTEGER,
			PRIMARY KEY(session_id, execution_id)
		);
		CREATE TABLE IF NOT EXISTS execution_activity_tools (
			session_id TEXT NOT NULL,
			execution_id TEXT NOT NULL,
			invocation_id TEXT NOT NULL,
			agent_id TEXT NOT NULL DEFAULT '',
			position INTEGER NOT NULL,
			payload BLOB NOT NULL,
			updated_at INTEGER NOT NULL,
			PRIMARY KEY(session_id, execution_id, invocation_id, agent_id)
		);
		CREATE TABLE IF NOT EXISTS execution_activity_messages (
			session_id TEXT NOT NULL,
			execution_id TEXT NOT NULL,
			text TEXT NOT NULL,
			images BLOB NOT NULL,
			status TEXT NOT NULL,
			queue_position INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY(session_id, execution_id)
		);
		CREATE TABLE IF NOT EXISTS desktop_sessions (
			session_id TEXT PRIMARY KEY,
			mode TEXT NOT NULL,
			workspace TEXT NOT NULL,
			state_dir TEXT NOT NULL,
			worktree_path TEXT NOT NULL,
			repository_root TEXT NOT NULL,
			title TEXT NOT NULL DEFAULT '',
			updated_at INTEGER NOT NULL,
			status TEXT NOT NULL DEFAULT 'idle'
		);
		CREATE INDEX IF NOT EXISTS execution_activity_runs_session
			ON execution_activity_runs(session_id, started_at, execution_id);
		CREATE INDEX IF NOT EXISTS execution_activity_tools_session
			ON execution_activity_tools(session_id, execution_id, position);
	`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("initialize desktop activity database: %w", err)
	}
	return &ActivityStore{db: db}, nil
}

func (s *ActivityStore) SaveDesktopSession(ctx context.Context, record DesktopSessionRecord) error {
	if s == nil || s.db == nil || strings.TrimSpace(record.ID) == "" || record.Mode != "worktree" || record.Workspace == "" || record.StateDir == "" || record.WorktreePath == "" || record.RepositoryRoot == "" {
		return errors.New("valid Worktree session metadata is required")
	}
	updatedAt := record.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = time.Now()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO desktop_sessions(session_id, mode, workspace, state_dir, worktree_path, repository_root, title, updated_at, status)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET
			mode=excluded.mode, workspace=excluded.workspace, state_dir=excluded.state_dir,
			worktree_path=excluded.worktree_path, repository_root=excluded.repository_root,
			title=excluded.title, updated_at=excluded.updated_at, status=excluded.status
	`, record.ID, record.Mode, record.Workspace, record.StateDir, record.WorktreePath, record.RepositoryRoot,
		record.Title, updatedAt.UnixNano(), record.Status)
	if err != nil {
		return fmt.Errorf("save Worktree session metadata: %w", err)
	}
	return nil
}

func (s *ActivityStore) DesktopSessions(ctx context.Context) ([]DesktopSessionRecord, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("activity store is not open")
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT session_id, mode, workspace, state_dir, worktree_path, repository_root, title, updated_at, status
		FROM desktop_sessions ORDER BY updated_at DESC, session_id
	`)
	if err != nil {
		return nil, fmt.Errorf("list Worktree sessions: %w", err)
	}
	defer rows.Close()
	var records []DesktopSessionRecord
	for rows.Next() {
		var record DesktopSessionRecord
		var updatedAt int64
		if err := rows.Scan(&record.ID, &record.Mode, &record.Workspace, &record.StateDir, &record.WorktreePath,
			&record.RepositoryRoot, &record.Title, &updatedAt, &record.Status); err != nil {
			return nil, fmt.Errorf("read Worktree session: %w", err)
		}
		record.UpdatedAt = time.Unix(0, updatedAt)
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list Worktree sessions: %w", err)
	}
	return records, nil
}

func (s *ActivityStore) DesktopSession(ctx context.Context, sessionID string) (DesktopSessionRecord, bool, error) {
	if s == nil || s.db == nil || strings.TrimSpace(sessionID) == "" {
		return DesktopSessionRecord{}, false, errors.New("activity store and session ID are required")
	}
	var record DesktopSessionRecord
	var updatedAt int64
	err := s.db.QueryRowContext(ctx, `
		SELECT session_id, mode, workspace, state_dir, worktree_path, repository_root, title, updated_at, status
		FROM desktop_sessions WHERE session_id = ?
	`, sessionID).Scan(&record.ID, &record.Mode, &record.Workspace, &record.StateDir, &record.WorktreePath,
		&record.RepositoryRoot, &record.Title, &updatedAt, &record.Status)
	if errors.Is(err, sql.ErrNoRows) {
		return DesktopSessionRecord{}, false, nil
	}
	if err != nil {
		return DesktopSessionRecord{}, false, fmt.Errorf("read Worktree session metadata: %w", err)
	}
	record.UpdatedAt = time.Unix(0, updatedAt)
	return record, true, nil
}

func (s *ActivityStore) UpdateDesktopSession(ctx context.Context, sessionID, title, status string, updatedAt time.Time) error {
	if s == nil || s.db == nil || strings.TrimSpace(sessionID) == "" {
		return errors.New("activity store and session ID are required")
	}
	if updatedAt.IsZero() {
		updatedAt = time.Now()
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE desktop_sessions SET title = ?, status = ?, updated_at = ? WHERE session_id = ?
	`, title, status, updatedAt.UnixNano(), sessionID)
	if err != nil {
		return fmt.Errorf("update Worktree session metadata: %w", err)
	}
	return nil
}

func (s *ActivityStore) DeleteDesktopSession(ctx context.Context, sessionID string) error {
	if s == nil || s.db == nil || strings.TrimSpace(sessionID) == "" {
		return errors.New("activity store and session ID are required")
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM desktop_sessions WHERE session_id = ?`, sessionID); err != nil {
		return fmt.Errorf("delete Worktree session metadata: %w", err)
	}
	return nil
}

func (s *ActivityStore) DeleteDesktopSessions(ctx context.Context, sessionIDs []string) error {
	if s == nil || s.db == nil || len(sessionIDs) == 0 {
		return errors.New("activity store and session IDs are required")
	}
	for _, sessionID := range sessionIDs {
		if strings.TrimSpace(sessionID) == "" {
			return errors.New("session ID cannot be empty")
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin Worktree session cleanup: %w", err)
	}
	defer tx.Rollback()
	for _, sessionID := range sessionIDs {
		for _, table := range []string{"execution_activity_tools", "execution_activity_messages", "execution_activity_runs", "desktop_sessions"} {
			if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE session_id = ?", sessionID); err != nil {
				return fmt.Errorf("delete %s for Worktree session: %w", table, err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit Worktree session cleanup: %w", err)
	}
	return nil
}

func (s *ActivityStore) SaveExecutionMessage(ctx context.Context, sessionID, executionID, text string, images []string, status string, queuePosition int64) error {
	if s == nil || s.db == nil || sessionID == "" || executionID == "" || status == "" {
		return errors.New("activity store, session, execution, and status are required")
	}
	encodedImages, err := json.Marshal(images)
	if err != nil {
		return fmt.Errorf("encode queued message images: %w", err)
	}
	var existingStatus string
	err = s.db.QueryRowContext(ctx, `SELECT status FROM execution_activity_runs WHERE session_id = ? AND execution_id = ?`, sessionID, executionID).Scan(&existingStatus)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("inspect queued message status: %w", err)
	}
	if isTerminalActivityStatus(existingStatus) && existingStatus != "failed" {
		return nil
	}
	if existingStatus != "" {
		status = existingStatus
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO execution_activity_messages(session_id, execution_id, text, images, status, queue_position)
		VALUES(?, ?, ?, ?, ?, ?)
		ON CONFLICT(session_id, execution_id) DO UPDATE SET
			text = excluded.text, images = excluded.images, status = excluded.status, queue_position = excluded.queue_position
	`, sessionID, executionID, text, encodedImages, status, queuePosition)
	if err != nil {
		return fmt.Errorf("save queued message for session: %w", err)
	}
	return nil
}

func (s *ActivityStore) UpdateExecutionMessageStatus(ctx context.Context, sessionID, executionID, status string, queuePosition int64) error {
	if s == nil || s.db == nil || sessionID == "" || executionID == "" || status == "" {
		return errors.New("activity store, session, execution, and status are required")
	}
	if status == "completed" || status == "interrupted" {
		_, err := s.db.ExecContext(ctx, `DELETE FROM execution_activity_messages WHERE session_id = ? AND execution_id = ?`, sessionID, executionID)
		if err != nil {
			return fmt.Errorf("delete completed queued message: %w", err)
		}
		return nil
	}
	_, err := s.db.ExecContext(ctx, `UPDATE execution_activity_messages SET status = ?, queue_position = ? WHERE session_id = ? AND execution_id = ?`, status, queuePosition, sessionID, executionID)
	if err != nil {
		return fmt.Errorf("update queued message status: %w", err)
	}
	return nil
}

func (s *ActivityStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *ActivityStore) UpsertExecution(ctx context.Context, sessionID, executionID string, userMessageIndex *int, status string) error {
	if s == nil || s.db == nil || sessionID == "" || executionID == "" || status == "" {
		return errors.New("activity store, session, execution, and status are required")
	}
	index := -1
	if userMessageIndex != nil {
		index = *userMessageIndex
	}
	now := time.Now().UTC()
	var finishedAt any
	if isTerminalActivityStatus(status) {
		finishedAt = now.UnixNano()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO execution_activity_runs(session_id, execution_id, user_message_index, status, started_at, finished_at)
		VALUES(?, ?, ?, ?, ?, ?)
		ON CONFLICT(session_id, execution_id) DO UPDATE SET
			user_message_index = CASE WHEN excluded.user_message_index >= 0 THEN excluded.user_message_index ELSE execution_activity_runs.user_message_index END,
			status = excluded.status,
			finished_at = CASE WHEN excluded.finished_at IS NOT NULL THEN excluded.finished_at ELSE execution_activity_runs.finished_at END
	`, sessionID, executionID, index, status, now.UnixNano(), finishedAt)
	if err != nil {
		return fmt.Errorf("save execution activity status: %w", err)
	}
	return nil
}

func (s *ActivityStore) AssociateExecution(ctx context.Context, sessionID, executionID string, userMessageIndex int, initialStatus string) error {
	if s == nil || s.db == nil || sessionID == "" || executionID == "" || userMessageIndex < 0 || initialStatus == "" {
		return errors.New("activity store, session, execution, message index, and status are required")
	}
	now := time.Now().UTC().UnixNano()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO execution_activity_runs(session_id, execution_id, user_message_index, status, started_at)
		VALUES(?, ?, ?, ?, ?)
		ON CONFLICT(session_id, execution_id) DO UPDATE SET
			user_message_index = excluded.user_message_index
	`, sessionID, executionID, userMessageIndex, initialStatus, now)
	if err != nil {
		return fmt.Errorf("associate execution with user message: %w", err)
	}
	return nil
}

func (s *ActivityStore) DeleteFromUserMessageIndexExcept(ctx context.Context, sessionID string, userMessageIndex int, keepExecutionID string) error {
	if s == nil || s.db == nil || sessionID == "" || userMessageIndex < 0 {
		return errors.New("activity store, session, and user message index are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin execution history cleanup: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM execution_activity_tools
		WHERE session_id = ? AND execution_id IN (
			SELECT execution_id FROM execution_activity_runs
			WHERE session_id = ? AND user_message_index >= ? AND execution_id <> ?
		)
	`, sessionID, sessionID, userMessageIndex, keepExecutionID); err != nil {
		return fmt.Errorf("delete tools for edited conversation branch: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM execution_activity_messages
		WHERE session_id = ? AND execution_id IN (
			SELECT execution_id FROM execution_activity_runs
			WHERE session_id = ? AND user_message_index >= ? AND execution_id <> ?
		)
	`, sessionID, sessionID, userMessageIndex, keepExecutionID); err != nil {
		return fmt.Errorf("delete pending messages for edited conversation branch: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM execution_activity_runs
		WHERE session_id = ? AND user_message_index >= ? AND execution_id <> ?
	`, sessionID, userMessageIndex, keepExecutionID); err != nil {
		return fmt.Errorf("delete executions for edited conversation branch: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit edited conversation activity cleanup: %w", err)
	}
	return nil
}

func (s *ActivityStore) UpsertTool(ctx context.Context, sessionID, executionID string, activity ToolActivityView) error {
	if s == nil || s.db == nil || sessionID == "" || executionID == "" || activity.Name == "" {
		return errors.New("activity store, session, execution, and tool name are required")
	}
	invocationID := activity.InvocationID
	if invocationID == "" {
		invocationID = activity.ID
	}
	if invocationID == "" {
		return errors.New("tool invocation identity is required")
	}
	activity.InvocationID = invocationID
	payload, err := boundedToolActivityPayload(activity)
	if err != nil {
		return fmt.Errorf("encode tool activity: %w", err)
	}
	now := time.Now().UTC().UnixNano()
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO execution_activity_tools(session_id, execution_id, invocation_id, agent_id, position, payload, updated_at)
		VALUES(?, ?, ?, ?, coalesce((SELECT max(position) + 1 FROM execution_activity_tools WHERE session_id = ? AND execution_id = ?), 0), ?, ?)
		ON CONFLICT(session_id, execution_id, invocation_id, agent_id) DO UPDATE SET
			payload = excluded.payload,
			updated_at = excluded.updated_at
	`, sessionID, executionID, invocationID, activity.AgentID, sessionID, executionID, payload, now)
	if err != nil {
		return fmt.Errorf("save tool activity: %w", err)
	}
	return nil
}

func (s *ActivityStore) ListSession(ctx context.Context, sessionID string) ([]ExecutionActivityView, error) {
	if s == nil || s.db == nil || sessionID == "" {
		return nil, errors.New("activity store and session are required")
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT execution_id, user_message_index, status, started_at, finished_at
		FROM execution_activity_runs WHERE session_id = ? ORDER BY started_at, execution_id
	`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list execution activity: %w", err)
	}
	defer rows.Close()
	var runs []ExecutionActivityView
	indexes := make(map[string]int)
	for rows.Next() {
		var run ExecutionActivityView
		var startedAt int64
		var finishedAt sql.NullInt64
		if err := rows.Scan(&run.ExecutionID, &run.UserMessageIndex, &run.Status, &startedAt, &finishedAt); err != nil {
			return nil, fmt.Errorf("read execution activity: %w", err)
		}
		run.StartedAt = time.Unix(0, startedAt).UTC()
		if finishedAt.Valid {
			finished := time.Unix(0, finishedAt.Int64).UTC()
			run.FinishedAt = &finished
		}
		run.Tools = []ToolActivityView{}
		indexes[run.ExecutionID] = len(runs)
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read execution activity rows: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close execution activity rows: %w", err)
	}

	messageRows, err := s.db.QueryContext(ctx, `
		SELECT execution_id, text, images, status, queue_position
		FROM execution_activity_messages WHERE session_id = ?
	`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list pending execution messages: %w", err)
	}
	for messageRows.Next() {
		var executionID, status string
		var text string
		var encodedImages []byte
		var position int64
		if err := messageRows.Scan(&executionID, &text, &encodedImages, &status, &position); err != nil {
			_ = messageRows.Close()
			return nil, fmt.Errorf("read pending execution message: %w", err)
		}
		index, found := indexes[executionID]
		if !found {
			continue
		}
		runs[index].MessageText = text
		runs[index].Status = status
		runs[index].QueuePosition = position
		if len(encodedImages) > 0 {
			if err := json.Unmarshal(encodedImages, &runs[index].MessageImages); err != nil {
				_ = messageRows.Close()
				return nil, fmt.Errorf("decode pending execution message images: %w", err)
			}
		}
	}
	if err := messageRows.Err(); err != nil {
		_ = messageRows.Close()
		return nil, fmt.Errorf("read pending execution messages: %w", err)
	}
	if err := messageRows.Close(); err != nil {
		return nil, fmt.Errorf("close pending execution messages: %w", err)
	}

	toolRows, err := s.db.QueryContext(ctx, `
		SELECT execution_id, payload FROM execution_activity_tools
		WHERE session_id = ? ORDER BY execution_id, position
	`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list tool activity: %w", err)
	}
	defer toolRows.Close()
	for toolRows.Next() {
		var executionID string
		var payload []byte
		if err := toolRows.Scan(&executionID, &payload); err != nil {
			return nil, fmt.Errorf("read tool activity: %w", err)
		}
		index, found := indexes[executionID]
		if !found {
			continue
		}
		var activity ToolActivityView
		if err := json.Unmarshal(payload, &activity); err != nil {
			return nil, fmt.Errorf("decode tool activity: %w", err)
		}
		runs[index].Tools = append(runs[index].Tools, activity)
	}
	if err := toolRows.Err(); err != nil {
		return nil, fmt.Errorf("read tool activity rows: %w", err)
	}
	if runs == nil {
		runs = []ExecutionActivityView{}
	}
	return runs, nil
}

func (s *ActivityStore) DeleteSession(ctx context.Context, sessionID string) error {
	if s == nil || s.db == nil || sessionID == "" {
		return errors.New("activity store and session id are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin desktop activity cleanup: %w", err)
	}
	defer tx.Rollback()
	for _, table := range []string{"execution_activity_tools", "execution_activity_messages", "execution_activity_runs"} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE session_id = ?", sessionID); err != nil {
			return fmt.Errorf("delete %s for session: %w", table, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit desktop activity cleanup: %w", err)
	}
	return nil
}

func isTerminalActivityStatus(status string) bool {
	switch status {
	case "completed", "failed", "interrupted", "cancelled", "discarded":
		return true
	default:
		return false
	}
}

func boundedToolActivityPayload(activity ToolActivityView) ([]byte, error) {
	encoded, err := json.Marshal(activity)
	if err != nil || len(encoded) <= maxStoredToolActivityBytes {
		return encoded, err
	}
	var value any
	if err := json.Unmarshal(encoded, &value); err != nil {
		return nil, err
	}
	root, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("tool activity payload is not an object")
	}
	root["truncated"] = true
	for target := maxStoredToolActivityBytes / 2; len(encoded) > maxStoredToolActivityBytes && target >= 32; target /= 2 {
		value = trimJSONStrings(value, target)
		encoded, err = json.Marshal(value)
		if err != nil {
			return nil, err
		}
	}
	if len(encoded) > maxStoredToolActivityBytes {
		minimal := ToolActivityView{
			ID: activity.ID, InvocationID: activity.InvocationID, AgentID: activity.AgentID,
			Name: activity.Name, Status: activity.Status, DurationMs: activity.DurationMs,
			Truncated: true,
		}
		encoded, err = json.Marshal(minimal)
	}
	return encoded, err
}

func trimJSONStrings(value any, maxBytes int) any {
	switch item := value.(type) {
	case map[string]any:
		for key, child := range item {
			item[key] = trimJSONStrings(child, maxBytes)
		}
		return item
	case []any:
		for index, child := range item {
			item[index] = trimJSONStrings(child, maxBytes)
		}
		return item
	case string:
		return truncateUTF8(item, maxBytes)
	default:
		return value
	}
}

func truncateUTF8(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	if maxBytes <= 0 {
		return "…"
	}
	const suffix = "…"
	limit := maxBytes - len(suffix)
	if limit <= 0 {
		return suffix
	}
	for limit > 0 && !utf8.RuneStart(value[limit]) {
		limit--
	}
	return value[:limit] + suffix
}
