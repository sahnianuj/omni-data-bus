package mapping

import (
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/docschema"
	"omnidatabus/internal/schema"
)

// referenceSpec: an order design imported from two {orderId, status, note?, items:[{sku}]} with
// orderId and status mapped, note unmapped, items[] bound; the tool added "internal".
func referenceSpec(t *testing.T) (*Spec, Context) {
	t.Helper()
	ds, _, err := docschema.Import(docschema.KindJSONSample, `{"orderId":1,"status":"paid","note":"x","items":[{"sku":"A"}]}`+"\n"+`{"orderId":2,"status":"open","items":[{"sku":"B"}]}`, "")
	require.NoError(t, err)
	byName := map[string]*docschema.DocField{}
	ds.Walk(func(f *docschema.DocField, p string, _ *docschema.DocField) { byName[p] = f })
	ds.Root.Children = append(ds.Root.Children, docschema.DocField{ID: "f_internal", Name: "internal", Type: docschema.String})
	s := &Spec{
		SpecVersion: 2, Mode: ModeComposed, DocSchema: ds, Key: KeySpec{Template: "order::{order_id}"},
		Relations: []Relation{{ID: "rel_items", Kind: EmbedOneToMany, Table: TableRef{Schema: "shop", Name: "order_items"}, Join: []JoinPair{{Parent: "order_id", Child: "order_id"}}, TargetFieldID: byName["items"].ID}},
		FieldMappings: []FieldMapping{
			{ID: "m1", TargetFieldID: byName["orderId"].ID, Sources: []SourceRef{{Column: "order_id"}}, Origin: OriginManual, State: StateActive},
			{ID: "m2", TargetFieldID: byName["status"].ID, Sources: []SourceRef{{Column: "status"}}, Origin: OriginManual, State: StateActive},
			{ID: "m3", TargetFieldID: byName["items[].sku"].ID, Sources: []SourceRef{{Relation: "rel_items", Column: "sku"}}, Origin: OriginManual, State: StateActive},
			{ID: "m4", TargetFieldID: "f_internal", Sources: []SourceRef{{Column: "status"}}, Origin: OriginManual, State: StateActive},
		},
	}
	s.Normalize()
	orders := &schema.Table{Name: "orders", PrimaryKey: []string{"order_id"}, Columns: []schema.Column{
		{Name: "order_id", JSONType: schema.TypeInteger, UDTName: "int8", IsPrimaryKey: true},
		{Name: "status", JSONType: schema.TypeString, UDTName: "text", Nullable: true},
	}}
	items := &schema.Table{Name: "order_items", Columns: []schema.Column{{Name: "order_id", JSONType: schema.TypeInteger, UDTName: "int8"}, {Name: "sku", JSONType: schema.TypeString, UDTName: "text"}}}
	return s, Context{Table: orders, Related: func(_, n string) *schema.Table {
		if n == "order_items" {
			return items
		}
		return nil
	}}
}

func refCodes(issues []Issue, prefix string) map[string]string {
	out := map[string]string{}
	for _, i := range issues {
		if len(i.Code) >= len(prefix) && i.Code[:len(prefix)] == prefix {
			out[i.Path+" "+i.Code] = i.Severity
		}
	}
	return out
}

func TestValidate_AC4_ReferenceStrict(t *testing.T) {
	s, ctx := referenceSpec(t)
	got := refCodes(Validate(s, ctx), "REFERENCE_")
	require.Equal(t, map[string]string{
		"note REFERENCE_FIELD_UNMAPPED":  SeverityInfo,    // an unfilled design field isn't a problem unless strict (OC-075)
		"internal REFERENCE_EXTRA_FIELD": SeverityInfo,    // an added field isn't a problem unless strict (OC-070)
		"status REFERENCE_NULLABLE":      SeverityWarning, // required in the sample, status is nullable
	}, got)
	require.True(t, Ready(Validate(s, ctx)), "not strict: nothing blocks")

	s.ReferenceStrict = true
	got = refCodes(Validate(s, ctx), "REFERENCE_")
	require.Equal(t, SeverityError, got["note REFERENCE_FIELD_UNMAPPED"])
	require.Equal(t, SeverityError, got["internal REFERENCE_EXTRA_FIELD"])
	require.Equal(t, SeverityWarning, got["status REFERENCE_NULLABLE"])
	require.False(t, Ready(Validate(s, ctx)), "strict: blocks migrations and exports")
}

func TestValidate_ReferenceTypeAndMissing(t *testing.T) {
	s, ctx := referenceSpec(t)
	// orderId retyped in the tool; note deleted; status defaulted.
	for i := range s.DocSchema.Root.Children {
		if s.DocSchema.Root.Children[i].Name == "orderId" {
			s.DocSchema.Root.Children[i].Type = docschema.String
		}
	}
	kids := s.DocSchema.Root.Children[:0]
	for _, c := range s.DocSchema.Root.Children {
		if c.Name != "note" {
			kids = append(kids, c)
		}
	}
	s.DocSchema.Root.Children = kids
	s.FieldMappings[1].Transforms = []Step{{Op: "default", Args: map[string]any{"value": "open"}}}
	got := refCodes(Validate(s, ctx), "REFERENCE_")
	require.Equal(t, SeverityError, got["orderId REFERENCE_TYPE_MISMATCH"])
	require.Equal(t, SeverityInfo, got["note REFERENCE_FIELD_UNMAPPED"])
	require.NotContains(t, got, "status REFERENCE_NULLABLE")
}

func TestValidate_NoReferenceWithoutImport(t *testing.T) {
	s, ctx := referenceSpec(t)
	s.DocSchema.Imported = nil
	require.Empty(t, refCodes(Validate(s, ctx), "REFERENCE_"))
}

func TestValidate_OC070_AC1_ExtraBranch(t *testing.T) {
	s, ctx := referenceSpec(t)
	s.DocSchema.Imported.FileName = "orders.json"
	// An embedded product in each item: one branch of two fields, not two issues.
	var items *docschema.DocField
	s.DocSchema.Walk(func(f *docschema.DocField, p string, _ *docschema.DocField) {
		if p == "items[]" {
			items = f
		}
	})
	items.Children = append(items.Children, docschema.DocField{ID: "f_prod", Name: "product", Type: docschema.Object, Children: []docschema.DocField{
		{ID: "f_sku2", Name: "sku", Type: docschema.String}, {ID: "f_title", Name: "title", Type: docschema.String},
	}})
	var extra []Issue
	for _, i := range Validate(s, ctx) {
		if i.Code == CodeReferenceExtraField {
			extra = append(extra, i)
		}
	}
	require.Len(t, extra, 2)
	require.Equal(t, "internal", extra[0].Path)
	require.Equal(t, "internal isn't in the design you imported from orders.json. Add it to the design to keep it, or remove it.", extra[0].Message)
	require.Equal(t, "items[].product", extra[1].Path)
	require.Equal(t, "f_prod", extra[1].FieldID)
	require.Equal(t, SeverityInfo, extra[1].Severity)
	require.Equal(t, "items[].product (2 fields) isn't in the design you imported from orders.json. Add it to the design to keep it, or remove it.", extra[1].Message)
	require.Equal(t, &Fix{Kind: "add-to-design", Label: "Add to design"}, extra[1].Fix)

	s.ReferenceStrict = true
	for _, i := range Validate(s, ctx) {
		if i.Code == CodeReferenceExtraField {
			require.Equal(t, SeverityError, i.Severity)
		}
	}
}

func TestValidate_OC071_AC1_RestoreAndMap(t *testing.T) {
	ds, _, err := docschema.Import(docschema.KindJSONSample, `{"orderId":1,"items":[{"lineNo":1,"productId":2}]}`, "")
	require.NoError(t, err)
	byPath := map[string]*docschema.DocField{}
	ds.Walk(func(f *docschema.DocField, p string, _ *docschema.DocField) { byPath[p] = f })
	item := byPath["items[]"]
	// productId was replaced by a product object (OC-064 "Replace").
	item.Children = []docschema.DocField{item.Children[0], {ID: "f_prod", Name: "product", Type: docschema.Object}}
	s := &Spec{
		SpecVersion: 2, Mode: ModeComposed, DocSchema: ds, Key: KeySpec{Template: "order::{order_id}"},
		Relations: []Relation{{ID: "rel_lines", Kind: EmbedOneToMany, Table: TableRef{Schema: "shop", Name: "order_items"}, Join: []JoinPair{{Parent: "order_id", Child: "order_id"}}, TargetFieldID: byPath["items"].ID}},
		FieldMappings: []FieldMapping{
			{ID: "m1", TargetFieldID: byPath["orderId"].ID, Sources: []SourceRef{{Column: "order_id"}}, Origin: OriginManual, State: StateActive},
			{ID: "m2", TargetFieldID: byPath["items[].lineNo"].ID, Sources: []SourceRef{{Relation: "rel_lines", Column: "line_no"}}, Origin: OriginManual, State: StateActive},
		},
	}
	s.Normalize()
	lines := &schema.Table{Name: "order_items", Columns: []schema.Column{
		{Name: "order_id", JSONType: schema.TypeInteger}, {Name: "line_no", JSONType: schema.TypeInteger}, {Name: "product_id", JSONType: schema.TypeInteger},
	}}
	ctx := Context{Table: &schema.Table{Name: "orders", Columns: []schema.Column{{Name: "order_id", JSONType: schema.TypeInteger}}}, Related: func(_, n string) *schema.Table {
		if n == "order_items" {
			return lines
		}
		return nil
	}}
	find := func() *Issue {
		for _, i := range Validate(s, ctx) {
			if i.Code == CodeReferenceFieldUnmapped && i.Path == "items[].productId" {
				return &i
			}
		}
		return nil
	}
	got := find()
	require.NotNil(t, got)
	require.Equal(t, &Fix{Kind: "restore-field", Label: "Add productId back (from product_id)", RelationID: "rel_lines", Columns: []string{"product_id"}}, got.Fix)
	require.Contains(t, got.Message, "Add it back")

	// Present but unmapped: map it.
	item.Children = append(item.Children, docschema.DocField{ID: "f_pid", Name: "productId", Type: docschema.Integer})
	got = find()
	require.NotNil(t, got)
	require.Equal(t, "f_pid", got.FieldID)
	require.Equal(t, &Fix{Kind: "map-field", Label: "Map from product_id", RelationID: "rel_lines", Columns: []string{"product_id"}}, got.Fix)
}
