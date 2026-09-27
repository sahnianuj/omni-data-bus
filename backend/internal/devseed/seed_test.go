package devseed

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func readSeed(t *testing.T) string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("..", "..", "..", "deploy", "compose", "seed", "source", "*.sql"))
	require.NoError(t, err)
	require.NotEmpty(t, files)
	var b strings.Builder
	for _, f := range files {
		raw, err := os.ReadFile(f)
		require.NoError(t, err)
		b.Write(raw)
		b.WriteByte('\n')
	}
	return strings.ToLower(b.String())
}

// Every PostgreSQL type from the architecture type-normalization table (§6.4).
var mappedTypes = []string{
	"smallint", "integer", "bigint", "numeric", "real", "double precision", "boolean",
	"text", "varchar", "char(", "citext", "uuid", "date", "timestamp", "timestamptz",
	"time", "timetz", "interval", "json", "jsonb", "bytea", "enum", "[]", "inet", "cidr",
	"macaddr", "money", "xml", "tsvector",
}

func TestSeed_AC2_ContainsAllMappedTypes(t *testing.T) {
	seed := readSeed(t)
	for _, typ := range mappedTypes {
		require.Containsf(t, seed, typ, "seed must use PostgreSQL type %q", typ)
	}
}

var createTable = regexp.MustCompile(`(?s)create table\s+([^\s(]+(?:\s+[^\s(]+)?)\s*\((.*?)\);`)

func TestSeed_AC2_HasTableWithoutPK(t *testing.T) {
	seed := readSeed(t)
	matches := createTable.FindAllStringSubmatch(seed, -1)
	require.NotEmpty(t, matches)
	withoutPK := 0
	for _, m := range matches {
		if !strings.Contains(m[2], "primary key") {
			withoutPK++
		}
	}
	require.GreaterOrEqual(t, withoutPK, 1, "at least one table must have no primary key")
}

func TestSeed_AC2_HasQuotedIdentifierAndBigTable(t *testing.T) {
	seed := readSeed(t)
	require.Contains(t, seed, `"order details"`)
	require.Contains(t, seed, "generate_series(1, 1000000)")
}
