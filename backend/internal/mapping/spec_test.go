package mapping

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/docschema"
)

func TestSpec_AC3_ReferenceRoundTrip(t *testing.T) {
	ds, _, err := docschema.Import(docschema.KindJSONSample, `{"type":"order","id":1}`, "o.json")
	require.NoError(t, err)
	in := Spec{SpecVersion: 2, DocSchema: ds}
	b, err := json.Marshal(in)
	require.NoError(t, err)
	var out Spec
	require.NoError(t, json.Unmarshal(b, &out))
	require.JSONEq(t, string(ds.Reference), string(out.DocSchema.Reference))
	require.Equal(t, ds.Imported.FileName, out.DocSchema.Imported.FileName)
	require.Equal(t, ds.ConstantHints, out.DocSchema.ConstantHints)
	require.JSONEq(t, `"order"`, string(out.DocSchema.Root.Children[0].Example))
}
