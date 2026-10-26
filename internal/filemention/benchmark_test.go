package filemention

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func BenchmarkFuzzyMatchFiles_5000(b *testing.B) {
	files := make([]File, 5000)
	for i := range files {
		path := fmt.Sprintf("internal/pkg%d/submodule/service_%d_handler.go", i%20, i)
		name := fmt.Sprintf("service_%d_handler.go", i)
		files[i] = File{
			Path:      path,
			pathLower: strings.ToLower(path),
			Name:      name,
			nameLower: strings.ToLower(name),
			Size:      int64(1024 + i),
		}
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = FuzzyMatch(files, "service_12", 10)
	}
}

func BenchmarkScannerFiles_ColdAndCached(b *testing.B) {
	workspace := b.TempDir()
	for i := 0; i < 2000; i++ {
		path := filepath.Join(workspace, "src", fmt.Sprintf("pkg%d", i%20), fmt.Sprintf("service_%04d.go", i))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("package benchmark\n"), 0o600); err != nil {
			b.Fatal(err)
		}
	}
	b.Run("cold", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			if got := NewScanner(workspace).Files(); len(got) == 0 {
				b.Fatal("cold scan returned no files")
			}
		}
	})
	b.Run("cached", func(b *testing.B) {
		scanner := NewScanner(workspace)
		_ = scanner.Files()
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			if got := scanner.Files(); len(got) == 0 {
				b.Fatal("cached scan returned no files")
			}
		}
	})
}
