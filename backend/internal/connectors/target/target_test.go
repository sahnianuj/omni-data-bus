package target_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors/target"
	"omnidatabus/internal/connectors/target/couchbase"
	"omnidatabus/internal/connectors/target/directory"
	"omnidatabus/internal/problem"
)

func TestNewRegistry_IsEmptyTargetRegistry(t *testing.T) {
	r := target.NewRegistry()
	require.Empty(t, r.Descriptors())
	_, err := r.Get("couchbase")
	require.ErrorContains(t, err, `unsupported target type "couchbase"`)
	require.Equal(t, problem.CodeTargetKindUnknown, problem.From(err).Code)
}

func TestTargetRegistry_AC1_KindsAndCapabilities(t *testing.T) {
	r := target.NewRegistry()
	r.Register(couchbase.New())
	r.Register(directory.New([]string{}))
	kinds := target.Kinds(r)
	require.Len(t, kinds, 2)
	byType := map[string]target.KindInfo{}
	for _, k := range kinds {
		byType[k.Type] = k
	}

	cb := byType["couchbase"]
	require.True(t, cb.Enabled)
	require.Equal(t, target.Capabilities{
		SupportsDurability: true, SupportsExpiry: true, SupportsCreateLocation: true, RequiresCredentials: true,
		MaxDocBytes: 20 << 20, LocationLevels: []string{"bucket", "scope", "collection"},
	}, cb.Capabilities)

	dir := byType["directory"]
	require.Equal(t, "Directory of JSON files", dir.DisplayName)
	require.False(t, dir.Capabilities.SupportsDurability)
	require.False(t, dir.Capabilities.SupportsExpiry)
	require.False(t, dir.Capabilities.RequiresCredentials)
	require.Equal(t, []string{"folder"}, dir.Capabilities.LocationLevels)
	// Without allowed roots the kind is listed but switched off, with a reason (OC-038).
	require.False(t, dir.Enabled)
	require.Equal(t, directory.DisabledReason, dir.DisabledReason)
	require.True(t, target.Info(directory.New([]string{"/data/odb-out"})).Enabled)
}
