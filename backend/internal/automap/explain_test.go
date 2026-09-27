package automap

import (
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/docschema"
	"omnidatabus/internal/mapping"
)

func TestExplain_AC7_ContainsSignalsAndReason(t *testing.T) {
	s := specWith(fld("a", "customerAddress", docschema.String), fld("b", "code", docschema.String), fld("c", "email", docschema.String),
		fld("d", "name", docschema.String), fld("e", "city", docschema.String))
	r := Run(s, []SourceCol{
		col("cust_addr", "string", 1), col("code", "integer", 2), col("email_address", "string", 3),
		col("cust_name", "string", 4), col("address_city", "string", 5),
	}, Options{})
	m := byTarget(r)
	e := r.Explain[m["a"].ID]
	require.Equal(t, 0.93, e.Score)
	require.Equal(t, 0.93, e.NameScore)
	require.Equal(t, 1.0, e.TypeScore)
	require.Equal(t, SignalExpandedEqual, e.Signals[0].Name)
	require.Equal(t, "Names match after expanding 'cust' → 'customer', 'addr' → 'address' (93%); types match", e.Reason)
	require.Equal(t, "Names are identical (97%); integer → string is lossless", r.Explain[m["b"].ID].Reason)
	require.Contains(t, r.Explain[m["c"].ID].Reason, "Names are synonyms ('email address' ~ 'email')")
	require.Contains(t, r.Explain[m["d"].ID].Reason, "after removing the table prefix")
	require.Equal(t, "expanded-equal", m["a"].Reason)
}

func TestExplain_RecomputedForExistingMappings(t *testing.T) {
	s := specWith(fld("a", "customerAddress", docschema.String), fld("b", "note", docschema.String))
	cols := []SourceCol{col("cust_addr", "string", 1), col("notes", "string", 2)}
	r := Run(s, cols, Options{})
	s.FieldMappings = r.FieldMappings
	s.FieldMappings = append(s.FieldMappings, mapping.FieldMapping{ID: "manual", TargetFieldID: "b", Sources: []mapping.SourceRef{{Column: "notes"}}, Origin: mapping.OriginManual, State: mapping.StateActive})
	got := Explain(s, cols, nil)
	id := byTarget(r)["a"].ID
	require.Equal(t, r.Explain[id], got[id])
	_, ok := got["manual"]
	require.False(t, ok, "manual mappings aren't explained")
}
