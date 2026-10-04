package main

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/export"
	"omnidatabus/internal/export/exporttest"
)

func runCLI(args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := run(args, &out, &errb)
	return code, out.String(), errb.String()
}

func TestBundleValidate_AC2_Tampered(t *testing.T) {
	dir := t.TempDir()
	path := exporttest.Write(t, dir, "b.odb.json", exporttest.Bundle(t, exporttest.Options{}))
	code, out, stderr := runCLI("bundle", "validate", path)
	require.Equal(t, 0, code, stderr)
	require.Contains(t, out, "OK:")

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	tampered := strings.Replace(string(raw), `"template": "orders::{id}"`, `"template": "orders::{status}"`, 1)
	require.NotEqual(t, string(raw), tampered)
	require.NoError(t, os.WriteFile(path, []byte(tampered), 0o600))
	code, _, stderr = runCLI("bundle", "validate", path)
	require.Equal(t, 4, code)
	require.Contains(t, stderr, export.CodeIntegrity)

	// Re-indenting doesn't break integrity: it covers the canonical form.
	var v any
	require.NoError(t, json.Unmarshal(raw, &v))
	compact, _ := json.Marshal(v)
	require.NoError(t, os.WriteFile(path, compact, 0o600))
	code, _, stderr = runCLI("bundle", "validate", path)
	require.Equal(t, 0, code, stderr)

	// Not a bundle at all.
	require.NoError(t, os.WriteFile(path, []byte(`{"hello":1}`), 0o600))
	code, _, stderr = runCLI("bundle", "validate", path)
	require.Equal(t, 4, code)
	require.Contains(t, stderr, export.CodeInvalid)
}

func TestBundleValidate_AC6_Versions(t *testing.T) {
	dir := t.TempDir()
	b := exporttest.Bundle(t, exporttest.Options{})
	b.BundleVersion = 2
	require.NoError(t, b.Finalize())
	code, _, stderr := runCLI("bundle", "validate", exporttest.Write(t, dir, "v2.odb.json", b))
	require.Equal(t, 4, code)
	require.Contains(t, stderr, export.CodeVersionUnsupported)
	require.Contains(t, stderr, "format version 2")

	// The range check, against an explicit running version.
	require.NoError(t, export.CheckVersion(1, ">=2.0.0 <3.0.0", "2.3.1"))
	require.NoError(t, export.CheckVersion(1, ">=2.0.0 <3.0.0", "2.1.0-rc.1"))
	err := export.CheckVersion(1, ">=2.1.0 <3.0.0", "2.0.4")
	require.ErrorContains(t, err, export.CodeVersionUnsupported)
	require.ErrorContains(t, err, ">=2.1.0 <3.0.0")
	require.ErrorContains(t, err, "2.0.4")
	require.Error(t, export.CheckVersion(1, ">=2.0.0 <3.0.0", "3.0.0"))
	require.NoError(t, export.CheckVersion(1, ">=2.0.0 <3.0.0", "dev"), "development builds run any bundle of a known format")
	require.Equal(t, ">=2.1.0 <3.0.0", export.RangeFor("v2.1.5"))
	require.Equal(t, "*", export.RangeFor("2.0.0-14-gabc-dirty"))
}

func TestBundleInspect_NoSecrets(t *testing.T) {
	dir := t.TempDir()
	b := exporttest.Bundle(t, exporttest.Options{SourceSecrets: export.Secrets{"password": "pg-very-secret-1"}, Passphrase: "correct-horse-battery-staple"})
	code, out, stderr := runCLI("bundle", "inspect", exporttest.Write(t, dir, "b.odb.json", b))
	require.Equal(t, 0, code, stderr)
	require.Contains(t, out, "PostgreSQL omni_ro@localhost:5432/shop")
	require.Contains(t, out, "sealed (passphrase required)")
	require.Contains(t, out, "orders")
	require.Contains(t, out, "public.orders → orders/")
	require.Contains(t, out, b.SHA256())
	require.Empty(t, export.ScanForSecrets([]byte(out+stderr), "pg-very-secret-1"))
}

func TestBundleDiff_Semantic(t *testing.T) {
	dir := t.TempDir()
	a := exporttest.Bundle(t, exporttest.Options{})
	o := exporttest.Bundle(t, exporttest.Options{}).JobOptions.Jobs()
	o.BatchSize = 500
	spec := strings.Replace(exporttest.OrdersSpec, `orders::{id}`, `order::{id}`, 1)
	b := exporttest.Bundle(t, exporttest.Options{TargetPath: "/srv/out", Options: &o, Mappings: append(a.Mappings[:0:0], a.Mappings[0], export.Mapping{
		ID: "map-x", Name: "x", Source: export.TableRef{Schema: "public", Table: "x"}, Location: a.Mappings[0].Location, Spec: a.Mappings[0].Spec, PlanHash: a.Mappings[0].PlanHash,
	})})
	b.Mappings[0].Spec = json.RawMessage(spec)
	require.NoError(t, b.Finalize())
	code, out, stderr := runCLI("bundle", "diff", exporttest.Write(t, dir, "a.odb.json", a), exporttest.Write(t, dir, "b.odb.json", b))
	require.Equal(t, 0, code, stderr)
	require.Contains(t, out, "~ target: Directory /tmp/out (layout flat) → Directory /srv/out (layout flat)")
	require.Contains(t, out, `~ mapping orders: key "orders::{id}" → "order::{id}"`)
	require.Contains(t, out, "+ mapping x (public.x → orders/)")
	require.Contains(t, out, "~ option batchSize: 1000 → 500")

	code, out, _ = runCLI("bundle", "diff", dir+"/a.odb.json", dir+"/a.odb.json")
	require.Equal(t, 0, code)
	require.Contains(t, out, "No differences")
}

// TestRun_IgnoresDotEnv: odb run and odb bundle never read the server's .env (in a container
// the working directory's .env may be unreadable, or someone else's).
func TestRun_IgnoresDotEnv(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(dir+"/.env", []byte("OC_MASTER_KEY=x\n"), 0o000))
	t.Chdir(dir)
	code, out, stderr := runCLI("run", "status", "--state-dir", dir+"/state")
	require.Equal(t, 4, code)
	require.Contains(t, out, "No run in")
	require.NotContains(t, stderr, ".env")
	code, _, stderr = runCLI("bundle", "validate")
	require.Equal(t, 4, code, "usage errors are configuration errors (4), never 2")
	require.Contains(t, stderr, "usage")
}
