//go:build lune_web

package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/afterlune/lune/internal/settings"
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
	reloadRes := post("/_lune/reload-workspace", map[string]any{"id": oldWorkspaceID}, "Bearer lan-secret", "https://10.0.0.5:5173")
	if reloadRes.Code != http.StatusOK {
		t.Fatalf("reload-workspace = %d %s", reloadRes.Code, reloadRes.Body.String())
	}
	workspacesRes = post("/_lune/workspaces", map[string]any{}, "Bearer lan-secret", "https://10.0.0.5:5173")
	if workspacesRes.Code != http.StatusOK || json.Unmarshal(workspacesRes.Body.Bytes(), &workspaceList) != nil || len(workspaceList.Items) != 1 || workspaceList.Items[0].ID == oldWorkspaceID {
		t.Fatalf("reloaded workspace = %d %s", workspacesRes.Code, workspacesRes.Body.String())
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
	_ = ws.shutdown(t.Context())
}
