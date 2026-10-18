package mapping

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// OC-170 AC3: which key templates overwrite each other's documents.
func TestKeyOverlap_OC170_AC3(t *testing.T) {
	require.True(t, KeyOverlap("cart::{id}", "cart::{id}"))
	require.True(t, KeyOverlap("cart::{id|lower}", "cart::{id}"), "filters change the text, not which row a key names")
	require.False(t, KeyOverlap("cart::{id}", "order::{id}"))
	require.False(t, KeyOverlap("cart::{id}", "cart::{store_number}::{id}"))
	require.False(t, KeyOverlap("cart::{", "cart::{id}"), "invalid: reported by validation, not here")
}
