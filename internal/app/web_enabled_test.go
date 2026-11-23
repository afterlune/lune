//go:build lune_web

package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
