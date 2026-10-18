//go:build windows

package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// OC-186 AC2: Windows paths, with backslashes or forward slashes, in flags and ODB_*; the
// export files are found beside odb.exe.
func TestRun_OC186_AC2_WindowsPaths(t *testing.T) {
	noExportEnv(t)
	dir := exportFolder(t)
	require.True(t, filepath.VolumeName(dir) != "", "a drive-letter path: %s", dir)
	state := filepath.Join(t.TempDir(), "odb", "state")

	for _, s := range []string{state, filepath.ToSlash(state)} {
		code, out, stderr := runCLI("run", "status", "--state-dir", s)
		require.Equal(t, exitPreflight, code, stderr)
		require.Contains(t, out, "No run in "+s)
	}
	t.Setenv("ODB_STATE_DIR", state)
	code, out, _ := runCLI("status")
	require.Equal(t, exitPreflight, code)
	require.Contains(t, out, "No run in "+state)
	t.Setenv("ODB_STATE_DIR", "")

	// odb.exe in the export's bin\ folder, run from elsewhere.
	t.Chdir(t.TempDir())
	executable = func() (string, error) { return filepath.Join(dir, "bin", "odb.exe"), nil }
	p, ok := mappingsFile.resolve("")
	require.True(t, ok)
	require.True(t, strings.EqualFold(filepath.Join(dir, "mappings.json"), p), p)

	// A directory target's absolute Windows path in config.json is kept as is.
	code, out, stderr := runCLI("run", "target-path", "--config", filepath.Join(dir, "config.json"))
	require.Equal(t, exitOK, code, stderr)
	require.True(t, strings.EqualFold(filepath.Join(dir, "out")+"\r\n", out) || strings.EqualFold(filepath.Join(dir, "out")+"\n", out), out)
}
