package docschema_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	. "omnidatabus/internal/docschema" //nolint:revive // reads like the package's own tests
	"omnidatabus/internal/fixtures/pos"
)

// OC-166 AC1: the hints of the cart design.
func TestHints_AC1_CartDesign(t *testing.T) {
	ds, _, err := Import(KindJSONSchema, string(pos.Design("cart.schema.json")), "cart.schema.json")
	require.NoError(t, err)
	by := map[string]*DocField{}
	ds.Walk(func(f *DocField, p string, _ *DocField) { by[p] = f })
	require.Equal(t, []string{"saved_orders.saved_at"}, ParseHints(by["savedAt"].Description).Sources)
	require.True(t, ParseHints(by["receipt"].Description).NotPopulated)
	require.True(t, ParseHints(by["items[].priceOverrideReason"].Description).NotPopulated)
	require.False(t, ParseHints(by["status"].Description).NotPopulated)
	require.Equal(t, "cart::{uuid}", ParseHints(ds.Root.Description).KeyTemplate)
	require.Equal(t, "1", ParseHints(by["items[].quantity"].Description).Always)
	require.Equal(t, "sum", ParseHints(by["pricing.tipInCents"].Description).Aggregate)
	require.Len(t, by["status"].Enum, 7)
	require.Len(t, by["orderCollectionType"].Enum, 4, "null in an enum makes the field nullable, not a value")
	require.False(t, by["orderCollectionType"].Required)
}

// OC-171 AC1: the cart design and its payment designs, imported together: payments[] gets one
// variant per file, told apart by paymentType.
func TestImportBundle_OC171_AC1_CrossFileRef(t *testing.T) {
	files := []File{{Name: "cart.schema.json", Content: string(pos.Design("cart.schema.json"))}}
	for _, n := range pos.PaymentDesignNames() {
		files = append(files, File{Name: n, Content: string(pos.PaymentDesigns()[n])})
	}
	s, warnings, err := ImportFiles(files)
	require.NoError(t, err)
	require.Empty(t, warnings)
	by := map[string]*DocField{}
	s.Walk(func(f *DocField, p string, _ *DocField) { by[p] = f })
	items := by["payments[]"]
	require.True(t, items.HasVariants())
	var names []string
	for _, v := range items.Children {
		names = append(names, v.Name)
		require.Equal(t, "paymentType", v.Variant.Field)
	}
	require.ElementsMatch(t, []string{"CASH", "CREDIT_CARD", "SCAN_AND_PAY", "SCC", "SVC"}, names)

	// A relative $ref to another file of the import.
	ref := []File{
		{Name: "order.json", Content: `{"type":"object","properties":{"customer":{"$ref":"defs/customer.json"}}}`},
		{Name: "defs/customer.json", Content: `{"type":"object","properties":{"name":{"type":"string"},"address":{"$ref":"#/$defs/addr"}},"$defs":{"addr":{"type":"object","properties":{"city":{"type":"string"}}}}}`},
		{Name: "unused.json", Content: `{"type":"object"}`},
	}
	s, warnings, err = ImportFiles(ref)
	require.NoError(t, err)
	by = map[string]*DocField{}
	s.Walk(func(f *DocField, p string, _ *DocField) { by[p] = f })
	require.NotNil(t, by["customer.address.city"], "local refs resolve in the referenced file")
	require.Equal(t, []string{"unused.json isn't referenced by the design and names no array of it; it wasn't used."}, warnings)
}
