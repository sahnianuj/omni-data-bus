package headless

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors"
	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/export"
)

// recordingSource is memSource remembering the config and password it was opened with.
type recordingSource struct {
	*memSource
	mu     sync.Mutex
	config json.RawMessage
	pass   string
}

func (r *recordingSource) Test(_ context.Context, cfg json.RawMessage, c connectors.Credentials) connectors.TestResult {
	r.record(cfg, c)
	return connectors.TestResult{OK: true}
}

func (r *recordingSource) Open(ctx context.Context, cfg json.RawMessage, c connectors.Credentials) (source.Session, error) {
	r.record(cfg, c)
	return r.memSource.Open(ctx, cfg, c)
}

func (r *recordingSource) record(cfg json.RawMessage, c connectors.Credentials) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.config, r.pass = cfg, c.Password
}

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), export.RunConfigFile)
	require.NoError(t, os.WriteFile(p, []byte(body), 0o600))
	return p
}

// configBundle is memConfig's bundle with the source details in a run config.
func configBundle(t *testing.T, src *recordingSource, stdout, stderr *bytes.Buffer) *Config {
	t.Helper()
	cfg, _ := memConfig(t, src.memSource, stdout, stderr)
	b := *cfg.Bundle
	b.Source.Credentials = &export.Credentials{Mode: export.ModeConfig}
	require.NoError(t, b.Finalize())
	cfg.Bundle = &b
	cfg.Sources = source.NewRegistry()
	cfg.Sources.Register(src)
	return cfg
}

// TestRunConfig_AppliesOverBundle: the run uses config.json's host, user and password, and
// the bundle, its sha256 and its plan hash stay as exported (OC-155 AC2).
func TestRunConfig_AppliesOverBundle(t *testing.T) {
	var stdout, stderr bytes.Buffer
	src := &recordingSource{memSource: &memSource{rows: ordersRows()}}
	cfg := configBundle(t, src, &stdout, &stderr)
	sha, plan := cfg.Bundle.SHA256(), cfg.Bundle.PlanHash()
	path := writeConfig(t, `{"source": {"type": "postgres", "host": "db.prod", "database": "shop_prod", "username": "migrator", "password": "base64:bjN3LXBhc3M="}}`)
	rc, err := LoadRunConfig(path, func(string) { t.Fatal("nothing to encode") })
	require.NoError(t, err)
	cfg.RunConfig, cfg.RunConfigPath = rc, path

	rep, code, err := Run(context.Background(), cfg)
	require.NoError(t, err, stderr.String())
	require.Equal(t, ExitOK, code, stderr.String())
	require.Equal(t, "n3w-pass", src.pass)
	var got map[string]any
	require.NoError(t, json.Unmarshal(src.config, &got))
	require.Equal(t, "db.prod", got["host"])
	require.Equal(t, "shop_prod", got["database"])
	require.Equal(t, "migrator", got["username"])
	require.Equal(t, "disable", got["sslMode"], "settings not in config.json keep the bundle's")
	require.Equal(t, sha, rep.BundleSHA256)
	require.Equal(t, plan, rep.PlanHash)
	require.NotContains(t, stdout.String()+stderr.String(), "n3w-pass")
}

// TestLoadRunConfig_WritesBackEncoded: a password typed in plain text is used, and the file is
// saved with it base64-encoded, mode 0600 (OC-156 AC2).
func TestLoadRunConfig_WritesBackEncoded(t *testing.T) {
	path := writeConfig(t, `{"source": {"type": "postgres", "host": "db.prod", "password": "n3w-pass"}}`)
	require.NoError(t, os.Chmod(path, 0o644))
	var notes []string
	rc, err := LoadRunConfig(path, func(m string) { notes = append(notes, m) })
	require.NoError(t, err)
	require.Equal(t, "n3w-pass", rc.Source.Secrets["password"])
	require.Equal(t, []string{path + ": encoded source.password as base64 and saved the file"}, notes)
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"password": "base64:bjN3LXBhc3M="`)
	require.Contains(t, string(raw), `"host": "db.prod"`)
	require.NotContains(t, string(raw), "n3w-pass")
	st, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), st.Mode().Perm())
	entries, _ := os.ReadDir(filepath.Dir(path))
	require.Len(t, entries, 1, "no temporary file left behind")

	// Already encoded: read as is, the file untouched.
	notes = nil
	before, _ := os.ReadFile(path)
	_, err = LoadRunConfig(path, func(m string) { notes = append(notes, m) })
	require.NoError(t, err)
	require.Empty(t, notes)
	after, _ := os.ReadFile(path)
	require.Equal(t, before, after)
}

// TestRunConfig_Errors: mistakes stop the run with exit 4, naming the file and the field
// (OC-155 AC3, OC-156 AC3).
func TestRunConfig_Errors(t *testing.T) {
	_, err := LoadRunConfig(filepath.Join(t.TempDir(), "missing.json"), func(string) {})
	require.ErrorContains(t, err, "Can't read the run config")
	for body, want := range map[string]string{
		`{"source": `:                            "not valid JSON",
		`{"source": {"password": "base64:***"}}`: `source.password starts with "base64:"`,
	} {
		path := writeConfig(t, body)
		_, err := LoadRunConfig(path, func(string) {})
		var ce *ConfigError
		require.ErrorAs(t, err, &ce, body)
		require.Equal(t, export.CodeRunConfigInvalid, ce.Code)
		require.Contains(t, ce.Message, path+": ")
		require.Contains(t, ce.Message, want)
	}

	var stdout, stderr bytes.Buffer
	src := &recordingSource{memSource: &memSource{rows: ordersRows()}}
	for _, c := range []struct {
		name, config, want string
	}{
		{"no config file", "", "pass it with --config"},
		{"no source section", `{}`, "the source section is missing"},
		{"another kind", `{"source": {"type": "mysql"}}`, `source.type is "mysql", but this migration's source is postgres`},
	} {
		cfg := configBundle(t, src, &stdout, &stderr)
		if c.config != "" {
			path := writeConfig(t, c.config)
			rc, err := LoadRunConfig(path, func(string) {})
			require.NoError(t, err, c.name)
			cfg.RunConfig, cfg.RunConfigPath = rc, path
		}
		_, code, err := Run(context.Background(), cfg)
		require.Equal(t, ExitPreflight, code, c.name)
		require.ErrorContains(t, err, c.want, c.name)
		require.ErrorContains(t, err, export.CodeRunConfigInvalid, c.name)
	}
}

// TestRunConfig_OC158_AC3_EmptyBundleConfig: a mappings.json has no connection settings. The
// run takes them all from config.json, including a directory target's folder, which is
// relative to config.json's folder; a missing section names the file.
func TestRunConfig_OC158_AC3_EmptyBundleConfig(t *testing.T) {
	var stdout, stderr bytes.Buffer
	src := &recordingSource{memSource: &memSource{rows: ordersRows()}}
	cfg := configBundle(t, src, &stdout, &stderr)
	b := *cfg.Bundle
	var target map[string]any
	require.NoError(t, json.Unmarshal(b.Target.Config, &target))
	b.Source.Config, b.Target.Config = json.RawMessage(`{}`), json.RawMessage(`{}`)
	require.NoError(t, b.Finalize())
	cfg.Bundle = &b

	target["type"], target["path"] = "directory", "out"
	tj, _ := json.Marshal(target)
	path := writeConfig(t, `{"source": {"type": "postgres", "host": "db.prod", "database": "shop", "username": "migrator", "password": "base64:bjN3LXBhc3M="}, "target": `+string(tj)+`}`)
	rc, err := LoadRunConfig(path, func(string) {})
	require.NoError(t, err)
	cfg.RunConfig, cfg.RunConfigPath = rc, path

	_, code, err := Run(context.Background(), cfg)
	require.NoError(t, err, stderr.String())
	require.Equal(t, ExitOK, code, stderr.String())
	var got map[string]any
	require.NoError(t, json.Unmarshal(src.config, &got))
	require.Equal(t, "db.prod", got["host"])
	files, err := filepath.Glob(filepath.Join(filepath.Dir(path), "out", "orders", "*.json"))
	require.NoError(t, err)
	require.Len(t, files, 3, "written under config.json's folder")

	// Without the target section: the file and section are named.
	cfg2 := configBundle(t, src, &stdout, &stderr)
	cfg2.Bundle = &b
	path2 := writeConfig(t, `{"source": {"type": "postgres", "password": "x"}}`)
	rc2, err := LoadRunConfig(path2, func(string) {})
	require.NoError(t, err)
	cfg2.RunConfig, cfg2.RunConfigPath = rc2, path2
	_, code, err = Run(context.Background(), cfg2)
	require.Equal(t, ExitPreflight, code)
	require.ErrorContains(t, err, path2+": the target section is missing")
}
