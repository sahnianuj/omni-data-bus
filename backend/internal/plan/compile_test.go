package plan

import (
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/docschema"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/schema"
	"omnidatabus/internal/transform"
)

func col(name, udt, typ string, ord int) schema.Column {
	return schema.Column{Name: name, Ordinal: ord, UDTName: udt, JSONType: typ}
}

func tables() map[string]*schema.Table {
	i, s := func(n string, o int) schema.Column { return col(n, "int4", schema.TypeInteger, o) }, func(n string, o int) schema.Column { return col(n, "text", schema.TypeString, o) }
	return map[string]*schema.Table{
		"orders": {Name: "orders", PrimaryKey: []string{"order_id"}, Readable: true, Columns: []schema.Column{
			i("order_id", 1), i("customer_id", 2), s("status", 3), s("note", 4), s("secret", 5),
		}},
		"order_items": {Name: "order_items", Readable: true, Columns: []schema.Column{
			i("order_id", 1), i("line_no", 2), s("sku", 3), i("quantity", 4), i("product_id", 5), s("unused", 6),
		}},
		"products": {Name: "products", PrimaryKey: []string{"product_id"}, Readable: true, Columns: []schema.Column{
			i("product_id", 1), s("name", 2), s("price", 3),
		}},
	}
}

func sp(s string) *string { return &s }

// orderSpec: orders + items[] (a, one-to-many) + items[].product (b, a child of a). b is listed
// first so the plan's order can't come from the list.
func orderSpec() *mapping.Spec {
	f := func(id, name string, t docschema.FieldType) docschema.DocField {
		return docschema.DocField{ID: id, Name: name, Type: t}
	}
	product := f("f_product", "product", docschema.Object)
	product.Children = []docschema.DocField{f("f_pname", "name", docschema.String)}
	item := f("f_item", "item", docschema.Object)
	item.Children = []docschema.DocField{f("f_sku", "sku", docschema.String), product}
	items := f("f_items", "items", docschema.Array)
	items.Items = &item
	root := docschema.NewRoot()
	root.Children = []docschema.DocField{f("f_oid", "orderId", docschema.Integer), f("f_status", "status", docschema.String), items}
	s := &mapping.Spec{
		DocSchema: docschema.Schema{Root: root},
		Key:       mapping.KeySpec{Template: "order::{order_id}"},
		Relations: []mapping.Relation{
			{
				ID: "b", ParentID: sp("a"), Kind: mapping.EmbedManyToOne, Table: mapping.TableRef{Schema: "shop", Name: "products"},
				Join: []mapping.JoinPair{{Parent: "product_id", Child: "product_id"}}, TargetFieldID: "f_product",
			},
			{
				ID: "a", Kind: mapping.EmbedOneToMany, Table: mapping.TableRef{Schema: "shop", Name: "order_items"},
				Join: []mapping.JoinPair{{Parent: "order_id", Child: "order_id"}}, OrderBy: []mapping.OrderColumn{{Column: "line_no"}}, TargetFieldID: "f_items",
			},
		},
		FieldMappings: []mapping.FieldMapping{
			{ID: "m_oid", TargetFieldID: "f_oid", Sources: []mapping.SourceRef{{Column: "order_id"}}, State: mapping.StateActive},
			{
				ID: "m_status", TargetFieldID: "f_status", Sources: []mapping.SourceRef{{Column: "status"}}, State: mapping.StateActive,
				Transforms: []mapping.Step{{Op: "replace", Args: map[string]any{"pattern": "a", "replacement": "b", "regex": false, "note": "several keys"}}},
			},
			{ID: "m_sku", TargetFieldID: "f_sku", Sources: []mapping.SourceRef{{Relation: "a", Column: "sku"}}, State: mapping.StateActive},
			{ID: "m_pname", TargetFieldID: "f_pname", Sources: []mapping.SourceRef{{Relation: "b", Column: "name"}}, State: mapping.StateActive},
			{ID: "m_note", TargetFieldID: "f_status", Sources: []mapping.SourceRef{{Column: "note"}}, State: mapping.StateRejected},
		},
	}
	s.Normalize()
	return s
}

func compile(t *testing.T, s *mapping.Spec) *ExecutionPlan {
	t.Helper()
	tb := tables()
	p, _, err := Compile(Input{
		Spec: s, Root: mapping.TableRef{Schema: "shop", Name: "orders"}, Table: tb["orders"],
		Related: func(_, name string) *schema.Table { return tb[name] }, Options: transform.Options{},
	})
	require.NoError(t, err)
	return p
}

func TestCompile_AC4_TopologicalOrder(t *testing.T) {
	p := compile(t, orderSpec())
	require.Len(t, p.Relations, 2)
	require.Equal(t, "a", p.Relations[0].Relation.ID)
	require.Equal(t, 1, p.Relations[0].Depth)
	require.Equal(t, "b", p.Relations[1].Relation.ID)
	require.Equal(t, 2, p.Relations[1].Depth)
	require.Equal(t, StrategyBatched, p.Relations[0].Strategy)
	require.Equal(t, mapping.TableRef{Schema: "shop", Name: "orders"}, p.Root)
	require.Equal(t, []string{"order_id"}, p.Key.Columns())
}

func TestCompile_AC4_ProjectionPushdown(t *testing.T) {
	p := compile(t, orderSpec())
	// Root: mapped columns, the key template and a's join column; not customer_id, note (only a
	// rejected suggestion) or secret.
	require.Equal(t, []string{"order_id", "status"}, p.Columns)
	require.Equal(t, []bool{true, false, true, false, false}, p.Project())
	require.Equal(t, []bool{true, true, true, false, false}, p.Project("customer_id"), "the read's keyset columns are added")
	// a: join, order, mapped sku, and b's join column; not quantity or unused.
	require.Equal(t, []string{"order_id", "line_no", "sku", "product_id"}, p.Relations[0].Columns)
	// b: join and mapped name; not price.
	require.Equal(t, []string{"product_id", "name"}, p.Relations[1].Columns)

	// An expression may read row.<any>: every root column is read.
	s := orderSpec()
	s.FieldMappings[1].Transforms = []mapping.Step{{Op: "expression", Args: map[string]any{"expr": "row.note"}}}
	require.Len(t, compile(t, s).Columns, 5)
}

func TestCompile_AC4_CycleRejected(t *testing.T) {
	s := orderSpec()
	s.Relations[1].ParentID = sp("b") // a ↔ b
	tb := tables()
	_, _, err := Compile(Input{Spec: s, Table: tb["orders"], Related: func(_, n string) *schema.Table { return tb[n] }})
	var perr *Error
	require.True(t, errors.As(err, &perr))
	require.Equal(t, mapping.CodeRelationCycle, perr.Code)

	// A related table that left the source is SOURCE_TABLE_MISSING.
	_, _, err = Compile(Input{Spec: orderSpec(), Table: tb["orders"], Related: func(_, n string) *schema.Table {
		if n == "products" {
			return nil
		}
		return tb[n]
	}})
	require.True(t, errors.As(err, &perr))
	require.Equal(t, CodeRelatedTableMissing, perr.Code)
}

func TestCompile_AC4_HashStable(t *testing.T) {
	want := compile(t, orderSpec()).Hash
	require.Len(t, want, 64)
	for range 25 { // transform args are a map: its iteration order changes between runs
		require.Equal(t, want, compile(t, orderSpec()).Hash)
	}
	// Bookkeeping that doesn't change documents doesn't change the hash…
	s := orderSpec()
	s.FieldMappings[0].Confidence, s.FieldMappings[0].Reason, s.FieldMappings[0].Origin = 0.42, "synonym", mapping.OriginSuggested
	s.FieldMappings[4].Sources[0].Column = "secret" // a rejected suggestion
	require.Equal(t, want, compile(t, s).Hash)
	// …anything that does, does.
	s = orderSpec()
	s.FieldMappings[1].Transforms[0].Args["replacement"] = "c"
	require.NotEqual(t, want, compile(t, s).Hash)
	s = orderSpec()
	s.Key.Template = "o::{order_id}"
	require.NotEqual(t, want, compile(t, s).Hash)
	s = orderSpec()
	s.Relations[1].OrderBy = []mapping.OrderColumn{{Column: "line_no", Desc: true}}
	require.NotEqual(t, want, compile(t, s).Hash)
}

func TestCompile_NewProgramAndLookups(t *testing.T) {
	s := orderSpec()
	s.Relations = s.Relations[1:] // only a (depth 1), as 1.0 embeds
	s.FieldMappings = s.FieldMappings[:3]
	p := compile(t, s)
	require.NotNil(t, p.Program)
	require.True(t, p.HasRelations())
	require.Len(t, p.Lookups(), 1)
	prog, err := p.NewProgram()
	require.NoError(t, err)
	require.NotSame(t, p.Program, prog, "each worker gets its own program")
	require.Same(t, p.Spec(), p.spec)

	s.FieldMappings[1].Transforms = []mapping.Step{{Op: "nope"}}
	p = compile(t, s)
	require.Nil(t, p.Program)
	_, err = p.NewProgram()
	require.Error(t, err)
	require.Equal(t, "", ErrorMessages([]mapping.Issue{{Severity: mapping.SeverityWarning, Message: "w"}}))
}

// A composed mapping is the same relation tree with composer metadata on its nodes: it must
// compile to the same plan and the same documents as the canvas mapping (OC-047 AC5, D3).
func TestCompile_AC5_ComposedEqualsCanvas(t *testing.T) {
	canvas := orderSpec()
	composed := orderSpec()
	composed.Mode = mapping.ModeComposed
	composed.Root = &mapping.Root{Table: mapping.TableRef{Schema: "shop", Name: "orders"}}
	composed.Normalize() // annotates the bound nodes
	require.NotNil(t, composed.DocSchema.Find("f_items").Binding)
	require.Equal(t, "a", composed.DocSchema.Find("f_items").Binding.RelationID)
	require.Nil(t, canvas.DocSchema.Find("f_items").Binding)

	pc, pk := compile(t, canvas), compile(t, composed)
	require.Equal(t, pc.Hash, pk.Hash)
	require.Equal(t, pc.Columns, pk.Columns)

	row := []any{int32(1), int32(7), "paid", "n", "s"}
	rel := source.Related{"a": {Rows: []source.RelatedRow{
		{Values: []any{int32(1), int32(1), "A-1", int32(2), int32(9), "x"}, Sub: source.Related{"b": {Rows: []source.RelatedRow{{Values: []any{int32(9), "Pen", "1.0"}}}}}},
		{Values: []any{int32(1), int32(2), "B-2", int32(1), int32(8), "x"}},
	}}}
	progC, err := pc.NewProgram()
	require.NoError(t, err)
	progK, err := pk.NewProgram()
	require.NoError(t, err)
	keyC, docC, _, rerr := progC.ApplyWith(row, rel, nil)
	require.Nil(t, rerr)
	keyK, docK, _, rerr := progK.ApplyWith(row, rel, nil)
	require.Nil(t, rerr)
	require.Equal(t, keyC, keyK)
	require.Equal(t, string(docC), string(docK), "byte-identical")

	// An adapter changes the documents, so it changes the plan.
	composed.DocSchema.Find("f_product").Binding.Adapter = docschema.AdapterFirst
	require.NotEqual(t, pc.Hash, compile(t, composed).Hash)
}

// The spec the composer UI saves when an order is built from blank (web Composer.test.tsx AC1)
// validates and compiles on the server: the UI and the server agree on the composed model.
func TestCompile_ComposerGolden(t *testing.T) {
	raw, err := os.ReadFile("../../../web/testdata/composer/order_from_blank.json")
	require.NoError(t, err)
	var s mapping.Spec
	require.NoError(t, json.Unmarshal(raw, &s))
	s.Normalize()
	i, txt := func(n string, o int) schema.Column { return col(n, "int4", schema.TypeInteger, o) }, func(n string, o int) schema.Column { return col(n, "text", schema.TypeString, o) }
	tb := map[string]*schema.Table{
		"orders":      {Name: "orders", PrimaryKey: []string{"order_id"}, Readable: true, Columns: []schema.Column{i("order_id", 1), txt("order_no", 2), i("customer_id", 3)}},
		"order_items": {Name: "order_items", Readable: true, Columns: []schema.Column{i("order_id", 1), i("line_no", 2), i("product_id", 3), i("quantity", 4)}},
		"products":    {Name: "products", PrimaryKey: []string{"product_id"}, Readable: true, Columns: []schema.Column{i("product_id", 1), txt("sku", 2), txt("name", 3)}},
	}
	rel := func(_, name string) *schema.Table { return tb[name] }
	for _, is := range mapping.Validate(&s, mapping.Context{Table: tb["orders"], Related: rel}) {
		require.NotEqual(t, mapping.SeverityError, is.Severity, "%+v", is)
	}
	p, issues, err := Compile(Input{Spec: &s, Root: s.Root.Table, Table: tb["orders"], Related: rel})
	require.NoError(t, err)
	require.NotNil(t, p.Program, "%v", issues)
	_, doc, _, rerr := p.Program.ApplyWith([]any{int32(1001), "SO-1001", int32(7)}, source.Related{"rel_2": {Rows: []source.RelatedRow{
		{Values: []any{int32(1001), int32(1), int32(9), int32(2)}, Sub: source.Related{"rel_1": {Rows: []source.RelatedRow{{Values: []any{int32(9), "A-1", "Pen"}}}}}},
	}}}, nil)
	require.Nil(t, rerr)
	require.JSONEq(t, `{"orderId":1001,"items":[{"quantity":2,"product":{"sku":"A-1"}}]}`, string(doc))
}
