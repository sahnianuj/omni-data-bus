package docschema

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func f(id, name string, t FieldType, kids ...DocField) DocField {
	return DocField{ID: id, Name: name, Type: t, Children: kids}
}

func arrOf(id, name string, item DocField) DocField {
	item.Name = "[]"
	return DocField{ID: id, Name: name, Type: Array, Items: &item}
}

// current: the mapped order design (IDs c_…); items is bound.
func currentOrder() Schema {
	items := arrOf("c_items", "items", f("c_item", "[]", Object, f("c_sku", "sku", String), f("c_qty", "qty", Integer), f("c_disc", "discount", Number)))
	items.Binding = &Binding{RelationID: "rel_items"}
	return Schema{Source: SourceJSONSample, Root: f(RootID, "$", Object,
		f("c_oid", "orderId", Integer), f("c_cust", "custName", String), f("c_zip", "zip", String), items,
	)}
}

func imported(t *testing.T, sample string) Schema {
	t.Helper()
	s, _, err := Import(KindJSONSample, sample, "rev2.json")
	require.NoError(t, err)
	return s
}

func TestReconcile_AC1_KeepsIDs(t *testing.T) {
	cur := currentOrder()
	// Revision: orderId now a string, a new currency field; everything else as before.
	inc := imported(t, `{"orderId":"A-1","custName":"Ada","zip":"94000","items":[{"sku":"A","qty":2,"discount":0.5}],"currency":"EUR"}`)
	out, diff := Reconcile(&cur, &inc, nil)
	require.Empty(t, Validate(&out))
	byID := map[string]string{}
	out.Walk(func(f *DocField, p string, _ *DocField) { byID[p] = f.ID })
	for path, id := range map[string]string{"orderId": "c_oid", "custName": "c_cust", "zip": "c_zip", "items": "c_items", "items[]": "c_item", "items[].sku": "c_sku", "items[].qty": "c_qty"} {
		require.Equal(t, id, byID[path], path)
	}
	require.NotEqual(t, "", byID["currency"])
	require.Equal(t, []DiffField{{FieldID: byID["currency"], Path: "currency", Type: String}}, diff.Added)
	require.Empty(t, diff.Removed)
	require.Equal(t, []DiffType{{FieldID: "c_oid", Path: "orderId", From: Integer, To: String}}, diff.TypeChanged)
	require.Equal(t, String, out.Find("c_oid").Type)
	require.Equal(t, &Binding{RelationID: "rel_items"}, out.Find("c_items").Binding, "bindings stay")
	// The revision's record, reference and examples win.
	require.Equal(t, "rev2.json", out.Imported.FileName)
	require.JSONEq(t, `"A-1"`, string(out.Find("c_oid").Example))
	require.NotNil(t, out.Reference)
	require.Equal(t, RootID, out.Root.ID)
}

func TestReconcile_AC2_RenameMove(t *testing.T) {
	cur := currentOrder()
	// custName → customerName (rename); zip → shipping.zip (move); discount removed.
	inc := imported(t, `{"orderId":1,"customerName":"Ada","shipping":{"zip":"94000"},"items":[{"sku":"A","qty":2}]}`)
	out, diff := Reconcile(&cur, &inc, nil)
	require.Equal(t, []DiffChange{{FieldID: "c_cust", From: "custName", To: "customerName"}}, diff.Renamed)
	require.Equal(t, []DiffChange{{FieldID: "c_zip", From: "zip", To: "shipping.zip"}}, diff.Moved)
	require.Equal(t, []DiffField{{FieldID: "c_disc", Path: "items[].discount", Type: Number}}, diff.Removed)
	require.Len(t, diff.Added, 1)
	require.Equal(t, "shipping", diff.Added[0].Path)
	require.Equal(t, "customerName", out.Find("c_cust").Name)
	require.Nil(t, out.Find("c_disc"))

	// Declined: the rename becomes a removal and an addition.
	out2, diff2 := Reconcile(&cur, &inc, []string{"c_cust"})
	require.Empty(t, diff2.Renamed)
	require.Nil(t, out2.Find("c_cust"))
	require.Contains(t, diff2.Removed, DiffField{FieldID: "c_cust", Path: "custName", Type: String})
	var added []string
	for _, a := range diff2.Added {
		added = append(added, a.Path)
	}
	require.Contains(t, added, "customerName")
}

func TestReconcile_SameExampleIsARename(t *testing.T) {
	cur := Schema{Root: f(RootID, "$", Object, DocField{ID: "c_q", Name: "qty", Type: Integer, Example: json.RawMessage(`7`)})}
	inc := Schema{Root: f(RootID, "$", Object, DocField{ID: "n_q", Name: "quantity", Type: Integer, Example: json.RawMessage(`7`)})}
	_, diff := Reconcile(&cur, &inc, nil)
	require.Equal(t, []DiffChange{{FieldID: "c_q", From: "qty", To: "quantity"}}, diff.Renamed)
}

func TestNameSimilarity(t *testing.T) {
	require.InDelta(t, 1, nameSimilarity("custName", "customerName"), 0.001)
	require.InDelta(t, 1, nameSimilarity("order_items", "orderItem"), 0.001)
	require.Less(t, nameSimilarity("firstName", "lastName"), RenameSimilarity)
	require.Less(t, nameSimilarity("zip", "zipCode"), RenameSimilarity)
}

func TestReconcile_ConstantHintsFollowIDs(t *testing.T) {
	cur := Schema{Root: f(RootID, "$", Object, f("c_type", "type", String))}
	inc := imported(t, `{"type":"order"}`)
	out, _ := Reconcile(&cur, &inc, nil)
	require.Equal(t, "c_type", out.ConstantHints[0].FieldID)
}
