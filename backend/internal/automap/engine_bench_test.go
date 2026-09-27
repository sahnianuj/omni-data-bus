package automap

import (
	"fmt"
	"runtime"
	"testing"
	"time"

	"omnidatabus/internal/race"

	"omnidatabus/internal/docschema"
)

func fixture500() ([]SourceCol, []docschema.DocField) {
	words := []string{"customer", "order", "product", "address", "city", "email", "phone", "status", "amount", "created", "updated", "name", "code", "total", "price"}
	cols := make([]SourceCol, 500)
	fields := make([]docschema.DocField, 500)
	for i := range 500 {
		a, b := words[i%len(words)], words[(i/len(words))%len(words)]
		cols[i] = SourceCol{Name: fmt.Sprintf("%s_%s_%d", a, b, i), JSONType: "string", Ordinal: i + 1, Table: "t"}
		fields[i] = docschema.DocField{ID: fmt.Sprintf("f%d", i), Name: fmt.Sprintf("%s%s%d", b, a, (i*7)%500), Type: docschema.String}
	}
	return cols, fields
}

// AC8: 500 × 500 in < 50 ms and < 20 MB.
func BenchmarkAutomap_AC8_500x500(b *testing.B) {
	cols, fields := fixture500()
	s := specWith(fields...)
	DefaultDicts()
	b.ReportAllocs()
	start := time.Now()
	for b.Loop() {
		Run(s, cols, Options{})
	}
	if per := time.Since(start) / time.Duration(b.N); per > 50*time.Millisecond {
		b.Fatalf("automap took %s/op; the budget is 50ms", per)
	}
}

func TestAutomap_AC8_Budget(t *testing.T) {
	if race.Enabled || testing.CoverMode() != "" {
		t.Skip("timing budgets need uninstrumented code: run `make test-budgets`")
	}
	cols, fields := fixture500()
	s := specWith(fields...)
	DefaultDicts()
	Run(s, cols, Options{}) // warm up (goroutines, caches), like a benchmark would
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	Run(s, cols, Options{})
	runtime.ReadMemStats(&after)
	elapsed := time.Duration(1<<62 - 1)
	for range 3 {
		start := time.Now()
		Run(s, cols, Options{})
		elapsed = min(elapsed, time.Since(start))
	}
	if elapsed > 50*time.Millisecond {
		t.Fatalf("automap took %s for 500 × 500; the budget is 50ms", elapsed)
	}
	if alloc := after.TotalAlloc - before.TotalAlloc; alloc > 20<<20 {
		t.Fatalf("automap allocated %d MB; the budget is 20 MB", alloc>>20)
	}
}
