//go:build lune_web

package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	core "github.com/afterlune/luneCore"
)

func TestWebTerminalEndpoint(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell test")
	}
	dir := t.TempDir()
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	const wsID = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	ws := &webWorkspaces{
		token: "term-secret",
		entries: map[string]*webWorkspaceEntry{
			wsID: {
				workspace: core.Workspace{ID: wsID, Path: dir},
				info:      info,
			},
		},
	}
	handler := &localWebHandler{
		host: "127.0.0.1:18001", useTLS: true, token: "term-secret",
		assets:     http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }),
		workspaces: ws,
	}
	srv := httptest.NewTLSServer(handler)
	defer srv.Close()

	// Unauthenticated request is rejected.
	unauthReq, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/workspaces/"+wsID+"/terminal", nil)
	unauthResp, err := srv.Client().Do(unauthReq)
	if err != nil {
		t.Fatal(err)
	}
	unauthResp.Body.Close()
	if unauthResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want 401", unauthResp.StatusCode)
	}

	wsURL := "wss" + strings.TrimPrefix(srv.URL, "https") + "/v1/workspaces/" + wsID + "/terminal?cols=90&rows=26"
	authSubprotocol := terminalAuthPrefix + base64.RawURLEncoding.EncodeToString([]byte("term-secret"))

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPClient:   srv.Client(),
		Subprotocols: []string{terminalSubprotocol, authSubprotocol},
	})
	if err != nil {
		t.Fatalf("websocket dial: %v", err)
	}
	defer conn.CloseNow()

	resizeFrame, _ := json.Marshal(map[string]any{"type": "resize", "cols": 110, "rows": 32})
	if err := conn.Write(ctx, websocket.MessageText, resizeFrame); err != nil {
		t.Fatalf("write resize: %v", err)
	}
	if err := conn.Write(ctx, websocket.MessageBinary, []byte("echo WEB_TERM_OK && exit 0\n")); err != nil {
		t.Fatalf("write command: %v", err)
	}

	var output strings.Builder
	var sawExit bool
	for !sawExit {
		msgType, data, rerr := conn.Read(ctx)
		if rerr != nil {
			break
		}
		if msgType == websocket.MessageBinary {
			output.Write(data)
			continue
		}
		var ctrl struct {
			Type string `json:"type"`
			Code *int   `json:"code"`
		}
		if json.Unmarshal(data, &ctrl) == nil && ctrl.Type == "exit" {
			if ctrl.Code == nil || *ctrl.Code != 0 {
				t.Fatalf("exit code = %v, want 0", ctrl.Code)
			}
			sawExit = true
		}
	}

	if !sawExit {
		t.Fatal("did not observe exit message from terminal websocket")
	}
	if !strings.Contains(output.String(), "WEB_TERM_OK") {
		t.Fatalf("terminal output %q missing WEB_TERM_OK", output.String())
	}
}
