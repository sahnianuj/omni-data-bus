package source_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/schema"
)

func TestTableRead_KeyIndexes(t *testing.T) {
	tr := source.TableRead{
		Columns: []schema.Column{{Name: "tenant"}, {Name: "id"}, {Name: "name"}},
		Key:     []string{"id", "tenant", "gone"},
	}
	require.Equal(t, []int{1, 0, -1}, tr.KeyIndexes(), "positions in Columns; -1 for a key column that isn't there")
	require.Empty(t, source.TableRead{}.KeyIndexes())
}

func TestTransientError_WrapsAndIsDetected(t *testing.T) {
	cause := errors.New("connection reset")
	err := fmt.Errorf("read shop.orders: %w", &source.TransientError{Err: cause})
	require.True(t, source.IsTransient(err))
	require.ErrorIs(t, err, cause)
	require.EqualError(t, err, "read shop.orders: transient: connection reset")
	require.False(t, source.IsTransient(cause))
	require.False(t, source.IsTransient(nil))
}
