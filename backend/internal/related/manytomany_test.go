package related

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/docschema"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/schema"
)

// enrollmentTables are students ↔ courses through enrollments (OC-044).
func enrollmentTables() (students *schema.Table, rel mapping.RelatedTables) {
	c := func(n, udt string) schema.Column { return schema.Column{Name: n, UDTName: udt} }
	students = &schema.Table{Name: "students", PrimaryKey: []string{"student_id"}, Columns: []schema.Column{c("student_id", "int4"), c("name", "text")}}
	courses := &schema.Table{Name: "courses", PrimaryKey: []string{"course_id"}, Columns: []schema.Column{c("course_id", "int4"), c("title", "text")}}
	enrollments := &schema.Table{Name: "enrollments", Columns: []schema.Column{c("student_id", "int4"), c("course_id", "int4"), c("grade", "text")}}
	return students, func(_, n string) *schema.Table {
		switch n {
		case "courses":
			return courses
		case "enrollments":
			return enrollments
		}
		return nil
	}
}

func enrollmentSpec(mod func(*mapping.Relation)) *mapping.Spec {
	e := mapping.Relation{
		ID: "c", Kind: mapping.RelationManyToMany, Table: mapping.TableRef{Schema: "s", Name: "courses"},
		Via: &mapping.Via{
			Table:      mapping.TableRef{Schema: "s", Name: "enrollments"},
			ParentJoin: []mapping.ViaJoin{{Parent: "student_id", Via: "student_id"}},
			ChildJoin:  []mapping.ViaJoin{{Via: "course_id", Child: "course_id"}},
			Columns:    []string{"grade"},
		},
		Distinct: true,
		OrderBy:  []mapping.OrderColumn{{Column: "title"}, {Column: "grade", From: mapping.FromVia}},
	}
	if mod != nil {
		mod(&e)
	}
	return &mapping.Spec{
		Relations: []mapping.Relation{e},
		FieldMappings: []mapping.FieldMapping{
			{ID: "m1", Sources: []mapping.SourceRef{{Column: "title", Relation: "c"}}, State: mapping.StateActive},
			{ID: "m2", Sources: []mapping.SourceRef{{Column: "grade", Relation: "c", From: mapping.FromVia}}, State: mapping.StateActive},
		},
		DocSchema: docschema.Schema{Root: docschema.NewRoot()},
	}
}

func TestPlans_ManyToMany(t *testing.T) {
	students, rel := enrollmentTables()
	plans, err := Plans(enrollmentSpec(nil), students, rel)
	require.NoError(t, err)
	require.Len(t, plans, 1)
	p := plans[0]
	require.True(t, p.Many)
	require.Equal(t, []int{0}, p.ParentIdx)
	require.Equal(t, []int{2}, p.ChildIdx, "the junction's student_id, after the course's 2 columns")
	require.Equal(t, []int{0}, p.DanglingIdx, "a missing course reads as a null course_id")
	require.Equal(t, []int{0}, p.DistinctIdx, "distinct by the course's primary key")
	require.Equal(t, []string{"student_id"}, p.Query.Join)
	require.NotNil(t, p.Query.Via)
	require.Equal(t, []string{"student_id", "grade"}, p.Query.Via.Columns)
	require.Zero(t, p.Query.PerKeyLimit, "rows may still be dropped after the query, so no SQL limit")
	require.Len(t, p.Query.OrderBy, 2)
	require.True(t, p.Query.OrderBy[1].Via)
	require.Equal(t, []bool{true, true}, p.Query.Table.Project)
	require.Equal(t, []string{"student_id"}, ParentColumns(enrollmentSpec(nil)), "the parent joins the junction")
}

func TestPlans_ManyToManyErrors(t *testing.T) {
	students, rel := enrollmentTables()
	cases := map[string]func(*mapping.Relation){
		"many-to-many without a junction":       func(e *mapping.Relation) { e.Via = nil },
		"junction table s.gone isn't":           func(e *mapping.Relation) { e.Via.Table.Name = "gone" },
		"join column course_id → nope":          func(e *mapping.Relation) { e.Via.ChildJoin[0].Child = "nope" },
		"join column student_id → nope":         func(e *mapping.Relation) { e.Via.ParentJoin[0].Via = "nope" },
		"join column missing_col → student_id ": func(e *mapping.Relation) { e.Via.ParentJoin[0].Parent = "missing_col" },
	}
	for want, mod := range cases {
		_, err := Plans(enrollmentSpec(mod), students, rel)
		require.ErrorContains(t, err, want[:len(want)-1], want)
	}
}

func TestResolve_ManyToManyDropsDuplicatesAndDangling(t *testing.T) {
	students, rel := enrollmentTables()
	rows := map[string][][]any{"courses": {
		// course_id, title | junction: student_id, grade
		{int32(10), "Algebra", int32(1), "A"},
		{int32(10), "Algebra", int32(1), "B"}, // the same course again: dropped (distinct)
		{nil, nil, int32(1), "C"},             // its course is gone: dangling
		{int32(11), "Biology", int32(2), "A"},
	}}

	plans, err := Plans(enrollmentSpec(nil), students, rel)
	require.NoError(t, err)
	got, fails, err := (&Resolver{Plans: plans, Reader: &fakeReader{rows: rows}}).Resolve(context.Background(),
		[][]any{{int32(1), "Ana"}, {int32(2), "Bo"}})
	require.NoError(t, err)
	require.Empty(t, fails)
	require.Equal(t, 1, got[0]["c"].Len())
	require.Equal(t, 1, got[0]["c"].Dangling)
	require.Equal(t, 1, got[1]["c"].Len())

	plans, err = Plans(enrollmentSpec(func(e *mapping.Relation) { e.KeepDangling, e.Distinct = true, false }), students, rel)
	require.NoError(t, err)
	got, _, err = (&Resolver{Plans: plans, Reader: &fakeReader{rows: rows}}).Resolve(context.Background(), [][]any{{int32(1), "Ana"}})
	require.NoError(t, err)
	require.Equal(t, 3, got[0]["c"].Len(), "kept: duplicates and the dangling row")
	require.Zero(t, got[0]["c"].Dangling)
}

func TestResolve_MemoryCapSplitsThenFailsRows(t *testing.T) {
	parent, _, _ := tables()
	plans, err := Plans(spec(), parent, rel())
	require.NoError(t, err)
	fr := &fakeReader{rows: map[string][][]any{"address": {{int32(1), "Lyon", "69"}, {int32(2), "Nice", "06"}}}}

	// One address row is ~100 bytes: a 1-byte cap fails every row that has one, after splitting.
	r := &Resolver{Plans: plans, Reader: fr, MemoryBytes: 1}
	got, fails, err := r.Resolve(context.Background(), [][]any{{int32(7), int32(1), "x"}, {int32(8), int32(2), "y"}, {int32(9), nil, "z"}})
	require.NoError(t, err)
	require.Len(t, got, 3)
	require.Len(t, fails, 2, "split down to single rows; each with an address fails")
	require.NotContains(t, fails, 2, "a null join key looks nothing up")
	require.Equal(t, CodeRelationMemoryExceeded, fails[0].Code)
	require.Contains(t, fails[0].Message, "0 MB")

	// The process-wide cap applies when the resolver sets none.
	defer SetMemoryLimitMB(DefaultMemoryMB)
	SetMemoryLimitMB(0)
	require.Equal(t, int64(1<<20), MemoryLimitBytes(), "at least 1 MB")
	require.Equal(t, int64(1<<20), (&Resolver{}).limit())
}

func TestRowBytes(t *testing.T) {
	base := rowBytes([]any{nil})
	require.Equal(t, base+5, rowBytes([]any{"hello"}))
	require.Equal(t, base+3, rowBytes([]any{[]byte("abc")}))
	require.Equal(t, base+64, rowBytes([]any{map[string]any{}}))
	require.Equal(t, base+64, rowBytes([]any{[]any{}}))
	require.Equal(t, base, rowBytes([]any{int64(1)}))
}

func TestAllNullAndIndexOf(t *testing.T) {
	require.True(t, allNull([]any{nil, 1}, []int{0}))
	require.True(t, allNull([]any{nil}, []int{0, 5}), "out-of-range columns read as null")
	require.False(t, allNull([]any{nil, 1}, []int{0, 1}))
	require.Equal(t, 1, indexOf([]string{"a", "b"}, "b"))
	require.Equal(t, -1, indexOf([]string{"a"}, "z"))
}
