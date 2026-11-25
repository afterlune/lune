//go:build linux

package ptyterm

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

type unixSession struct {
	ptmx   *os.File
	cmd    *exec.Cmd
	mu     sync.Mutex
	closed bool

	waitOnce sync.Once
	waitCode int
	waitErr  error

	closeOnce sync.Once
	closeErr  error
}

func defaultShell(explicit string) (string, []string) {
	candidates := []string{explicit, os.Getenv("SHELL"), "/bin/bash", "/bin/zsh", "/bin/sh"}
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if path, err := exec.LookPath(c); err == nil {
			return path, nil
		}
	}
	return "/bin/sh", nil
}

func openPTY(dir, explicitShell string, cols, rows uint16) (Session, error) {
	ptmx, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open /dev/ptmx: %w", err)
	}

	if err := unix.IoctlSetPointerInt(int(ptmx.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		_ = ptmx.Close()
		return nil, fmt.Errorf("unlock pty: %w", err)
	}
	ptsNum, err := unix.IoctlGetInt(int(ptmx.Fd()), unix.TIOCGPTN)
	if err != nil {
		_ = ptmx.Close()
		return nil, fmt.Errorf("query pty index: %w", err)
	}
	slaveName := fmt.Sprintf("/dev/pts/%d", ptsNum)
	slave, err := os.OpenFile(slaveName, os.O_RDWR|syscall.O_NOCTTY|syscall.O_CLOEXEC, 0)
	if err != nil {
		_ = ptmx.Close()
		return nil, fmt.Errorf("open %s: %w", slaveName, err)
	}

	if err := unix.IoctlSetWinsize(int(ptmx.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: rows, Col: cols}); err != nil {
		_ = slave.Close()
		_ = ptmx.Close()
		return nil, fmt.Errorf("set initial winsize: %w", err)
	}

	shell, args := defaultShell(explicitShell)
	cmd := exec.Command(shell, args...)
	cmd.Dir = dir
	cmd.Env = buildEnv()
	cmd.Stdin = slave
	cmd.Stdout = slave
	cmd.Stderr = slave
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid:  true,
		Setctty: true,
		Ctty:    0,
	}

	if err := cmd.Start(); err != nil {
		_ = slave.Close()
		_ = ptmx.Close()
		return nil, fmt.Errorf("start shell %q: %w", shell, err)
	}
	_ = slave.Close()

	return &unixSession{ptmx: ptmx, cmd: cmd}, nil
}

func (s *unixSession) Read(p []byte) (int, error) {
	return s.ptmx.Read(p)
}

func (s *unixSession) Write(p []byte) (int, error) {
	return s.ptmx.Write(p)
}

func (s *unixSession) Resize(cols, rows uint16) error {
	cols, rows = normalizeSize(cols, rows)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("terminal session is closed")
	}
	return unix.IoctlSetWinsize(int(s.ptmx.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: rows, Col: cols})
}

func (s *unixSession) Wait() (int, error) {
	s.waitOnce.Do(func() {
		err := s.cmd.Wait()
		if err == nil {
			s.waitCode = 0
			return
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			s.waitCode = exitErr.ExitCode()
			return
		}
		s.waitCode = -1
		s.waitErr = err
	})
	return s.waitCode, s.waitErr
}

func (s *unixSession) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()

		if s.cmd != nil && s.cmd.Process != nil {
			pid := s.cmd.Process.Pid
			_ = syscall.Kill(-pid, syscall.SIGHUP)
		}
		s.closeErr = s.ptmx.Close()

		done := make(chan struct{})
		go func() {
			_, _ = s.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(150 * time.Millisecond):
			if s.cmd != nil && s.cmd.Process != nil {
				_ = syscall.Kill(-s.cmd.Process.Pid, syscall.SIGKILL)
			}
			<-done
		}
	})
	return s.closeErr
}
