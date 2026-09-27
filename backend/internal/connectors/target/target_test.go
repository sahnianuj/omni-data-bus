package target_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors/target"
)

func TestNewRegistry_IsEmptyTargetRegistry(t *testing.T) {
	r := target.NewRegistry()
	require.Empty(t, r.Descriptors())
	_, err := r.Get("couchbase")
	require.ErrorContains(t, err, `unsupported target type "couchbase"`)
}
