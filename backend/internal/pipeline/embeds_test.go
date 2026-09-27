package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/docschema"
	"omnidatabus/internal/jobs"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/related"
	"omnidatabus/internal/schema"
	"omnidatabus/internal/store"
)

// visitsTable: visit_id, person_id, page — each person has id%4 visits.
func visitsTable() *schema.Table {
	return &schema.Table{Name: "visits", PrimaryKey: []string{"visit_id"}, Columns: []schema.Column{
		{Name: "visit_id", UDTName: "int8", JSONType: schema.TypeInteger, IsPrimaryKey: true},
		{Name: "person_id", UDTName: "int4", JSONType: schema.TypeInteger},
		{Name: "page", UDTName: "text", JSONType: schema.TypeString},
	}}
}

func visitsFor(id int32) [][]any {
	var out [][]any
	for v := int32(0); v < id%4; v++ {
		out = append(out, []any{int64(id)*10 + int64(v), id, fmt.Sprintf("/p%d/%d", id, v)})
	}
	return out
}

// fakeRelated answers ReadRelated from visitsFor and records every call.
type fakeRelated struct {
	mu    sync.Mutex
	calls []relatedCall
}

type relatedCall struct {
	query    source.RelatedQuery
	keys     int
	snapshot string
}

func (f *fakeRelated) read(snapshot string, q source.RelatedQuery, keys [][]any) ([][]any, error) {
	f.mu.Lock()
	f.calls = append(f.calls, relatedCall{query: q, keys: len(keys), snapshot: snapshot})
	f.mu.Unlock()
	var out [][]any
	for _, k := range keys {
		out = append(out, visitsFor(k[0].(int32))...)
	}
	return out, nil
}

func (f *fakeRelated) ReadRelated(_ context.Context, q source.RelatedQuery, keys [][]any) ([][]any, error) {
	return f.read("", q, keys)
}

// ReadRelated makes fakeRead a RelatedReader that records the session's snapshot.
func (r *fakeRead) ReadRelated(_ context.Context, q source.RelatedQuery, keys [][]any) ([][]any, error) {
	if r.f.related == nil {
		return nil, fmt.Errorf("no related rows")
	}
	return r.f.related.read(r.SnapshotID(), q, keys)
}

// peopleWithVisits embeds visits (one-to-many) into people.
func peopleWithVisits(limit int) *mapping.Spec {
	s := peopleSpec()
	s.DocSchema.Root.Children = append(s.DocSchema.Root.Children, docschema.DocField{
		ID: "f_visits", Name: "visits", Type: docschema.Array, Items: &docschema.DocField{ID: "f_v", Name: "[]", Type: docschema.Object, Children: []docschema.DocField{
			{ID: "f_page", Name: "page", Type: docschema.String},
		}},
	})
	s.Embeds = []mapping.Embed{{
		ID: "emb_v", Kind: mapping.EmbedOneToMany, Table: mapping.TableRef{Schema: "hr", Name: "visits"},
		Join: []mapping.JoinPair{{Parent: "id", Child: "person_id"}}, OrderBy: []mapping.OrderColumn{{Column: "visit_id"}}, Limit: limit, TargetFieldID: "f_visits",
	}}
	s.FieldMappings = append(s.FieldMappings, mapping.FieldMapping{
		ID: "m_page", TargetFieldID: "f_page", Sources: []mapping.SourceRef{{Column: "page", Embed: "emb_v"}}, State: mapping.StateActive,
	})
	s.Normalize()
	return s
}

func TestEmbedResolver_AC3_OneQueryPerEmbedPerBatch(t *testing.T) {
	spec := peopleWithVisits(1000)
	visits := visitsTable()
	rel := func(_, name string) *schema.Table {
		if name == "visits" {
			return visits
		}
		return nil
	}
	plans, err := related.Plans(spec, peopleTable(), rel)
	require.NoError(t, err)
	require.Len(t, plans, 1)
	require.Equal(t, []bool{true, true, true}, plans[0].Query.Table.Project, "the order, join and mapped columns")
	require.Equal(t, 1001, plans[0].Keep, "limit+1 so truncation shows")
	require.Zero(t, plans[0].Query.PerKeyLimit, "a large limit is applied in Go")
	small, _ := related.Plans(peopleWithVisits(10), peopleTable(), rel)
	require.Equal(t, 11, small[0].Query.PerKeyLimit, "a small one in SQL")
	fr := &fakeRelated{}
	r := &related.Resolver{Plans: plans, Reader: fr, Concurrency: 4}

	// 1,000 parent rows but only 800 distinct people (200 repeat).
	var rows [][]any
	for i := int32(1); i <= 1000; i++ {
		id := i
		if i > 800 {
			id = i - 800
		}
		rows = append(rows, []any{id, "n", "30"})
	}
	got, err := r.Resolve(context.Background(), rows)
	require.NoError(t, err)
	require.Len(t, fr.calls, 1, "exactly one query for the batch")
	require.Equal(t, 800, fr.calls[0].keys, "with the distinct keys")
	require.Equal(t, visitsFor(7), got["emb_v"][6], "grouped per parent row")
	require.Equal(t, visitsFor(7), got["emb_v"][806], "repeated parents get the same rows")
	require.Nil(t, got["emb_v"][3], "id 4 has no visits")

	// A null join key has no rows and isn't queried.
	fr.calls = nil
	got, err = r.Resolve(context.Background(), [][]any{{nil, "n", "1"}})
	require.NoError(t, err)
	require.Empty(t, fr.calls)
	require.Nil(t, got["emb_v"][0])
}

func TestEmbedResolver_AC5_UsesSharedSnapshot(t *testing.T) {
	f := newRunFixture(t, peopleRows(1, 3000), 3_000_000, opts(500)) // 3 partitions in one snapshot
	spec := peopleWithVisits(1000)
	raw, _ := json.Marshal(spec)
	m, _ := f.st.Mappings().Get(context.Background(), ws, f.job.MappingIDs[0])
	m.Spec = raw
	_, err := f.st.Mappings().Update(context.Background(), ws, m)
	require.NoError(t, err)
	f.tables = append(f.tables, *visitsTable())
	f.src.related = &fakeRelated{}

	o := f.run(jobs.Control{})
	require.True(t, o.Finished, "%+v", o.Fatal)
	require.Equal(t, 3000, f.w.count())
	calls := f.src.related.calls
	require.Len(t, calls, 3000/500+1, "one lookup per batch, and one for verification's sampled rows")
	for _, c := range calls {
		require.Equal(t, "snap", c.snapshot, "through the read session that exported the parents' snapshot")
	}
	var doc map[string]any
	require.NoError(t, json.Unmarshal(f.w.docs["person::7"], &doc))
	require.Equal(t, []any{map[string]any{"page": "/p7/0"}, map[string]any{"page": "/p7/1"}, map[string]any{"page": "/p7/2"}}, doc["visits"])
	require.NoError(t, json.Unmarshal(f.w.docs["person::8"], &doc))
	require.Equal(t, []any{}, doc["visits"])
}

func TestEmbeds_MissingTableFailsTheMapping(t *testing.T) {
	f := newRunFixture(t, peopleRows(1, 100), 100, opts(100))
	raw, _ := json.Marshal(peopleWithVisits(1000))
	m, _ := f.st.Mappings().Get(context.Background(), ws, f.job.MappingIDs[0])
	m.Spec = raw
	_, _ = f.st.Mappings().Update(context.Background(), ws, m)
	o := f.run(jobs.Control{})
	require.NotNil(t, o.Fatal)
	require.Equal(t, CodeSourceTableMissing, o.Fatal.Code)
	require.Contains(t, o.Fatal.Message, "hr.visits")
	_ = store.JobFailed
}
