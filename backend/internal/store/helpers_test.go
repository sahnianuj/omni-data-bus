package store

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMemberRole_Rank(t *testing.T) {
	require.Greater(t, RoleOwner.Rank(), RoleEditor.Rank())
	require.Greater(t, RoleEditor.Rank(), RoleViewer.Rank())
	require.Greater(t, RoleViewer.Rank(), MemberRole("guest").Rank())
	require.Zero(t, MemberRole("").Rank())
}

func TestNormalizeEmail(t *testing.T) {
	require.Equal(t, "ana@example.com", NormalizeEmail("  Ana@Example.COM \n"))
}

func TestJobTerminal(t *testing.T) {
	for _, s := range []string{JobCancelled, JobCompleted, JobCompletedWithErrors, JobFailed} {
		require.True(t, JobTerminal(s), s)
	}
	for _, s := range []string{"queued", "running", ""} {
		require.False(t, JobTerminal(s), s)
	}
}

func TestVersionLevel(t *testing.T) {
	cases := []struct {
		in    string
		level int
		ok    bool
	}{
		{"2.0.0", 2000000, true},
		{"v1.0.1-rc.1", 1000001, true},
		{" 1.2.3+build.7 ", 1002003, true},
		{"999.999.999", 999999999, true},
		{"dev", 0, false},
		{"1.2", 0, false},
		{"1.2.x", 0, false},
		{"1.1000.0", 0, false},
		{"1.-1.0", 0, false},
	}
	for _, c := range cases {
		level, ok := VersionLevel(c.in)
		require.Equal(t, c.ok, ok, c.in)
		require.Equal(t, c.level, level, c.in)
	}
}

func TestMapping_Driving(t *testing.T) {
	m := Mapping{SourceSchema: "public", SourceTable: "orders"}
	s, tbl := m.Driving()
	require.Equal(t, []string{"public", "orders"}, []string{s, tbl}, "no spec: the mapping's own table")

	m.Spec = json.RawMessage(`{"mode":"composed","root":{"table":{"schema":"sales","name":"customers"}}}`)
	s, tbl = m.Driving()
	require.Equal(t, []string{"sales", "customers"}, []string{s, tbl}, "a composed spec drives from its root table")

	m.Spec = json.RawMessage(`{"mode":"composed","root":{"table":{"schema":"sales"}}}`)
	s, tbl = m.Driving()
	require.Equal(t, []string{"public", "orders"}, []string{s, tbl}, "a root without a table name falls back")

	m.Spec = json.RawMessage(`{"mode":"table","root":{"table":{"schema":"sales","name":"customers"}}}`)
	s, tbl = m.Driving()
	require.Equal(t, []string{"public", "orders"}, []string{s, tbl}, "only composed specs have a root")

	m.Spec = json.RawMessage(`not json`)
	s, tbl = m.Driving()
	require.Equal(t, []string{"public", "orders"}, []string{s, tbl})
}

func TestRelationshipKey(t *testing.T) {
	require.Equal(t, "p1::r1", RelationshipKey("p1", "r1"))
}
