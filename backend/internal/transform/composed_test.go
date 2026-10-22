package transform

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/docschema"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/schema"
)

// composedTables: orders (driving) → customers, order_items → products (OC-047 fixtures).
func composedTables() (*schema.Table, map[string]*schema.Table) {
	orders := &schema.Table{Name: "orders", PrimaryKey: []string{"order_id"}, Columns: []schema.Column{
		col("order_id", "int8", schema.TypeInteger), col("order_no", "text", schema.TypeString), col("customer_id", "int4", schema.TypeInteger),
		col("ship_street", "text", schema.TypeString), col("ship_city", "text", schema.TypeString),
		col("home_phone", "text", schema.TypeString), col("mobile_phone", "text", schema.TypeString),
	}}
	customers := &schema.Table{Name: "customers", Columns: []schema.Column{col("customer_id", "int4", schema.TypeInteger), col("name", "text", schema.TypeString)}}
	items := &schema.Table{Name: "order_items", Columns: []schema.Column{
		col("order_id", "int8", schema.TypeInteger), col("line_no", "int4", schema.TypeInteger),
		col("product_id", "int4", schema.TypeInteger), col("quantity", "int4", schema.TypeInteger),
	}}
	products := &schema.Table{Name: "products", Columns: []schema.Column{
		col("product_id", "int4", schema.TypeInteger), col("sku", "text", schema.TypeString), col("name", "text", schema.TypeString),
	}}
	return orders, map[string]*schema.Table{"rel_cust": customers, "rel_first": items, "rel_items": items, "rel_prod": products, "rel_buyers": customers}
}

func fieldOf(id, name string, t docschema.FieldType, kids ...docschema.DocField) docschema.DocField {
	return docschema.DocField{ID: id, Name: name, Type: t, Children: kids}
}

func arrayOf(id, name string, item docschema.DocField) docschema.DocField {
	return docschema.DocField{ID: id, Name: name, Type: docschema.Array, Items: &item}
}

func active(id, field string, srcs ...mapping.SourceRef) mapping.FieldMapping {
	return mapping.FieldMapping{ID: id, TargetFieldID: field, Sources: srcs, State: mapping.StateActive}
}

func src(rel, column string) mapping.SourceRef {
	return mapping.SourceRef{Relation: rel, Column: column}
}

// composedSpec is one row of each binding kind (the OC-047 bindings table).
func composedSpec() *mapping.Spec {
	adapter := func(f docschema.DocField, a string) docschema.DocField {
		f.Binding = &docschema.Binding{Adapter: a}
		return f
	}
	join := func(p, c string) []mapping.JoinPair { return []mapping.JoinPair{{Parent: p, Child: c}} }
	items := mapping.TableRef{Schema: "public", Name: "order_items"}
	customers := mapping.TableRef{Schema: "public", Name: "customers"}
	spec := &mapping.Spec{
		Mode: mapping.ModeComposed,
		Root: &mapping.Root{Table: mapping.TableRef{Schema: "public", Name: "orders"}},
		DocSchema: docschema.Schema{Root: fieldOf(docschema.RootID, "$", docschema.Object,
			fieldOf("f_no", "orderNo", docschema.String),
			// object ← many-to-one
			fieldOf("f_customer", "customer", docschema.Object, fieldOf("f_cname", "name", docschema.String)),
			// object ← one-to-many with the first adapter
			adapter(fieldOf("f_first", "firstLine", docschema.Object, fieldOf("f_first_qty", "quantity", docschema.Integer)), docschema.AdapterFirst),
			// array ← one-to-many, with a nested many-to-one object
			arrayOf("f_items", "items", fieldOf("f_item", "[]", docschema.Object,
				fieldOf("f_line", "lineNo", docschema.Integer),
				fieldOf("f_product", "product", docschema.Object, fieldOf("f_sku", "sku", docschema.String)),
			)),
			// array ← many-to-one with the wrap adapter
			adapter(arrayOf("f_buyers", "buyers", fieldOf("f_buyer", "[]", docschema.Object, fieldOf("f_bname", "name", docschema.String))), docschema.AdapterWrap),
			// unbound object: grouping from the root row
			fieldOf("f_ship", "shipping", docschema.Object, fieldOf("f_street", "street", docschema.String), fieldOf("f_city", "city", docschema.String)),
			// unbound array: a literal array of root columns
			arrayOf("f_phones", "phones", fieldOf("f_phone", "[]", docschema.String)),
		)},
		Key: mapping.KeySpec{Template: "order::{order_id}"},
		Relations: []mapping.Relation{
			{ID: "rel_cust", Kind: mapping.EmbedManyToOne, Table: customers, Join: join("customer_id", "customer_id"), TargetFieldID: "f_customer"},
			{ID: "rel_first", Kind: mapping.EmbedOneToMany, Table: items, Join: join("order_id", "order_id"), OrderBy: []mapping.OrderColumn{{Column: "line_no"}}, TargetFieldID: "f_first"},
			{ID: "rel_items", Kind: mapping.EmbedOneToMany, Table: items, Join: join("order_id", "order_id"), OrderBy: []mapping.OrderColumn{{Column: "line_no"}}, TargetFieldID: "f_items"},
			{ID: "rel_prod", ParentID: sp("rel_items"), Kind: mapping.EmbedManyToOne, Table: mapping.TableRef{Schema: "public", Name: "products"}, Join: join("product_id", "product_id"), TargetFieldID: "f_product"},
			{ID: "rel_buyers", Kind: mapping.EmbedManyToOne, Table: customers, Join: join("customer_id", "customer_id"), TargetFieldID: "f_buyers"},
		},
		FieldMappings: []mapping.FieldMapping{
			active("m_no", "f_no", src("", "order_no")),
			active("m_cname", "f_cname", src("rel_cust", "name")),
			active("m_first_qty", "f_first_qty", src("rel_first", "quantity")),
			active("m_line", "f_line", src("rel_items", "line_no")),
			active("m_sku", "f_sku", src("rel_prod", "sku")),
			active("m_bname", "f_bname", src("rel_buyers", "name")),
			active("m_street", "f_street", src("", "ship_street")),
			active("m_city", "f_city", src("", "ship_city")),
		},
	}
	phones := active("m_phones", "f_phones", src("", "home_phone"), src("", "mobile_phone"))
	phones.Transforms = []mapping.Step{{Op: "array", Args: map[string]any{"dropNulls": true}}}
	spec.FieldMappings = append(spec.FieldMappings, phones)
	spec.Normalize()
	return spec
}

// order1001Composed is order SO-1001's row and related rows: its customer, 3 lines (each with
// its product), and no mobile phone.
func order1001Composed() ([]any, source.Related) {
	product := func(id int32, sku string) source.Related {
		return source.Related{"rel_prod": {Rows: []source.RelatedRow{{Values: []any{id, sku, "P" + sku}}}}}
	}
	lines := func() *source.RelatedSet {
		return &source.RelatedSet{Rows: []source.RelatedRow{
			{Values: []any{int64(1001), int32(1), int32(7), int32(2)}, Sub: product(7, "A-1")},
			{Values: []any{int64(1001), int32(2), int32(9), int32(1)}, Sub: product(9, "B-2")},
			{Values: []any{int64(1001), int32(3), int32(7), int32(5)}, Sub: product(7, "A-1")},
		}}
	}
	ada := func() *source.RelatedSet {
		return &source.RelatedSet{Rows: []source.RelatedRow{{Values: []any{int32(42), "Ada"}}}}
	}
	return []any{int64(1001), "SO-1001", int32(42), "1 Main St", "Springfield", "555-1", nil},
		source.Related{"rel_cust": ada(), "rel_first": lines(), "rel_items": lines(), "rel_buyers": ada()}
}

func compileComposed(t *testing.T, spec *mapping.Spec) *Program {
	t.Helper()
	orders, rel := composedTables()
	p, issues := Compile(spec, orders, Options{Related: rel})
	require.NotNil(t, p, "%v", issues)
	return p
}

func TestComposed_AC1_BindingMatrix(t *testing.T) {
	p := compileComposed(t, composedSpec())
	row, rel := order1001Composed()
	key, doc, warnings, rerr := p.ApplyWith(row, rel, nil)
	require.Nil(t, rerr)
	require.Equal(t, "order::1001", key)

	raw, err := os.ReadFile("testdata/golden/composed_binding_matrix.json")
	require.NoError(t, err)
	var golden map[string]map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &golden))
	var got map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(doc, &got))
	for row, want := range golden {
		for field, value := range want {
			require.JSONEq(t, string(value), string(got[field]), "%s: %s", row, field)
		}
	}

	// The first adapter says it dropped rows (the preview shows it, OC-049).
	require.Len(t, warnings, 1)
	require.Equal(t, CodeAdapterDiscardsRows, warnings[0].Code)
	require.Equal(t, "firstLine", warnings[0].FieldPath)
	require.Contains(t, warnings[0].Message, "first of 3 rows")

	// Without related rows: the object is null, the wrapped array empty.
	delete(rel, "rel_cust")
	delete(rel, "rel_buyers")
	_, doc, _, rerr = p.ApplyWith(row, rel, nil)
	require.Nil(t, rerr)
	require.Contains(t, string(doc), `"customer":null`)
	require.Contains(t, string(doc), `"buyers":[]`)
}

func TestComposed_AC1_ObjectOnManyNeedsAdapter(t *testing.T) {
	spec := composedSpec()
	spec.DocSchema.Find("f_first").Binding = nil // an object bound to one-to-many
	orders, rel := composedTables()
	issues := mapping.ValidateRelations(spec, orders, func(_, name string) *schema.Table {
		for _, tbl := range rel {
			if tbl.Name == name {
				return tbl
			}
		}
		return nil
	})
	var found *mapping.Issue
	for i := range issues {
		if issues[i].Code == mapping.CodeBindingKindMismatch {
			found = &issues[i]
		}
	}
	require.NotNil(t, found, "%v", issues)
	require.Equal(t, "firstLine", found.Path)
	require.Contains(t, found.Message, "use the first adapter")
}

func TestComposed_AC3_GroupingAndLiteralArrays(t *testing.T) {
	p := compileComposed(t, composedSpec())
	row, rel := order1001Composed()
	_, doc, _, rerr := p.ApplyWith(row, rel, nil)
	require.Nil(t, rerr)
	require.Contains(t, string(doc), `"shipping":{"street":"1 Main St","city":"Springfield"}`, "grouping reads the root row")
	require.Contains(t, string(doc), `"phones":["555-1"]`, "dropNulls leaves the null mobile out")

	spec := composedSpec()
	for i := range spec.FieldMappings {
		if spec.FieldMappings[i].ID == "m_phones" {
			spec.FieldMappings[i].Transforms[0].Args = map[string]any{}
		}
	}
	p = compileComposed(t, spec)
	_, doc, _, rerr = p.ApplyWith(row, rel, nil)
	require.Nil(t, rerr)
	require.Contains(t, string(doc), `"phones":["555-1",null]`)
}

func TestComposed_AC6_CrossScopeExpression(t *testing.T) {
	spec := composedSpec()
	item := spec.DocSchema.Find("f_item")
	item.Children = append(item.Children, fieldOf("f_ref", "ref", docschema.String))
	prod := spec.DocSchema.Find("f_product")
	prod.Children = append(prod.Children, fieldOf("f_tag", "tag", docschema.String))
	ref := active("m_ref", "f_ref")
	ref.Transforms = []mapping.Step{{Op: "expression", Args: map[string]any{"expr": `concat($root.order_no, "-", line_no)`}}}
	// Three levels: the root, the item (by its field's name) and the product's own column.
	tag := active("m_tag", "f_tag")
	tag.Transforms = []mapping.Step{{Op: "expression", Args: map[string]any{"expr": `concat($root.order_no, "/", items.line_no, "/", sku, "/", $parent.quantity)`}}}
	spec.FieldMappings = append(spec.FieldMappings, ref, tag)
	p := compileComposed(t, spec)
	row, rel := order1001Composed()
	_, doc, _, rerr := p.ApplyWith(row, rel, nil)
	require.Nil(t, rerr)
	for _, want := range []string{`"ref":"SO-1001-1"`, `"ref":"SO-1001-2"`, `"ref":"SO-1001-3"`, `"tag":"SO-1001/2/B-2/1"`} {
		require.Contains(t, string(doc), want)
	}

	// A root-level field reading products: out of scope, reported at the expression's position.
	bad := active("m_bad", "f_no")
	bad.Transforms = []mapping.Step{{Op: "expression", Args: map[string]any{"expr": `concat(order_no, products.sku)`}}}
	spec = composedSpec()
	spec.FieldMappings[0] = bad
	orders, rel2 := composedTables()
	prog, issues := Compile(spec, orders, Options{Related: rel2})
	require.Nil(t, prog)
	require.Len(t, issues, 1)
	require.Equal(t, mapping.CodeSourceOutOfScope, issues[0].Code)
	require.Equal(t, 1, issues[0].Line)
	require.Equal(t, 18, issues[0].Column, "at products")
	require.Contains(t, issues[0].Message, "products isn't bound on the path to this field")

	// A typo in a relation's column is caught at compile time too.
	bad.Transforms[0].Args["expr"] = `$root.order_nr`
	spec.FieldMappings[0] = bad
	_, issues = Compile(spec, orders, Options{Related: rel2})
	require.Contains(t, issues[0].Message, `did you mean "order_no"?`)
}

// Text joined to a number with + compiles in expr and fails on every row; it's reported at
// compile time with the concat that works.
func TestComposed_ExpressionTextPlus(t *testing.T) {
	issuesFor := func(e string) []mapping.Issue {
		spec := composedSpec()
		m := active("m_x", "f_no")
		m.Transforms = []mapping.Step{{Op: "expression", Args: map[string]any{"expr": e}}}
		spec.FieldMappings[0] = m
		orders, rel := composedTables()
		_, issues := Compile(spec, orders, Options{Related: rel})
		return issues
	}
	for _, c := range []struct {
		expr, want string
		column     int
	}{
		{`"paid_in_out::" + $root.order_id`, `+ joins text only with text, and $root.order_id is an integer; use concat("paid_in_out::", $root.order_id) to join them`, 19},
		{`"SO-" + order_id + "/" + customer_id`, `use concat("SO-", order_id, "/", customer_id)`, 9},
		{`"SO-" + order_no`, `+ can't join text with the column order_no; use concat("SO-", order_no)`, 9},
	} {
		issues := issuesFor(c.expr)
		require.Len(t, issues, 1, c.expr)
		require.Contains(t, issues[0].Message, c.want, c.expr)
		require.Equal(t, c.column, issues[0].Column, c.expr)
	}
	for _, ok := range []string{`concat("paid_in_out::", $root.order_id)`, `$root.order_id + 1`, `upper(order_no) + "-x"`} {
		require.Empty(t, issuesFor(ok), ok)
	}
}

func TestComposed_ExpressionNames(t *testing.T) {
	spec := composedSpec()
	orders, rel := composedTables()
	// Inside items[].product: the product's relation and the item's, by id, field and table name.
	require.Equal(t, []string{"$root", "$parent", "items", "order_items", "product", "products", "rel_items", "rel_prod"},
		ExpressionNames(spec, "f_sku", rel, orders))
}
