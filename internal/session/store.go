package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// Snapshot is the persisted state for one workspace session. Data is owned by
// the caller so the session package does not depend on the TUI or model layer.
type Snapshot struct {
	ID        string
	Workspace string
	UpdatedAt time.Time
	Data      json.RawMessage
}

type Store struct{ db *sql.DB }

func Open(path string) (*Store, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve session database path: %w", err)
	}
	filePath := filepath.ToSlash(absolute)
	if filepath.VolumeName(absolute) != "" {
		filePath = "/" + filePath
	}
	params := url.Values{}
	params.Set("_busy_timeout", "5000")
	params.Set("_journal_mode", "WAL")
	params.Set("_synchronous", "FULL")
	fileURL := url.URL{Scheme: "file", Path: filePath, RawQuery: params.Encode()}
	db, err := sql.Open("sqlite", fileURL.String())
	if err != nil {
		return nil, fmt.Errorf("open session database: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			workspace TEXT NOT NULL,
			updated_at INTEGER NOT NULL,
			data BLOB NOT NULL
		)`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("initialize session database: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS sessions_workspace_updated ON sessions(workspace, updated_at DESC)`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("initialize session index: %w", err)
	}
	if err := os.Chmod(absolute, 0600); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("secure session database permissions: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) Save(ctx context.Context, snapshot Snapshot) error {
	if s == nil || s.db == nil {
		return errors.New("session store is not open")
	}
	if snapshot.ID == "" || snapshot.Workspace == "" {
		return errors.New("session id and workspace are required")
	}
	if !json.Valid(snapshot.Data) {
		return errors.New("session data must be valid JSON")
	}
	updatedAt := snapshot.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = time.Now()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sessions(id, workspace, updated_at, data) VALUES(?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			workspace = excluded.workspace,
			updated_at = excluded.updated_at,
			data = CAST(json_set(
				CAST(excluded.data AS TEXT),
				'$.history',
				json(coalesce(json_extract(CAST(sessions.data AS TEXT), '$.history'), 'null'))
			) AS BLOB)
	`, snapshot.ID, snapshot.Workspace, updatedAt.UnixNano(), []byte(snapshot.Data))
	if err != nil {
		return fmt.Errorf("save session %q: %w", snapshot.ID, err)
	}
	return nil
}

// UpdateHistory replaces only the conversation history in a session payload.
// The single SQL update makes it safe to persist completed history before its
// graph checkpoint is removed.
func (s *Store) UpdateHistory(ctx context.Context, id, workspace string, history json.RawMessage) error {
	if s == nil || s.db == nil {
		return errors.New("session store is not open")
	}
	if id == "" || workspace == "" || !json.Valid(history) {
		return errors.New("session id, workspace, and valid history are required")
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE sessions
		SET data = CAST(json_set(CAST(data AS TEXT), '$.history', json(?)) AS BLOB), updated_at = ?
		WHERE id = ? AND workspace = ?
	`, string(history), time.Now().UnixNano(), id, workspace)
	if err != nil {
		return fmt.Errorf("persist history for session %q: %w", id, err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check session history update: %w", err)
	}
	if updated == 0 {
		return fmt.Errorf("session %q does not exist in workspace", id)
	}
	return nil
}

func (s *Store) Latest(ctx context.Context, workspace string) (*Snapshot, error) {
	rows, err := s.query(ctx, `
		SELECT id, workspace, updated_at, data FROM sessions
		WHERE workspace = ? ORDER BY updated_at DESC LIMIT 1
	`, workspace)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return &rows[0], nil
}

func (s *Store) List(ctx context.Context, workspace string, limit int) ([]Snapshot, error) {
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	return s.query(ctx, `
		SELECT id, workspace, updated_at, data FROM sessions
		WHERE workspace = ? ORDER BY updated_at DESC LIMIT ?
	`, workspace, limit)
}

func (s *Store) query(ctx context.Context, statement string, args ...any) ([]Snapshot, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("session store is not open")
	}
	rows, err := s.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, fmt.Errorf("query sessions: %w", err)
	}
	defer rows.Close()

	var snapshots []Snapshot
	for rows.Next() {
		var snapshot Snapshot
		var updatedAt int64
		var data []byte
		if err := rows.Scan(&snapshot.ID, &snapshot.Workspace, &updatedAt, &data); err != nil {
			return nil, fmt.Errorf("read session: %w", err)
		}
		snapshot.UpdatedAt = time.Unix(0, updatedAt)
		snapshot.Data = append(json.RawMessage(nil), data...)
		snapshots = append(snapshots, snapshot)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read sessions: %w", err)
	}
	return snapshots, nil
}
