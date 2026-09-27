package automap

import (
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/docschema"
)

func TestNameScore_AC2_SignalTable(t *testing.T) {
	d := DefaultDicts()
	cases := []struct {
		src, tgt string
		parents  []string
		score    float64
		atLeast  bool
		signal   string
	}{
		{"email", "email", nil, 1.0, false, SignalExact},
		{"first_name", "firstName", nil, 0.97, false, SignalNormalizedEqual},
		{"cust_addr", "customerAddress", nil, 0.93, false, SignalExpandedEqual},
		{"email_address", "email", nil, 0.88, false, SignalSynonym},
		{"adress_line", "addressLine", nil, 0.80, true, SignalFuzzy},
		{"created_on", "createdAt", nil, 0.88, false, SignalSynonym},
		{"city", "city", []string{"billing"}, 1.0, false, SignalExact},
		{"billing_city", "city", []string{"billing"}, 0.97, false, SignalNormalizedEqual},
		{"home_phone_number", "phoneNumber", nil, 0.55 + 0.35*2.0/3, false, SignalTokenOverlap},
	}
	for _, c := range cases {
		score, sig := d.NameScore(SourceCol{Name: c.src, Table: "t"}, TargetLeaf{Name: c.tgt, ParentNames: c.parents})
		if c.atLeast {
			require.GreaterOrEqual(t, score, c.score, "%s ↔ %s", c.src, c.tgt)
		} else {
			require.InDelta(t, c.score, score, 1e-9, "%s ↔ %s", c.src, c.tgt)
		}
		require.Equal(t, c.signal, sig.Name, "%s ↔ %s", c.src, c.tgt)
	}
	score, _ := d.NameScore(SourceCol{Name: "zzz", Table: "t"}, TargetLeaf{Name: "qqq"})
	require.Zero(t, score)
}

func TestNameScore_AC2_MaxOverVariants(t *testing.T) {
	d := DefaultDicts()
	// cust_name on customer: the stripped variant ("name") matches, with the -0.02 penalty.
	score, sig := d.NameScore(SourceCol{Name: "cust_name", Table: "customer"}, TargetLeaf{Name: "name"})
	require.InDelta(t, 0.95, score, 1e-9)
	require.Equal(t, "prefix-stripped", sig.Source)
	// The PK alias matches a target called id.
	score, sig = d.NameScore(SourceCol{Name: "customer_id", Table: "customer", IsPK: true}, TargetLeaf{Name: "id"})
	require.InDelta(t, 0.97, score, 1e-9)
	require.Equal(t, "pk-alias", sig.Source)
	// The path without its root-level object.
	score, _ = d.NameScore(SourceCol{Name: "address_city", Table: "t"}, TargetLeaf{Name: "city", ParentNames: []string{"customer", "address"}})
	require.InDelta(t, 0.97, score, 1e-9)
}

func TestJaroWinkler(t *testing.T) {
	require.InDelta(t, 0.961, jaroWinkler("martha", "marhta"), 0.001)
	require.InDelta(t, 0.840, jaroWinkler("dwayne", "duane"), 0.001)
	require.Equal(t, 1.0, jaroWinkler("a", "a"))
	require.Zero(t, jaroWinkler("", "a"))
	require.Zero(t, jaroWinkler("abc", "xyz"))
	require.False(t, fuzzyCandidate("abc", "xbc"))
	require.False(t, fuzzyCandidate("ab", "abcdef"))
	_ = docschema.String
}
