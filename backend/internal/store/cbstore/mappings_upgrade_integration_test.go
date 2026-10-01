//go:build integration

package cbstore

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/mapping"
	"omnidatabus/internal/store"
)

// TestMappingsRepo_AC3_UpgradeOnReadNoWrite (OC-037): a 1.0 mapping stored in Couchbase reads
// as-is (decoding upgrades it in memory) and fetching it twice leaves the document, its CAS and
// its version (the ETag) alone; saving it stores v2.
func TestMappingsRepo_AC3_UpgradeOnReadNoWrite(t *testing.T) {
	ctx := context.Background()
	s := shared
	ws := store.WorkspaceID("ws-ac3")
	p, err := s.Projects().Create(ctx, ws, store.NewProject{Name: "ac3-" + t.Name()})
	require.NoError(t, err)
	v1, err := os.ReadFile("../../mapping/testdata/specs/v1/customer_address_orders.json")
	require.NoError(t, err)
	m, err := s.Mappings().Create(ctx, ws, store.Mapping{
		ProjectID: p.ID, SourceSchema: "shop", SourceTable: "customer", Spec: v1, Status: "ready",
		Target: store.MappingTarget{Bucket: "sandbox", Scope: "s", Collection: "customer"},
	})
	require.NoError(t, err)
	_, cas0, err := mappings{s}.get(ctx, ws, m.ID)
	require.NoError(t, err)

	var read store.Mapping
	for range 2 {
		read, err = s.Mappings().Get(ctx, ws, m.ID)
		require.NoError(t, err)
		require.EqualValues(t, 1, read.Version)
		require.Equal(t, 1, mapping.VersionOf(read.Spec), "the stored spec isn't rewritten by a read")
		var spec mapping.Spec
		require.NoError(t, json.Unmarshal(read.Spec, &spec))
		require.Equal(t, mapping.SpecVersion, spec.SpecVersion, "…but it decodes as v2")
		require.Len(t, spec.Relations, 2)
	}
	_, cas1, err := mappings{s}.get(ctx, ws, m.ID)
	require.NoError(t, err)
	require.Equal(t, cas0, cas1, "no write happened")

	var spec mapping.Spec
	require.NoError(t, json.Unmarshal(read.Spec, &spec))
	read.Spec, _ = json.Marshal(spec)
	saved, err := s.Mappings().Update(ctx, ws, read)
	require.NoError(t, err)
	require.EqualValues(t, 2, saved.Version)
	got, err := s.Mappings().Get(ctx, ws, m.ID)
	require.NoError(t, err)
	require.Equal(t, mapping.SpecVersion, mapping.VersionOf(got.Spec))
	require.NotContains(t, string(got.Spec), `"embeds"`)
}
