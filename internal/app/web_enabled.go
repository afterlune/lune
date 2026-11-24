//go:build lune_web

package app

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/afterlune/lune/internal/settings"
)

//go:embed webdist
var webAssets embed.FS

type localWebHandler struct {
	host       string
	useTLS     bool
	token      string
	code       string
	paired     bool
	mu         sync.Mutex
	assets     http.Handler
	runtime    http.Handler
	workspaces *webWorkspaces
}

func (h *localWebHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !h.useTLS {
		ip, _, _ := net.SplitHostPort(r.RemoteAddr)
		if r.Host != h.host || ip != "127.0.0.1" {
			http.Error(w, "unexpected local host", http.StatusForbidden)
			return
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	if h.useTLS {
		w.Header().Set("Referrer-Policy", "same-origin")
	} else {
		w.Header().Set("Referrer-Policy", "no-referrer")
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")

	if r.URL.Path == "/_lune/status" {
		if h.useTLS {
			if origin := r.Header.Get("Origin"); origin != "" && origin != "null" && !strings.EqualFold(origin, "https://"+r.Host) {
				http.Error(w, "unexpected origin", http.StatusForbidden)
				return
			}
		} else if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "use GET", http.StatusMethodNotAllowed)
			return
		}
		authMode := "pair"
		if h.useTLS {
			authMode = "token"
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"host": true, "auth": authMode})
		return
	}

	switch {
	case strings.HasPrefix(r.URL.Path, "/_lune/"), r.URL.Path == "/v1/runtime", strings.HasPrefix(r.URL.Path, "/v1/workspaces/"):
		if h.useTLS {
			if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
				http.Error(w, "unexpected cross-site request", http.StatusForbidden)
				return
			}
			origin := r.Header.Get("Origin")
			expected := "https://" + r.Host
			if origin != "" && origin != "null" && !strings.EqualFold(origin, expected) {
				http.Error(w, "unexpected web origin", http.StatusForbidden)
				return
			}
		} else {
			if r.Header.Get("Origin") != "http://"+h.host {
				http.Error(w, "unexpected local origin", http.StatusForbidden)
				return
			}
		}
		if r.URL.Path == "/_lune/auth" {
			h.verifyAuth(w, r)
			return
		}
		if r.URL.Path == "/v1/runtime" {
			if h.runtime == nil {
				http.NotFound(w, r)
				return
			}
			h.runtime.ServeHTTP(w, r)
			return
		}
		if !h.useTLS && r.URL.Path == "/_lune/pair" {
			h.pair(w, r)
			return
		}
		if h.workspaces == nil {
			http.NotFound(w, r)
			return
		}
		h.workspaces.serve(w, r)
	default:
		h.assets.ServeHTTP(w, r)
	}
}

func (h *localWebHandler) verifyAuth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "use POST", http.StatusMethodNotAllowed)
		return
	}
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+h.token)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "auth": "token"})
}

func (h *localWebHandler) pair(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "use POST", http.StatusMethodNotAllowed)
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 128))
	if err != nil {
		http.Error(w, "invalid pairing request", http.StatusBadRequest)
		return
	}
	h.mu.Lock()
	accepted := !h.paired && subtle.ConstantTimeCompare(data, []byte(h.code)) == 1
	if accepted {
		h.paired = true
	}
	h.mu.Unlock()
	if !accepted {
		http.Error(w, "invalid or used pairing link", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"token": h.token})
}

func runWeb(args []string, stdout, stderr io.Writer) (runErr error) {
	options, run, err := parseWebOptions(args, stdout)
	if err != nil || !run {
		return err
	}
	useTLS := options.tlsCert != "" && options.tlsKey != ""
	var token string
	if options.tokenFile != "" {
		rawToken, err := os.ReadFile(options.tokenFile)
		if err != nil {
			return fmt.Errorf("read token file: %w", err)
		}
		token = strings.TrimSpace(string(rawToken))
		if token == "" {
			return errors.New("token file is empty")
		}
	} else {
		token = rand.Text()
	}

	store, err := settings.OpenUser()
	if err != nil {
		return fmt.Errorf("open settings database: %w", err)
	}
	defer func() {
		runErr = errors.Join(runErr, store.Close())
	}()

	workspaces := &webWorkspaces{
		token:       token,
		diagnostics: stderr,
		store:       store,
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		runErr = errors.Join(runErr, workspaces.shutdown(ctx))
	}()

	var initialPath string
	if strings.TrimSpace(options.workspace) != "" {
		abs, err := filepath.Abs(options.workspace)
		if err != nil {
			return err
		}
		initialPath = abs
	} else if recent, err := store.WebConnections(); err == nil {
		for _, conn := range recent {
			if conn.Mode == "local" {
				if info, statErr := os.Stat(conn.Address); statErr == nil && info.IsDir() {
					initialPath = conn.Address
					break
				}
			}
		}
	}

	if initialPath != "" {
		initial, err := workspaces.open(initialPath)
		if errors.Is(err, errWebConfigRequired) {
			workspaces.mu.Lock()
			workspaces.pendingInitial = initialPath
			workspaces.needsSetup = true
			workspaces.mu.Unlock()
			fmt.Fprintln(stderr, "No model provider configured in settings DB yet; open Settings in Web UI to configure providers and models.")
		} else if err != nil {
			return err
		} else {
			workspaces.initial = initial.ID
		}
	}

	assets, err := fs.Sub(webAssets, "webdist")
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", options.listen)
	if err != nil {
		return err
	}
	defer listener.Close()

	var initialRuntime http.Handler
	if workspaces.initial != "" {
		initialRuntime = workspaces.entries[workspaces.initial].host
	}
	handler := &localWebHandler{
		host:       listener.Addr().String(),
		useTLS:     useTLS,
		token:      token,
		code:       rand.Text(),
		assets:     http.FileServerFS(assets),
		runtime:    initialRuntime,
		workspaces: workspaces,
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 90 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	result := make(chan error, 1)
	go func() {
		var serveErr error
		if useTLS {
			serveErr = server.ServeTLS(listener, options.tlsCert, options.tlsKey)
		} else {
			serveErr = server.Serve(listener)
		}
		if errors.Is(serveErr, http.ErrServerClosed) {
			serveErr = nil
		}
		result <- serveErr
	}()

	if useTLS {
		fmt.Fprintf(stderr, "Lune Web HTTPS is listening on https://%s/\n", handler.host)
	} else {
		link := "http://" + handler.host + "/#lune-pair=" + handler.code
		if options.noOpen {
			fmt.Fprintln(stdout, link)
		} else {
			fmt.Fprintf(stderr, "Lune Web is listening on http://%s/\n", handler.host)
			go func() {
				if err := openWebBrowser(link); err != nil && ctx.Err() == nil {
					fmt.Fprintln(stdout, link)
				}
			}()
		}
	}

	select {
	case runErr = <-result:
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return errors.Join(runErr, server.Shutdown(shutdownCtx), server.Close())
}

func openWebBrowser(address string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", address).Run()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", address).Run()
	default:
		if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
			return errors.New("no graphical session")
		}
		return exec.Command("xdg-open", address).Run()
	}
}
