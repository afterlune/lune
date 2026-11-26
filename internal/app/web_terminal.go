//go:build lune_web

package app

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/afterlune/lune/internal/ptyterm"
	"github.com/coder/websocket"
)

const terminalSubprotocol = "lune-terminal.v1"
const terminalAuthPrefix = "lune-terminal-auth."

func authorizeTerminalRequest(r *http.Request, expectedToken string) bool {
	if expectedToken == "" {
		return false
	}
	if auth := r.Header.Get("Authorization"); auth != "" {
		if subtle.ConstantTimeCompare([]byte(auth), []byte("Bearer "+expectedToken)) == 1 {
			return true
		}
	}
	for _, header := range r.Header.Values("Sec-WebSocket-Protocol") {
		for _, part := range strings.Split(header, ",") {
			tokenProto := strings.TrimSpace(part)
			if encoded, ok := strings.CutPrefix(tokenProto, terminalAuthPrefix); ok {
				decoded, err := base64.RawURLEncoding.DecodeString(encoded)
				if err == nil && subtle.ConstantTimeCompare(decoded, []byte(expectedToken)) == 1 {
					return true
				}
			}
		}
	}
	return false
}

func parseTerminalDimension(raw string, fallback uint16) uint16 {
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v <= 0 || v > 1000 {
		return fallback
	}
	return uint16(v)
}

func (s *webWorkspaces) trackTerminal(sess ptyterm.Session, workspaceID string, add bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activeTerminals == nil {
		if !add {
			return false
		}
		s.activeTerminals = make(map[ptyterm.Session]string)
	}
	if add {
		entry := s.entries[workspaceID]
		if s.closing || entry == nil || entry.closing {
			return false
		}
		s.activeTerminals[sess] = workspaceID
	} else {
		delete(s.activeTerminals, sess)
	}
	return true
}

func (s *webWorkspaces) closeAllTerminals() {
	s.mu.Lock()
	list := make([]ptyterm.Session, 0, len(s.activeTerminals))
	for sess := range s.activeTerminals {
		list = append(list, sess)
	}
	s.activeTerminals = nil
	s.mu.Unlock()
	for _, sess := range list {
		_ = sess.Close()
	}
}

func (s *webWorkspaces) closeWorkspaceTerminals(workspaceID string) {
	s.mu.Lock()
	var sessions []ptyterm.Session
	for sess, owner := range s.activeTerminals {
		if owner == workspaceID {
			sessions = append(sessions, sess)
			delete(s.activeTerminals, sess)
		}
	}
	s.mu.Unlock()
	for _, sess := range sessions {
		_ = sess.Close()
	}
}

func (s *webWorkspaces) serveTerminal(w http.ResponseWriter, r *http.Request, workspaceID string) {
	if !authorizeTerminalRequest(r, s.token) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	s.mu.Lock()
	entry := s.entries[workspaceID]
	closing := s.closing || entry == nil || entry.closing
	workspacePath := ""
	if entry != nil {
		workspacePath = entry.workspace.Path
	}
	s.mu.Unlock()
	if closing {
		http.Error(w, "workspace host is shutting down", http.StatusServiceUnavailable)
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		Subprotocols:       []string{terminalSubprotocol},
		InsecureSkipVerify: true, // Origin & cross-site checks are enforced by localWebHandler.ServeHTTP.
	})
	if err != nil {
		return
	}
	defer conn.CloseNow()

	q := r.URL.Query()
	cols := parseTerminalDimension(q.Get("cols"), 80)
	rows := parseTerminalDimension(q.Get("rows"), 24)

	sess, err := ptyterm.Open(ptyterm.Options{
		Dir:  workspacePath,
		Cols: cols,
		Rows: rows,
	})
	if err != nil {
		msg, _ := json.Marshal(map[string]any{"type": "error", "message": err.Error()})
		_ = conn.Write(r.Context(), websocket.MessageText, msg)
		_ = conn.Close(websocket.StatusInternalError, "failed to open terminal")
		return
	}
	if !s.trackTerminal(sess, workspaceID, true) {
		_ = sess.Close()
		_ = conn.Close(websocket.StatusTryAgainLater, "workspace is closing")
		return
	}
	defer func() {
		s.trackTerminal(sess, workspaceID, false)
		_ = sess.Close()
	}()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Pump PTY output -> WebSocket binary frames.
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		defer cancel()
		buf := make([]byte, 4096)
		for {
			n, rerr := sess.Read(buf)
			if n > 0 {
				if werr := conn.Write(ctx, websocket.MessageBinary, buf[:n]); werr != nil {
					return
				}
			}
			if rerr != nil {
				if rerr == io.EOF || ctx.Err() == nil {
					code, _ := sess.Wait()
					exitMsg, _ := json.Marshal(map[string]any{"type": "exit", "code": code})
					_ = conn.Write(ctx, websocket.MessageText, exitMsg)
					_ = conn.Close(websocket.StatusNormalClosure, "terminal exited")
				}
				return
			}
		}
	}()

	// Pump WebSocket frames -> PTY input / resize.
	for {
		msgType, data, rerr := conn.Read(ctx)
		if rerr != nil {
			break
		}
		if msgType == websocket.MessageBinary {
			if _, werr := sess.Write(data); werr != nil {
				break
			}
			continue
		}
		var ctrl struct {
			Type string `json:"type"`
			Data string `json:"data"`
			Cols uint16 `json:"cols"`
			Rows uint16 `json:"rows"`
		}
		if err := json.Unmarshal(data, &ctrl); err != nil {
			continue
		}
		switch ctrl.Type {
		case "resize":
			_ = sess.Resize(ctrl.Cols, ctrl.Rows)
		case "input":
			if ctrl.Data != "" {
				if _, werr := io.WriteString(sess, ctrl.Data); werr != nil {
					cancel()
					break
				}
			}
		case "close":
			cancel()
		}
		if ctx.Err() != nil {
			break
		}
	}

	cancel()
	_ = sess.Close()
	<-readDone
}
