package automap

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/docschema"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/race"
	"omnidatabus/internal/schema"
)

// bigSource: a hub table referenced by 299 others, each with 10 columns (AC8's 300 tables);
// the design has 200 objects under the root, 3 fields each.
func bigSource() (SuggestInput, *mapping.Spec) {
	sc := schema.Schema{Name: "public"}
	hub := schema.Table{Name: "hub", PrimaryKey: []string{"hub_id"}, Columns: []schema.Column{{Name: "hub_id", JSONType: schema.TypeInteger, IsPrimaryKey: true}}}
	sc.Tables = append(sc.Tables, hub)
	for i := 1; i < 300; i++ {
		t := schema.Table{Name: fmt.Sprintf("entity_%03d", i), PrimaryKey: []string{"id"}}
		t.Columns = append(t.Columns, schema.Column{Name: "id", JSONType: schema.TypeInteger, IsPrimaryKey: true}, schema.Column{Name: "hub_id", JSONType: schema.TypeInteger})
		for c := 0; c < 8; c++ {
			t.Columns = append(t.Columns, schema.Column{Name: fmt.Sprintf("attr_%d_%d", i, c), JSONType: schema.TypeString})
		}
		t.ForeignKeys = []schema.ForeignKey{{Name: t.Name + "_hub_fk", Columns: []string{"hub_id"}, RefSchema: "public", RefTable: "hub", RefColumns: []string{"hub_id"}}}
		sc.Tables = append(sc.Tables, t)
	}
	snap := &schema.Snapshot{Schemas: []schema.Schema{sc}}
	for ti := range snap.Schemas[0].Tables {
		t := &snap.Schemas[0].Tables[ti]
		for _, k := range t.ForeignKeys {
			ref := snap.Table(k.RefSchema, k.RefTable)
			ref.ReferencedBy = append(ref.ReferencedBy, schema.Reference{Schema: "public", Table: t.Name, Name: k.Name, Columns: k.Columns, RefColumns: k.RefColumns})
		}
	}
	root := docschema.DocField{ID: docschema.RootID, Name: "$", Type: docschema.Object}
	for i := 0; i < 200; i++ {
		n := i%299 + 1
		root.Children = append(root.Children, docschema.DocField{ID: fmt.Sprintf("c_%d", i), Name: fmt.Sprintf("entity%03d", n), Type: docschema.Object, Children: []docschema.DocField{
			{ID: fmt.Sprintf("c_%d_a", i), Name: fmt.Sprintf("attr%d0", n), Type: docschema.String},
			{ID: fmt.Sprintf("c_%d_b", i), Name: fmt.Sprintf("attr%d1", n), Type: docschema.String},
			{ID: fmt.Sprintf("c_%d_c", i), Name: "note", Type: docschema.String},
		}})
	}
	spec := &mapping.Spec{Mode: mapping.ModeComposed, DocSchema: docschema.Schema{Root: root}}
	return SuggestInput{
		Spec: spec, Driving: mapping.TableRef{Schema: "public", Name: "hub"}, Root: true, Tables: allTables(snap), Links: linksOf(snap),
		Table: func(r mapping.TableRef) *schema.Table { return snap.Table(r.Schema, r.Name) },
	}, spec
}

func BenchmarkSuggestBindings_AC8(b *testing.B) {
	in, _ := bigSource()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		SuggestBindings(in)
	}
}

func TestSuggestBindings_AC8_Budget(t *testing.T) {
	in, _ := bigSource()
	res := SuggestBindings(in)
	require.Len(t, res.Suggestions, 200)
	require.Equal(t, "entity_001", res.Suggestions[0].Candidate.Table.Name)
	if race.Enabled || testing.CoverMode() != "" {
		t.Skip("timing budgets need uninstrumented code: run `make test-budgets`")
	}
	elapsed := time.Duration(1<<62 - 1)
	for range 3 {
		start := time.Now()
		SuggestBindings(in)
		elapsed = min(elapsed, time.Since(start))
	}
	require.Less(t, elapsed, time.Second, "300 tables × 200 containers")
}
