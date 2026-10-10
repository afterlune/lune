package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func openTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sessions.db")
	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return store, path
}

func TestOpenMigratesUnversionedSessionMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE sessions (
		id TEXT PRIMARY KEY, workspace TEXT NOT NULL, updated_at INTEGER NOT NULL, data BLOB NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}
	legacy := []byte(`{"history":[{"type":"message","role":"user","content":"hello"}],"settings":{"model_profile":"main"}}`)
	if _, err := db.Exec(`INSERT INTO sessions(id,workspace,updated_at,data) VALUES(?,?,?,?)`, "legacy", "workspace", int64(7), legacy); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open legacy database: %v", err)
	}
	var version int
	if err := store.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != currentSchemaVersion {
		t.Fatalf("user_version = %d, want %d", version, currentSchemaVersion)
	}
	snapshot, err := store.Get(context.Background(), "legacy", "workspace")
	if err != nil {
		t.Fatal(err)
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal(snapshot.Data, &data); err != nil {
		t.Fatal(err)
	}
	if string(data["title"]) != `""` || string(data["archived"]) != "false" {
		t.Fatalf("migrated metadata title=%s archived=%s", data["title"], data["archived"])
	}
	if !strings.Contains(string(data["history"]), "hello") {
		t.Fatalf("migration lost history: %s", data["history"])
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("reopen migrated database: %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestOpenRejectsNewerSchemaVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 99`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Fatal("Open accepted a database from a newer version")
	}
}

func TestSaveAndHistoryUpdatePreserveSessionMetadata(t *testing.T) {
	store, _ := openTestStore(t)
	ctx := context.Background()
	first := Snapshot{ID: "one", Workspace: "workspace", Data: json.RawMessage(`{"history":["first"],"title":"Keep me","archived":true,"settings":{"model_profile":"main"}}`)}
	if err := store.Save(ctx, first); err != nil {
		t.Fatal(err)
	}
	second := Snapshot{ID: "one", Workspace: "workspace", Data: json.RawMessage(`{"history":null,"title":"stale","archived":false,"settings":{"model_profile":"review"}}`)}
	if err := store.Save(ctx, second); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateHistory(ctx, "one", "workspace", json.RawMessage(`["second"]`)); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateTitle(ctx, "one", "workspace", "Renamed"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetArchived(ctx, "one", "workspace", false); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.Get(ctx, "one", "workspace")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		History  []string `json:"history"`
		Title    string   `json:"title"`
		Archived bool     `json:"archived"`
		Settings struct {
			ModelProfile string `json:"model_profile"`
		} `json:"settings"`
	}
	if err := json.Unmarshal(snapshot.Data, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.History) != 1 || got.History[0] != "second" || got.Title != "Renamed" || got.Archived || got.Settings.ModelProfile != "review" {
		t.Fatalf("session after updates = %#v", got)
	}
	if err := store.UpdateTitle(ctx, "missing", "workspace", "title"); err == nil {
		t.Fatal("UpdateTitle succeeded for a missing session")
	}
}

func TestLatestAndListFilterArchivedBeforeLimit(t *testing.T) {
	store, _ := openTestStore(t)
	ctx := context.Background()
	for _, snapshot := range []Snapshot{
		{ID: "active-old", Workspace: "workspace", UpdatedAt: time.Unix(1, 0), Data: json.RawMessage(`{"title":"old","archived":false}`)},
		{ID: "archived-new", Workspace: "workspace", UpdatedAt: time.Unix(3, 0), Data: json.RawMessage(`{"title":"hidden","archived":true}`)},
		{ID: "active-new", Workspace: "workspace", UpdatedAt: time.Unix(2, 0), Data: json.RawMessage(`{"title":"new","archived":false}`)},
		{ID: "other-workspace", Workspace: "other", UpdatedAt: time.Unix(4, 0), Data: json.RawMessage(`{"archived":false}`)},
	} {
		if err := store.Save(ctx, snapshot); err != nil {
			t.Fatal(err)
		}
	}
	latest, err := store.Latest(ctx, "workspace")
	if err != nil || latest == nil || latest.ID != "active-new" {
		t.Fatalf("Latest = %#v, %v; want active-new", latest, err)
	}
	active, err := store.List(ctx, "workspace", 1, false)
	if err != nil || len(active) != 1 || active[0].ID != "active-new" {
		t.Fatalf("List active = %#v, %v; want active-new despite limit 1", active, err)
	}
	all, err := store.List(ctx, "workspace", 10, true)
	if err != nil || len(all) != 3 || all[0].ID != "archived-new" {
		t.Fatalf("List including archived = %#v, %v", all, err)
	}
}

func TestDeleteIsWorkspaceScopedAndIdempotent(t *testing.T) {
	store, _ := openTestStore(t)
	ctx := context.Background()
	if err := store.Save(ctx, Snapshot{ID: "one", Workspace: "workspace", Data: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	deleted, err := store.Delete(ctx, "one", "other")
	if err != nil || deleted {
		t.Fatalf("cross-workspace Delete = %v, %v", deleted, err)
	}
	deleted, err = store.Delete(ctx, "one", "workspace")
	if err != nil || !deleted {
		t.Fatalf("Delete = %v, %v", deleted, err)
	}
	deleted, err = store.Delete(ctx, "one", "workspace")
	if err != nil || deleted {
		t.Fatalf("repeat Delete = %v, %v", deleted, err)
	}
}
