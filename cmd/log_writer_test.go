package main

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestRotatingLogWriterAppendsAndKeepsNewestBackups(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.log")
	writer, err := newRotatingLogWriter(path, 4, 2)
	if err != nil {
		t.Fatalf("newRotatingLogWriter: %v", err)
	}
	for _, record := range []string{"a\n", "b\n", "c\n", "d\n", "e\n", "f\n", "g\n"} {
		if _, err := writer.Write([]byte(record)); err != nil {
			t.Fatalf("Write(%q): %v", record, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	assertLogContents(t, path, "g\n")
	assertLogContents(t, path+".1", "e\nf\n")
	assertLogContents(t, path+".2", "c\nd\n")
	if _, err := os.Stat(path + ".3"); !os.IsNotExist(err) {
		t.Fatalf("unexpected third backup: stat error = %v", err)
	}
}

func TestRotatingLogWriterReopensAndRotatesExistingOversizedLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.log")
	if err := os.WriteFile(path, []byte("old log"), 0600); err != nil {
		t.Fatal(err)
	}
	writer, err := newRotatingLogWriter(path, 4, 3)
	if err != nil {
		t.Fatalf("newRotatingLogWriter: %v", err)
	}
	if _, err := writer.Write([]byte("new\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	assertLogContents(t, path, "new\n")
	assertLogContents(t, path+".1", "old log")
}

func TestRotatingLogWriterAllowsOneOversizedRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.log")
	writer, err := newRotatingLogWriter(path, 4, 1)
	if err != nil {
		t.Fatalf("newRotatingLogWriter: %v", err)
	}
	if _, err := writer.Write([]byte("oversized")); err != nil {
		t.Fatalf("Write oversized record: %v", err)
	}
	if _, err := writer.Write([]byte("next")); err != nil {
		t.Fatalf("Write next record: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	assertLogContents(t, path, "next")
	assertLogContents(t, path+".1", "oversized")
}

func TestRotatingLogWriterSerializesConcurrentWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.log")
	writer, err := newRotatingLogWriter(path, 1<<20, 1)
	if err != nil {
		t.Fatalf("newRotatingLogWriter: %v", err)
	}
	const goroutines, writesPerGoroutine = 8, 100
	var wg sync.WaitGroup
	errs := make(chan error, goroutines*writesPerGoroutine)
	for worker := 0; worker < goroutines; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < writesPerGoroutine; i++ {
				if _, err := writer.Write([]byte("record\n")); err != nil {
					errs <- err
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent Write: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	want := goroutines * writesPerGoroutine
	if got := strings.Count(string(data), "record\n"); got != want {
		t.Fatalf("complete records = %d, want %d", got, want)
	}
}

func TestNewRotatingLogWriterReturnsOpenErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "agent.log")
	if _, err := newRotatingLogWriter(path, 1024, 3); err == nil {
		t.Fatal("newRotatingLogWriter succeeded with a missing parent directory")
	}
	if _, err := newRotatingLogWriter(filepath.Join(t.TempDir(), "agent.log"), 0, 3); err == nil {
		t.Fatal("newRotatingLogWriter accepted a non-positive size limit")
	}
}

func assertLogContents(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if got := string(data); got != want {
		t.Fatalf("contents of %s = %q, want %q", path, got, want)
	}
}
