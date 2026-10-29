package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrationCommandDoesNotRequireProviderOrStdin(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "execution-format"), []byte("2\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := RunWithStreams([]string{"migrate", "--state-dir", dir}, nil, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "already current") {
		t.Fatalf("output=%q", stdout.String())
	}
}
func TestMigrationCommandRequiresExplicitDirectory(t *testing.T) {
	for _, args := range [][]string{{"migrate"}, {"migrate", "--state-dir", t.TempDir(), "unexpected"}} {
		if err := RunWithStreams(args, nil, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}
