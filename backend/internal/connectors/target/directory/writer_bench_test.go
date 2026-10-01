package directory

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"omnidatabus/internal/connectors/target"
)

// BenchmarkDirectoryWriter writes 1 KB documents with 16 writers, per durability level (OC-040
// AC7: ≥ 15,000 files/s with durability file on Linux tmpfs; the reference NVMe gate is
// OC-056). On macOS, durability file is a full device flush per file (F_FULLFSYNC), so
// expect far less there. Reports files/s.
func BenchmarkDirectoryWriter(b *testing.B) {
	for _, d := range []string{"none", "file"} {
		b.Run("durability="+d, func(b *testing.B) { benchWriter(b, d) })
	}
}

func benchWriter(b *testing.B, durability string) {
	root := filepath.Join(b.TempDir(), "out")
	_ = os.MkdirAll(filepath.Join(root, "orders"), 0o750)
	c := New([]string{root})
	cfg, abs, err := c.parse([]byte(`{"path":"` + root + `","manifest":true,"durability":"` + durability + `"}`))
	if err != nil {
		b.Fatal(err)
	}
	w, err := c.openWriter(osFS{}, cfg, abs, target.WriteOptions{Location: target.Location{Folder: "orders"}, Mode: "upsert", Concurrency: 16, MappingID: "bench"})
	if err != nil {
		b.Fatal(err)
	}
	body := []byte(`{"pad":"` + strings.Repeat("x", 1000) + `"}`)
	const batch = 500
	b.ResetTimer()
	for i := 0; i < b.N; i += batch {
		docs := make([]target.Doc, 0, batch)
		for j := i; j < i+batch && j < b.N; j++ {
			docs = append(docs, target.Doc{Key: fmt.Sprint(j), Value: body})
		}
		for _, r := range w.WriteBatch(context.Background(), docs) {
			if r.Outcome != target.Written {
				b.Fatalf("%+v", r)
			}
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "files/s")
	_ = w.Close()
}
