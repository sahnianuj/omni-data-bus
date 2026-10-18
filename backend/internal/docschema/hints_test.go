package docschema

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseHints(t *testing.T) {
	cases := map[string]Hints{
		"Not populated.":                    {NotPopulated: true},
		"Not populated - no column.":        {NotPopulated: true},
		"Flattens x. Not tracked: isValid.": {NotPopulated: true},
		"Populated only when saved; not populated otherwise is wrong wording":               {},
		"Set only when status is 'saved' - sourced from saved_orders.saved_at.":             {Sources: []string{"saved_orders.saved_at"}},
		"tipInCents is the sum of payments[].tipInCents (collected_payments.tip_in_cents).": {Aggregate: "sum", Sources: []string{"collected_payments.tip_in_cents"}},
		"Always 1 - cart_items has no quantity column, each unit is its own row.":           {Always: "1", NotPopulated: false},
		"A cart. Document ID: cart::{uuid}. See payments/*.schema.json.":                    {KeyTemplate: "cart::{uuid}"},
		"Version 2.5.0 of migrate.go, see x.json":                                           {},
		"": {},
	}
	for in, want := range cases {
		got := ParseHints(in)
		if len(got.Sources) == 0 {
			got.Sources = nil
		}
		require.Equal(t, want, got, in)
	}
}
