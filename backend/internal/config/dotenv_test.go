package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseDotEnv_LiteralValues(t *testing.T) {
	vars, err := ParseDotEnv(`
# comment
OC_DEV=true
export OC_LOG_LEVEL=debug
OC_META_PASSWORD=p@$$w0rd#with$HOME` + "`x`" + `
OC_META_USERNAME="quoted user"
OC_OTHER='single'
EMPTY=
SPACED = value with spaces
`)
	require.NoError(t, err)
	require.Equal(t, "true", vars["OC_DEV"])
	require.Equal(t, "debug", vars["OC_LOG_LEVEL"])
	require.Equal(t, "p@$$w0rd#with$HOME`x`", vars["OC_META_PASSWORD"], "no expansion, no inline comments")
	require.Equal(t, "quoted user", vars["OC_META_USERNAME"])
	require.Equal(t, "single", vars["OC_OTHER"])
	require.Equal(t, "", vars["EMPTY"])
	require.Equal(t, "value with spaces", vars["SPACED"])
}

func TestParseDotEnv_RejectsMalformed(t *testing.T) {
	_, err := ParseDotEnv("OC_DEV=true\nnot a pair\n")
	require.ErrorContains(t, err, "line 2")
	_, err = ParseDotEnv("1BAD=x\n")
	require.Error(t, err)
}

func TestLoadDotEnv_ProcessEnvWins(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	require.NoError(t, os.WriteFile(path, []byte("OC_TEST_A=from-file\nOC_TEST_B=from-file\n"), 0o600))
	t.Setenv("OC_TEST_A", "from-env")
	t.Setenv("OC_TEST_B", "")
	require.NoError(t, os.Unsetenv("OC_TEST_B"))

	loaded, err := LoadDotEnv(filepath.Join(dir, "missing"), path)
	require.NoError(t, err)
	require.Equal(t, path, loaded)
	require.Equal(t, "from-env", os.Getenv("OC_TEST_A"), "real environment wins")
	require.Equal(t, "from-file", os.Getenv("OC_TEST_B"))
	t.Cleanup(func() { _ = os.Unsetenv("OC_TEST_B") })
}

func TestLoadDotEnv_NoFileIsFine(t *testing.T) {
	loaded, err := LoadDotEnv(filepath.Join(t.TempDir(), "nope"))
	require.NoError(t, err)
	require.Empty(t, loaded)
}

func TestDotEnvCandidates(t *testing.T) {
	t.Setenv("OC_ENV_FILE", "")
	require.NoError(t, os.Unsetenv("OC_ENV_FILE"))
	require.Equal(t, []string{".env", "../.env"}, DotEnvCandidates())
	t.Setenv("OC_ENV_FILE", "/etc/oc.env")
	require.Equal(t, []string{"/etc/oc.env"}, DotEnvCandidates())
}

func TestExampleEnvFileParses(t *testing.T) {
	raw, err := os.ReadFile("../../../.env.example")
	require.NoError(t, err)
	vars, err := ParseDotEnv(string(raw))
	require.NoError(t, err)
	for _, k := range []string{"OC_META_CONNSTR", "OC_META_USERNAME", "OC_META_PASSWORD", "OC_META_BUCKET", "OC_META_CAPELLA", "OC_MASTER_KEY"} {
		require.Contains(t, vars, k, ".env.example must document %s", k)
	}
}
