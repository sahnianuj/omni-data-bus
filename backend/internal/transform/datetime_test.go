package transform

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/docschema"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/schema"
)

// OC-179 AC2: now writes the run's start (the same for every document) or each document's
// write time, in formatDatetime's formats.
func TestNow_OC179_RunAndWrite(t *testing.T) {
	start := time.Date(2026, 10, 17, 14, 3, 22, 418e6, time.UTC)
	env := &compileEnv{opts: Options{Now: start}}
	at := func(args map[string]any) any {
		fn, err := compileNow(args, env)
		require.NoError(t, err)
		v, err := fn(nil, nil)
		require.NoError(t, err)
		return v
	}
	require.Equal(t, "2026-10-17T14:03:22.418Z", at(map[string]any{}))
	require.Equal(t, start.UnixMilli(), at(map[string]any{"format": "epoch_ms"}))
	require.Equal(t, start.Unix(), at(map[string]any{"format": "epoch_s"}))
	require.Equal(t, "2026-10-17", at(map[string]any{"format": "date"}))
	require.Equal(t, "2026-10-17 10:03", at(map[string]any{"format": "custom", "pattern": "yyyy-MM-dd HH:mm", "timezone": "America/New_York"}))

	// The same as formatDatetime for the same instant.
	for _, f := range []string{"iso8601", "date", "epoch_ms", "epoch_s"} {
		fd, err := compileFormatDatetime(map[string]any{"format": f, "timezone": "Asia/Kolkata"}, env)
		require.NoError(t, err)
		want, _ := fd(ISO(start), nil)
		require.Equal(t, want, at(map[string]any{"format": f, "timezone": "Asia/Kolkata"}), f)
	}

	// At write: the clock when each document is built.
	fn, err := compileNow(map[string]any{"at": "write", "format": "epoch_ms"}, env)
	require.NoError(t, err)
	before := time.Now().UnixMilli()
	a, _ := fn(nil, nil)
	time.Sleep(3 * time.Millisecond)
	b, _ := fn(nil, nil)
	require.GreaterOrEqual(t, a.(int64), before)
	require.Greater(t, b.(int64), a.(int64))

	_, err = compileNow(map[string]any{"at": "later"}, env)
	require.ErrorContains(t, err, "at must be run or write")
}

// A sourceless now mapping compiles and writes the run's start into each document.
func TestNow_OC179_InADocument(t *testing.T) {
	start := time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)
	carts := &schema.Table{Name: "carts", PrimaryKey: []string{"id"}, Columns: []schema.Column{{Name: "id", JSONType: schema.TypeInteger, UDTName: "int4"}}}
	spec := &mapping.Spec{
		SpecVersion: 2, Key: mapping.KeySpec{Template: "cart::{id}"}, NullHandling: mapping.NullOmit,
		DocSchema: docschema.Schema{Root: docschema.DocField{ID: docschema.RootID, Name: "$", Type: docschema.Object, Children: []docschema.DocField{
			{ID: "f_id", Name: "id", Type: docschema.Integer},
			{ID: "f_upd", Name: "updatedAt", Type: docschema.Integer},
		}}},
		FieldMappings: []mapping.FieldMapping{
			{ID: "m_id", TargetFieldID: "f_id", Sources: []mapping.SourceRef{{Column: "id"}}, Transforms: []mapping.Step{}, State: mapping.StateActive},
			{ID: "m_upd", TargetFieldID: "f_upd", Sources: []mapping.SourceRef{}, Transforms: []mapping.Step{{Op: "now", Args: map[string]any{"format": "epoch_ms"}}}, State: mapping.StateActive},
		},
	}
	spec.Normalize()
	p, issues := Compile(spec, carts, Options{Now: start})
	require.NotNil(t, p, "%+v", issues)
	for _, id := range []int32{1, 2} {
		_, doc, _, _, rerr := p.ApplyTraceWith([]any{id}, nil, nil)
		require.Nil(t, rerr)
		var got map[string]any
		require.NoError(t, json.Unmarshal(doc, &got))
		require.EqualValues(t, start.UnixMilli(), got["updatedAt"])
	}
}
