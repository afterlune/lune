package ptyterm

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

type Options struct {
	Dir   string
	Shell string
	Cols  uint16
	Rows  uint16
}

type Session interface {
	Read([]byte) (int, error)
	Write([]byte) (int, error)
	Resize(cols, rows uint16) error
	Wait() (int, error)
	Close() error
}

func Open(opts Options) (Session, error) {
	dir := strings.TrimSpace(opts.Dir)
	if dir != "" {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return nil, err
		}
		info, err := os.Stat(abs)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			return nil, errors.New("terminal working directory must be a directory")
		}
		dir = abs
	}
	cols, rows := normalizeSize(opts.Cols, opts.Rows)
	return openPTY(dir, strings.TrimSpace(opts.Shell), cols, rows)
}

func normalizeSize(cols, rows uint16) (uint16, uint16) {
	if cols == 0 {
		cols = 80
	}
	if rows == 0 {
		rows = 24
	}
	if cols < 20 {
		cols = 20
	} else if cols > 500 {
		cols = 500
	}
	if rows < 5 {
		rows = 5
	} else if rows > 200 {
		rows = 200
	}
	return cols, rows
}

func buildEnv() []string {
	raw := os.Environ()
	env := make([]string, 0, len(raw)+2)
	hasTerm := false
	hasColorTerm := false
	for _, entry := range raw {
		switch {
		case strings.HasPrefix(entry, "TERM="):
			env = append(env, "TERM=xterm-256color")
			hasTerm = true
		case strings.HasPrefix(entry, "COLORTERM="):
			env = append(env, "COLORTERM=truecolor")
			hasColorTerm = true
		default:
			env = append(env, entry)
		}
	}
	if !hasTerm {
		env = append(env, "TERM=xterm-256color")
	}
	if !hasColorTerm {
		env = append(env, "COLORTERM=truecolor")
	}
	return env
}
