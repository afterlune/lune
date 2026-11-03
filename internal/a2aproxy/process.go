package a2aproxy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"
)

type ProcessConfig struct {
	Binary         string
	Workspace      string
	ListenAddress  string
	StartupTimeout time.Duration
}

type processWorker struct {
	cmd     *exec.Cmd
	client  *http.Client
	base    string
	done    chan struct{}
	waitErr error
}

func NewProcessWorkerFactory(cfg ProcessConfig) WorkerFactory {
	return func(ctx context.Context) (Worker, error) {
		return startProcessWorker(ctx, cfg)
	}
}

func startProcessWorker(ctx context.Context, cfg ProcessConfig) (*processWorker, error) {
	if cfg.Binary == "" {
		cfg.Binary = "/usr/local/bin/lune"
	}
	if cfg.Workspace == "" {
		cfg.Workspace = "/workspace"
	}
	if cfg.ListenAddress == "" {
		cfg.ListenAddress = "127.0.0.1:18092"
	}
	if cfg.StartupTimeout <= 0 {
		cfg.StartupTimeout = 60 * time.Second
	}
	host, _, err := net.SplitHostPort(cfg.ListenAddress)
	if err != nil || (host != "127.0.0.1" && host != "localhost" && host != "::1") {
		return nil, errors.New("worker A2A address must bind to loopback")
	}
	if info, err := os.Stat(cfg.Workspace); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("workspace is unavailable: %s", cfg.Workspace)
	}

	cmd := exec.Command(
		cfg.Binary,
		"-C", cfg.Workspace,
		"--permission", "workspace",
		"--a2a-listen", cfg.ListenAddress,
	)
	cmd.Dir = cfg.Workspace
	// The parent is the authenticated network boundary. The native worker only
	// listens on loopback, so keep the public bearer token out of its environment.
	cmd.Env = removeEnv(os.Environ(), "LUNE_A2A_TOKEN")
	cmd.Stdout = io.Discard
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start Lune A2A worker: %w", err)
	}
	worker := &processWorker{
		cmd: cmd,
		client: &http.Client{
			Timeout:   75 * time.Second,
			Transport: &http.Transport{Proxy: nil},
		},
		base: "http://" + cfg.ListenAddress,
		done: make(chan struct{}),
	}
	go func() {
		worker.waitErr = cmd.Wait()
		close(worker.done)
	}()

	startupCtx, cancel := context.WithTimeout(ctx, cfg.StartupTimeout)
	defer cancel()
	if err := waitForWorker(startupCtx, worker); err != nil {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = worker.Stop(stopCtx)
		stopCancel()
		return nil, err
	}
	return worker, nil
}

func waitForWorker(ctx context.Context, worker *processWorker) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{Proxy: nil}}
	for {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, worker.base+"/healthz", nil)
		if err == nil {
			response, requestErr := client.Do(request)
			if requestErr == nil {
				_ = response.Body.Close()
				if response.StatusCode == http.StatusOK {
					return nil
				}
			}
		}
		select {
		case <-worker.done:
			return fmt.Errorf("Lune A2A worker exited during startup: %w", worker.waitErr)
		case <-ctx.Done():
			return fmt.Errorf("Lune A2A worker did not become ready: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func (w *processWorker) RoundTrip(ctx context.Context, path, host string, headers http.Header, body []byte) (int, http.Header, []byte, error) {
	if !strings.HasPrefix(path, "/") {
		return 0, nil, nil, errors.New("invalid A2A request path")
	}
	target, err := url.Parse(w.base + path)
	if err != nil {
		return 0, nil, nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), bytes.NewReader(body))
	if err != nil {
		return 0, nil, nil, err
	}
	request.Header = headers.Clone()
	request.Host = host
	response, err := w.client.Do(request)
	if err != nil {
		return 0, nil, nil, err
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxJSONBodyBytes+1))
	if err != nil {
		return 0, nil, nil, err
	}
	if len(responseBody) > maxJSONBodyBytes {
		return 0, nil, nil, errors.New("Lune response body is too large")
	}
	return response.StatusCode, response.Header.Clone(), responseBody, nil
}

func (w *processWorker) Done() <-chan struct{} { return w.done }

func (w *processWorker) Stop(ctx context.Context) error {
	select {
	case <-w.done:
		return nil
	default:
	}
	if err := w.cmd.Process.Signal(os.Interrupt); err != nil {
		if killErr := w.cmd.Process.Kill(); killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
			return errors.Join(err, killErr)
		}
	}
	select {
	case <-w.done:
		return nil
	case <-ctx.Done():
		_ = w.cmd.Process.Kill()
		<-w.done
		return ctx.Err()
	}
}

func removeEnv(environment []string, key string) []string {
	prefix := key + "="
	result := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		if strings.HasPrefix(entry, prefix) {
			continue
		}
		result = append(result, entry)
	}
	return result
}
