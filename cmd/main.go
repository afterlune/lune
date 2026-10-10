package main

import (
	"bufio"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/afterlune/lune/core"
	"github.com/afterlune/lune/internal/tui"
	"github.com/eiannone/keyboard"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() (runErr error) {
	workspace, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get workspace: %w", err)
	}
	client, err := core.New(core.Config{Workspace: workspace})
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, client.Close()) }()
	if tui.UseTUI(os.Stdin, os.Stdout) {
		logPath := filepath.Join(workspace, ".luneagent", "agent.log")
		logFile, err := newRotatingLogWriter(logPath, tuiLogMaxBytes, tuiLogBackupCount)
		if err != nil {
			return fmt.Errorf("open TUI log: %w", err)
		}
		defer logFile.Close()
		previousLogger := slog.Default()
		defer slog.SetDefault(previousLogger)
		slog.SetDefault(slog.New(slog.NewTextHandler(logFile, &slog.HandlerOptions{Level: slog.LevelInfo})))
		return tui.Run(client)
	}
	return runCoreChatLoop(client, bufio.NewReader(os.Stdin), os.Stdout, os.Stderr, startKeyboardInputWatcher)
}

func startKeyboardInputWatcher() (<-chan cliInputEvent, func(), error) {
	info, err := os.Stdin.Stat()
	if err != nil {
		return nil, func() {}, fmt.Errorf("inspect stdin: %w", err)
	}
	if info.Mode()&os.ModeCharDevice == 0 {
		return nil, func() {}, nil
	}
	keys, err := keyboard.GetKeys(10)
	if err != nil {
		return nil, func() {}, err
	}

	inputEvents := make(chan cliInputEvent, 16)
	stopRequested := make(chan struct{})
	watcherDone := make(chan struct{})
	go func() {
		defer close(watcherDone)
		defer close(inputEvents)
		for event := range keys {
			if event.Err == nil && event.Key != keyboard.KeyEsc && event.Rune == 0 {
				continue
			}
			input := cliInputEvent{
				rune:   event.Rune,
				escape: event.Key == keyboard.KeyEsc,
				err:    event.Err,
			}
			select {
			case inputEvents <- input:
			case <-stopRequested:
				return
			}
		}
	}()

	var once sync.Once
	stop := func() {
		once.Do(func() {
			close(stopRequested)
			_ = keyboard.Close()
			<-watcherDone
		})
	}
	return inputEvents, stop, nil
}
