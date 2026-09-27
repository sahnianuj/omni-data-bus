package verify

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCompare_AC3_CanonicalJSON(t *testing.T) {
	d, err := Compare([]byte(`{"a":1,"b":2}`), []byte(`{"b":2,"a":1}`))
	require.NoError(t, err)
	require.Empty(t, d, "key order doesn't matter")
	d, _ = Compare([]byte(`{"n":1.0,"m":12.50,"big":12345678901234567890,"e":1e2}`), []byte(`{"n":1,"m":12.5,"big":12345678901234567890,"e":100}`))
	require.Empty(t, d, "numbers compare by value")
	c1, _ := Canonical([]byte(`{"b":[1.50,{"z":true,"a":null}],"a":"x"}`))
	require.Equal(t, `{"a":"x","b":[1.5,{"a":null,"z":true}]}`, string(c1))

	d, _ = Compare([]byte(`{"total":12.5,"items":[{"sku":"A"}],"name":"x","gone":1}`), []byte(`{"total":12.51,"items":[{"sku":"B"}],"name":"x","extra":2}`))
	var paths []string
	for _, x := range d {
		paths = append(paths, x.Path)
	}
	require.Equal(t, []string{"$.extra", "$.gone", "$.items[0].sku", "$.total"}, paths)
	require.Equal(t, missingValue, d[0].Expected)
	d, _ = Compare([]byte(`{"a":[1,2]}`), []byte(`{"a":[1]}`))
	require.Len(t, d, 1, "different lengths: one diff at the array")
	d, _ = Compare([]byte(`{"a":{"b":1}}`), []byte(`{"a":"s"}`))
	require.Equal(t, "$.a", d[0].Path)
	_, err = Compare([]byte(`{`), []byte(`{}`))
	require.Error(t, err)
	_, err = Compare([]byte(`{}`), []byte(`nope`))
	require.Error(t, err)
	_, err = Canonical([]byte(`[`))
	require.Error(t, err)
}
