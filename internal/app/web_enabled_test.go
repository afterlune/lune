//go:build lune_web

package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/afterlune/lune/internal/ptyterm"
	"github.com/afterlune/lune/internal/settings"
	core "github.com/afterlune/luneCore"
)

func TestLocalWebAdmission(t *testing.T) {
	handler := &localWebHandler{
		host: "127.0.0.1:18000", code: "pair-code", token: "runtime-token",
		assets:     http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }),
		runtime:    http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusSwitchingProtocols) }),
		workspaces: &webWorkspaces{token: "runtime-token"},
	}
	for _, tc := range []struct {
		name, host, origin, address, path string
		status                            int
	}{
		{"invalid host", "not-local.invalid:18000", "", "127.0.0.1:18001", "/", http.StatusForbidden},
		{"invalid address", "127.0.0.1:18000", "", "192.0.2.1:18001", "/", http.StatusForbidden},
		{"other origin", "127.0.0.1:18000", "http://other.invalid", "127.0.0.1:18001", "/_lune/pair", http.StatusForbidden},
		{"missing runtime origin", "127.0.0.1:18000", "", "127.0.0.1:18001", "/v1/runtime", http.StatusForbidden},
		{"pair", "127.0.0.1:18000", "http://127.0.0.1:18000", "127.0.0.1:18001", "/_lune/pair", http.StatusOK},
		{"pair replay", "127.0.0.1:18000", "http://127.0.0.1:18000", "127.0.0.1:18001", "/_lune/pair", http.StatusUnauthorized},
		{"workspace action wrong origin", "127.0.0.1:18000", "http://other.invalid", "127.0.0.1:18001", "/_lune/open-workspace", http.StatusForbidden},
		{"workspace action requires token", "127.0.0.1:18000", "http://127.0.0.1:18000", "127.0.0.1:18001", "/_lune/open-workspace", http.StatusUnauthorized},
		{"same origin runtime", "127.0.0.1:18000", "http://127.0.0.1:18000", "127.0.0.1:18001", "/v1/runtime", http.StatusSwitchingProtocols},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "http://"+tc.host+tc.path, strings.NewReader("pair-code"))
			request.RemoteAddr = tc.address
			if tc.origin != "" {
				request.Header.Set("Origin", tc.origin)
			}
			result := httptest.NewRecorder()
			handler.ServeHTTP(result, request)
			if result.Code != tc.status {
				t.Fatalf("status = %d, want %d", result.Code, tc.status)
			}
			if tc.name == "pair" && !strings.Contains(result.Body.String(), "runtime-token") {
				t.Fatal("missing runtime credential")
			}
			if tc.status >= 400 && strings.Contains(result.Body.String(), "runtime-token") {
				t.Fatal("runtime credential disclosed to a rejected request")
			}
		})
	}
}

type workspaceTerminalStub struct{ closed bool }

var _ ptyterm.Session = (*workspaceTerminalStub)(nil)

func (*workspaceTerminalStub) Read([]byte) (int, error)       { return 0, io.EOF }
func (*workspaceTerminalStub) Write(data []byte) (int, error) { return len(data), nil }
func (*workspaceTerminalStub) Resize(uint16, uint16) error    { return nil }
func (*workspaceTerminalStub) Wait() (int, error)             { return 0, nil }
func (s *workspaceTerminalStub) Close() error                 { s.closed = true; return nil }

func TestClosingWorkspaceOnlyClosesItsTerminals(t *testing.T) {
	first := &workspaceTerminalStub{}
	second := &workspaceTerminalStub{}
	workspaces := &webWorkspaces{
		entries: map[string]*webWorkspaceEntry{
			"first":  {workspace: core.Workspace{ID: "first"}},
			"second": {workspace: core.Workspace{ID: "second"}},
		},
	}
	if !workspaces.trackTerminal(first, "first", true) || !workspaces.trackTerminal(second, "second", true) {
		t.Fatal("could not register workspace terminals")
	}
	workspaces.closeWorkspaceTerminals("first")
	if !first.closed || second.closed {
		t.Fatalf("terminal close state = (%v, %v), want (true, false)", first.closed, second.closed)
	}
	workspaces.entries["first"].closing = true
	if workspaces.trackTerminal(&workspaceTerminalStub{}, "first", true) {
		t.Fatal("accepted a terminal for a closing workspace")
	}
}

func TestWebHostTLSAndSQLiteEndpoints(t *testing.T) {
	store, err := settings.Open(filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	workspaceDir := t.TempDir()
	ws := &webWorkspaces{token: "lan-secret", store: store, pendingInitial: workspaceDir, needsSetup: true}
	handler := &localWebHandler{
		host: "10.0.0.5:5173", useTLS: true, token: "lan-secret",
		assets:     http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }),
		workspaces: ws,
	}

	// GET /_lune/status works without Origin header and reports token mode.
	statusReq := httptest.NewRequest(http.MethodGet, "https://10.0.0.5:5173/_lune/status", nil)
	statusRes := httptest.NewRecorder()
	handler.ServeHTTP(statusRes, statusReq)
	if statusRes.Code != http.StatusOK || !strings.Contains(statusRes.Body.String(), `"token"`) {
		t.Fatalf("status = %d %s", statusRes.Code, statusRes.Body.String())
	}

	post := func(path string, body any, auth string, origin string) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "https://10.0.0.5:5173"+path, bytes.NewReader(raw))
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		return res
	}

	if res := post("/_lune/auth", map[string]any{}, "Bearer wrong", "https://10.0.0.5:5173"); res.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token status = %d", res.Code)
	}
	if res := post("/_lune/auth", map[string]any{}, "Bearer lan-secret", "https://evil.example"); res.Code != http.StatusForbidden {
		t.Fatalf("wrong origin status = %d", res.Code)
	}
	crossReq := httptest.NewRequest(http.MethodPost, "https://10.0.0.5:5173/_lune/auth", strings.NewReader("{}"))
	crossReq.Header.Set("Authorization", "Bearer lan-secret")
	crossReq.Header.Set("Sec-Fetch-Site", "cross-site")
	crossRes := httptest.NewRecorder()
	handler.ServeHTTP(crossRes, crossReq)
	if crossRes.Code != http.StatusForbidden {
		t.Fatalf("cross-site Sec-Fetch-Site = %d, want 403", crossRes.Code)
	}
	if res := post("/_lune/auth", map[string]any{}, "Bearer lan-secret", ""); res.Code != http.StatusOK {
		t.Fatalf("same-origin omitted origin status = %d %s", res.Code, res.Body.String())
	}
	if res := post("/_lune/auth", map[string]any{}, "Bearer lan-secret", "null"); res.Code != http.StatusOK {
		t.Fatalf("same-origin null origin status = %d %s", res.Code, res.Body.String())
	}
	if res := post("/_lune/auth", map[string]any{}, "Bearer lan-secret", "https://10.0.0.5:5173"); res.Code != http.StatusOK {
		t.Fatalf("valid auth status = %d %s", res.Code, res.Body.String())
	}

	// Opening unconfigured workspace cleanly indicates needsSetup.
	if res := post("/_lune/open-workspace", map[string]any{"path": workspaceDir}, "Bearer lan-secret", "https://10.0.0.5:5173"); res.Code != http.StatusPreconditionFailed || !strings.Contains(res.Body.String(), `"needsSetup":true`) {
		t.Fatalf("unconfigured open-workspace = %d %s", res.Code, res.Body.String())
	}

	// Saving config stores provider & API key in settings.db without creating config.json.
	saveRes := post("/_lune/config/save", map[string]any{
		"scope": "",
		"document": map[string]any{
			"models": map[string]any{
				"providers": map[string]any{
					"deepseek": map[string]any{"protocol": "responses", "baseURL": "https://api.deepseek.com", "apiKey": "sk-db-only"},
				},
				"profiles": map[string]any{
					"v3": map[string]any{"provider": "deepseek", "model": "deepseek-chat"},
				},
				"defaultProfile": "v3",
			},
		},
	}, "Bearer lan-secret", "https://10.0.0.5:5173")
	if saveRes.Code != http.StatusOK {
		t.Fatalf("config/save = %d %s", saveRes.Code, saveRes.Body.String())
	}
	var saveResult map[string]any
	if err := json.Unmarshal(saveRes.Body.Bytes(), &saveResult); err != nil || saveResult["reloaded"] != true {
		t.Fatalf("idle config/save did not reload the workspace: %s", saveRes.Body.String())
	}

	workspacesRes := post("/_lune/workspaces", map[string]any{}, "Bearer lan-secret", "https://10.0.0.5:5173")
	var workspaceList struct {
		Items []struct {
			ID   string `json:"id"`
			Path string `json:"path"`
		} `json:"items"`
	}
	if workspacesRes.Code != http.StatusOK || json.Unmarshal(workspacesRes.Body.Bytes(), &workspaceList) != nil || len(workspaceList.Items) != 1 {
		t.Fatalf("workspaces = %d %s", workspacesRes.Code, workspacesRes.Body.String())
	}
	oldWorkspaceID := workspaceList.Items[0].ID
	oldCore := ws.entries[oldWorkspaceID].core
	reloadRes := post("/_lune/reload-workspace", map[string]any{"id": oldWorkspaceID}, "Bearer lan-secret", "https://10.0.0.5:5173")
	if reloadRes.Code != http.StatusOK {
		t.Fatalf("reload-workspace = %d %s", reloadRes.Code, reloadRes.Body.String())
	}
	workspacesRes = post("/_lune/workspaces", map[string]any{}, "Bearer lan-secret", "https://10.0.0.5:5173")
	if workspacesRes.Code != http.StatusOK || json.Unmarshal(workspacesRes.Body.Bytes(), &workspaceList) != nil || len(workspaceList.Items) != 1 || workspaceList.Items[0].ID != oldWorkspaceID {
		t.Fatalf("reloaded workspace = %d %s", workspacesRes.Code, workspacesRes.Body.String())
	}
	if ws.entries[oldWorkspaceID] == nil || ws.entries[oldWorkspaceID].core == oldCore {
		t.Fatalf("reload-workspace did not recreate workspace core while preserving stable ID %q", oldWorkspaceID)
	}

	delegationOf := func() map[string]any {
		res := post("/_lune/workspaces/delegation", map[string]any{"id": oldWorkspaceID}, "Bearer lan-secret", "https://10.0.0.5:5173")
		var body struct {
			Items []map[string]any `json:"items"`
		}
		if res.Code != http.StatusOK || json.Unmarshal(res.Body.Bytes(), &body) != nil || len(body.Items) != 1 {
			t.Fatalf("delegation = %d %s", res.Code, res.Body.String())
		}
		return body.Items[0]
	}
	if got := delegationOf(); got["enabled"] != false || got["configStale"] != false {
		t.Fatalf("default delegation should be disabled: %#v", got)
	}
	configRes := post("/_lune/config/get", map[string]any{"scope": ""}, "Bearer lan-secret", "https://10.0.0.5:5173")
	var configView struct {
		Document map[string]any `json:"document"`
	}
	if configRes.Code != http.StatusOK || json.Unmarshal(configRes.Body.Bytes(), &configView) != nil || configView.Document == nil {
		t.Fatalf("config/get before runtime update = %d %s", configRes.Code, configRes.Body.String())
	}
	configView.Document["runtime"] = map[string]any{"maxParallelAgents": 6, "maxTeamChildrenPerAgent": 3, "maxTeamDepth": 2}
	runtimeSave := post("/_lune/config/save", map[string]any{
		"scope":    "",
		"document": configView.Document,
	}, "Bearer lan-secret", "https://10.0.0.5:5173")
	if runtimeSave.Code != http.StatusOK || !strings.Contains(runtimeSave.Body.String(), `"reloaded":true`) {
		t.Fatalf("runtime config/save = %d %s", runtimeSave.Code, runtimeSave.Body.String())
	}
	if got := delegationOf(); got["enabled"] != true || got["childrenPerAgent"] != float64(3) || got["depth"] != float64(2) || got["parallel"] != float64(6) {
		t.Fatalf("saved delegation limits were not applied: %#v", got)
	}

	getRes := post("/_lune/config/get", map[string]any{"scope": ""}, "Bearer lan-secret", "https://10.0.0.5:5173")
	if getRes.Code != http.StatusOK || strings.Contains(getRes.Body.String(), "sk-db-only") || !strings.Contains(getRes.Body.String(), `"savedKeys":["deepseek"]`) {
		t.Fatalf("config/get = %d %s", getRes.Code, getRes.Body.String())
	}

	secret := "transient-provider-secret"
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("credential rejected: " + secret))
	}))
	providerTest := post("/_lune/providers/test", map[string]any{
		"provider": "temporary", "protocol": "responses", "baseURL": provider.URL + "/v1", "apiKey": secret,
	}, "Bearer lan-secret", "https://10.0.0.5:5173")
	provider.Close()
	if providerTest.Code != http.StatusOK || strings.Contains(providerTest.Body.String(), secret) || strings.Contains(providerTest.Body.String(), "credential rejected") {
		t.Fatalf("provider test leaked provider error or secret: %d %s", providerTest.Code, providerTest.Body.String())
	}

	okProvider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"m1","display_name":"Model One","context_window":128000}]}`))
	}))
	okTest := post("/_lune/providers/test", map[string]any{
		"provider": "temporary", "protocol": "responses", "baseURL": okProvider.URL + "/v1", "apiKey": "k-ok",
	}, "Bearer lan-secret", "https://10.0.0.5:5173")
	okProvider.Close()
	if okTest.Code != http.StatusOK || !strings.Contains(okTest.Body.String(), `"modelCount":1`) || !strings.Contains(okTest.Body.String(), `"contextWindow":128000`) {
		t.Fatalf("provider ok test = %d %s", okTest.Code, okTest.Body.String())
	}

	// Remote connection persistence in unified settings.db.
	connSave := post("/_lune/connections/save", map[string]any{
		"mode": "remote", "address": "wss://remote.example:18092/v1/runtime", "name": "Prod", "token": "tok-999", "rememberToken": true,
	}, "Bearer lan-secret", "https://10.0.0.5:5173")
	if connSave.Code != http.StatusOK {
		t.Fatalf("connections/save = %d %s", connSave.Code, connSave.Body.String())
	}
	connList := post("/_lune/connections/list", map[string]any{}, "Bearer lan-secret", "https://10.0.0.5:5173")
	if connList.Code != http.StatusOK || !strings.Contains(connList.Body.String(), "wss://remote.example:18092/v1/runtime") {
		t.Fatalf("connections/list = %d %s", connList.Code, connList.Body.String())
	}

	// Workspace permission persistence & live update.
	permRes := post("/_lune/workspaces/permission", map[string]any{
		"path": workspaceDir, "permissionMode": "auto",
	}, "Bearer lan-secret", "https://10.0.0.5:5173")
	if permRes.Code != http.StatusOK || !strings.Contains(permRes.Body.String(), `"mode":"auto"`) {
		t.Fatalf("workspaces/permission = %d %s", permRes.Code, permRes.Body.String())
	}
	if savedMode, found, permErr := store.LoadWorkspacePermissionMode(workspaceDir); permErr != nil || !found || savedMode != "auto" {
		t.Fatalf("LoadWorkspacePermissionMode = %q, %v, %v; want auto", savedMode, found, permErr)
	}
	badPerm := post("/_lune/workspaces/permission", map[string]any{
		"path": workspaceDir, "permissionMode": "invalid",
	}, "Bearer lan-secret", "https://10.0.0.5:5173")
	if badPerm.Code != http.StatusBadRequest {
		t.Fatalf("invalid permission returned %d, want 400", badPerm.Code)
	}

	// A save that removes the required model config restores the previous Core
	// and reports that the newly saved configuration could not be applied.
	setupSave := post("/_lune/config/save", map[string]any{
		"scope":    "",
		"document": map[string]any{"runtime": map[string]any{"maxParallelAgents": 2}},
	}, "Bearer lan-secret", "https://10.0.0.5:5173")
	var setupResult map[string]any
	if setupSave.Code != http.StatusOK || json.Unmarshal(setupSave.Body.Bytes(), &setupResult) != nil || setupResult["reloaded"] != false || setupResult["requiresReconnect"] != true || setupResult["reconnectError"] == "" {
		t.Fatalf("incomplete config save was reported as reloaded: %d %s", setupSave.Code, setupSave.Body.String())
	}
	if opened := ws.listOpened(); len(opened) != 1 {
		t.Fatalf("old Core was not restored after invalid configuration: %+v", opened)
	} else if entry := ws.entries[opened[0].ID]; !entry.stale || entry.reloadError == "" {
		t.Fatalf("restored workspace is missing its stale configuration status: %+v", entry)
	}
	workspaceID := ws.listOpened()[0].ID
	ws.newCore = func(core.Config) (*core.Core, error) {
		return nil, errors.New("injected Core startup failure")
	}
	if err := ws.reloadWorkspace(workspaceID); err == nil {
		t.Fatal("reload succeeded despite both the saved config and old Core failing to reopen")
	}
	recoveryRes := post("/_lune/workspaces", map[string]any{}, "Bearer lan-secret", "https://10.0.0.5:5173")
	var recoveryList struct {
		Initial string             `json:"initial"`
		Items   []webWorkspaceView `json:"items"`
	}
	if recoveryRes.Code != http.StatusOK || json.Unmarshal(recoveryRes.Body.Bytes(), &recoveryList) != nil || len(recoveryList.Items) != 1 || recoveryList.Items[0].ID != workspaceID || !recoveryList.Items[0].RecoveryRequired || recoveryList.Initial != workspaceID {
		t.Fatalf("recovery workspace was not listed: %d %s", recoveryRes.Code, recoveryRes.Body.String())
	}
	ws.newCore = nil
	repaired := post("/_lune/config/save", map[string]any{
		"scope": "",
		"document": map[string]any{
			"models": map[string]any{
				"providers": map[string]any{
					"deepseek": map[string]any{"protocol": "responses", "baseURL": "https://api.deepseek.com", "apiKey": "sk-recovery-test"},
				},
				"profiles":       map[string]any{"v3": map[string]any{"provider": "deepseek", "model": "deepseek-chat"}},
				"defaultProfile": "v3",
			},
		},
	}, "Bearer lan-secret", "https://10.0.0.5:5173")
	if repaired.Code != http.StatusOK || !strings.Contains(repaired.Body.String(), `"reloaded":true`) {
		t.Fatalf("config repair did not reopen recovery workspace: %d %s", repaired.Code, repaired.Body.String())
	}
	if opened := ws.listOpened(); len(opened) != 1 || opened[0].ID != workspaceID {
		t.Fatalf("workspace did not reopen after config repair: %+v", opened)
	}
	for _, view := range ws.listOpenedDetailed(t.Context()) {
		if view.ID == workspaceID && view.RecoveryRequired {
			t.Fatalf("recovery state remained after reopen: %+v", view)
		}
	}
	_ = ws.shutdown(t.Context())
}

func TestCloseRecoveryWorkspaceMarksItClosed(t *testing.T) {
	store, err := settings.Open(filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	path := t.TempDir()
	record, err := store.EnsureWorkspace(path, true)
	if err != nil {
		t.Fatal(err)
	}
	workspaces := &webWorkspaces{store: store}
	workspaces.recordRecovery(record.ID, record.Path, errors.New("test reopen failure"))
	if err := workspaces.closeWorkspace(record.ID); err != nil {
		t.Fatalf("close recovery workspace: %v", err)
	}
	opened, err := store.ListWorkspaces(true)
	if err != nil {
		t.Fatal(err)
	}
	if len(opened) != 0 {
		t.Fatalf("open workspaces after close = %+v, want none", opened)
	}
	workspaces.mu.Lock()
	_, remains := workspaces.recoveries[record.ID]
	workspaces.mu.Unlock()
	if remains {
		t.Fatal("closed recovery workspace remained in the recovery list")
	}
}

func TestWebWorkspacePersistenceAndSessionSync(t *testing.T) {
	store, err := settings.Open(filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	err = store.SaveWebConfig("", map[string]any{
		"models": map[string]any{
			"providers": map[string]any{
				"deepseek": map[string]any{"protocol": "responses", "baseURL": "https://api.deepseek.com", "apiKey": "sk-test"},
			},
			"profiles": map[string]any{
				"v3": map[string]any{"provider": "deepseek", "model": "deepseek-chat"},
			},
			"defaultProfile": "v3",
		},
	}, "")
	if err != nil {
		t.Fatal(err)
	}

	wsA := t.TempDir()
	wsB := t.TempDir()

	ws := &webWorkspaces{token: "lan-secret", store: store}
	handler := &localWebHandler{
		host: "10.0.0.5:5173", useTLS: true, token: "lan-secret",
		assets:     http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }),
		workspaces: ws,
	}

	post := func(path string, body any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "https://10.0.0.5:5173"+path, bytes.NewReader(raw))
		req.Header.Set("Origin", "https://10.0.0.5:5173")
		req.Header.Set("Authorization", "Bearer lan-secret")
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		return res
	}

	openA := post("/_lune/open-workspace", map[string]any{"path": wsA})
	if openA.Code != http.StatusOK {
		t.Fatalf("open A = %d %s", openA.Code, openA.Body.String())
	}
	var viewA, viewB struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(openA.Body.Bytes(), &viewA)

	openB := post("/_lune/open-workspace", map[string]any{"path": wsB})
	if openB.Code != http.StatusOK {
		t.Fatalf("open B = %d %s", openB.Code, openB.Body.String())
	}
	_ = json.Unmarshal(openB.Body.Bytes(), &viewB)

	if viewA.ID == "" || viewB.ID == "" || viewA.ID == viewB.ID {
		t.Fatalf("invalid workspace IDs: %q, %q", viewA.ID, viewB.ID)
	}
	entryA := ws.entries[viewA.ID]
	oldestSessionID := ""
	for range 501 {
		created, err := entryA.core.CreateSession(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		oldestSessionID = created.ID
	}
	synced, err := ws.syncEntrySessions(t.Context(), entryA)
	if err != nil {
		t.Fatal(err)
	}
	if len(synced) != 501 || !slices.ContainsFunc(synced, func(item settings.WorkspaceSessionRecord) bool { return item.SessionID == oldestSessionID }) {
		t.Fatalf("sync retained %d sessions but omitted the oldest session", len(synced))
	}

	// Create two sessions inside workspace B and remember one as active.
	entryB := ws.entries[viewB.ID]
	s1, err := entryB.core.CreateSession(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = entryB.core.SetSessionTitle(t.Context(), s1.ID, "First B")
	s2, err := entryB.core.CreateSession(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = entryB.core.SetSessionTitle(t.Context(), s2.ID, "Second B")

	if res := post("/_lune/workspaces/active-session", map[string]any{"id": viewB.ID, "sessionId": s1.ID}); res.Code != http.StatusOK {
		t.Fatalf("active-session = %d %s", res.Code, res.Body.String())
	}

	var listPayload struct {
		Initial string `json:"initial"`
		Items   []struct {
			ID            string                            `json:"id"`
			Path          string                            `json:"path"`
			LastSessionID string                            `json:"lastSessionId"`
			Sessions      []settings.WorkspaceSessionRecord `json:"sessions"`
		} `json:"items"`
	}
	listRes := post("/_lune/workspaces", map[string]any{})
	if listRes.Code != http.StatusOK || json.Unmarshal(listRes.Body.Bytes(), &listPayload) != nil {
		t.Fatalf("workspaces = %d %s", listRes.Code, listRes.Body.String())
	}
	if len(listPayload.Items) != 2 || listPayload.Items[0].ID != viewB.ID || listPayload.Initial != viewB.ID {
		t.Fatalf("expected B first and initial, got %+v", listPayload)
	}
	if listPayload.Items[0].LastSessionID != s1.ID || len(listPayload.Items[0].Sessions) < 2 {
		t.Fatalf("expected sessions & lastSessionId on B, got %+v", listPayload.Items[0])
	}

	// Shutdown and restore into a brand new webWorkspaces manager using the same SQLite store.
	if err := ws.shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}

	wsRestarted := &webWorkspaces{token: "lan-secret", store: store}
	savedOpen, err := store.ListWorkspaces(true)
	if err != nil {
		t.Fatal(err)
	}
	for _, rec := range savedOpen {
		if _, err := wsRestarted.openWithOptions(rec.Path, false); err != nil {
			t.Fatalf("restore %s: %v", rec.Path, err)
		}
	}
	restoredViews := wsRestarted.listOpenedDetailed(t.Context())
	if len(restoredViews) != 2 || restoredViews[0].ID != viewB.ID || restoredViews[1].ID != viewA.ID {
		t.Fatalf("restored order/IDs mismatch: %+v", restoredViews)
	}
	if restoredViews[0].LastSessionID != s1.ID || len(restoredViews[0].Sessions) < 2 || len(restoredViews[1].Sessions) != 501 {
		t.Fatalf("restored sessions mismatch: %+v", restoredViews[0])
	}

	// Closing A marks it closed in SQLite; reopening A reuses its exact stable ID.
	if err := wsRestarted.closeWorkspace(viewA.ID); err != nil {
		t.Fatal(err)
	}
	remaining, err := store.ListWorkspaces(true)
	if err != nil || len(remaining) != 1 || remaining[0].ID != viewB.ID {
		t.Fatalf("remaining open workspaces = %+v, err=%v", remaining, err)
	}
	reopenedA, err := wsRestarted.open(wsA)
	if err != nil {
		t.Fatal(err)
	}
	if reopenedA.ID != viewA.ID {
		t.Fatalf("reopened ID = %q, want %q", reopenedA.ID, viewA.ID)
	}
	_ = wsRestarted.shutdown(t.Context())
}
