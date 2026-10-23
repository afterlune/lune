package settings

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/afterlune/luneCore/capability"
)

var _ capability.Store = (*Store)(nil)

func (s *Store) List(ctx context.Context, limit int, includeDisabled bool) ([]capability.GeneratedTool, error) {
	if err := s.checkCapabilityStore(); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	query := `SELECT ` + capabilityColumns + ` FROM task_experiences`
	if !includeDisabled {
		query += ` WHERE enabled = 1`
	}
	query += ` ORDER BY COALESCE(last_used_at, 0) DESC, created_at DESC LIMIT ?`
	rows, err := s.db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("list generated tools: %w", err)
	}
	defer rows.Close()
	items := make([]capability.GeneratedTool, 0)
	for rows.Next() {
		item, err := scanCapability(rows)
		if err != nil {
			return nil, fmt.Errorf("read generated tool: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read generated tools: %w", err)
	}
	return items, nil
}

func (s *Store) Get(ctx context.Context, id string) (capability.GeneratedTool, error) {
	if err := s.checkCapabilityStore(); err != nil {
		return capability.GeneratedTool{}, err
	}
	item, err := scanCapability(s.db.QueryRowContext(ctx, `SELECT `+capabilityColumns+` FROM task_experiences WHERE id = ?`, strings.TrimSpace(id)))
	if errors.Is(err, sql.ErrNoRows) {
		return capability.GeneratedTool{}, fmt.Errorf("generated tool %q not found", id)
	}
	if err != nil {
		return capability.GeneratedTool{}, fmt.Errorf("get generated tool: %w", err)
	}
	return item, nil
}

func (s *Store) Save(ctx context.Context, item capability.GeneratedTool) (capability.GeneratedTool, bool, error) {
	if err := s.checkCapabilityStore(); err != nil {
		return capability.GeneratedTool{}, false, err
	}
	item.Name = strings.TrimSpace(item.Name)
	item.Description = strings.TrimSpace(item.Description)
	item.Purpose = capability.Purpose(strings.TrimSpace(string(item.Purpose)))
	if item.Name == "" || item.Description == "" || !item.Purpose.Valid() || strings.TrimSpace(item.Script) == "" {
		return capability.GeneratedTool{}, false, errors.New("generated tool requires a name, description, purpose, and script")
	}
	if item.InputSchema == nil || !json.Valid(item.InputSchema) {
		return capability.GeneratedTool{}, false, errors.New("generated tool input schema is invalid")
	}
	if item.ID == "" {
		item.ID = newCapabilityID(item)
	}
	now := time.Now().UTC()
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	item.UpdatedAt = now
	item.Enabled = true
	tags, err := json.Marshal(item.Tags)
	if err != nil {
		return capability.GeneratedTool{}, false, fmt.Errorf("encode generated tool tags: %w", err)
	}
	preconditions, err := json.Marshal(item.Preconditions)
	if err != nil {
		return capability.GeneratedTool{}, false, fmt.Errorf("encode generated tool preconditions: %w", err)
	}
	tools, err := json.Marshal(item.Dependencies)
	if err != nil {
		return capability.GeneratedTool{}, false, fmt.Errorf("encode generated tool tools: %w", err)
	}
	capabilities := []byte("[]")
	dedupeKey := strings.ToLower(string(item.Purpose) + "\x00" + item.Name)
	result, err := s.db.ExecContext(ctx, `INSERT INTO task_experiences(
id, dedupe_key, name, description, purpose, tags_json, input_schema_json, preconditions_json,
script, required_tools_json, required_capabilities_json, enabled, source_session_id,
source_execution_id, created_at, updated_at, use_count)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?, ?, ?, 0)
ON CONFLICT(dedupe_key) DO NOTHING`, item.ID, dedupeKey, item.Name, item.Description, string(item.Purpose), string(tags),
		string(item.InputSchema), string(preconditions), item.Script, string(tools), string(capabilities), item.SourceSessionID,
		item.SourceExecutionID, item.CreatedAt.UnixMilli(), item.UpdatedAt.UnixMilli())
	if err != nil {
		return capability.GeneratedTool{}, false, fmt.Errorf("save generated tool: %w", err)
	}
	created, err := result.RowsAffected()
	if err != nil {
		return capability.GeneratedTool{}, false, fmt.Errorf("check saved generated tool: %w", err)
	}
	saved, err := scanCapability(s.db.QueryRowContext(ctx, `SELECT `+capabilityColumns+` FROM task_experiences WHERE dedupe_key = ?`, dedupeKey))
	if err != nil {
		return capability.GeneratedTool{}, false, fmt.Errorf("read saved generated tool: %w", err)
	}
	return saved, created == 1, nil
}

func (s *Store) SetEnabled(ctx context.Context, id string, enabled bool) error {
	if err := s.checkCapabilityStore(); err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE task_experiences SET enabled = ?, updated_at = ? WHERE id = ?`, enabled, time.Now().UTC().UnixMilli(), strings.TrimSpace(id))
	if err != nil {
		return fmt.Errorf("update generated tool: %w", err)
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return fmt.Errorf("generated tool %q not found", id)
	}
	return nil
}

func (s *Store) Delete(ctx context.Context, id string) error {
	if err := s.checkCapabilityStore(); err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM task_experiences WHERE id = ?`, strings.TrimSpace(id))
	if err != nil {
		return fmt.Errorf("delete generated tool: %w", err)
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return fmt.Errorf("generated tool %q not found", id)
	}
	return nil
}

func (s *Store) MarkUsed(ctx context.Context, id string) error {
	if err := s.checkCapabilityStore(); err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE task_experiences SET use_count = use_count + 1, last_used_at = ?, updated_at = ? WHERE id = ?`, time.Now().UTC().UnixMilli(), time.Now().UTC().UnixMilli(), strings.TrimSpace(id))
	if err != nil {
		return fmt.Errorf("record generated tool use: %w", err)
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return fmt.Errorf("generated tool %q not found", id)
	}
	return nil
}

func (s *Store) checkCapabilityStore() error {
	if s == nil || s.db == nil {
		return errors.New("settings store is closed")
	}
	return nil
}

const capabilityColumns = `id, name, description, purpose, tags_json, input_schema_json, preconditions_json,
script, required_tools_json, required_capabilities_json, enabled, source_session_id, source_execution_id,
created_at, updated_at, last_used_at, use_count`

type capabilityScanner interface {
	Scan(...any) error
}

func scanCapability(scanner capabilityScanner) (capability.GeneratedTool, error) {
	var item capability.GeneratedTool
	var purpose, tags, inputSchema, preconditions, requiredTools, requiredCapabilities string
	var enabled bool
	var createdAt, updatedAt int64
	var lastUsedAt sql.NullInt64
	if err := scanner.Scan(&item.ID, &item.Name, &item.Description, &purpose, &tags, &inputSchema, &preconditions,
		&item.Script, &requiredTools, &requiredCapabilities, &enabled, &item.SourceSessionID, &item.SourceExecutionID,
		&createdAt, &updatedAt, &lastUsedAt, &item.UseCount); err != nil {
		return capability.GeneratedTool{}, err
	}
	item.Purpose = capability.Purpose(purpose)
	item.Enabled = enabled
	item.CreatedAt = time.UnixMilli(createdAt).UTC()
	item.UpdatedAt = time.UnixMilli(updatedAt).UTC()
	if lastUsedAt.Valid {
		item.LastUsedAt = time.UnixMilli(lastUsedAt.Int64).UTC()
	}
	if err := json.Unmarshal([]byte(tags), &item.Tags); err != nil {
		return capability.GeneratedTool{}, fmt.Errorf("decode generated tool tags: %w", err)
	}
	item.InputSchema = json.RawMessage(inputSchema)
	if err := json.Unmarshal([]byte(preconditions), &item.Preconditions); err != nil {
		return capability.GeneratedTool{}, fmt.Errorf("decode generated tool preconditions: %w", err)
	}
	if err := json.Unmarshal([]byte(requiredTools), &item.Dependencies); err != nil {
		return capability.GeneratedTool{}, fmt.Errorf("decode generated tool tools: %w", err)
	}
	_ = requiredCapabilities
	return item, nil
}

func newCapabilityID(item capability.GeneratedTool) string {
	seed := item.SourceExecutionID + "\x00" + string(item.Purpose) + "\x00" + strings.ToLower(item.Name)
	if item.SourceExecutionID == "" {
		var random [16]byte
		if _, err := rand.Read(random[:]); err == nil {
			seed = hex.EncodeToString(random[:])
		}
	}
	digest := sha256.Sum256([]byte(seed))
	return "exp-" + hex.EncodeToString(digest[:12])
}
