package mapping

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The v1 fixtures are 1.0 specs as stored (customer_address_orders.json was saved by 1.0
// itself); v2/ holds what each upgrades to. The web client's upgradeSpec runs the same files.
// Regenerate v2/ with ODB_UPDATE_GOLDEN=1 and review the diff.
func v1Fixtures(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob("testdata/specs/v1/*.json")
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(files), 4)
	return files
}

func encode(t *testing.T, s Spec) []byte {
	t.Helper()
	b, err := json.MarshalIndent(s, "", "  ")
	require.NoError(t, err)
	return append(b, '\n')
}

func TestUpgradeV1_AC1_Golden(t *testing.T) {
	for _, f := range v1Fixtures(t) {
		t.Run(filepath.Base(f), func(t *testing.T) {
			raw, err := os.ReadFile(f)
			require.NoError(t, err)
			require.Equal(t, 1, VersionOf(raw))
			var s Spec
			require.NoError(t, json.Unmarshal(raw, &s))
			got := encode(t, s)
			golden := filepath.Join("testdata/specs/v2", filepath.Base(f))
			if os.Getenv("ODB_UPDATE_GOLDEN") != "" {
				require.NoError(t, os.WriteFile(golden, got, 0o644))
			}
			want, err := os.ReadFile(golden)
			require.NoError(t, err)
			require.Equal(t, string(want), string(got))
			require.Equal(t, SpecVersion, VersionOf(got))

			// Lossless: every embed is a root relation with the same fields, every {embed}
			// source is a {relation} source, nothing else changed.
			var v1 struct {
				Embeds []map[string]any `json:"embeds"`
				Maps   []struct {
					Sources []map[string]string `json:"sources"`
				} `json:"fieldMappings"`
			}
			require.NoError(t, json.Unmarshal(raw, &v1))
			require.Len(t, s.Relations, len(v1.Embeds))
			for i, e := range v1.Embeds {
				r := s.Relations[i]
				require.Nil(t, r.ParentID)
				require.Equal(t, e["id"], r.ID)
				require.Equal(t, e["kind"], r.Kind)
				require.Equal(t, e["targetFieldId"], r.TargetFieldID)
			}
			for i, m := range v1.Maps {
				for j, src := range m.Sources {
					require.Equal(t, src["embed"], s.FieldMappings[i].Sources[j].Relation)
					require.Equal(t, src["column"], s.FieldMappings[i].Sources[j].Column)
				}
			}
			require.NotContains(t, string(got), `"embeds"`)
			require.NotContains(t, string(got), `"embed"`)
		})
	}
}

func TestUpgradeV1_AC1_Idempotent(t *testing.T) {
	for _, f := range v1Fixtures(t) {
		raw, err := os.ReadFile(f)
		require.NoError(t, err)
		var once Spec
		require.NoError(t, json.Unmarshal(raw, &once))
		twice := UpgradeV1(UpgradeV1(once))
		require.Equal(t, encode(t, once), encode(t, twice), f)

		// A v2 spec decodes to itself: its relation tree is kept.
		var again Spec
		require.NoError(t, json.Unmarshal(encode(t, once), &again))
		require.Equal(t, encode(t, once), encode(t, again), f)
	}
	parent := "rel_items"
	v2 := Spec{SpecVersion: 2, Relations: []Relation{{ID: "rel_product", ParentID: &parent, Kind: EmbedManyToOne}}}
	require.Equal(t, "rel_items", UpgradeV1(v2).Relations[0].Parent(), "a v2 tree isn't flattened")
}

func TestSpec_VersionAndEmbedAlias(t *testing.T) {
	require.Equal(t, 1, (&Spec{}).Version())
	require.Equal(t, 1, VersionOf([]byte(`{"embeds":[]}`)))
	require.Equal(t, 1, VersionOf([]byte(`not json`)))
	// A 1.0 client sends {embed} in a v2 spec (an old browser tab): it's read as {relation}.
	var r SourceRef
	require.NoError(t, json.Unmarshal([]byte(`{"embed":"emb_1","column":"city"}`), &r))
	require.Equal(t, SourceRef{Relation: "emb_1", Column: "city"}, r)
	require.NoError(t, json.Unmarshal([]byte(`{"relation":"rel_1","embed":"emb_1","column":"c"}`), &r))
	require.Equal(t, "rel_1", r.Relation, "relation wins over the alias")
	b, _ := json.Marshal(SourceRef{Relation: "rel_1", Column: "c"})
	require.Equal(t, `{"relation":"rel_1","column":"c"}`, string(b))
	require.Error(t, json.Unmarshal([]byte(`{"fieldMappings":"x"}`), &Spec{}))
	require.Error(t, json.Unmarshal([]byte(`{"column":1}`), &r))
	require.True(t, strings.HasPrefix(string(encode(t, Spec{})), "{\n  \"specVersion\": 0"), "a zero Spec isn't normalized by encoding")
}
