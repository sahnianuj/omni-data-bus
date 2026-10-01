package mapping

import (
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/docschema"
	"omnidatabus/internal/schema"
)

func TestDefaultKeyTemplate_SameForEveryTargetKind(t *testing.T) {
	// A directory target uses the same key as Couchbase: <table>::{pk…} (the file name is the
	// encoded key, order::1001.json), so a mapping builds the same keys whatever the target.
	orders := &schema.Table{Name: "orders", PrimaryKey: []string{"order_id"}}
	tpl, _ := DefaultKeyTemplate(orders)
	require.Equal(t, "orders::{order_id}", tpl)
	lines := &schema.Table{Name: "order_lines", PrimaryKey: []string{"order_id", "line_no"}}
	tpl, _ = DefaultKeyTemplate(lines)
	require.Equal(t, "order_lines::{order_id}::{line_no}", tpl)
	tpl, codes := DefaultKeyTemplate(&schema.Table{Name: "page_view"})
	require.Equal(t, "{$uuid}", tpl)
	require.Contains(t, codes, CodeKeyNonIdempotent)
}

func TestValidate_AC6_CaseCollisionRisk(t *testing.T) {
	tbl := &schema.Table{Name: "product", PrimaryKey: []string{"sku"}, Columns: []schema.Column{
		{Name: "sku", UDTName: "varchar", JSONType: schema.TypeString},
		{Name: "id", UDTName: "int4", JSONType: schema.TypeInteger},
	}}
	spec := &Spec{DocSchema: docschema.Schema{Root: docschema.NewRoot()}, Key: KeySpec{Template: "{sku}"}}
	spec.Normalize()
	has := func(is []Issue) bool {
		for _, i := range is {
			if i.Code == CodeKeyCaseCollisionRisk {
				return true
			}
		}
		return false
	}
	require.True(t, has(Validate(spec, Context{Table: tbl, CaseInsensitive: true})))
	require.False(t, has(Validate(spec, Context{Table: tbl})), "a case-sensitive target is fine")
	spec.Key.Template = "{id}"
	require.False(t, has(Validate(spec, Context{Table: tbl, CaseInsensitive: true})), "integer keys can't collide by case")
}
