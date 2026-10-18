package transform

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/docschema"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/race"
	"omnidatabus/internal/schema"
)

// paymentsSpec: carts with payments[] over a payments table (id, cart_id, card_id, cash_id,
// amount); card payments read the cards table (a lookup under the relation). With variants,
// card and cash rows are two shapes; without, one shape with every field.
func paymentsSpec(variants bool) (*mapping.Spec, *schema.Table, Options) {
	col := func(n, jt string) schema.Column { return schema.Column{Name: n, JSONType: jt, UDTName: jt} }
	carts := &schema.Table{Name: "carts", PrimaryKey: []string{"id"}, Columns: []schema.Column{col("id", schema.TypeInteger)}}
	payments := &schema.Table{Name: "payments", Columns: []schema.Column{col("id", schema.TypeInteger), col("cart_id", schema.TypeInteger), col("card_id", schema.TypeInteger), col("cash_id", schema.TypeInteger), col("amount", schema.TypeInteger)}}
	cards := &schema.Table{Name: "cards", Columns: []schema.Column{col("id", schema.TypeInteger), col("brand", schema.TypeString)}}
	f := func(id, name string, t docschema.FieldType) docschema.DocField {
		return docschema.DocField{ID: id, Name: name, Type: t}
	}
	pid := "rel_pay"
	items := docschema.DocField{ID: "f_item", Name: "[]", Type: docschema.Object, Children: []docschema.DocField{f("f_amount", "amount", docschema.Integer)}}
	maps := []mapping.FieldMapping{{ID: "m_amount", TargetFieldID: "f_amount", Sources: []mapping.SourceRef{{Relation: pid, Column: "amount"}}, Transforms: []mapping.Step{}, State: mapping.StateActive}}
	rel := mapping.Relation{ID: pid, Kind: mapping.EmbedOneToMany, Table: mapping.TableRef{Name: "payments"}, Join: []mapping.JoinPair{{Parent: "id", Child: "cart_id"}}, TargetFieldID: "f_pay"}
	lookup := mapping.Relation{ID: "rel_card", ParentID: &pid, Kind: mapping.EmbedManyToOne, LookupOnly: true, Table: mapping.TableRef{Name: "cards"}, Join: []mapping.JoinPair{{Parent: "card_id", Child: "id"}}}
	if variants {
		items.Children = append(items.Children,
			docschema.DocField{ID: "v_card", Name: "card", Type: docschema.Object, Variant: &docschema.Variant{Field: "type", Value: json.RawMessage(`"card"`)}, Children: []docschema.DocField{f("f_ctype", "type", docschema.String), f("f_brand", "brand", docschema.String)}},
			docschema.DocField{ID: "v_cash", Name: "cash", Type: docschema.Object, Variant: &docschema.Variant{Field: "type", Value: json.RawMessage(`"cash"`)}, Children: []docschema.DocField{f("f_xtype", "type", docschema.String)}},
		)
		rel.Variants = []mapping.RelationVariant{{FieldID: "v_card", When: "card_id"}, {FieldID: "v_cash", When: "cash_id"}}
		maps = append(maps,
			mapping.FieldMapping{ID: "m_ctype", TargetFieldID: "f_ctype", Sources: []mapping.SourceRef{}, Transforms: []mapping.Step{{Op: "constant", Args: map[string]any{"value": "card"}}}, State: mapping.StateActive},
			mapping.FieldMapping{ID: "m_brand", TargetFieldID: "f_brand", Sources: []mapping.SourceRef{{Relation: "rel_card", Column: "brand"}}, Transforms: []mapping.Step{}, State: mapping.StateActive},
			mapping.FieldMapping{ID: "m_xtype", TargetFieldID: "f_xtype", Sources: []mapping.SourceRef{}, Transforms: []mapping.Step{{Op: "constant", Args: map[string]any{"value": "cash"}}}, State: mapping.StateActive},
		)
	} else {
		// The same fields in one shape: a type constant and the card's brand.
		items.Children = append(items.Children, f("f_ctype", "type", docschema.String), f("f_brand", "brand", docschema.String))
		maps = append(maps,
			mapping.FieldMapping{ID: "m_ctype", TargetFieldID: "f_ctype", Sources: []mapping.SourceRef{}, Transforms: []mapping.Step{{Op: "constant", Args: map[string]any{"value": "card"}}}, State: mapping.StateActive},
			mapping.FieldMapping{ID: "m_brand", TargetFieldID: "f_brand", Sources: []mapping.SourceRef{{Relation: "rel_card", Column: "brand"}}, Transforms: []mapping.Step{}, State: mapping.StateActive})
	}
	spec := &mapping.Spec{
		SpecVersion: 2, Mode: mapping.ModeComposed, Key: mapping.KeySpec{Template: "cart::{id}"}, NullHandling: mapping.NullOmit,
		DocSchema: docschema.Schema{Root: docschema.DocField{ID: docschema.RootID, Name: "$", Type: docschema.Object, Children: []docschema.DocField{
			{ID: "f_pay", Name: "payments", Type: docschema.Array, Items: &items},
		}}},
		Relations:     []mapping.Relation{rel, lookup},
		FieldMappings: maps,
	}
	spec.Normalize()
	return spec, carts, Options{Related: map[string]*schema.Table{"rel_pay": payments, "rel_card": cards}}
}

func paymentRows(n int) source.Related {
	visa := source.Related{"rel_card": {Rows: []source.RelatedRow{{Values: []any{int32(1), "VISA"}}}}}
	var rows []source.RelatedRow
	for i := range n {
		switch i % 3 {
		case 0: // card
			rows = append(rows, source.RelatedRow{Values: []any{int32(i), int32(1), int32(1), nil, int32(100 + i)}, Sub: visa})
		case 1: // cash
			rows = append(rows, source.RelatedRow{Values: []any{int32(i), int32(1), nil, int32(9), int32(200 + i)}})
		default: // neither: of no variant
			rows = append(rows, source.RelatedRow{Values: []any{int32(i), int32(1), nil, nil, int32(300 + i)}})
		}
	}
	return source.Related{"rel_pay": {Rows: rows}}
}

// OC-171 AC3/AC4: each row is written in its variant's shape; a row of none is skipped and
// counted, or fails the document with onNoMatch fail.
func TestVariants_OC171_Shapes(t *testing.T) {
	spec, carts, opts := paymentsSpec(true)
	p, issues := Compile(spec, carts, opts)
	require.NotNil(t, p, "%+v", issues)
	_, doc, warnings, _, rerr := p.ApplyTraceWith([]any{int32(1)}, paymentRows(3), nil)
	require.Nil(t, rerr)
	require.JSONEq(t, `{"payments":[{"amount":100,"type":"card","brand":"VISA"},{"amount":201,"type":"cash"}]}`, string(doc))
	require.Len(t, warnings, 1)
	require.Equal(t, CodeVariantNoMatch, warnings[0].Code)
	require.Contains(t, warnings[0].Message, "1 row(s) of")
	require.Contains(t, warnings[0].Message, "are of none of the variants; skipped")

	spec.Relations[0].OnNoMatch = mapping.VariantNoMatchFail
	p, _ = Compile(spec, carts, opts)
	_, _, _, _, rerr = p.ApplyTraceWith([]any{int32(1)}, paymentRows(3), nil)
	require.NotNil(t, rerr)
	require.Equal(t, CodeVariantNoMatch, rerr.Code)

	// A row setting both keys is the first variant.
	spec.Relations[0].OnNoMatch = ""
	p, _ = Compile(spec, carts, opts)
	both := source.Related{"rel_pay": {Rows: []source.RelatedRow{{Values: []any{int32(1), int32(1), int32(1), int32(9), int32(5)}}}}}
	_, doc, _, _, _ = p.ApplyTraceWith([]any{int32(1)}, both, nil)
	require.JSONEq(t, `{"payments":[{"amount":5,"type":"card"}]}`, string(doc))
}

func benchVariants(b *testing.B, variants bool) {
	spec, carts, opts := paymentsSpec(variants)
	p, issues := Compile(spec, carts, opts)
	if p == nil {
		b.Fatal(issues)
	}
	rel := paymentRows(30)
	row := []any{int32(1)}
	buf := make([]byte, 0, 8192)
	b.ResetTimer()
	for range b.N {
		if _, _, _, _, rerr := p.ApplyTraceWith(row, rel, buf); rerr != nil {
			b.Fatal(rerr)
		}
	}
}

func BenchmarkVariants_Plain(b *testing.B)    { benchVariants(b, false) }
func BenchmarkVariants_Variants(b *testing.B) { benchVariants(b, true) }

// OC-173: a variant array costs at most 10 % more per document than a plain one over the
// same rows (it writes fewer fields: rows of no variant are skipped).
func TestVariants_OC173_ThroughputBudget(t *testing.T) {
	if race.Enabled || testing.CoverMode() != "" {
		t.Skip("timing budgets need uninstrumented code: run `make test-budgets`")
	}
	measure := func(variants bool) time.Duration {
		best := time.Duration(1<<62 - 1)
		for range 5 {
			r := testing.Benchmark(func(b *testing.B) { benchVariants(b, variants) })
			best = min(best, time.Duration(r.NsPerOp()))
		}
		return best
	}
	plain, withVariants := measure(false), measure(true)
	t.Logf("plain %s/doc, variants %s/doc", plain, withVariants)
	require.LessOrEqual(t, float64(withVariants), 1.10*float64(plain), "variants within 10%% of a plain array")
}

// unionSpec: orders with events[] whose variants have rows of their own (pattern C): notes and
// calls, each ordered by at.
func unionSpec() (*mapping.Spec, *schema.Table, Options) {
	col := func(n, jt string) schema.Column { return schema.Column{Name: n, JSONType: jt, UDTName: jt} }
	orders := &schema.Table{Name: "orders", PrimaryKey: []string{"id"}, Columns: []schema.Column{col("id", schema.TypeInteger)}}
	notes := &schema.Table{Name: "notes", Columns: []schema.Column{col("id", schema.TypeInteger), col("order_id", schema.TypeInteger), col("at", schema.TypeInteger), col("text", schema.TypeString)}}
	calls := &schema.Table{Name: "calls", Columns: []schema.Column{col("id", schema.TypeInteger), col("order_id", schema.TypeInteger), col("at", schema.TypeInteger), col("secs", schema.TypeInteger)}}
	f := func(id, name string, t docschema.FieldType) docschema.DocField {
		return docschema.DocField{ID: id, Name: name, Type: t}
	}
	items := docschema.DocField{ID: "f_item", Name: "[]", Type: docschema.Object, Children: []docschema.DocField{
		{ID: "v_note", Name: "note", Type: docschema.Object, Variant: &docschema.Variant{Field: "kind", Value: json.RawMessage(`"note"`)}, Children: []docschema.DocField{f("f_text", "text", docschema.String)}},
		{ID: "v_call", Name: "call", Type: docschema.Object, Variant: &docschema.Variant{Field: "kind", Value: json.RawMessage(`"call"`)}, Children: []docschema.DocField{f("f_secs", "secs", docschema.Integer)}},
	}}
	join := []mapping.JoinPair{{Parent: "id", Child: "order_id"}}
	at := []mapping.OrderColumn{{Column: "at"}}
	spec := &mapping.Spec{
		SpecVersion: 2, Mode: mapping.ModeComposed, Key: mapping.KeySpec{Template: "order::{id}"}, NullHandling: mapping.NullOmit,
		DocSchema: docschema.Schema{Root: docschema.DocField{ID: docschema.RootID, Name: "$", Type: docschema.Object, Children: []docschema.DocField{
			{ID: "f_events", Name: "events", Type: docschema.Array, Items: &items},
		}}},
		Relations: []mapping.Relation{
			{ID: "rel_note", Kind: mapping.EmbedOneToMany, Table: mapping.TableRef{Name: "notes"}, Join: join, TargetFieldID: "v_note", OrderBy: at},
			{ID: "rel_call", Kind: mapping.EmbedOneToMany, Table: mapping.TableRef{Name: "calls"}, Join: join, TargetFieldID: "v_call", OrderBy: at},
		},
		FieldMappings: []mapping.FieldMapping{
			{ID: "m_text", TargetFieldID: "f_text", Sources: []mapping.SourceRef{{Relation: "rel_note", Column: "text"}}, Transforms: []mapping.Step{}, State: mapping.StateActive},
			{ID: "m_secs", TargetFieldID: "f_secs", Sources: []mapping.SourceRef{{Relation: "rel_call", Column: "secs"}}, Transforms: []mapping.Step{}, State: mapping.StateActive},
		},
	}
	spec.Normalize()
	return spec, orders, Options{Related: map[string]*schema.Table{"rel_note": notes, "rel_call": calls}}
}

func unionRows() source.Related {
	return source.Related{
		"rel_note": {Rows: []source.RelatedRow{
			{Values: []any{int32(1), int32(1), int32(10), "first"}},
			{Values: []any{int32(2), int32(1), int32(30), "last"}},
		}},
		"rel_call": {Rows: []source.RelatedRow{{Values: []any{int32(7), int32(1), int32(20), int32(65)}}}},
	}
}

// OC-171 pattern C: the array is the union of its variants' rows, merged in their order
// column (descending too), and a variant past its limit is truncated with a warning.
func TestVariants_OC171_Union(t *testing.T) {
	spec, orders, opts := unionSpec()
	p, issues := Compile(spec, orders, opts)
	require.NotNil(t, p, "%+v", issues)
	_, doc, warnings, _, rerr := p.ApplyTraceWith([]any{int32(1)}, unionRows(), nil)
	require.Nil(t, rerr)
	require.Empty(t, warnings)
	require.JSONEq(t, `{"events":[{"text":"first"},{"secs":65},{"text":"last"}]}`, string(doc))

	for i := range spec.Relations {
		spec.Relations[i].OrderBy[0].Desc = true
	}
	p, _ = Compile(spec, orders, opts)
	_, doc, _, _, _ = p.ApplyTraceWith([]any{int32(1)}, unionRows(), nil)
	require.JSONEq(t, `{"events":[{"text":"last"},{"secs":65},{"text":"first"}]}`, string(doc))

	// Without an order on every part, the parts are written in design order; a missing part
	// writes nothing.
	spec.Relations[1].OrderBy = nil
	spec.Relations[0].Limit = 1
	p, _ = Compile(spec, orders, opts)
	_, doc, warnings, _, _ = p.ApplyTraceWith([]any{int32(1)}, unionRows(), nil)
	require.JSONEq(t, `{"events":[{"text":"first"},{"secs":65}]}`, string(doc))
	require.Len(t, warnings, 1)
	require.Equal(t, CodeEmbedTruncated, warnings[0].Code)
	require.Equal(t, "events", warnings[0].FieldPath)

	rel := unionRows()
	delete(rel, "rel_call")
	_, doc, _, _, _ = p.ApplyTraceWith([]any{int32(1)}, rel, nil)
	require.JSONEq(t, `{"events":[{"text":"first"}]}`, string(doc))
}

// OC-171: a related row with the wrong number of values fails the union's document.
func TestVariants_OC171_UnionRowError(t *testing.T) {
	spec, orders, opts := unionSpec()
	p, _ := Compile(spec, orders, opts)
	rel := unionRows()
	rel["rel_call"].Rows[0].Values = []any{int32(7)}
	_, _, _, _, rerr := p.ApplyTraceWith([]any{int32(1)}, rel, nil)
	require.NotNil(t, rerr)
	require.Equal(t, CodeTypeMismatch, rerr.Code)
}

// OC-171: a union item's warnings carry its path in the array; a strict failure fails the
// document. ApplyTraceRelated reads the same rows, flat.
func TestVariants_OC171_UnionItemWarnings(t *testing.T) {
	spec, orders, opts := unionSpec()
	spec.FieldMappings[0].Transforms = []mapping.Step{{Op: "parseInteger"}}
	p, issues := Compile(spec, orders, opts)
	require.NotNil(t, p, "%+v", issues)
	_, doc, warnings, _, rerr := p.ApplyTraceWith([]any{int32(1)}, unionRows(), nil)
	require.Nil(t, rerr)
	require.JSONEq(t, `{"events":[{},{"secs":65},{}]}`, string(doc))
	require.Len(t, warnings, 2)
	require.Equal(t, "events[0].text", warnings[0].FieldPath)
	require.Equal(t, "events[2].text", warnings[1].FieldPath)

	flat := map[string][][]any{}
	for id, set := range unionRows() {
		for _, r := range set.Rows {
			flat[id] = append(flat[id], r.Values)
		}
	}
	_, doc2, _, _, _ := p.ApplyTraceRelated([]any{int32(1)}, flat, nil)
	require.JSONEq(t, string(doc), string(doc2))

	spec.FieldMappings[0].Transforms = []mapping.Step{{Op: "parseInteger", Args: map[string]any{"strict": true}}}
	p, _ = Compile(spec, orders, opts)
	_, _, _, _, rerr = p.ApplyTraceWith([]any{int32(1)}, unionRows(), nil)
	require.NotNil(t, rerr)
	require.Equal(t, CodeParseFailed, rerr.Code)
}
