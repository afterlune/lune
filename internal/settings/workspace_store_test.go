package settings

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestSyncWorkspaceSessionsFromSerializesAcrossStores(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.db")
	firstStore, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer firstStore.Close()
	secondStore, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer secondStore.Close()
	workspace, err := firstStore.EnsureWorkspace(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}

	firstLoaderEntered := make(chan struct{})
	secondLoaderEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	released := false
	release := func() {
		if !released {
			close(releaseFirst)
			released = true
		}
	}
	defer release()
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- firstStore.SyncWorkspaceSessionsFrom(t.Context(), workspace.ID, func(context.Context) ([]WorkspaceSessionRecord, error) {
			close(firstLoaderEntered)
			<-releaseFirst
			return []WorkspaceSessionRecord{{
				SessionID: "older-snapshot", WorkspaceID: workspace.ID, Title: "older", CreatedAt: 1, UpdatedAt: 1,
			}}, nil
		})
	}()
	<-firstLoaderEntered

	secondStarted := make(chan struct{})
	secondDone := make(chan error, 1)
	go func() {
		close(secondStarted)
		secondDone <- secondStore.SyncWorkspaceSessionsFrom(t.Context(), workspace.ID, func(context.Context) ([]WorkspaceSessionRecord, error) {
			close(secondLoaderEntered)
			return []WorkspaceSessionRecord{{
				SessionID: "newer-snapshot", WorkspaceID: workspace.ID, Title: "newer", CreatedAt: 2, UpdatedAt: 2,
			}}, nil
		})
	}()
	<-secondStarted

	secondLoadedEarly := false
	select {
	case <-secondLoaderEntered:
		secondLoadedEarly = true
	case <-time.After(100 * time.Millisecond):
	}
	release()
	if err := <-firstDone; err != nil {
		t.Fatalf("first sync: %v", err)
	}
	if err := <-secondDone; err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if secondLoadedEarly {
		t.Fatal("second snapshot loader ran before the first sync committed")
	}

	saved, err := firstStore.ListWorkspaceSessions(workspace.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved) != 1 || saved[0].SessionID != "newer-snapshot" {
		t.Fatalf("final sessions = %+v, want only newer snapshot", saved)
	}
}

func TestSyncWorkspaceSessionsFromLoaderFailurePreservesIndex(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	workspace, err := store.EnsureWorkspace(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SyncWorkspaceSessions(workspace.ID, []WorkspaceSessionRecord{{
		SessionID: "existing", WorkspaceID: workspace.ID, Title: "keep", CreatedAt: 1, UpdatedAt: 1,
	}}); err != nil {
		t.Fatal(err)
	}
	if err := store.SyncWorkspaceSessionsFrom(t.Context(), workspace.ID, func(context.Context) ([]WorkspaceSessionRecord, error) {
		return nil, errors.New("snapshot failed")
	}); err == nil {
		t.Fatal("expected snapshot failure")
	}
	saved, err := store.ListWorkspaceSessions(workspace.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved) != 1 || saved[0].SessionID != "existing" {
		t.Fatalf("failed sync changed existing index: %+v", saved)
	}
}

func TestWorkspaceStorePersistenceForeignKeysAndOrdering(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	var fk int
	if err := store.db.QueryRow(`PRAGMA foreign_keys`).Scan(&fk); err != nil || fk != 1 {
		t.Fatalf("PRAGMA foreign_keys = %d, err=%v; want 1", fk, err)
	}

	dirA := t.TempDir()
	dirB := t.TempDir()

	wsA, err := store.EnsureWorkspace(dirA, true)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	wsB, err := store.EnsureWorkspace(dirB, true)
	if err != nil {
		t.Fatal(err)
	}
	if wsA.ID == "" || wsB.ID == "" || wsA.ID == wsB.ID {
		t.Fatalf("unexpected workspace IDs: %q, %q", wsA.ID, wsB.ID)
	}

	// Ordering: B is newer than A.
	openList, err := store.ListWorkspaces(true)
	if err != nil {
		t.Fatal(err)
	}
	if len(openList) != 2 || openList[0].ID != wsB.ID || openList[1].ID != wsA.ID {
		t.Fatalf("ListWorkspaces(true) = %#v, want [B, A]", openList)
	}

	// EnsureWorkspace(touch=false) preserves order.
	if _, err := store.EnsureWorkspace(dirA, false); err != nil {
		t.Fatal(err)
	}
	openList, err = store.ListWorkspaces(true)
	if err != nil {
		t.Fatal(err)
	}
	if openList[0].ID != wsB.ID {
		t.Fatalf("touch=false altered ordering: %#v", openList)
	}

	// Sync sessions for B and verify time ordering + foreign-key association.
	if err := store.SyncWorkspaceSessions(wsB.ID, []WorkspaceSessionRecord{
		{SessionID: "sess-old", Title: "Older", CreatedAt: 1000, UpdatedAt: 1000},
		{SessionID: "sess-new", Title: "Newer", CreatedAt: 1500, UpdatedAt: 2000},
		{SessionID: "sess-arch", Title: "Archived", Archived: true, CreatedAt: 1200, UpdatedAt: 3000},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.RememberWorkspaceActiveSession(wsB.ID, "sess-old"); err != nil {
		t.Fatal(err)
	}

	activeSessions, err := store.ListWorkspaceSessions(wsB.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(activeSessions) != 2 || activeSessions[0].SessionID != "sess-new" || activeSessions[1].SessionID != "sess-old" {
		t.Fatalf("ListWorkspaceSessions(false) = %#v", activeSessions)
	}

	// Upsert keeps last_opened_at and removes stale rows.
	if err := store.SyncWorkspaceSessions(wsB.ID, []WorkspaceSessionRecord{
		{SessionID: "sess-old", Title: "Renamed Older", UpdatedAt: 2500},
	}); err != nil {
		t.Fatal(err)
	}
	afterSync, err := store.ListWorkspaceSessions(wsB.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(afterSync) != 1 || afterSync[0].Title != "Renamed Older" || afterSync[0].LastOpenedAt == 0 || afterSync[0].CreatedAt != 1000 {
		t.Fatalf("after stale cleanup = %#v", afterSync)
	}

	// Close workspace A, verify excluded from openOnly, and reopening reuses A's exact ID.
	if err := store.CloseWorkspace(wsA.ID); err != nil {
		t.Fatal(err)
	}
	openList, err = store.ListWorkspaces(true)
	if err != nil || len(openList) != 1 || openList[0].ID != wsB.ID {
		t.Fatalf("after closing A, ListWorkspaces(true) = %#v, err=%v", openList, err)
	}
	time.Sleep(5 * time.Millisecond)
	reopenedA, err := store.EnsureWorkspace(dirA, true)
	if err != nil {
		t.Fatal(err)
	}
	if reopenedA.ID != wsA.ID {
		t.Fatalf("reopened ID = %q, want stable ID %q", reopenedA.ID, wsA.ID)
	}
	openList, err = store.ListWorkspacesWithSessions(true)
	if err != nil || len(openList) != 2 || openList[0].ID != wsA.ID {
		t.Fatalf("after reopening A, ListWorkspacesWithSessions = %#v, err=%v", openList, err)
	}

	// Cascade delete removes associated sessions when workspace row is deleted.
	if _, err := store.db.Exec(`DELETE FROM workspaces WHERE id = ?`, wsB.ID); err != nil {
		t.Fatal(err)
	}
	var orphanCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM workspace_sessions WHERE workspace_id = ?`, wsB.ID).Scan(&orphanCount); err != nil || orphanCount != 0 {
		t.Fatalf("orphan sessions = %d, err=%v; want 0", orphanCount, err)
	}
}

func TestMigrateLegacyWorkspacesFromConnectionsAndRecent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "settings.db")
	store, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	dir1 := t.TempDir()
	dir2 := t.TempDir()
	canon1, _ := canonicalWorkspacePath(dir1, true)
	canon2, _ := canonicalWorkspacePath(dir2, true)

	// Simulate an older DB before migration ran by clearing workspaces and inserting legacy rows.
	if _, err := store.db.Exec(`DELETE FROM workspaces`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`INSERT INTO web_connections(mode, address, name, touched, session) VALUES('local', ?, 'dir1', 1000, 's-1')`, canon1); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`INSERT INTO web_connections(mode, address, name, touched, session) VALUES('local', ?, 'dir2', 2000, 's-2')`, canon2); err != nil {
		t.Fatal(err)
	}
	_ = store.Close()

	reopened, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()

	list, err := reopened.ListWorkspaces(true)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Path != canon2 || list[1].Path != canon1 || list[0].LastSessionID != "s-2" {
		t.Fatalf("migrated workspaces = %#v", list)
	}
}
