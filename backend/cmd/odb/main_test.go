package main

import (
	"bytes"
	"encoding/base64"
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
	require.Equal(t, "odb dev (commit none)\n", out.String())
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
