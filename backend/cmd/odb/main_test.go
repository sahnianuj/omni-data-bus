package main

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRun_AC1_ExitCode2OnMissingConfig(t *testing.T) {
	t.Setenv("OC_ENV_FILE", t.TempDir()+"/none") // don't pick up a developer's real .env
	for _, k := range []string{"OC_DEV", "OC_META_CONNSTR", "OC_META_USERNAME", "OC_META_PASSWORD", "OC_MASTER_KEY", "OC_MASTER_KEYS"} {
		t.Setenv(k, "")
	}
	t.Setenv("OC_META_PASSWORD", "top-secret-value")
	var out, errOut bytes.Buffer
	code := run(nil, &out, &errOut)
	require.Equal(t, 2, code)
	lines := bytes.Count(bytes.TrimSpace(errOut.Bytes()), []byte("\n")) + 1
	require.Equal(t, 1, lines, "a single log line")
	require.Contains(t, errOut.String(), "OC_META_CONNSTR")
	require.NotContains(t, errOut.String(), "top-secret-value")
}

func TestVersion_AC1_PrintsVersion(t *testing.T) {
	t.Setenv("OC_ENV_FILE", t.TempDir()+"/none")
	var out, errOut bytes.Buffer
	require.Equal(t, 0, run([]string{"--version"}, &out, &errOut))
	// A plain `go build` (no -ldflags) reports dev/none and doesn't crash (OC-058 AC1).
	require.Contains(t, out.String(), "Omni-Data-Bus dev (dev)\nCommit: none  Built: unknown")
}

func TestAdmin_AC8_UsageAndUnknownCommand(t *testing.T) {
	var out, errOut bytes.Buffer
	require.Equal(t, 2, run([]string{"admin"}, &out, &errOut))
	require.Contains(t, errOut.String(), "provision [--print]")
	errOut.Reset()
	require.Equal(t, 2, run([]string{"admin", "nope"}, &out, &errOut))
	require.Contains(t, errOut.String(), `unknown admin command "nope"`)
}

func TestAdminGenKey_AC2_Length(t *testing.T) {
	var out, errOut bytes.Buffer
	require.Equal(t, 0, run([]string{"admin", "gen-key"}, &out, &errOut))
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(out.String()))
	require.NoError(t, err)
	require.Len(t, key, 32)
	var out2 bytes.Buffer
	require.Equal(t, 0, run([]string{"admin", "gen-key"}, &out2, &errOut))
	require.NotEqual(t, out.String(), out2.String(), "keys are random")
}

// OC-158: odb run reads --mappings (or --bundle, its older name), and says so when neither is set.
func TestRun_MappingsFlag(t *testing.T) {
	var out, errOut bytes.Buffer
	require.Equal(t, exitUsage, run([]string{"run"}, &out, &errOut))
	require.Contains(t, errOut.String(), "--mappings is required")
	for _, flag := range []string{"--mappings", "--bundle"} {
		errOut.Reset()
		require.Equal(t, exitPreflight, run([]string{"run", flag, t.TempDir() + "/missing.json"}, &out, &errOut), flag)
		require.Contains(t, errOut.String(), "BUNDLE_INVALID", flag)
	}
}

// OC-159: odb run target-path prints a directory target's folder, resolved against config.json's
// folder, and nothing for another target.
func TestRunTargetPath(t *testing.T) {
	dir := t.TempDir()
	write := func(body string) string {
		p := filepath.Join(dir, "config.json")
		require.NoError(t, os.WriteFile(p, []byte(body), 0o600))
		return p
	}
	var out, errOut bytes.Buffer
	p := write(`{"source": {"type": "postgres"}, "target": {"type": "directory", "path": "out"}}`)
	require.Equal(t, exitOK, run([]string{"run", "target-path", "--config", p}, &out, &errOut), errOut.String())
	want, _ := filepath.Abs(filepath.Join(dir, "out"))
	require.Equal(t, want+"\n", out.String())

	out.Reset()
	p = write(`{"source": {"type": "postgres"}, "target": {"type": "couchbase", "connectionString": "couchbase://x"}}`)
	require.Equal(t, exitOK, run([]string{"run", "target-path", "--config", p}, &out, &errOut))
	require.Empty(t, out.String())

	p = write(`{"target": {"type": "directory"}}`)
	require.Equal(t, exitPreflight, run([]string{"run", "target-path", "--config", p}, &out, &errOut))
	require.Contains(t, errOut.String(), "target.path is required")
}
