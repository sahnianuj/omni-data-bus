package docschema

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConform(t *testing.T) {
	s, _, err := Import(KindJSONSample, `{"orderId":1,"placed":"2024-03-01T10:00:00Z","total":9.5,"items":[{"sku":"A","qty":2}],"customer":{"name":"Ada"}}`, "")
	require.NoError(t, err)
	fields := s.Imported.Fields

	ok := Conform(fields, json.RawMessage(`{"orderId":7,"placed":"2024-05-01","total":3,"items":[{"sku":"B","qty":1}],"customer":{"name":null}}`))
	require.True(t, ok.Empty(), "%+v", ok)

	bad := Conform(fields, json.RawMessage(`{"orderId":"7","total":3,"items":[{"sku":5,"qty":1,"note":"x"}],"customer":{"name":"B"},"extra":{"a":1}}`))
	require.Equal(t, []string{"placed"}, bad.Missing)
	require.Equal(t, []string{"orderId", "items[].sku"}, bad.TypeDiff)
	require.Equal(t, []string{"extra", "items[].note"}, bad.Extra) // extra.a is reported through extra

	// An empty array says nothing about its items.
	empty := Conform(fields, json.RawMessage(`{"orderId":1,"placed":"x","total":1,"items":[],"customer":{"name":"A"}}`))
	require.True(t, empty.Empty(), "%+v", empty)
}

// OC-166 AC3: a value the design's enum doesn't allow is reported; null and allowed values aren't.
func TestConform_OC166_AC3_Enum(t *testing.T) {
	q := func(s string) json.RawMessage { return json.RawMessage(`"` + s + `"`) }
	fields := []ContractField{
		{Path: "consumptionType", Type: String, Enum: []json.RawMessage{q("ConsumeInStore"), q("ConsumeOutOfStore")}},
		{Path: "items", Type: Array},
		{Path: "items[]", Type: Object},
		{Path: "items[].size", Type: String, Enum: []json.RawMessage{q("TL"), q("GR")}},
		{Path: "tier", Type: Integer, Enum: []json.RawMessage{json.RawMessage("1"), json.RawMessage("2")}},
	}
	c := Conform(fields, json.RawMessage(`{"consumptionType":"Other","items":[{"size":"GR"},{"size":"XX"}],"tier":2}`))
	require.Equal(t, []string{"consumptionType", "items[].size"}, c.EnumValues)
	require.False(t, c.Empty())
	ok := Conform(fields, json.RawMessage(`{"consumptionType":null,"items":[{"size":"TL"}],"tier":1}`))
	require.Empty(t, ok.EnumValues)
	require.True(t, ok.Empty())
}
