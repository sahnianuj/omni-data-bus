package pipeline

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/connectors/target"
	"omnidatabus/internal/docschema"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/schema"
	"omnidatabus/internal/transform"
)

// people: id int4 PK, name text, age text (strictly parsed to integer: "x" fails).
func peopleTable() *schema.Table {
	return &schema.Table{Name: "people", PrimaryKey: []string{"id"}, Columns: []schema.Column{
		{Name: "id", UDTName: "int4", JSONType: schema.TypeInteger, IsPrimaryKey: true},
		{Name: "name", UDTName: "text", JSONType: schema.TypeString},
		{Name: "age", UDTName: "text", JSONType: schema.TypeString},
	}}
}

func peopleSpec() *mapping.Spec {
	return &mapping.Spec{
		DocSchema: docschema.Schema{Root: docschema.DocField{ID: "fld_root", Name: "$", Type: docschema.Object, Children: []docschema.DocField{
			{ID: "f_id", Name: "id", Type: docschema.Integer},
			{ID: "f_name", Name: "name", Type: docschema.String},
			{ID: "f_age", Name: "age", Type: docschema.Integer},
		}}},
		Key:          mapping.KeySpec{Template: "person::{id}"},
		NullHandling: mapping.NullInclude,
		FieldMappings: []mapping.FieldMapping{
			{ID: "m1", TargetFieldID: "f_id", Sources: []mapping.SourceRef{{Column: "id"}}, State: mapping.StateActive},
			{ID: "m2", TargetFieldID: "f_name", Sources: []mapping.SourceRef{{Column: "name"}}, State: mapping.StateActive},
			{
				ID: "m3", TargetFieldID: "f_age", Sources: []mapping.SourceRef{{Column: "age"}}, State: mapping.StateActive,
				Transforms: []mapping.Step{{Op: "parseInteger", Args: map[string]any{"strict": true}}},
			},
		},
	}
}

func compilePeople(_ testing.TB) func() (*transform.Program, error) {
	return func() (*transform.Program, error) {
		p, issues := transform.Compile(peopleSpec(), peopleTable(), transform.Options{})
		if p == nil {
			return nil, fmt.Errorf("compile: %v", issues)
		}
		return p, nil
	}
}

// peopleRows returns rows id from..to; ids in bad get age "x".
func peopleRows(from, to int32, bad ...int32) [][]any {
	isBad := map[int32]bool{}
	for _, b := range bad {
		isBad[b] = true
	}
	var rows [][]any
	for id := from; id <= to; id++ {
		age := fmt.Sprint(20 + id%50)
		if isBad[id] {
			age = "x"
		}
		rows = append(rows, []any{id, fmt.Sprintf("p%d", id), age})
	}
	return rows
}

// memWriter is a DocWriter storing documents; fatal marks keys that fail fatally.
type memWriter struct {
	mu    sync.Mutex
	docs  map[string][]byte
	fail  map[string]target.WriteResult
	calls int
	// insert makes an existing key a Skipped outcome (insert write mode).
	insert bool
	// writes counts every write of each key (duplicates show up as > 1).
	writes map[string]int
	// block, when set, is called before each batch (a stuck writer waits on ctx there).
	block func(ctx context.Context, docs []target.Doc)
	// onWrite is told the number of documents stored after each batch.
	onWrite func(stored int)
}

func newMemWriter() *memWriter {
	return &memWriter{docs: map[string][]byte{}, fail: map[string]target.WriteResult{}, writes: map[string]int{}}
}

func (w *memWriter) WriteBatch(ctx context.Context, docs []target.Doc) []target.WriteResult {
	if w.block != nil {
		w.block(ctx, docs)
	}
	w.mu.Lock()
	w.calls++
	out := make([]target.WriteResult, len(docs))
	for i, d := range docs {
		if r, ok := w.fail[d.Key]; ok {
			r.Key = d.Key
			out[i] = r
			continue
		}
		if ctx.Err() != nil {
			out[i] = target.WriteResult{Key: d.Key, Outcome: target.Failed, Code: "CANCELLED"}
			continue
		}
		if _, exists := w.docs[d.Key]; exists && w.insert {
			out[i] = target.WriteResult{Key: d.Key, Outcome: target.Skipped, Attempts: 1}
			continue
		}
		w.docs[d.Key] = d.Value
		w.writes[d.Key]++
		out[i] = target.WriteResult{Key: d.Key, Outcome: target.Written, Attempts: 1}
	}
	stored, hook := len(w.docs), w.onWrite
	w.mu.Unlock()
	if hook != nil {
		hook(stored)
	}
	return out
}

func (w *memWriter) Close() error { return nil }

// GetDocs implements target.DocReader.
func (w *memWriter) GetDocs(_ context.Context, keys []string) (map[string][]byte, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := map[string][]byte{}
	for _, k := range keys {
		if d, ok := w.docs[k]; ok {
			out[k] = d
		}
	}
	return out, nil
}

func (w *memWriter) count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.docs)
}

func batchOf(seq uint64, part int, rows [][]any) *source.Batch {
	return &source.Batch{Seq: seq, Partition: part, Rows: rows, LastKey: []any{rows[len(rows)-1][0]}}
}

func requireNoErr(t *testing.T, err error) { t.Helper(); require.NoError(t, err) }
