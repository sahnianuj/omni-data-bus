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
