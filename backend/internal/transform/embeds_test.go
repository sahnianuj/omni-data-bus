package transform

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/docschema"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/schema"
)

func embedFixture(nullHandling string, limit int, empty string) (*mapping.Spec, *schema.Table, map[string]*schema.Table) {
	customer := &schema.Table{Name: "customer", Columns: []schema.Column{
		{Name: "customer_id", UDTName: "int4", JSONType: schema.TypeInteger},
		{Name: "address_id", UDTName: "int4", JSONType: schema.TypeInteger},
	}}
	address := &schema.Table{Name: "address", Columns: []schema.Column{
		{Name: "address_id", UDTName: "int4", JSONType: schema.TypeInteger},
		{Name: "city", UDTName: "text", JSONType: schema.TypeString},
	}}
	orders := &schema.Table{Name: "orders", Columns: []schema.Column{
		{Name: "order_id", UDTName: "int8", JSONType: schema.TypeInteger},
		{Name: "customer_id", UDTName: "int4", JSONType: schema.TypeInteger},
		{Name: "order_date", UDTName: "text", JSONType: schema.TypeString},
	}}
	spec := &mapping.Spec{
		DocSchema: docschema.Schema{Root: docschema.DocField{ID: docschema.RootID, Name: "$", Type: docschema.Object, Children: []docschema.DocField{
			{ID: "f_id", Name: "id", Type: docschema.Integer},
			{ID: "f_addr", Name: "address", Type: docschema.Object, Children: []docschema.DocField{{ID: "f_city", Name: "city", Type: docschema.String}}},
			{ID: "f_orders", Name: "orders", Type: docschema.Array, Items: &docschema.DocField{ID: "f_item", Name: "[]", Type: docschema.Object, Children: []docschema.DocField{
				{ID: "f_oid", Name: "orderId", Type: docschema.Integer},
				{ID: "f_date", Name: "date", Type: docschema.String},
			}}},
		}}},
		Key:          mapping.KeySpec{Template: "customer::{customer_id}"},
		NullHandling: nullHandling,
		Embeds: []mapping.Embed{
			{ID: "emb_1", Kind: mapping.EmbedManyToOne, Table: mapping.TableRef{Schema: "shop", Name: "address"}, Join: []mapping.JoinPair{{Parent: "address_id", Child: "address_id"}}, TargetFieldID: "f_addr"},
			{
				ID: "emb_2", Kind: mapping.EmbedOneToMany, Table: mapping.TableRef{Schema: "shop", Name: "orders"}, Join: []mapping.JoinPair{{Parent: "customer_id", Child: "customer_id"}},
				OrderBy: []mapping.OrderColumn{{Column: "order_date", Desc: true}}, Limit: limit, Empty: empty, TargetFieldID: "f_orders",
			},
		},
		FieldMappings: []mapping.FieldMapping{
			{ID: "m1", TargetFieldID: "f_id", Sources: []mapping.SourceRef{{Column: "customer_id"}}, State: mapping.StateActive},
			{ID: "m2", TargetFieldID: "f_city", Sources: []mapping.SourceRef{{Column: "city", Embed: "emb_1"}}, State: mapping.StateActive},
			{ID: "m3", TargetFieldID: "f_oid", Sources: []mapping.SourceRef{{Column: "order_id", Embed: "emb_2"}}, State: mapping.StateActive},
			{ID: "m4", TargetFieldID: "f_date", Sources: []mapping.SourceRef{{Column: "order_date", Embed: "emb_2"}}, State: mapping.StateActive},
		},
	}
	spec.Normalize()
	return spec, customer, map[string]*schema.Table{"emb_1": address, "emb_2": orders}
}

func compileEmbeds(t *testing.T, nullHandling string, limit int, empty string) *Program {
	t.Helper()
	spec, customer, rel := embedFixture(nullHandling, limit, empty)
	p, issues := Compile(spec, customer, Options{Related: rel})
	require.NotNil(t, p, "%v", issues)
	require.True(t, p.HasEmbeds())
	return p
}

// orderRows returns n orders of customer 7, newest first (the query's order).
func orderRows(n int) [][]any {
	out := make([][]any, n)
	for i := range out {
		out[i] = []any{int64(n - i), int32(7), fmt.Sprintf("2026-01-%04d", n-i)}
	}
	return out
}

func TestApplyEmbeds_AC4_OneToManyOrdered(t *testing.T) {
	p := compileEmbeds(t, mapping.NullInclude, 1000, "")
	rel := map[string][][]any{
		"emb_1": {{int32(3), "Lyon"}},
		"emb_2": {{int64(30), int32(7), "2026-03-01"}, {int64(20), int32(7), "2026-02-01"}, {int64(10), int32(7), "2026-01-01"}},
	}
	key, doc, warnings, rerr := p.ApplyRelated([]any{int32(7), int32(3)}, rel, nil)
	require.Nil(t, rerr)
	require.Empty(t, warnings)
	require.Equal(t, "customer::7", key)
	require.JSONEq(t, `{"id":7,"address":{"city":"Lyon"},"orders":[
		{"orderId":30,"date":"2026-03-01"},{"orderId":20,"date":"2026-02-01"},{"orderId":10,"date":"2026-01-01"}]}`, string(doc),
		"3 objects in the related rows' order (order_date desc)")
}

func TestApplyEmbeds_AC4_EmptyArrayOrOmit(t *testing.T) {
	p := compileEmbeds(t, mapping.NullInclude, 1000, "")
	_, doc, _, rerr := p.ApplyRelated([]any{int32(7), int32(3)}, map[string][][]any{"emb_1": {{int32(3), "Lyon"}}}, nil)
	require.Nil(t, rerr)
	require.JSONEq(t, `{"id":7,"address":{"city":"Lyon"},"orders":[]}`, string(doc))

	p = compileEmbeds(t, mapping.NullInclude, 1000, mapping.EmptyOmit)
	_, doc, _, _ = p.ApplyRelated([]any{int32(7), int32(3)}, map[string][][]any{"emb_1": {{int32(3), "Lyon"}}}, nil)
	require.JSONEq(t, `{"id":7,"address":{"city":"Lyon"}}`, string(doc), "configured to omit")
}

func TestApplyEmbeds_AC4_LimitTruncationWarning(t *testing.T) {
	p := compileEmbeds(t, mapping.NullInclude, 1000, "")
	_, doc, warnings, rerr := p.ApplyRelated([]any{int32(7), nil}, map[string][][]any{"emb_2": orderRows(1500)}, nil)
	require.Nil(t, rerr)
	require.Equal(t, []Warning{{FieldPath: "orders", Code: CodeEmbedTruncated, Message: "more than 1000 related rows; kept the first 1000"}}, warnings)
	var n int
	for i := 0; i+len(`"orderId"`) <= len(doc); i++ {
		if string(doc[i:i+len(`"orderId"`)]) == `"orderId"` {
			n++
		}
	}
	require.Equal(t, 1000, n)
	require.Contains(t, string(doc), `{"orderId":1500,"date":"2026-01-1500"}`, "the first rows in order are kept")
	require.NotContains(t, string(doc), `"orderId":500,`)
}

func TestApplyEmbeds_AC4_ManyToOneNull(t *testing.T) {
	p := compileEmbeds(t, mapping.NullInclude, 1000, "")
	_, doc, _, rerr := p.ApplyRelated([]any{int32(7), nil}, nil, nil)
	require.Nil(t, rerr)
	require.JSONEq(t, `{"id":7,"address":null,"orders":[]}`, string(doc), "null address_id: null object")
	p = compileEmbeds(t, mapping.NullOmit, 1000, "")
	_, doc, _, _ = p.ApplyRelated([]any{int32(7), int32(99)}, map[string][][]any{"emb_1": {}}, nil)
	require.JSONEq(t, `{"id":7,"orders":[]}`, string(doc), "dangling FK with omit: left out")
}

func TestApplyEmbeds_ErrorsCarryTheFullPath(t *testing.T) {
	spec, customer, rel := embedFixture(mapping.NullInclude, 1000, "")
	spec.FieldMappings[3].Transforms = []mapping.Step{{Op: "parseInteger", Args: map[string]any{"strict": true}}}
	spec.DocSchema.Root.Children[2].Items.Children[1].Type = docschema.Integer
	p, issues := Compile(spec, customer, Options{Related: rel})
	require.NotNil(t, p, "%v", issues)
	_, _, _, rerr := p.ApplyRelated([]any{int32(7), nil}, map[string][][]any{"emb_2": {{int64(1), int32(7), "12"}, {int64(2), int32(7), "x"}}}, nil)
	require.NotNil(t, rerr)
	require.Equal(t, "orders[1].date", rerr.FieldPath)

	// Compile issues inside an embed have absolute paths; an unknown table is reported.
	spec.FieldMappings[2].Sources[0].Column = "nope"
	_, issues = Compile(spec, customer, Options{Related: rel})
	require.Equal(t, "orders[].orderId", issues[0].Path)
	_, issues = Compile(spec, customer, Options{})
	require.Equal(t, mapping.CodeEmbedTableMissing, issues[0].Code)
}

func TestApplyEmbeds_UnmappedFieldsInsideAnEmbed(t *testing.T) {
	// An unmapped field inside an embedded object follows the spec's OmitUnmapped too.
	for _, omit := range []bool{false, true} {
		spec, customer, rel := embedFixture(mapping.NullInclude, 1000, "")
		spec.OmitUnmapped = omit
		addr := &spec.DocSchema.Root.Children[1]
		addr.Children = append(addr.Children, docschema.DocField{ID: "f_net", Name: "network", Type: docschema.String})
		p, issues := Compile(spec, customer, Options{Related: rel})
		require.NotNil(t, p, "%v", issues)
		_, doc, _, rerr := p.ApplyRelated([]any{int32(7), int32(1)}, map[string][][]any{"emb_1": {{int32(1), "Pune"}}}, nil)
		require.Nil(t, rerr)
		want := `{"id":7,"address":{"city":"Pune","network":null},"orders":[]}`
		if omit {
			want = `{"id":7,"address":{"city":"Pune"},"orders":[]}`
		}
		require.JSONEq(t, want, string(doc))
	}
}
