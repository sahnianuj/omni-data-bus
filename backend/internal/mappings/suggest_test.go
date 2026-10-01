package mappings

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/automap"
	"omnidatabus/internal/docschema"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/schema"
)

func TestSuggestBindings_Service(t *testing.T) {
	f := newFixture(t, true)
	withOrders(f)
	orders := &f.schemas.snap.Schemas[0].Tables[2]
	orders.ForeignKeys = []schema.ForeignKey{{Name: "orders_customer_fk", Columns: []string{"customer_id"}, RefSchema: "shop", RefTable: "customer", RefColumns: []string{"customer_id"}}}
	v, err := f.svc.Create(ctx, ws, "u1", f.project, CreateInput{SourceSchema: "shop", SourceTable: "customer"})
	require.NoError(t, err)

	// An unsaved composed design: the customer's orders[] with their totals.
	item := docschema.DocField{ID: "f_order", Name: "[]", Type: docschema.Object, Children: []docschema.DocField{{ID: "f_total", Name: "total", Type: docschema.Number}}}
	unsaved := v.Spec
	unsaved.Mode = mapping.ModeComposed
	unsaved.DocSchema = docschema.Schema{Source: docschema.SourceJSONSample, Root: docschema.DocField{ID: docschema.RootID, Name: "$", Type: docschema.Object, Children: []docschema.DocField{
		{ID: "f_first", Name: "firstName", Type: docschema.String},
		{ID: "f_orders", Name: "orders", Type: docschema.Array, Items: &item},
	}}}
	unsaved.FieldMappings = nil

	res, err := f.svc.SuggestBindings(ctx, ws, f.project, v.ID, SuggestInput{Root: true, SpecOverride: &unsaved})
	require.NoError(t, err)
	require.Len(t, res.Suggestions, 1)
	s := res.Suggestions[0]
	require.Equal(t, "f_orders", s.NodeID)
	require.Equal(t, automap.ConfidenceHigh, s.Confidence)
	require.Equal(t, mapping.TableRef{Schema: "shop", Name: "orders"}, *s.Candidate.Table)
	require.Equal(t, mapping.EmbedOneToMany, s.Candidate.Kind)
	require.Equal(t, []mapping.JoinPair{{Parent: "customer_id", Child: "customer_id"}}, s.Candidate.Join)
	require.Equal(t, "shop.customer", res.Root[0].Table.String())

	// Suggesting changes nothing.
	stored, err := f.svc.Get(ctx, ws, f.project, v.ID)
	require.NoError(t, err)
	require.Equal(t, v.Version, stored.Version)
}

func TestPreview_AC5_Conformance(t *testing.T) {
	f := newFixture(t, true)
	f.src.PreviewRows = customerRows()
	v := f.create(t, "customer")
	plain, err := f.svc.Preview(ctx, ws, f.project, v.ID, PreviewInput{})
	require.NoError(t, err)
	require.Nil(t, plain.Items[0].Conformance, "no imported design, no conformance")

	// The customer's design: the generated shape, but customerId as a string and a phone.
	var doc map[string]any
	require.NoError(t, json.Unmarshal(plain.Items[0].Document, &doc))
	doc["customerId"], doc["phone"] = "1", "555"
	sample, _ := json.Marshal(doc)
	ds, _, err := docschema.Import(docschema.KindJSONSample, string(sample), "")
	require.NoError(t, err)
	unsaved := v.Spec
	unsaved.DocSchema.Imported = ds.Imported
	res, err := f.svc.Preview(ctx, ws, f.project, v.ID, PreviewInput{SpecOverride: &unsaved})
	require.NoError(t, err)
	c := res.Items[0].Conformance
	require.NotNil(t, c)
	require.Equal(t, []string{"phone"}, c.Missing)
	require.Equal(t, []string{"customerId"}, c.TypeDiff)
	require.Empty(t, c.Extra)
}
