package mapping

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/docschema"
	"omnidatabus/internal/schema"
)

func custTable() *schema.Table {
	return &schema.Table{Name: "customer", PrimaryKey: []string{"customer_id"}, Columns: []schema.Column{
		{Name: "customer_id", UDTName: "int4", JSONType: schema.TypeInteger, IsPrimaryKey: true},
		{Name: "first_name", UDTName: "text", JSONType: schema.TypeString},
		{Name: "age_text", UDTName: "text", JSONType: schema.TypeString},
		{Name: "notes", UDTName: "text", JSONType: schema.TypeString},
		{Name: "profile", UDTName: "jsonb", JSONType: schema.TypeAny},
	}}
}

func f(id, name string, t docschema.FieldType, required bool) docschema.DocField {
	return docschema.DocField{ID: id, Name: name, Type: t, Required: required}
}

func baseSpec() Spec {
	s := Spec{
		DocSchema: docschema.Schema{Root: docschema.DocField{ID: docschema.RootID, Name: "$", Type: docschema.Object, Children: []docschema.DocField{
			f("f_id", "customerId", docschema.Integer, true),
			f("f_first", "firstName", docschema.String, false),
			f("f_age", "age", docschema.Integer, false),
			f("f_email", "email", docschema.String, true),
			f("f_desc", "description", docschema.String, false),
			{ID: "f_prof", Name: "profile", Type: docschema.Object, Children: []docschema.DocField{}},
		}}},
		Key:       KeySpec{Template: "customer::{customer_id}"},
		TypeField: TypeField{Enabled: true, Name: "type", Value: "customer"},
		FieldMappings: []FieldMapping{
			{ID: "m1", TargetFieldID: "f_id", Sources: []SourceRef{{Column: "customer_id"}}, Origin: OriginAuto, State: StateActive, Confidence: 0.97},
			{ID: "m2", TargetFieldID: "f_first", Sources: []SourceRef{{Column: "first_name"}}, Origin: OriginAuto, State: StateActive, Confidence: 0.97},
			{ID: "m3", TargetFieldID: "f_prof", Sources: []SourceRef{{Column: "profile"}}, Origin: OriginManual, State: StateActive},
		},
	}
	s.Normalize()
	return s
}

func codesOf(issues []Issue) []string {
	out := []string{}
	for _, i := range issues {
		out = append(out, i.Severity+":"+i.Code+"@"+i.Path)
	}
	return out
}

func TestValidate_AC1_AggregatesAllRules(t *testing.T) {
	s := baseSpec()
	s.FieldMappings = append(s.FieldMappings,
		FieldMapping{ID: "m4", TargetFieldID: "f_age", Sources: []SourceRef{{Column: "age_text"}}, Origin: OriginManual, State: StateActive},        // lossy, no transform
		FieldMapping{ID: "m5", TargetFieldID: "f_desc", Sources: []SourceRef{{Column: "notes"}}, Origin: OriginSuggested, State: StatePending},      // pending
		FieldMapping{ID: "m6", TargetFieldID: "f_first", Sources: []SourceRef{{Column: "dropped_col"}}, Origin: OriginManual, State: StateRejected}, // rejected: ignored
	)
	s.FieldMappings[1].Sources = []SourceRef{{Column: "legacy_name"}} // dropped from the source
	missing := false
	issues := Validate(&s, Context{Table: custTable(), TargetExists: &missing, Location: LocationFromV1(Target{Bucket: "b", Scope: "s", Collection: "c"})})
	require.Equal(t, []string{
		"error:REQUIRED_FIELD_UNMAPPED@email",
		"error:SOURCE_COLUMN_MISSING@firstName",
		"error:TARGET_KEYSPACE_MISSING@target",
		"warning:LOSSY_CONVERSION@age",
		"warning:SUGGESTIONS_PENDING@fieldMappings",
		"info:UNMAPPED_SOURCE_COLUMNS@source",
	}, codesOf(issues))
	require.False(t, Ready(issues))
	require.Equal(t, StatusStale, Status(issues))
	require.Equal(t, "parseInteger", issues[3].Fix.Step.Op)
	require.Equal(t, "mark-optional", issues[0].Fix.Kind)
}

func TestValidate_AC1_ReadyWhenOnlyWarnings(t *testing.T) {
	s := baseSpec()
	s.DocSchema.Root.Children[3].Required = false
	s.Key.Template = "customer::{first_name}" // may collide: a warning
	issues := Validate(&s, Context{Table: custTable()})
	require.True(t, Ready(issues))
	require.Equal(t, StatusReady, Status(issues))
	require.Contains(t, codesOf(issues), "warning:KEY_MAY_COLLIDE@key")
}

func TestValidate_AC1_DeterministicOrder(t *testing.T) {
	s := baseSpec()
	first := codesOf(Validate(&s, Context{Table: custTable()}))
	for range 20 {
		require.Equal(t, first, codesOf(Validate(&s, Context{Table: custTable()})))
	}
}

func TestValidate_KeyAndStructure(t *testing.T) {
	s := baseSpec()
	s.DocSchema.Root.Children[3].Required = false
	s.Key.Template = ""
	require.Contains(t, codesOf(Validate(&s, Context{Table: custTable()})), "error:KEY_TEMPLATE_MISSING@key")
	s.Key.Template = "c::{oops"
	require.Contains(t, codesOf(Validate(&s, Context{Table: custTable()})), "error:KEY_TEMPLATE_INVALID@key")
	s.Key.Template = "c::{gone}"
	require.Contains(t, codesOf(Validate(&s, Context{Table: custTable()})), "error:SOURCE_COLUMN_MISSING@key")
	s.Key.Template = "{$uuid}"
	require.Contains(t, codesOf(Validate(&s, Context{Table: custTable()})), "warning:NON_IDEMPOTENT_KEY@key")

	s = baseSpec()
	s.FieldMappings = append(s.FieldMappings,
		FieldMapping{ID: "m1", TargetFieldID: "f_id", Sources: []SourceRef{{Column: "customer_id"}}, Origin: OriginManual, State: StateActive},
		FieldMapping{ID: "m9", TargetFieldID: "f_email", Sources: []SourceRef{{Column: "profile"}}, Origin: OriginManual, State: "weird"},
		FieldMapping{ID: "m10", TargetFieldID: "f_age", Sources: []SourceRef{{Column: "profile"}}, Origin: OriginManual, State: StateActive},
		FieldMapping{ID: "m11", TargetFieldID: "f_desc", Sources: []SourceRef{{Column: "customer_id"}}, Origin: OriginManual, State: StateActive},
	)
	s.FieldMappings = append(s.FieldMappings, FieldMapping{ID: "m12", TargetFieldID: "f_x", Origin: OriginManual, State: StateActive})
	got := codesOf(Validate(&s, Context{Table: custTable()}))
	require.Contains(t, got, "error:INVALID_MAPPING@customerId")
	require.Contains(t, got, "error:DUPLICATE_TARGET@customerId")
	require.Contains(t, got, "error:INVALID_MAPPING@email")
	require.Contains(t, got, "error:UNKNOWN_TARGET_FIELD@fieldMappings.m12")
	require.Contains(t, got, "warning:LOSSY_CONVERSION@age", "any → integer is lossy")
	s.FieldMappings[len(s.FieldMappings)-2].TargetFieldID = "f_prof"
	s.DocSchema.Root.Children[1].Type = docschema.Array // firstName becomes an array without items
	require.Contains(t, codesOf(Validate(&s, Context{Table: custTable()})), "error:ARRAY_WITHOUT_ITEMS@firstName")
}

func TestSpecValidate_AC5_TypeFieldCollision(t *testing.T) {
	s := baseSpec()
	s.DocSchema.Root.Children[3].Required = false
	s.TypeField.Name = "firstName"
	require.Contains(t, codesOf(Validate(&s, Context{Table: custTable()})), "error:TYPE_FIELD_COLLISION@typeField")
	s.TypeField.Name = ""
	require.Contains(t, codesOf(Validate(&s, Context{Table: custTable()})), "error:TYPE_FIELD_INVALID@typeField")
	s.TypeField.Enabled = false
	require.True(t, Ready(Validate(&s, Context{Table: custTable()})))
}

func TestSpecValidate_AC9_UnknownReferences(t *testing.T) {
	s := baseSpec()
	s.FieldMappings = append(s.FieldMappings,
		FieldMapping{ID: "bad1", TargetFieldID: "f_nope", Sources: []SourceRef{{Column: "first_name"}}},
		FieldMapping{ID: "bad2", TargetFieldID: "f_first", Sources: []SourceRef{{Column: "no_such_col"}}},
	)
	issues := CheckReferences(&s, custTable(), nil, nil)
	require.Equal(t, []string{"error:UNKNOWN_TARGET_FIELD@fieldMappings.bad1", "error:SOURCE_COLUMN_MISSING@fieldMappings.bad2"}, codesOf(issues))

	// A column the stored spec already used survives drift (it can be re-mapped later).
	prev := baseSpec()
	prev.FieldMappings[1].Sources = []SourceRef{{Column: "no_such_col"}}
	require.Len(t, CheckReferences(&s, custTable(), &prev, nil), 1)
}

func TestMappingStatus_AC3_Table(t *testing.T) {
	s := baseSpec()
	s.Key.Template = ""
	require.Equal(t, StatusDraft, Status(Validate(&s, Context{Table: custTable()})), "no key template → draft")
	s = baseSpec()
	s.DocSchema.Root.Children[3].Required = false
	require.Equal(t, StatusReady, Status(Validate(&s, Context{Table: custTable()})))
	tb := custTable()
	tb.Columns = tb.Columns[:1] // first_name dropped from the source
	require.Equal(t, StatusStale, Status(Validate(&s, Context{Table: tb})))
}

func TestMappingSpec_AC6_CreateIfMissingPersisted(t *testing.T) {
	b, err := json.Marshal(Target{Bucket: "shop", Scope: "sales", Collection: "orders", CreateIfMissing: true})
	require.NoError(t, err)
	require.JSONEq(t, `{"bucket":"shop","scope":"sales","collection":"orders","createIfMissing":true}`, string(b))
	var back Target
	require.NoError(t, json.Unmarshal(b, &back))
	require.True(t, back.CreateIfMissing)
}

func TestReattachMappings_AC7_ByPath(t *testing.T) {
	old := baseSpec()
	next := docschema.Schema{Root: docschema.DocField{ID: docschema.RootID, Name: "$", Type: docschema.Object, Children: []docschema.DocField{
		f("n_id", "customerId", docschema.Integer, true),
		f("n_email", "email", docschema.String, false),
		{ID: "n_prof", Name: "profile", Type: docschema.Object, Children: []docschema.DocField{}},
	}}}
	kept, orphans := ReattachByPath(&old.DocSchema, &next, append(old.FieldMappings, FieldMapping{ID: "gone", TargetFieldID: "f_unknown"}))
	require.Len(t, kept, 2)
	require.Equal(t, "n_id", kept[0].TargetFieldID)
	require.Equal(t, "n_prof", kept[1].TargetFieldID)
	require.Equal(t, []string{"m2", "gone"}, []string{orphans[0].ID, orphans[1].ID}, "firstName no longer exists")
}

func TestMergeDesigns_KeepsExistingAddsNew(t *testing.T) {
	old := baseSpec()
	incoming := docschema.Schema{Root: docschema.DocField{ID: docschema.RootID, Name: "$", Type: docschema.Object, Children: []docschema.DocField{
		f("x1", "customerId", docschema.String, false), // exists: existing type wins
		f("x2", "loyaltyTier", docschema.String, false),
		{ID: "x3", Name: "profile", Type: docschema.Object, Children: []docschema.DocField{f("x4", "bio", docschema.String, false)}},
	}}}
	merged := MergeDesigns(&old.DocSchema, &incoming)
	require.Len(t, merged.Root.Children, 7)
	require.Equal(t, docschema.Integer, merged.Root.Children[0].Type)
	require.Equal(t, "f_id", merged.Root.Children[0].ID, "existing IDs kept")
	require.Equal(t, "bio", merged.Root.Children[5].Children[0].Name)
	require.Len(t, old.DocSchema.Root.Children[5].Children, 0, "the original is untouched")
	require.Empty(t, docschema.Validate(&merged))
}

func TestSpecValidate_AC7_EmbedErrors(t *testing.T) {
	snap := shopSnap()
	customer := snap.Table("shop", "customer")
	ok := embedSpec()
	for _, i := range Validate(&ok, Context{Table: customer, Related: snapRel(snap)}) {
		require.NotContains(t, i.Code, "EMBED", i.Message)
	}

	type tc struct {
		name   string
		mutate func(s *Spec)
		code   string
		embed  string
		field  string
	}
	cases := []tc{
		{"join column missing from the child", func(s *Spec) { s.Relations[0].Join[0].Child = "addr_id" }, CodeEmbedJoinInvalid, "emb_1", "f_addr"},
		{"join column missing from the parent", func(s *Spec) { s.Relations[1].Join[0].Parent = "cust_id" }, CodeEmbedJoinInvalid, "emb_2", "f_orders"},
		{"type mismatch int4 vs text", func(s *Spec) { s.Relations[0].Join[0] = JoinPair{Parent: "name", Child: "address_id"} }, CodeEmbedJoinInvalid, "emb_1", "f_addr"},
		{"array target for many-to-one", func(s *Spec) { s.Relations[0].TargetFieldID = "f_orders" }, CodeEmbedTargetKind, "emb_1", "f_orders"},
		{"object target for one-to-many", func(s *Spec) { s.Relations[1].TargetFieldID = "f_addr"; s.Relations[0].TargetFieldID = "f_id" }, CodeEmbedTargetKind, "emb_2", "f_addr"},
		{"duplicate target", func(s *Spec) { s.Relations = append(s.Relations, s.Relations[0]); s.Relations[2].ID = "emb_3" }, CodeEmbedDuplicate, "emb_3", "f_addr"},
		{"unknown table", func(s *Spec) { s.Relations[0].Table.Name = "adress" }, CodeEmbedTableMissing, "emb_1", "f_addr"},
		{"order column missing", func(s *Spec) { s.Relations[1].OrderBy[0].Column = "placed_at" }, CodeEmbedJoinInvalid, "emb_2", "f_orders"},
		// A root column inside an embed is in scope from 2.0 (OC-043 AC4); an embed's column at the root isn't.
		{"embed column into a root field", func(s *Spec) { s.FieldMappings[0].Sources[0].Relation = "emb_2" }, CodeEmbedScope, "", "f_id"},
		{"embed column into another embed", func(s *Spec) { s.FieldMappings[2].Sources[0].Relation = "emb_1" }, CodeEmbedScope, "", "f_total"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := embedSpec()
			c.mutate(&s)
			var found *Issue
			for _, i := range Validate(&s, Context{Table: customer, Related: snapRel(snap)}) {
				if i.Code == c.code && i.Severity == SeverityError {
					found = &i
					break
				}
			}
			require.NotNil(t, found, "want %s", c.code)
			require.Equal(t, c.field, found.FieldID)
			if c.embed != "" {
				require.Contains(t, found.Message, "Embed "+c.embed+":")
			}
		})
	}

	// Embedded sources are checked against their table.
	s := embedSpec()
	s.FieldMappings[2].Sources[0].Column = "amount"
	require.Contains(t, codesOf(Validate(&s, Context{Table: customer, Related: snapRel(snap)})), "error:"+CodeSourceColumnMissing+"@orders[].total")
	require.NotEmpty(t, CheckReferences(&s, customer, nil, snapRel(snap)))
	s.FieldMappings[2].Sources[0].Column = "total"
	require.Empty(t, CheckReferences(&s, customer, nil, snapRel(snap)))
}

// orderTables: orders → order_items → products, orders ↔ products via order_items.
func orderTables() (*schema.Table, RelatedTables) {
	cl := func(n, udt, jt string) schema.Column { return schema.Column{Name: n, UDTName: udt, JSONType: jt} }
	orders := &schema.Table{Name: "orders", PrimaryKey: []string{"order_id"}, Columns: []schema.Column{cl("order_id", "int8", "integer"), cl("customer_id", "int4", "integer")}}
	tables := map[string]*schema.Table{
		"order_items": {Name: "order_items", Columns: []schema.Column{
			cl("order_id", "int8", "integer"), cl("product_id", "int4", "integer"), cl("line_no", "int4", "integer"), cl("quantity", "int4", "integer"),
			cl("status", "text", "string"), cl("placed", "date", "datetime"),
		}},
		"products":  {Name: "products", PrimaryKey: []string{"product_id"}, Columns: []schema.Column{cl("product_id", "int4", "integer"), cl("sku", "text", "string"), cl("meta", "jsonb", "any")}},
		"customers": {Name: "customers", Columns: []schema.Column{cl("customer_id", "int4", "integer"), cl("name", "text", "string")}},
	}
	return orders, func(_, n string) *schema.Table { return tables[n] }
}

func nestedSpec() Spec {
	s := Spec{
		DocSchema: docschema.Schema{Root: docschema.DocField{ID: docschema.RootID, Name: "$", Type: docschema.Object, Children: []docschema.DocField{
			f("f_id", "orderId", docschema.Integer, false),
			f("f_cname", "customerName", docschema.String, false),
			{ID: "f_items", Name: "items", Type: docschema.Array, Items: &docschema.DocField{ID: "f_item", Name: "[]", Type: docschema.Object, Children: []docschema.DocField{
				f("f_qty", "quantity", docschema.Integer, false),
				{ID: "f_product", Name: "product", Type: docschema.Object, Children: []docschema.DocField{f("f_sku", "sku", docschema.String, false)}},
			}}},
			{ID: "f_rootprod", Name: "rootProduct", Type: docschema.Object, Children: []docschema.DocField{}},
		}}},
		Key: KeySpec{Template: "{order_id}"},
		Relations: []Relation{
			{ID: "rel_items", Kind: EmbedOneToMany, Table: TableRef{"public", "order_items"}, Join: []JoinPair{{"order_id", "order_id"}}, TargetFieldID: "f_items"},
			{ID: "rel_product", ParentID: sptr("rel_items"), Kind: EmbedManyToOne, Table: TableRef{"public", "products"}, Join: []JoinPair{{"product_id", "product_id"}}, TargetFieldID: "f_product"},
			{ID: "rel_cust", Kind: EmbedManyToOne, LookupOnly: true, Table: TableRef{"public", "customers"}, Join: []JoinPair{{"customer_id", "customer_id"}}},
		},
		FieldMappings: []FieldMapping{
			{ID: "m1", TargetFieldID: "f_id", Sources: []SourceRef{{Column: "order_id"}}, State: StateActive},
			{ID: "m2", TargetFieldID: "f_cname", Sources: []SourceRef{{Relation: "rel_cust", Column: "name"}}, State: StateActive},
			{ID: "m3", TargetFieldID: "f_qty", Sources: []SourceRef{{Relation: "rel_items", Column: "quantity"}}, State: StateActive},
			{ID: "m4", TargetFieldID: "f_sku", Sources: []SourceRef{{Relation: "rel_product", Column: "sku"}}, State: StateActive},
		},
	}
	s.Normalize()
	return s
}

func relationErrors(s *Spec) []Issue {
	orders, rel := orderTables()
	var out []Issue
	for _, i := range Validate(s, Context{Table: orders, Related: rel}) {
		if i.Severity == SeverityError {
			out = append(out, i)
		}
	}
	return out
}

func TestSpecValidate_AC6_NestedRelationErrors(t *testing.T) {
	s := nestedSpec()
	require.Empty(t, relationErrors(&s), "items[] with product{}, and a lookup-only customer, are valid")

	// A relation under items whose target field is at the root.
	s = nestedSpec()
	s.Relations[1].TargetFieldID = "f_rootprod"
	require.Contains(t, codes(relationErrors(&s)), CodeRelationFieldOutsideParent)

	// Depth 5 with the default cap of 4.
	s = nestedSpec()
	parent := "rel_product"
	for i := range 3 {
		id := fmt.Sprintf("rel_d%d", i)
		s.Relations = append(s.Relations, Relation{ID: id, ParentID: sptr(parent), Kind: EmbedManyToOne, LookupOnly: true, Table: TableRef{"public", "products"}, Join: []JoinPair{{"product_id", "product_id"}}})
		parent = id
	}
	require.Contains(t, codes(relationErrors(&s)), CodeRelationTooDeep)

	// A one-to-many relation marked lookup-only.
	s = nestedSpec()
	s.Relations[0].LookupOnly = true
	require.Contains(t, codes(relationErrors(&s)), CodeRelationLookupKind)

	// A lookup's columns outside its path are out of scope.
	s = nestedSpec()
	s.Relations[2].ParentID = sptr("rel_product")
	require.Contains(t, codes(relationErrors(&s)), CodeEmbedScope, "customers now hang off products: not in the root's scope")
}

func m2mValidateSpec() Spec {
	s := Spec{
		DocSchema: docschema.Schema{Root: docschema.DocField{ID: docschema.RootID, Name: "$", Type: docschema.Object, Children: []docschema.DocField{
			f("f_id", "orderId", docschema.Integer, false),
			{ID: "f_products", Name: "products", Type: docschema.Array, Items: &docschema.DocField{ID: "f_pi", Name: "[]", Type: docschema.Object, Children: []docschema.DocField{
				f("f_sku", "sku", docschema.String, false), f("f_qty", "quantity", docschema.Integer, false),
			}}},
		}}},
		Key: KeySpec{Template: "{order_id}"},
		Relations: []Relation{{
			ID: "rel_p", Kind: RelationManyToMany, Table: TableRef{"public", "products"}, TargetFieldID: "f_products",
			Via: &Via{Table: TableRef{"public", "order_items"}, ParentJoin: []ViaJoin{{Parent: "order_id", Via: "order_id"}}, ChildJoin: []ViaJoin{{Via: "product_id", Child: "product_id"}}, Columns: []string{"quantity"}},
		}},
		FieldMappings: []FieldMapping{
			{ID: "m1", TargetFieldID: "f_id", Sources: []SourceRef{{Column: "order_id"}}, State: StateActive},
			{ID: "m2", TargetFieldID: "f_sku", Sources: []SourceRef{{Relation: "rel_p", Column: "sku"}}, State: StateActive},
			{ID: "m3", TargetFieldID: "f_qty", Sources: []SourceRef{{Relation: "rel_p", Column: "quantity", From: FromVia}}, State: StateActive},
		},
	}
	s.Normalize()
	return s
}

func TestSpecValidate_AC5_ManyToManyErrors(t *testing.T) {
	s := m2mValidateSpec()
	require.Empty(t, relationErrors(&s))

	s = m2mValidateSpec()
	s.Relations[0].Via.Columns = []string{"product_id"} // a join column
	errs := relationErrors(&s)
	require.Contains(t, codes(errs), CodeRelationViaInvalid)
	require.Contains(t, errs[0].Message, "rel_p", "the relation is named")

	s = m2mValidateSpec()
	s.Relations[0].Via.Columns = []string{"discount"} // not in the junction
	errs = relationErrors(&s)
	require.Contains(t, codes(errs), CodeRelationViaInvalid)
	for _, e := range errs {
		if e.Code == CodeRelationViaInvalid {
			require.Contains(t, e.Message, "rel_p")
			require.Contains(t, e.Message, "discount")
		}
	}

	s = m2mValidateSpec()
	s.Relations[0].Via.ChildJoin[0].Child = "sku" // int4 junction column vs text
	require.Contains(t, codes(relationErrors(&s)), CodeRelationViaInvalid)

	s = m2mValidateSpec()
	s.FieldMappings[2].Sources[0].Column = "line_no" // a junction column that isn't payload
	require.Contains(t, codes(relationErrors(&s)), CodeEmbedScope)
}

func TestSpecValidate_AC3_FilterCoercion(t *testing.T) {
	s := nestedSpec()
	s.Relations[0].Filter = []Filter{
		{Column: "status", Op: "eq", Value: "active"},
		{Column: "quantity", Op: "gt", Value: float64(0)},
		{Column: "placed", Op: "gte", Value: "2026-01-01"},
		{Column: "status", Op: "in", Value: []any{"a", "b"}},
		{Column: "status", Op: "notNull"},
	}
	require.Empty(t, relationErrors(&s))

	for _, bad := range []Filter{
		{Column: "quantity", Op: "eq", Value: "lots"},
		{Column: "quantity", Op: "eq", Value: 1.5},
		{Column: "placed", Op: "lt", Value: "yesterday"},
		{Column: "status", Op: "in", Value: "a"},
		{Column: "status", Op: "eq", Value: nil},
		{Column: "nope", Op: "eq", Value: "x"},
		{Column: "status", Op: "matches", Value: "x"},
	} {
		s = nestedSpec()
		s.Relations[0].Filter = []Filter{bad}
		require.Contains(t, codes(relationErrors(&s)), CodeRelationFilterValue, "%+v", bad)
	}
	args, err := FilterArgs(schema.Column{Name: "quantity", UDTName: "int4", JSONType: "integer"}, Filter{Op: "in", Value: []any{float64(1), "2"}})
	require.NoError(t, err)
	require.Equal(t, []string{"1", "2"}, args)
}
