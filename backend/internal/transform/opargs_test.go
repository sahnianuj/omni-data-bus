package transform

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// The same fixture drives web/src/features/mapping/opSchemas.test.ts.
func TestOpArgs_AC8_SharedFixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/op-args.json")
	require.NoError(t, err)
	var f struct {
		Cases []struct {
			Op    string         `json:"op"`
			Args  map[string]any `json:"args"`
			Valid bool           `json:"valid"`
		} `json:"cases"`
	}
	require.NoError(t, json.Unmarshal(raw, &f))
	covered := map[string]bool{}
	for _, c := range f.Cases {
		_, err := ops[c.Op].compile(c.Args, &compileEnv{sources: 2})
		require.Equal(t, c.Valid, err == nil, "%s %v: %v", c.Op, c.Args, err)
		covered[c.Op] = true
	}
	for _, op := range Ops() {
		require.True(t, covered[op], "op %s has no fixture case", op)
	}
}
