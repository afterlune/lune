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
