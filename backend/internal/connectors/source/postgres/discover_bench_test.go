package postgres

import (
	"fmt"
	"testing"
	"time"

	"omnidatabus/internal/race"

	"omnidatabus/internal/connectors/source"
)

// syntheticCatalog has n tables × cols columns, each table with a PK, one FK and one index.
func syntheticCatalog(n, cols int) rawCatalog {
	raw := rawCatalog{Database: "big", ServerVersion: "16.3", Enums: map[int64][]string{}}
	for i := 1; i <= n; i++ {
		oid := int64(i)
		name := fmt.Sprintf("t%04d", i)
		raw.Relations = append(raw.Relations, rawRelation{OID: oid, Schema: "s", Name: name, Kind: "r", RelTuples: 1000, Size: 8192, Readable: true})
		for c := 1; c <= cols; c++ {
			raw.Columns = append(raw.Columns, col(oid, c, fmt.Sprintf("c%02d", c), "int4", c == 1))
		}
		raw.Constraints = append(raw.Constraints, rawConstraint{RelOID: oid, Name: name + "_pkey", Type: "p", Columns: []string{"c01"}})
		if i > 1 {
			raw.Constraints = append(raw.Constraints, rawConstraint{
				RelOID: oid, Name: name + "_fk", Type: "f", Columns: []string{"c02"},
				RefSchema: "s", RefTable: fmt.Sprintf("t%04d", i-1), RefColumns: []string{"c01"},
			})
		}
		raw.Indexes = append(raw.Indexes, rawIndex{RelOID: oid, Name: name + "_pkey", Unique: true, Primary: true, Columns: []string{"c01"}})
	}
	return raw
}

// AC8: assembly of 1,000 tables × 20 columns must take < 500 ms/op.
func BenchmarkAssembleSnapshot_AC8_1000Tables(b *testing.B) {
	raw := syntheticCatalog(1000, 20)
	b.ReportAllocs()
	b.ResetTimer()
	start := time.Now()
	for b.Loop() {
		_ = AssembleSnapshot(raw, source.DiscoverOptions{})
	}
	perOp := time.Since(start) / time.Duration(b.N)
	b.ReportMetric(float64(perOp.Milliseconds()), "ms/op")
	if perOp > 500*time.Millisecond {
		b.Fatalf("AssembleSnapshot took %s/op for 1,000 tables; the budget is 500ms", perOp)
	}
}

// The same budget as a plain test, so `go test` enforces it without -bench.
func TestAssembleSnapshot_AC8_Budget(t *testing.T) {
	if race.Enabled || testing.CoverMode() != "" {
		t.Skip("timing budgets need uninstrumented code: run `make test-budgets`")
	}
	raw := syntheticCatalog(1000, 20)
	start := time.Now()
	snap := AssembleSnapshot(raw, source.DiscoverOptions{})
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Fatalf("AssembleSnapshot took %s for 1,000 tables; the budget is 500ms", d)
	}
	if snap.TableCount() != 1000 {
		t.Fatalf("got %d tables", snap.TableCount())
	}
}
