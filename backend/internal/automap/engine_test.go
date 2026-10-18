package automap

import (
	"math/rand"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/docschema"
	"omnidatabus/internal/mapping"
)

func fld(id, name string, t docschema.FieldType, children ...docschema.DocField) docschema.DocField {
	f := docschema.DocField{ID: id, Name: name, Type: t}
	if t == docschema.Object {
		f.Children = append([]docschema.DocField{}, children...)
	}
	return f
}

func specWith(fields ...docschema.DocField) *mapping.Spec {
	s := &mapping.Spec{DocSchema: docschema.Schema{Root: docschema.DocField{ID: docschema.RootID, Name: "$", Type: docschema.Object, Children: fields}}}
	s.Normalize()
	return s
}

func col(name, jt string, ord int) SourceCol {
	return SourceCol{Name: name, JSONType: jt, Ordinal: ord, Table: "customer"}
}

func byTarget(r Result) map[string]mapping.FieldMapping {
	out := map[string]mapping.FieldMapping{}
	for _, m := range r.FieldMappings {
		if m.State != mapping.StateRejected {
			out[m.TargetFieldID] = m
		}
	}
	return out
}

func TestAutomap_AC3_AttachesDefaultTransform(t *testing.T) {
	s := specWith(fld("f_code", "code", docschema.String), fld("f_age", "age", docschema.Integer), fld("f_notes", "notes", docschema.Object), fld("f_prefs", "preferences", docschema.Object))
	r := Run(s, []SourceCol{col("code", "integer", 1), col("age", "string", 2), col("notes", "string", 3), col("preferences", "any", 4)}, Options{})
	m := byTarget(r)
	require.Equal(t, "toString", m["f_code"].Transforms[0].Op)
	require.False(t, m["f_code"].NeedsReview)
	require.InDelta(t, 1.0*(0.8+0.2*0.85), m["f_code"].Confidence, 1e-4)
	require.Equal(t, "parseInteger", m["f_age"].Transforms[0].Op)
	require.True(t, m["f_age"].NeedsReview)
	require.Equal(t, mapping.StateActive, m["f_age"].State, "exact name × lossy type = 1.0 × 0.9 = 0.90: auto, flagged for review")
	_, mappedNotes := m["f_notes"]
	require.False(t, mappedNotes, "text → object is excluded")
	require.Equal(t, 1.0, m["f_prefs"].Confidence, "jsonb → object is exact")
	require.Empty(t, m["f_prefs"].Transforms)
}

func TestAutomap_AC4_OneToOneGreedy(t *testing.T) {
	// email_address (synonym 0.88) and email (exact 1.0) compete for email; email wins and
	// email_address falls back to its next best target, contact.
	s := specWith(fld("f_email", "email", docschema.String), fld("f_contact", "contactEmailAddress", docschema.String))
	r := Run(s, []SourceCol{col("email_address", "string", 1), col("email", "string", 2)}, Options{})
	m := byTarget(r)
	require.Equal(t, "email", m["f_email"].Sources[0].Column)
	require.Equal(t, "email_address", m["f_contact"].Sources[0].Column)
	require.Len(t, r.FieldMappings, 2)
}

func TestAutomap_AC4_DeterministicUnderShuffle(t *testing.T) {
	s := specWith(fld("a", "firstName", docschema.String), fld("b", "lastName", docschema.String), fld("c", "email", docschema.String),
		fld("d", "createdAt", docschema.Datetime), fld("e", "address", docschema.Object, fld("e1", "city", docschema.String), fld("e2", "zip", docschema.String)))
	cols := []SourceCol{
		col("first_name", "string", 1), col("last_name", "string", 2), col("email_address", "string", 3),
		col("created_on", "datetime", 4), col("city", "string", 5), col("postal_code", "string", 6), col("billing_city", "string", 7),
	}
	want := Run(s, cols, Options{})
	rng := rand.New(rand.NewSource(1))
	for range 100 {
		shuffled := append([]SourceCol(nil), cols...)
		rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
		got := Run(s, shuffled, Options{})
		require.ElementsMatch(t, want.FieldMappings, got.FieldMappings)
	}
}

func TestAutomap_AC4_Thresholds(t *testing.T) {
	s := specWith(fld("a", "email", docschema.String), fld("b", "addressLine", docschema.String), fld("c", "zzz", docschema.String))
	r := Run(s, []SourceCol{col("email", "string", 1), col("adress_line", "string", 2), col("unrelated", "string", 3)}, Options{})
	m := byTarget(r)
	require.Equal(t, mapping.OriginAuto, m["a"].Origin)
	require.Equal(t, mapping.StateActive, m["a"].State)
	require.Equal(t, mapping.OriginSuggested, m["b"].Origin, "fuzzy ~0.82")
	require.Equal(t, mapping.StatePending, m["b"].State)
	_, ok := m["c"]
	require.False(t, ok, "below 0.60 → unmapped")
	require.Equal(t, 1, r.Stats.AutoMapped)
	require.Equal(t, 1, r.Stats.Suggested)
	require.Equal(t, 1, r.Stats.UnmappedSource)
}

func TestAutomap_AC5_FillRespectsManual(t *testing.T) {
	s := specWith(fld("f_mobile", "mobile", docschema.String), fld("f_phone", "phone", docschema.String))
	s.FieldMappings = []mapping.FieldMapping{{ID: "man", TargetFieldID: "f_mobile", Sources: []mapping.SourceRef{{Column: "phone"}}, Origin: mapping.OriginManual, State: mapping.StateActive}}
	r := Run(s, []SourceCol{col("phone", "string", 1)}, Options{Mode: ModeFill})
	require.Len(t, r.FieldMappings, 1, "phone is taken: not reassigned to f_phone")
	require.Equal(t, "man", r.FieldMappings[0].ID)
}

func TestAutomap_AC5_RejectedNotResuggested(t *testing.T) {
	s := specWith(fld("f_desc", "description", docschema.String))
	s.FieldMappings = []mapping.FieldMapping{{ID: "rej", TargetFieldID: "f_desc", Sources: []mapping.SourceRef{{Column: "notes"}}, Origin: mapping.OriginSuggested, State: mapping.StateRejected}}
	for _, mode := range []string{ModeFill, ModeReset} {
		r := Run(s, []SourceCol{col("notes", "string", 1)}, Options{Mode: mode})
		require.Len(t, r.FieldMappings, 1, mode)
		require.Equal(t, mapping.StateRejected, r.FieldMappings[0].State, mode)
	}
}

func TestAutomap_AC5_ResetKeepsManual(t *testing.T) {
	s := specWith(fld("f_email", "email", docschema.String), fld("f_mobile", "mobile", docschema.String), fld("f_name", "name", docschema.String))
	s.FieldMappings = []mapping.FieldMapping{
		{ID: "man", TargetFieldID: "f_mobile", Sources: []mapping.SourceRef{{Column: "phone"}}, Origin: mapping.OriginManual, State: mapping.StateActive},
		{ID: "old", TargetFieldID: "f_name", Sources: []mapping.SourceRef{{Column: "email"}}, Origin: mapping.OriginAuto, State: mapping.StateActive},
	}
	cols := []SourceCol{col("phone", "string", 1), col("email", "string", 2)}
	fill := Run(s, cols, Options{Mode: ModeFill})
	require.Len(t, fill.FieldMappings, 2, "fill keeps the old auto mapping")
	reset := Run(s, cols, Options{Mode: ModeReset})
	m := byTarget(reset)
	require.Equal(t, "man", m["f_mobile"].ID)
	require.Equal(t, "email", m["f_email"].Sources[0].Column, "auto rebuilt: email → email")
	_, ok := m["f_name"]
	require.False(t, ok)
	all := Run(s, cols, Options{Mode: ModeReset, IncludeManual: true})
	require.Equal(t, "phone", byTarget(all)["f_mobile"].Sources[0].Column, "rebuilt as a synonym match")
	require.Equal(t, mapping.OriginAuto, byTarget(all)["f_mobile"].Origin)
}

func TestAutomap_IgnoredColumnsAndStructuralBonus(t *testing.T) {
	s := specWith(fld("f_addr", "address", docschema.Object, fld("f_city", "city", docschema.String)))
	s.IgnoredColumns = []string{"legacy_city"}
	r := Run(s, []SourceCol{col("billing_city", "string", 1), col("address_city", "string", 2), col("legacy_city", "string", 3)}, Options{})
	m := byTarget(r)
	require.Equal(t, "address_city", m["f_city"].Sources[0].Column, "the parent prefix breaks the tie")
	require.Equal(t, 2, r.Stats.TotalSourceColumns)
	exp := r.Explain[m["f_city"].ID]
	require.Equal(t, SignalStructural, exp.Signals[len(exp.Signals)-1].Name)
}

// OC-139 AC5: a computed field is never mapped onto, and a reset keeps it, even with manual
// mappings included.
func TestAutomap_AC5_SkipsComputed(t *testing.T) {
	s := specWith(fld("f_full", "fullName", docschema.String), fld("f_email", "email", docschema.String))
	s.FieldMappings = []mapping.FieldMapping{{
		ID: "c", TargetFieldID: "f_full", State: mapping.StateActive, Origin: mapping.OriginManual,
		Computed: &mapping.Computed{Lang: mapping.ComputedTemplate, Expr: "${doc.email}"},
	}}
	cols := []SourceCol{col("full_name", "string", 1), col("email", "string", 2)}
	for _, opts := range []Options{{}, {Mode: ModeReset, IncludeManual: true}} {
		m := byTarget(Run(s, cols, opts))
		require.NotNil(t, m["f_full"].Computed, "%+v", opts)
		require.Empty(t, m["f_full"].Sources)
		require.Equal(t, "email", m["f_email"].Sources[0].Column)
	}
}
