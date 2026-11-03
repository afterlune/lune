package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/afterlune/lune/internal/a2aproxy"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	root, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	workerFactory := a2aproxy.NewProcessWorkerFactory(a2aproxy.ProcessConfig{
		Binary:         envOr("LUNE_BINARY", "/usr/local/bin/lune"),
		Workspace:      envOr("LUNE_WORKSPACE", "/workspace"),
		ListenAddress:  envOr("LUNE_A2A_WORKER_ADDR", "127.0.0.1:18092"),
		StartupTimeout: 60 * time.Second,
	})
	manager, err := a2aproxy.NewManager(root, a2aproxy.Config{
		Token:         os.Getenv("LUNE_A2A_TOKEN"),
		PublicURL:     envOr("LUNE_A2A_PUBLIC_URL", "http://lune-a2a:18091"),
		IdleTimeout:   time.Hour,
		WorkerFactory: workerFactory,
	})
	if err != nil {
		return err
	}
	go manager.RunReaper(root, time.Minute)

	address := envOr("LUNE_A2A_PROXY_ADDR", ":18091")
	server := &http.Server{
		Addr:              address,
		Handler:           manager,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      90 * time.Second,
		IdleTimeout:       time.Minute,
	}
	serveResult := make(chan error, 1)
	go func() { serveResult <- server.ListenAndServe() }()
	slog.Info("Lune A2A proxy listening", "address", address, "workspace", envOr("LUNE_WORKSPACE", "/workspace"))

	select {
	case err := <-serveResult:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-root.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	shutdownErr := server.Shutdown(shutdownCtx)
	workerErr := manager.Close(shutdownCtx)
	return errors.Join(shutdownErr, workerErr)
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
