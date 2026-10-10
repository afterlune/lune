package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
)

const (
	tuiLogMaxBytes    int64 = 10 * 1024 * 1024
	tuiLogBackupCount       = 3
)

type rotatingLogWriter struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	backups  int
	file     *os.File
	size     int64
	closed   bool
}

func newRotatingLogWriter(path string, maxBytes int64, backups int) (*rotatingLogWriter, error) {
	if path == "" {
		return nil, errors.New("log path is empty")
	}
	if maxBytes <= 0 {
		return nil, errors.New("log max size must be positive")
	}
	if backups < 0 {
		return nil, errors.New("log backup count must not be negative")
	}
	w := &rotatingLogWriter{path: path, maxBytes: maxBytes, backups: backups}
	if err := w.openCurrent(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *rotatingLogWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return 0, os.ErrClosed
	}
	if w.file == nil {
		return 0, errors.New("log file is unavailable")
	}
	if len(p) > 0 && w.size > 0 && w.size+int64(len(p)) > w.maxBytes {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := w.file.Write(p)
	w.size += int64(n)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	return n, err
}

func (w *rotatingLogWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

func (w *rotatingLogWriter) rotate() error {
	if err := w.file.Close(); err != nil {
		w.file = nil
		return errors.Join(fmt.Errorf("close log before rotation: %w", err), w.reopenAfterRotationError())
	}
	w.file = nil

	if err := w.rotateFiles(); err != nil {
		return errors.Join(fmt.Errorf("rotate log: %w", err), w.reopenAfterRotationError())
	}
	if err := w.openCurrent(); err != nil {
		return errors.Join(fmt.Errorf("open new log file: %w", err), w.reopenAfterRotationError())
	}
	return nil
}

func (w *rotatingLogWriter) rotateFiles() error {
	if w.backups == 0 {
		if err := os.Remove(w.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := os.Remove(w.backupPath(w.backups)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for index := w.backups - 1; index >= 1; index-- {
		if err := moveIfPresent(w.backupPath(index), w.backupPath(index+1)); err != nil {
			return err
		}
	}
	return moveIfPresent(w.path, w.backupPath(1))
}

func (w *rotatingLogWriter) backupPath(index int) string {
	return fmt.Sprintf("%s.%d", w.path, index)
}

func moveIfPresent(source, target string) error {
	if _, err := os.Stat(source); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(source, target)
}

func (w *rotatingLogWriter) reopenAfterRotationError() error {
	if err := w.openCurrent(); err != nil {
		return fmt.Errorf("reopen active log after rotation failure: %w", err)
	}
	return nil
}

func (w *rotatingLogWriter) openCurrent() error {
	file, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return err
	}
	w.file = file
	w.size = info.Size()
	return nil
}
