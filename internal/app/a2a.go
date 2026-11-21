package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/afterlune/lune/internal/a2a"
	core "github.com/afterlune/luneCore"
)

var runA2AServer = serveA2A

func validateA2AListen(address, token string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("invalid A2A listen address: %w", err)
	}
	ip := net.ParseIP(host)
	if strings.TrimSpace(token) == "" && (ip == nil || !ip.IsLoopback()) {
		return errors.New("non-loopback A2A listening requires LUNE_A2A_TOKEN; use HTTPS through a trusted reverse proxy for remote access")
	}
	return nil
}
func serveA2A(client *core.Core, address, token string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return serveA2AContext(ctx, client, address, token)
}
func serveA2AContext(ctx context.Context, client *core.Core, address, token string) error {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	handler, err := a2a.OpenServer(ctx, a2a.NewCoreBackend(client), token, filepath.Join(client.Workspace(), ".lune", "a2a"))
	if err != nil {
		_ = listener.Close()
		return err
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second}
	result := make(chan error, 1)
	go func() { result <- server.Serve(listener) }()
	var serveErr error
	select {
	case serveErr = <-result:
	case <-ctx.Done():
	}
	cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// Close admission and stop tasks while Shutdown releases any waiting handlers.
	closed := make(chan error, 1)
	go func() { closed <- handler.Close(cleanup) }()
	shutdownErr := server.Shutdown(cleanup)
	if shutdownErr != nil {
		_ = server.Close()
	}
	closeErr := <-closed
	if errors.Is(serveErr, http.ErrServerClosed) {
		serveErr = nil
	}
	return errors.Join(serveErr, shutdownErr, closeErr)
}
