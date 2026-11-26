package settings

import (
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// WorkspaceSessionRecord represents a persisted session indexed under a workspace.
type WorkspaceSessionRecord struct {
	SessionID    string `json:"sessionId"`
	WorkspaceID  string `json:"workspaceId"`
	Title        string `json:"title"`
	Archived     bool   `json:"archived"`
	CreatedAt    int64  `json:"createdAt"`
	UpdatedAt    int64  `json:"updatedAt"`
	LastOpenedAt int64  `json:"lastOpenedAt"`
	TaskStatus   string `json:"taskStatus,omitempty"`
	ExecutionID  string `json:"executionId,omitempty"`
	QueueCount   int    `json:"queueCount,omitempty"`
}

// WorkspaceRecord represents a persistent workspace tracked in settings.db.
type WorkspaceRecord struct {
	ID            string                   `json:"id"`
	Path          string                   `json:"path"`
	Name          string                   `json:"name"`
	IsOpen        bool                     `json:"isOpen"`
	LastSessionID string                   `json:"lastSessionId"`
	CreatedAt     int64                    `json:"createdAt"`
	UpdatedAt     int64                    `json:"updatedAt"`
	LastOpenedAt  int64                    `json:"lastOpenedAt"`
	Sessions      []WorkspaceSessionRecord `json:"sessions,omitempty"`
}

func canonicalWorkspacePath(path string, requireDir bool) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", errors.New("workspace path is required")
	}
	abs, err := filepath.Abs(trimmed)
	if err != nil {
		return "", fmt.Errorf("resolve workspace path: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		if requireDir {
			return "", err
		}
		resolved = filepath.Clean(abs)
	}
	if requireDir {
		info, err := os.Stat(resolved)
		if err != nil {
			return "", err
		}
		if !info.IsDir() {
			return "", fmt.Errorf("workspace directory is unavailable: %s", resolved)
		}
	}
	return resolved, nil
}

func scanWorkspaceRecord(scanner interface{ Scan(...any) error }) (WorkspaceRecord, error) {
	var rec WorkspaceRecord
	var isOpen int
	if err := scanner.Scan(
		&rec.ID,
		&rec.Path,
		&rec.Name,
		&isOpen,
		&rec.LastSessionID,
		&rec.CreatedAt,
		&rec.UpdatedAt,
		&rec.LastOpenedAt,
	); err != nil {
		return WorkspaceRecord{}, err
	}
	rec.IsOpen = isOpen != 0
	return rec, nil
}

func scanWorkspaceSessionRecord(scanner interface{ Scan(...any) error }) (WorkspaceSessionRecord, error) {
	var rec WorkspaceSessionRecord
	var archived int
	if err := scanner.Scan(
		&rec.SessionID,
		&rec.WorkspaceID,
		&rec.Title,
		&archived,
		&rec.CreatedAt,
		&rec.UpdatedAt,
		&rec.LastOpenedAt,
	); err != nil {
		return WorkspaceSessionRecord{}, err
	}
	rec.Archived = archived != 0
	return rec, nil
}

func (s *Store) migrateLegacyWorkspaces() error {
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM workspaces`).Scan(&count); err != nil {
		return fmt.Errorf("count workspaces: %w", err)
	}
	if count > 0 {
		return nil
	}

	type candidate struct {
		path      string
		name      string
		sessionID string
		ts        int64
	}
	byPath := make(map[string]candidate)

	connRows, err := s.db.Query(`SELECT address, name, touched, coalesce(session, '') FROM web_connections WHERE mode = 'local'`)
	if err != nil {
		return fmt.Errorf("read legacy local connections: %w", err)
	}
	for connRows.Next() {
		var addr, name, session string
		var touched int64
		if scanErr := connRows.Scan(&addr, &name, &touched, &session); scanErr != nil {
			connRows.Close()
			return scanErr
		}
		canonical, canonErr := canonicalWorkspacePath(addr, true)
		if canonErr != nil {
			continue
		}
		if strings.TrimSpace(name) == "" {
			name = filepath.Base(canonical)
		}
		if touched <= 0 {
			touched = time.Now().UTC().UnixMilli()
		}
		byPath[canonical] = candidate{
			path:      canonical,
			name:      name,
			sessionID: strings.TrimSpace(session),
			ts:        touched,
		}
	}
	if err := connRows.Err(); err != nil {
		connRows.Close()
		return err
	}
	_ = connRows.Close()

	recentRows, err := s.db.Query(`SELECT path, last_used FROM recent_workspaces`)
	if err == nil {
		for recentRows.Next() {
			var rawPath, lastUsed string
			if scanErr := recentRows.Scan(&rawPath, &lastUsed); scanErr != nil {
				recentRows.Close()
				return scanErr
			}
			canonical, canonErr := canonicalWorkspacePath(rawPath, true)
			if canonErr != nil {
				continue
			}
			var ts int64
			if parsed, parseErr := time.Parse(time.RFC3339Nano, lastUsed); parseErr == nil {
				ts = parsed.UTC().UnixMilli()
			}
			if ts <= 0 {
				ts = time.Now().UTC().UnixMilli()
			}
			existing, ok := byPath[canonical]
			if !ok {
				byPath[canonical] = candidate{
					path: canonical,
					name: filepath.Base(canonical),
					ts:   ts,
				}
			} else if ts > existing.ts {
				existing.ts = ts
				byPath[canonical] = existing
			}
		}
		if err := recentRows.Err(); err != nil {
			recentRows.Close()
			return err
		}
		_ = recentRows.Close()
	}

	if len(byPath) == 0 {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	for _, item := range byPath {
		if _, err := tx.Exec(`
			INSERT INTO workspaces(id, path, name, is_open, last_session_id, created_at, updated_at, last_opened_at)
			VALUES (?, ?, ?, 1, ?, ?, ?, ?)
			ON CONFLICT(path) DO NOTHING
		`, rand.Text(), item.path, item.name, item.sessionID, item.ts, item.ts, item.ts); err != nil {
			return fmt.Errorf("insert migrated workspace %s: %w", item.path, err)
		}
	}
	return tx.Commit()
}

// EnsureWorkspace returns a persistent workspace record with a stable ID.
// When touch is true, last_opened_at and updated_at are bumped to now;
// when touch is false, existing timestamps are preserved (for startup restore).
func (s *Store) EnsureWorkspace(path string, touch bool) (WorkspaceRecord, error) {
	canonical, err := canonicalWorkspacePath(path, true)
	if err != nil {
		return WorkspaceRecord{}, err
	}
	now := time.Now().UTC().UnixMilli()
	name := filepath.Base(canonical)
	if name == "" || name == string(filepath.Separator) {
		name = canonical
	}

	tx, err := s.db.Begin()
	if err != nil {
		return WorkspaceRecord{}, err
	}
	defer func() { _ = tx.Rollback() }()

	existing, err := scanWorkspaceRecord(tx.QueryRow(`
		SELECT id, path, name, is_open, last_session_id, created_at, updated_at, last_opened_at
		FROM workspaces WHERE path = ?
	`, canonical))
	if errors.Is(err, sql.ErrNoRows) {
		rec := WorkspaceRecord{
			ID:           rand.Text(),
			Path:         canonical,
			Name:         name,
			IsOpen:       true,
			CreatedAt:    now,
			UpdatedAt:    now,
			LastOpenedAt: now,
		}
		if _, err := tx.Exec(`
			INSERT INTO workspaces(id, path, name, is_open, last_session_id, created_at, updated_at, last_opened_at)
			VALUES (?, ?, ?, 1, '', ?, ?, ?)
		`, rec.ID, rec.Path, rec.Name, rec.CreatedAt, rec.UpdatedAt, rec.LastOpenedAt); err != nil {
			return WorkspaceRecord{}, fmt.Errorf("create workspace record: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return WorkspaceRecord{}, err
		}
		return rec, nil
	}
	if err != nil {
		return WorkspaceRecord{}, fmt.Errorf("read workspace record: %w", err)
	}

	if touch {
		existing.IsOpen = true
		existing.Name = name
		existing.UpdatedAt = now
		existing.LastOpenedAt = now
		if _, err := tx.Exec(`
			UPDATE workspaces
			SET is_open = 1, name = ?, updated_at = ?, last_opened_at = ?
			WHERE id = ?
		`, existing.Name, existing.UpdatedAt, existing.LastOpenedAt, existing.ID); err != nil {
			return WorkspaceRecord{}, fmt.Errorf("update workspace record: %w", err)
		}
	} else if !existing.IsOpen {
		existing.IsOpen = true
		existing.Name = name
		if _, err := tx.Exec(`UPDATE workspaces SET is_open = 1, name = ? WHERE id = ?`, existing.Name, existing.ID); err != nil {
			return WorkspaceRecord{}, fmt.Errorf("reopen workspace record: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return WorkspaceRecord{}, err
	}
	return existing, nil
}

// WorkspaceByID looks up a workspace by its persistent ID.
func (s *Store) WorkspaceByID(id string) (WorkspaceRecord, bool, error) {
	rec, err := scanWorkspaceRecord(s.db.QueryRow(`
		SELECT id, path, name, is_open, last_session_id, created_at, updated_at, last_opened_at
		FROM workspaces WHERE id = ?
	`, strings.TrimSpace(id)))
	if errors.Is(err, sql.ErrNoRows) {
		return WorkspaceRecord{}, false, nil
	}
	if err != nil {
		return WorkspaceRecord{}, false, err
	}
	return rec, true, nil
}

// WorkspaceByPath looks up a workspace by its canonical path.
func (s *Store) WorkspaceByPath(path string) (WorkspaceRecord, bool, error) {
	canonical, err := canonicalWorkspacePath(path, false)
	if err != nil {
		return WorkspaceRecord{}, false, err
	}
	rec, err := scanWorkspaceRecord(s.db.QueryRow(`
		SELECT id, path, name, is_open, last_session_id, created_at, updated_at, last_opened_at
		FROM workspaces WHERE path = ?
	`, canonical))
	if errors.Is(err, sql.ErrNoRows) {
		return WorkspaceRecord{}, false, nil
	}
	if err != nil {
		return WorkspaceRecord{}, false, err
	}
	return rec, true, nil
}

// SelectWorkspace marks an existing workspace as open and bumps its last_opened_at timestamp.
func (s *Store) SelectWorkspace(id string) error {
	now := time.Now().UTC().UnixMilli()
	res, err := s.db.Exec(`
		UPDATE workspaces SET is_open = 1, last_opened_at = ?, updated_at = ? WHERE id = ?
	`, now, now, strings.TrimSpace(id))
	if err != nil {
		return fmt.Errorf("select workspace: %w", err)
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return errors.New("workspace not found")
	}
	return nil
}

// CloseWorkspace marks a workspace closed while preserving its ID and session history.
func (s *Store) CloseWorkspace(id string) error {
	now := time.Now().UTC().UnixMilli()
	if _, err := s.db.Exec(`
		UPDATE workspaces SET is_open = 0, updated_at = ? WHERE id = ?
	`, now, strings.TrimSpace(id)); err != nil {
		return fmt.Errorf("close workspace: %w", err)
	}
	return nil
}

// ListWorkspaces lists persisted workspaces ordered by last_opened_at DESC, created_at DESC.
func (s *Store) ListWorkspaces(openOnly bool) ([]WorkspaceRecord, error) {
	query := `
		SELECT id, path, name, is_open, last_session_id, created_at, updated_at, last_opened_at
		FROM workspaces
	`
	if openOnly {
		query += ` WHERE is_open = 1`
	}
	query += ` ORDER BY last_opened_at DESC, created_at DESC, rowid DESC`

	rows, err := s.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("list workspaces: %w", err)
	}
	defer rows.Close()

	var result []WorkspaceRecord
	for rows.Next() {
		rec, err := scanWorkspaceRecord(rows)
		if err != nil {
			return nil, fmt.Errorf("scan workspace: %w", err)
		}
		result = append(result, rec)
	}
	return result, rows.Err()
}

// ListWorkspaceSessions lists sessions for a workspace ordered by updated_at DESC, created_at DESC.
func (s *Store) ListWorkspaceSessions(workspaceID string, includeArchived bool) ([]WorkspaceSessionRecord, error) {
	query := `
		SELECT session_id, workspace_id, title, archived, created_at, updated_at, last_opened_at
		FROM workspace_sessions
		WHERE workspace_id = ?
	`
	if !includeArchived {
		query += ` AND archived = 0`
	}
	query += ` ORDER BY updated_at DESC, created_at DESC`

	rows, err := s.db.Query(query, strings.TrimSpace(workspaceID))
	if err != nil {
		return nil, fmt.Errorf("list workspace sessions: %w", err)
	}
	defer rows.Close()

	var result []WorkspaceSessionRecord
	for rows.Next() {
		rec, err := scanWorkspaceSessionRecord(rows)
		if err != nil {
			return nil, fmt.Errorf("scan workspace session: %w", err)
		}
		result = append(result, rec)
	}
	return result, rows.Err()
}

// ListWorkspacesWithSessions lists workspaces ordered by time with all associated sessions attached.
func (s *Store) ListWorkspacesWithSessions(openOnly bool) ([]WorkspaceRecord, error) {
	workspaces, err := s.ListWorkspaces(openOnly)
	if err != nil {
		return nil, err
	}
	if len(workspaces) == 0 {
		return workspaces, nil
	}
	for i := range workspaces {
		sessions, err := s.ListWorkspaceSessions(workspaces[i].ID, true)
		if err != nil {
			return nil, err
		}
		workspaces[i].Sessions = sessions
	}
	return workspaces, nil
}

// SyncWorkspaceSessions replaces/updates the session index for a workspace within a transaction.
// Existing created_at (when earlier) and last_opened_at values are preserved across syncs.
func (s *Store) SyncWorkspaceSessions(workspaceID string, sessions []WorkspaceSessionRecord) error {
	wsID := strings.TrimSpace(workspaceID)
	if wsID == "" {
		return errors.New("workspace id is required")
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var exists int
	if err := tx.QueryRow(`SELECT 1 FROM workspaces WHERE id = ?`, wsID).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
		return errors.New("workspace not found")
	} else if err != nil {
		return err
	}

	now := time.Now().UTC().UnixMilli()
	keep := make(map[string]struct{}, len(sessions))
	var maxUpdated int64

	for _, item := range sessions {
		sid := strings.TrimSpace(item.SessionID)
		if sid == "" {
			continue
		}
		keep[sid] = struct{}{}
		updatedAt := item.UpdatedAt
		if updatedAt <= 0 {
			updatedAt = now
		}
		createdAt := item.CreatedAt
		if createdAt <= 0 {
			createdAt = updatedAt
		}
		if updatedAt > maxUpdated {
			maxUpdated = updatedAt
		}
		archived := 0
		if item.Archived {
			archived = 1
		}
		if _, err := tx.Exec(`
			INSERT INTO workspace_sessions(workspace_id, session_id, title, archived, created_at, updated_at, last_opened_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(workspace_id, session_id) DO UPDATE SET
				title = excluded.title,
				archived = excluded.archived,
				updated_at = excluded.updated_at,
				created_at = CASE
					WHEN workspace_sessions.created_at > 0 AND workspace_sessions.created_at < excluded.created_at THEN workspace_sessions.created_at
					ELSE excluded.created_at
				END
		`, wsID, sid, item.Title, archived, createdAt, updatedAt, item.LastOpenedAt); err != nil {
			return fmt.Errorf("upsert workspace session %s: %w", sid, err)
		}
	}

	rows, err := tx.Query(`SELECT session_id FROM workspace_sessions WHERE workspace_id = ?`, wsID)
	if err != nil {
		return fmt.Errorf("read existing workspace sessions: %w", err)
	}
	var stale []string
	for rows.Next() {
		var sid string
		if scanErr := rows.Scan(&sid); scanErr != nil {
			rows.Close()
			return scanErr
		}
		if _, ok := keep[sid]; !ok {
			stale = append(stale, sid)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	_ = rows.Close()

	for _, sid := range stale {
		if _, err := tx.Exec(`DELETE FROM workspace_sessions WHERE workspace_id = ? AND session_id = ?`, wsID, sid); err != nil {
			return fmt.Errorf("delete stale workspace session %s: %w", sid, err)
		}
	}

	if len(stale) > 0 {
		var lastSID string
		if err := tx.QueryRow(`SELECT last_session_id FROM workspaces WHERE id = ?`, wsID).Scan(&lastSID); err == nil && lastSID != "" {
			if _, ok := keep[lastSID]; !ok {
				if _, err := tx.Exec(`UPDATE workspaces SET last_session_id = '' WHERE id = ?`, wsID); err != nil {
					return err
				}
			}
		}
	}

	if maxUpdated > 0 {
		if _, err := tx.Exec(`
			UPDATE workspaces SET updated_at = MAX(updated_at, ?) WHERE id = ?
		`, maxUpdated, wsID); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// RememberWorkspaceActiveSession records the last active session for a workspace and updates timestamps.
func (s *Store) RememberWorkspaceActiveSession(workspaceID, sessionID string) error {
	wsID := strings.TrimSpace(workspaceID)
	sid := strings.TrimSpace(sessionID)
	if wsID == "" || sid == "" {
		return errors.New("workspace id and session id are required")
	}
	now := time.Now().UTC().UnixMilli()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.Exec(`
		UPDATE workspaces SET last_session_id = ?, updated_at = ? WHERE id = ?
	`, sid, now, wsID)
	if err != nil {
		return fmt.Errorf("remember workspace session: %w", err)
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return errors.New("workspace not found")
	}

	if _, err := tx.Exec(`
		UPDATE workspace_sessions SET last_opened_at = ? WHERE workspace_id = ? AND session_id = ?
	`, now, wsID, sid); err != nil {
		return fmt.Errorf("update session last_opened_at: %w", err)
	}

	return tx.Commit()
}
