package source_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/connectors/source/sourcetest"
)

func TestNewRegistry_RegistersSourceConnectors(t *testing.T) {
	r := source.NewRegistry()
	r.Register(&sourcetest.Fake{Type: "postgres"})
	c, err := r.Get("postgres")
	require.NoError(t, err)
	require.Equal(t, "postgres", c.Descriptor().Type)
	_, err = r.Get("oracle")
	require.ErrorContains(t, err, `unsupported source type "oracle"`)
}

func TestClampPreviewLimit(t *testing.T) {
	require.Equal(t, source.DefaultPreviewLimit, source.ClampPreviewLimit(-1))
	require.Equal(t, 1, source.ClampPreviewLimit(1))
	require.Equal(t, source.MaxPreviewLimit, source.ClampPreviewLimit(source.MaxPreviewLimit+1))
}
