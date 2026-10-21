package filemention

import (
	"fmt"
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
