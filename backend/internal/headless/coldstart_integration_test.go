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

// TestColdStartFixture writes a sealed export of the golden orders project (migrate.sh and
// bundle.odb.json, the dev PostgreSQL as source, a directory target) to $ODB_COLDSTART_OUT,
// for the runner cold-start benchmark (OC-056; bench/README.md). Skipped unless that's set.
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
	script, _, err := export.RenderScriptFiles(b, raw, export.Script{Runtime: "binary", IncludeBundle: true}, export.ScriptConfig{}, buildinfo.Get().Version)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(out, "bundle.odb.json"), raw, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(out, "migrate.sh"), script, 0o755)) //nolint:gosec // an executable script
	t.Logf("wrote %s (odb %s); passphrase %q", out, buildinfo.Get().Version, passphrase)
}
