package mapping

import (
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/docschema"
	"omnidatabus/internal/schema"
)

func groupedSpec() (*Spec, Context) {
	f := func(id, name string, t docschema.FieldType, kids ...docschema.DocField) docschema.DocField {
		return docschema.DocField{ID: id, Name: name, Type: t, Children: kids}
	}
	item := f("f_item", "[]", docschema.Object, f("f_line", "lineNo", docschema.Integer), f("f_qty", "quantity", docschema.Integer))
	m := func(id, field, rel, col string) FieldMapping {
		return FieldMapping{ID: id, TargetFieldID: field, Sources: []SourceRef{{Relation: rel, Column: col}}, Origin: OriginManual, State: StateActive}
	}
	s := &Spec{
		SpecVersion: 2, Mode: ModeComposed,
		Root:      &Root{Table: TableRef{Schema: "shop", Name: "order_detail"}, GroupBy: []string{"order_id"}},
		DocSchema: docschema.Schema{Root: f(docschema.RootID, "$", docschema.Object, f("f_type", "type", docschema.String), f("f_oid", "orderId", docschema.Integer), docschema.DocField{ID: "f_products", Name: "products", Type: docschema.Array, Items: &item})},
		Key:       KeySpec{Template: "order::{order_id}"},
		Relations: []Relation{{ID: "rel_lines", Group: true, TargetFieldID: "f_products", OrderBy: []OrderColumn{{Column: "line_no"}}}},
		FieldMappings: []FieldMapping{
			m("m_type", "f_type", "", "type"), m("m_oid", "f_oid", "", "order_id"),
			m("m_line", "f_line", "rel_lines", "line_no"), m("m_qty", "f_qty", "rel_lines", "quantity"),
		},
	}
	s.Normalize()
	t := &schema.Table{Name: "order_detail", PrimaryKey: []string{"order_id", "line_no"}, Columns: []schema.Column{
		{Name: "type", JSONType: schema.TypeString, UDTName: "text"},
		{Name: "order_id", JSONType: schema.TypeInteger, UDTName: "int8"},
		{Name: "line_no", JSONType: schema.TypeInteger, UDTName: "int4"},
		{Name: "quantity", JSONType: schema.TypeInteger, UDTName: "int4"},
	}, Indexes: []schema.Index{{Name: "od_pk", Columns: []string{"order_id", "line_no"}}}}
	return s, Context{Table: t, Related: func(_, n string) *schema.Table {
		if n == "order_detail" {
			return t
		}
		return nil
	}}
}

func byCode(issues []Issue) map[string]Issue {
	out := map[string]Issue{}
	for _, i := range issues {
		out[i.Code] = i
	}
	return out
}

func TestSyncGroup_SelfRelationOnGroupColumns(t *testing.T) {
	s, _ := groupedSpec()
	r := s.GroupRelation()
	require.Equal(t, TableRef{Schema: "shop", Name: "order_detail"}, r.Table)
	require.Equal(t, EmbedOneToMany, r.Kind)
	require.Equal(t, []JoinPair{{Parent: "order_id", Child: "order_id"}}, r.Join)
	require.Nil(t, r.ParentID)
	require.Equal(t, []string{"order_id"}, s.GroupBy())
}

func TestValidate_AC2_Grouped(t *testing.T) {
	s, ctx := groupedSpec()
	got := byCode(Validate(s, ctx))
	for _, i := range got {
		require.NotEqual(t, SeverityError, i.Severity, "%s %s", i.Code, i.Message)
	}
	// type is read from each order's first line: a warning that says so.
	require.Equal(t, SeverityWarning, got[CodeRootFieldFirstRow].Severity)
	require.Equal(t, "f_type", got[CodeRootFieldFirstRow].FieldID)
	require.NotContains(t, got, CodeKeyMayCollide, "order_id is unique per document once grouped")

	// A key using a line column.
	s.Key.Template = "od::{order_id}-{line_no}"
	require.Equal(t, SeverityError, byCode(Validate(s, ctx))[CodeKeyNotGrouped].Severity)

	// An unknown group column; a group relation without grouping.
	s, ctx = groupedSpec()
	s.Root.GroupBy = []string{"order_no"}
	require.Contains(t, byCode(Validate(s, ctx)), CodeGroupByColumnMissing)
	s, ctx = groupedSpec()
	s.Root.GroupBy = nil
	require.Contains(t, byCode(Validate(s, ctx)), CodeGroupRowsWithoutGroup)
}

func TestValidate_UngroupedKeyCollides(t *testing.T) {
	// Without grouping, order::{order_id} on order_detail collides (several lines per order).
	s, ctx := groupedSpec()
	s.Root.GroupBy = nil
	s.Relations = []Relation{}
	s.FieldMappings = s.FieldMappings[:2]
	s.Normalize()
	require.Contains(t, byCode(Validate(s, ctx)), CodeKeyMayCollide)
}

func TestValidate_AC1_ItemReadsParentRow(t *testing.T) {
	s, ctx := groupedSpec()
	require.NotContains(t, byCode(Validate(s, ctx)), CodeItemReadsParentRow, "the fixture reads each row")
	// products[].lineNo from the driving table: the order's first row, repeated.
	for i := range s.FieldMappings {
		if s.FieldMappings[i].ID == "m_line" {
			s.FieldMappings[i].Sources = []SourceRef{{Column: "line_no"}}
		}
	}
	is := byCode(Validate(s, ctx))[CodeItemReadsParentRow]
	require.Equal(t, SeverityError, is.Severity)
	require.Equal(t, "f_line", is.FieldID)
	require.Equal(t, "products[].lineNo", is.Path)
	require.Equal(t, &Fix{Kind: "repoint-source", Label: "Read it from each row", RelationID: "rel_lines"}, is.Fix)
	require.False(t, Ready(Validate(s, ctx)))
}

func TestValidate_AC4_OtherTableParentValueAllowed(t *testing.T) {
	// orders with items[] from order_items: an item reading orders.order_id is a parent value.
	s, ctx := groupedSpec()
	s.Root = &Root{Table: TableRef{Schema: "shop", Name: "orders"}}
	s.Relations = []Relation{{ID: "rel_items", Kind: EmbedOneToMany, Table: TableRef{Schema: "shop", Name: "order_items"}, Join: []JoinPair{{Parent: "order_id", Child: "order_id"}}, TargetFieldID: "f_products"}}
	s.FieldMappings = []FieldMapping{{ID: "m_line", TargetFieldID: "f_line", Sources: []SourceRef{{Column: "order_id"}}, Origin: OriginManual, State: StateActive}}
	s.Normalize()
	require.NotContains(t, byCode(Validate(s, ctx)), CodeItemReadsParentRow)
}

// selfBoundSpec is the design users build when they want one document per order: products[]
// bound back to order_detail (its own driving table) instead of grouping.
func selfBoundSpec(kind string, join ...JoinPair) (*Spec, Context) {
	s, ctx := groupedSpec()
	s.Root.GroupBy = nil
	s.Key.Template = "order_details::{order_id}::{line_no}"
	s.Relations = []Relation{{ID: "rel_self", Kind: kind, Table: s.Root.Table, Join: join, TargetFieldID: "f_products"}}
	ctx.Table.Columns = append(ctx.Table.Columns, schema.Column{Name: "product_id", JSONType: schema.TypeInteger, UDTName: "int8"})
	return s, ctx
}

func TestValidate_ArrayRepeatsRoot(t *testing.T) {
	// Many-to-one on unrelated same-name columns: an error, and grouping is by the PK's lead.
	s, ctx := selfBoundSpec(EmbedManyToOne, JoinPair{Parent: "product_id", Child: "product_id"}, JoinPair{Parent: "line_no", Child: "line_no"})
	got := byCode(Validate(s, ctx))
	is := got[CodeArrayRepeatsRoot]
	require.Equal(t, SeverityError, is.Severity)
	require.Equal(t, "f_products", is.FieldID)
	require.Equal(t, &Fix{Kind: "combine-rows", Label: "Combine rows by order_id", Columns: []string{"order_id"}, RelationID: "rel_self"}, is.Fix)
	require.NotContains(t, got, CodeBindingKindMismatch, "the combine issue replaces the adapter advice")

	// One-to-many on a PK column: it runs, so a warning, grouped by the join's own columns.
	s, ctx = selfBoundSpec(EmbedOneToMany, JoinPair{Parent: "order_id", Child: "order_id"})
	is = byCode(Validate(s, ctx))[CodeArrayRepeatsRoot]
	require.Equal(t, SeverityWarning, is.Severity)
	require.Equal(t, []string{"order_id"}, is.Fix.Columns)
}

func TestValidate_ArrayRepeatsRoot_NotForHierarchiesOrGroups(t *testing.T) {
	// A self join on different columns is a hierarchy, not a group.
	s, ctx := selfBoundSpec(EmbedOneToMany, JoinPair{Parent: "order_id", Child: "line_no"})
	require.NotContains(t, byCode(Validate(s, ctx)), CodeArrayRepeatsRoot)

	// Already grouped: the group relation is the right binding.
	s, ctx = groupedSpec()
	require.NotContains(t, byCode(Validate(s, ctx)), CodeArrayRepeatsRoot)
}
