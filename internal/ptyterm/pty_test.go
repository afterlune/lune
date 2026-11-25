package ptyterm

import (
	"bytes"
	"io"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestOpenCommandResizeAndExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell test")
	}
	dir := t.TempDir()
	resolvedDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		resolvedDir = dir
	}
	sess, err := Open(Options{Dir: dir, Shell: "/bin/sh", Cols: 80, Rows: 24})
	if err != nil {
		t.Fatalf("Open PTY: %v", err)
	}
	defer sess.Close()

	if err := sess.Resize(100, 30); err != nil {
		t.Fatalf("Resize PTY: %v", err)
	}

	var out bytes.Buffer
	doneRead := make(chan error, 1)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, rerr := sess.Read(buf)
			if n > 0 {
				out.Write(buf[:n])
			}
			if rerr != nil {
				doneRead <- rerr
				return
			}
		}
	}()

	if _, err := io.WriteString(sess, "pwd && echo PTY_TEST_OK && exit 0\n"); err != nil {
		t.Fatalf("Write to PTY: %v", err)
	}

	code, err := sess.Wait()
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	select {
	case <-doneRead:
	case <-time.After(2 * time.Second):
	}
	text := out.String()
	if !strings.Contains(text, "PTY_TEST_OK") {
		t.Fatalf("output %q missing PTY_TEST_OK", text)
	}
	if !strings.Contains(text, resolvedDir) {
		t.Fatalf("output %q missing cwd %q", text, resolvedDir)
	}
}

func TestForcedCloseStopsRunningShell(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell test")
	}
	sess, err := Open(Options{Dir: t.TempDir(), Shell: "/bin/sh", Cols: 80, Rows: 24})
	if err != nil {
		t.Fatalf("Open PTY: %v", err)
	}
	if _, err := io.WriteString(sess, "sleep 30\n"); err != nil {
		t.Fatalf("Write to PTY: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	start := time.Now()
	if err := sess.Close(); err != nil {
		// Closing ptmx while process holds it is fine; verify it doesn't hang.
		t.Logf("Close returned: %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("Close took too long: %v", time.Since(start))
	}
}
