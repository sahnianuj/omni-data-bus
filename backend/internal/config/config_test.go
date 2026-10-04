package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func prodEnv() map[string]string {
	return map[string]string{
		"OC_META_CONNSTR":  "couchbases://cb.example.cloud.couchbase.com",
		"OC_META_USERNAME": "omni",
		"OC_META_PASSWORD": "s3cret-pass",
		"OC_MASTER_KEY":    "a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2U=",
	}
}

func TestLoad_AC1_MissingRequiredVar(t *testing.T) {
	e := prodEnv()
	delete(e, "OC_META_CONNSTR")
	_, err := Load(e)
	require.Error(t, err)
	require.Contains(t, err.Error(), "OC_META_CONNSTR")
	require.NotContains(t, err.Error(), "s3cret-pass", "error must not echo secrets")
}

func TestLoad_AC1_MissingMasterKey(t *testing.T) {
	e := prodEnv()
	delete(e, "OC_MASTER_KEY")
	_, err := Load(e)
	require.ErrorContains(t, err, "OC_MASTER_KEY")
}

func TestLoad_AC1_InvalidRole(t *testing.T) {
	e := prodEnv()
	e["OC_ROLE"] = "foo"
	_, err := Load(e)
	require.ErrorContains(t, err, "Role")
}

func TestLoad_AC1_ProductionDefaults(t *testing.T) {
	cfg, err := Load(prodEnv())
	require.NoError(t, err)
	require.Equal(t, "all", cfg.Role)
	require.Equal(t, ":8080", cfg.HTTPAddr)
	require.Equal(t, "omniconnect", cfg.Meta.Bucket)
	require.Equal(t, "meta", cfg.Meta.Scope)
	require.True(t, cfg.Session.CookieSecure)
	require.Equal(t, 12*time.Hour, cfg.Session.IdleTTL)
}

func TestLoad_AC1_DevDefaultsFillMissing(t *testing.T) {
	cfg, err := Load(map[string]string{"OC_DEV": "true"})
	require.NoError(t, err)
	require.Equal(t, DefaultMetaConnStr, cfg.Meta.ConnStr)
	require.Equal(t, DefaultMetaUsername, cfg.Meta.Username)
	require.Equal(t, DevMasterKey, cfg.MasterKey)
	require.False(t, cfg.Session.CookieSecure)
}

func TestRedacted_AC1_MasksSecrets(t *testing.T) {
	cfg, err := Load(prodEnv())
	require.NoError(t, err)
	r := cfg.Redacted()
	require.Equal(t, "***", r.Meta.Password)
	require.Equal(t, "***", r.MasterKey)
	require.Equal(t, "s3cret-pass", cfg.Meta.Password, "original is untouched")
}

func TestIsLocalConnStr(t *testing.T) {
	for cs, want := range map[string]bool{
		"couchbase://localhost":                     true,
		"couchbase://127.0.0.1:11210":               true,
		"couchbase://[::1]":                         true,
		"couchbase://couchbase":                     true,
		"couchbase://localhost,127.0.0.1?network=x": true,
		"couchbases://cb.abc.cloud.couchbase.com":   false,
		"couchbase://localhost,db.example.com":      false,
		"":                                          false,
	} {
		require.Equal(t, want, IsLocalConnStr(cs), cs)
	}
}

func TestLoad_DevDefaultsDoNotApplyToCapella(t *testing.T) {
	_, err := Load(map[string]string{"OC_DEV": "true", "OC_META_CONNSTR": "couchbases://cb.abc.cloud.couchbase.com"})
	require.Error(t, err)
	for _, k := range []string{"OC_META_USERNAME", "OC_META_PASSWORD", "OC_MASTER_KEY"} {
		require.ErrorContains(t, err, k)
	}
	require.ErrorContains(t, err, "only apply to a local cluster")
}

func TestLoad_RefusesDevKeyForRemoteStore(t *testing.T) {
	e := prodEnv()
	e["OC_MASTER_KEY"] = DevMasterKey
	_, err := Load(e)
	require.ErrorContains(t, err, "published development key")
}

func TestLoad_CapellaDevModeWithRealValues(t *testing.T) {
	e := prodEnv()
	e["OC_DEV"] = "true"
	cfg, err := Load(e)
	require.NoError(t, err)
	require.Equal(t, "omni", cfg.Meta.Username, "explicit values are kept")
	require.False(t, cfg.Session.CookieSecure, "dev mode still allows plain-http cookies locally")
}

func TestLoad_Observability(t *testing.T) {
	cfg, err := Load(prodEnv())
	require.NoError(t, err)
	require.Empty(t, cfg.Observe.MetricsAddr, "metrics on the public port by default")
	require.Empty(t, cfg.Observe.OTLPEndpoint, "tracing off by default")
	require.InDelta(t, 0.1, cfg.Observe.SampleRatio, 0)

	e := prodEnv()
	e["OC_METRICS_ADDR"], e["OC_OTEL_EXPORTER_OTLP_ENDPOINT"], e["OC_OTEL_SAMPLE_RATIO"] = ":9090", "http://otel:4318", "1"
	cfg, err = Load(e)
	require.NoError(t, err)
	require.Equal(t, ":9090", cfg.Observe.MetricsAddr)
	require.Equal(t, "http://otel:4318", cfg.Observe.OTLPEndpoint)

	e["OC_OTEL_SAMPLE_RATIO"] = "1.5"
	_, err = Load(e)
	require.ErrorContains(t, err, "SampleRatio")
}

func TestLoad_SecretsFromFiles(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(dir, "master_key")
	pass := filepath.Join(dir, "meta_password")
	require.NoError(t, os.WriteFile(key, []byte("a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2U=\n"), 0o600))
	require.NoError(t, os.WriteFile(pass, []byte("from-a-file"), 0o600))
	e := prodEnv()
	delete(e, "OC_MASTER_KEY")
	delete(e, "OC_META_PASSWORD")
	e["OC_MASTER_KEY_FILE"], e["OC_META_PASSWORD_FILE"] = key, pass
	cfg, err := Load(e)
	require.NoError(t, err)
	require.Equal(t, "a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2U=", cfg.MasterKey, "trailing newline trimmed")
	require.Equal(t, "from-a-file", cfg.Meta.Password)

	e["OC_META_PASSWORD"] = "also-set"
	_, err = Load(e)
	require.ErrorContains(t, err, "OC_META_PASSWORD or OC_META_PASSWORD_FILE, not both")
	require.NotContains(t, err.Error(), "also-set")

	delete(e, "OC_META_PASSWORD")
	e["OC_META_PASSWORD_FILE"] = filepath.Join(dir, "missing")
	_, err = Load(e)
	require.ErrorContains(t, err, "OC_META_PASSWORD_FILE")
}

func TestLoad_EgressPolicy(t *testing.T) {
	cfg, err := Load(prodEnv())
	require.NoError(t, err)
	p, err := cfg.Guard.EgressPolicy()
	require.NoError(t, err)
	require.True(t, p.AllowPrivateNetworks, "self-hosted default")
	require.Equal(t, 365, cfg.Guard.AuditRetentionDays)

	e := prodEnv()
	e["OC_ALLOW_PRIVATE_NETWORKS"], e["OC_EGRESS_ALLOW_CIDRS"], e["OC_EGRESS_DENY_CIDRS"], e["OC_EGRESS_PORTS"] = "false", "10.20.0.0/16", "203.0.113.0/24", "5432,11207"
	cfg, err = Load(e)
	require.NoError(t, err)
	p, err = cfg.Guard.EgressPolicy()
	require.NoError(t, err)
	require.False(t, p.AllowPrivateNetworks)
	require.Len(t, p.Allow, 1)
	require.Len(t, p.Deny, 1)
	require.Len(t, p.Ports, 2)

	for k, v := range map[string]string{"OC_EGRESS_ALLOW_CIDRS": "10.0.0.0/99", "OC_EGRESS_DENY_CIDRS": "nope", "OC_EGRESS_PORTS": "0"} {
		e := prodEnv()
		e[k] = v
		_, err := Load(e)
		require.ErrorContains(t, err, k)
	}
}

// OC-072: the all-in-one image passes the bundled node's password as a file.
func TestLoad_BundledTarget_OC072(t *testing.T) {
	cfg, err := Load(prodEnv())
	require.NoError(t, err)
	require.Empty(t, cfg.Bundled.Target, "nothing bundled outside the appliance")

	pass := filepath.Join(t.TempDir(), "cb_admin_password")
	require.NoError(t, os.WriteFile(pass, []byte("generated\n"), 0o600))
	e := prodEnv()
	e["OC_BUNDLED_TARGET"], e["OC_BUNDLED_TARGET_PASSWORD_FILE"] = "couchbase://127.0.0.1", pass
	e["OC_BUNDLED_STORE"] = "Couchbase Server CE 8.0.2"
	cfg, err = Load(e)
	require.NoError(t, err)
	require.Equal(t, BundledConfig{
		Store: "Couchbase Server CE 8.0.2", Target: "couchbase://127.0.0.1", TargetUsername: "Administrator",
		TargetPassword: "generated", TargetBuckets: []string{"sandbox"},
	}, cfg.Bundled)
}

// OC-080: the all-in-one image names the folders shared with the user's computer.
func TestLoad_DirectoryShared_OC080(t *testing.T) {
	e := prodEnv()
	e["OC_DIRECTORY_SHARED"] = "/data=/Users/ana/omni-data-bus-output"
	cfg, err := Load(e)
	require.NoError(t, err)
	require.Equal(t, []string{"/data=/Users/ana/omni-data-bus-output"}, cfg.Directory.Shared)
	require.False(t, cfg.Directory.ContainerOnly)
}
