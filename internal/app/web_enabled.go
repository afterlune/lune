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
)

//go:embed webdist
var webAssets embed.FS

type localWebHandler struct {
	host       string
	token      string
	code       string
	paired     bool
	mu         sync.Mutex
	assets     http.Handler
	runtime    http.Handler
	workspaces *webWorkspaces
}

func (h *localWebHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	if r.Host != h.host || ip != "127.0.0.1" {
		http.Error(w, "unexpected local host", http.StatusForbidden)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	switch {
	case strings.HasPrefix(r.URL.Path, "/_lune/"), r.URL.Path == "/v1/runtime", strings.HasPrefix(r.URL.Path, "/v1/workspaces/"):
		if r.Header.Get("Origin") != "http://"+h.host {
			http.Error(w, "unexpected local origin", http.StatusForbidden)
			return
		}
		if r.URL.Path == "/v1/runtime" {
			h.runtime.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/_lune/pair" {
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
	workspace, err := filepath.Abs(options.workspace)
	if err != nil {
		return err
	}
	token := rand.Text()
	workspaces := &webWorkspaces{token: token, diagnostics: stderr}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		runErr = errors.Join(runErr, workspaces.shutdown(ctx))
	}()
	initial, err := workspaces.open(workspace)
	if err != nil {
		return err
	}
	workspaces.initial = initial.ID
	assets, err := fs.Sub(webAssets, "webdist")
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer listener.Close()
	handler := &localWebHandler{host: listener.Addr().String(), token: token, code: rand.Text(),
		assets: http.FileServerFS(assets), runtime: workspaces.hosts[initial.ID], workspaces: workspaces}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 90 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	result := make(chan error, 1)
	go func() {
		err := server.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		result <- err
	}()
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
