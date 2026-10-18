//go:build integration

package headless

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/buildinfo"
	"omnidatabus/internal/export"
)

// TestColdStartFixture writes a sealed export folder of the golden orders project (mappings.json
// and an empty config.json: the sealed bundle keeps its connections; the dev PostgreSQL as
// source, a directory target) to $ODB_COLDSTART_OUT, for the runner cold-start benchmark
// (OC-056, OC-184: odbbench coldstart --dir; bench/README.md). Skipped unless that's set.
func TestColdStartFixture(t *testing.T) {
	out := os.Getenv("ODB_COLDSTART_OUT")
	if out == "" {
		t.Skip("set ODB_COLDSTART_OUT to write the cold-start fixture")
	}
	require.NoError(t, os.MkdirAll(out, 0o750))
	tl := newTool(t, filepath.Join(out, "server"))
	b := tl.export(t, filepath.Join(out, "out"), export.Request{})
	raw, err := export.Encode(b)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(out, export.MappingsFile), raw, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(out, export.RunConfigFile), []byte("{}\n"), 0o600))
	t.Logf("wrote %s (odb %s); passphrase %q", out, buildinfo.Get().Version, passphrase)
}
