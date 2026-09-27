package cbstore

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func testKeyspace(c string) string { return "`omniconnect`.`meta`.`" + c + "`" }

var tenantFilter = regexp.MustCompile(`(?i)\bworkspaceId\s*=\s*\$workspaceId\b`)

func TestQueries_AC2_TenantQueriesFilterByWorkspace(t *testing.T) {
	qs, err := loadQueries(queryFiles, testKeyspace)
	require.NoError(t, err)
	require.NotEmpty(t, qs)
	for name, q := range qs {
		if !q.Tenant {
			continue
		}
		require.Regexpf(t, tenantFilter, q.SQL, "tenant query %s must filter workspaceId = $workspaceId", name)
	}
	require.True(t, qs["ListProjects"].Tenant, "ListProjects must be annotated -- tenant")
}

func TestQueries_AC2_KeyspacePlaceholdersExpand(t *testing.T) {
	qs, err := loadQueries(queryFiles, testKeyspace)
	require.NoError(t, err)
	for name, q := range qs {
		require.NotContainsf(t, q.SQL, "#{", "%s has unexpanded placeholders", name)
		require.NotContains(t, q.SQL, "-- tenant")
	}
	require.Contains(t, qs["ListProjects"].SQL, "FROM `omniconnect`.`meta`.`projects` AS p")
}

func TestQueries_AC2_UnannotatedTenantQueryIsCaught(t *testing.T) {
	// Guard the guard: a tenant query without the filter must fail the regexp.
	bad := "SELECT RAW p FROM `b`.`s`.`projects` AS p WHERE p.name = $name"
	require.False(t, tenantFilter.MatchString(bad))
	require.True(t, tenantFilter.MatchString(strings.ReplaceAll(bad, "p.name = $name", "p.workspaceId = $workspaceId")))
}
