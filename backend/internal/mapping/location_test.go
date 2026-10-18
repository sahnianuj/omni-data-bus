package mapping

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/problem"
	"omnidatabus/internal/store"
)

func TestLocation_AC4_FromV1Target(t *testing.T) {
	v1 := Target{Bucket: "shop", Scope: "sales", Collection: "orders", CreateIfMissing: true}
	l := LocationFromV1(v1)
	require.Equal(t, Location{Kind: "couchbase", Bucket: "shop", Scope: "sales", Collection: "orders", CreateIfMissing: true}, l)
	require.Equal(t, v1, V1Target(l))
	require.NoError(t, ValidateLocation(l, store.KindCouchbase))

	// A stored 1.0 mapping (target only) exposes location when read.
	var m store.Mapping
	require.NoError(t, json.Unmarshal([]byte(`{"id":"m1","target":{"bucket":"shop","scope":"sales","collection":"orders"}}`), &m))
	require.Equal(t, "couchbase", m.Where().Kind)
	require.Equal(t, "shop.sales.orders", m.Where().String())
	require.Equal(t, Target{}, V1Target(Location{Kind: "directory", Folder: "orders"}))
}

func TestLocation_AC4_Validate(t *testing.T) {
	code := func(err error) string { return problem.From(err).Code }
	field := func(err error) string { return problem.From(err).FieldErrors[0].Field }

	err := ValidateLocation(Location{Kind: "directory"}, store.KindDirectory)
	require.Equal(t, problem.CodeLocationInvalid, code(err))
	require.Equal(t, "location.folder", field(err))

	require.NoError(t, ValidateLocation(Location{Kind: "directory", Folder: "orders"}, store.KindDirectory))
	require.NoError(t, ValidateLocation(Location{Kind: "directory", Folder: "archive/2026/q1"}, ""))
	for _, bad := range []string{"../x", "/abs", "a/../b", "a//b", "a/b/c/d", "sp ace", `win\dir`, "x/./y", strings.Repeat("a", 65)} {
		require.Equal(t, problem.CodeLocationInvalid, code(ValidateLocation(Location{Kind: "directory", Folder: bad}, "")), bad)
	}

	err = ValidateLocation(Location{Kind: "couchbase", Bucket: "shop", Scope: "sales"}, store.KindCouchbase)
	require.Equal(t, "location.collection", field(err))
	err = ValidateLocation(Location{Kind: "directory", Folder: "orders"}, store.KindCouchbase)
	require.Equal(t, "location.kind", field(err), "a location must match the project's target kind")
	require.Equal(t, "location.kind", field(ValidateLocation(Location{Folder: "x"}, "")))
	require.Equal(t, "location.kind", field(ValidateLocation(Location{Kind: "s3"}, "")))
	require.Equal(t, "location.bucket", field(ValidateLocation(Location{Kind: "directory", Folder: "x", Bucket: "b"}, "")))
	require.Equal(t, "location.folder", field(ValidateLocation(Location{Kind: "couchbase", Bucket: "b", Scope: "s", Collection: "c", Folder: "x"}, "")))
}

// OC-186 AC3: folders Windows can't create are refused on every OS, so a test run on one
// machine writes the same folders as the run on another. Document file names are already
// encoded the same way everywhere (pathcodec, OC-039).
func TestValidateFolder_OC186_AC3_WindowsNames(t *testing.T) {
	for _, bad := range []string{"con", "CON", "orders/Nul", "aux.json", "com1", "lpt9/x", "orders.", "a/b."} {
		require.Error(t, ValidateFolder(bad), bad)
	}
	for _, ok := range []string{"orders", "console", "com10", "contacts.v2", "a/b.c", "lpt"} {
		require.NoError(t, ValidateFolder(ok), ok)
	}
}
