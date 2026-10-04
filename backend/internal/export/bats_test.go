package export_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/export"
	"omnidatabus/internal/export/exporttest"
	"omnidatabus/internal/store"
)

// stubOdb is a fake odb: it answers `version --json` and logs "NAME args…" for anything else,
// then sleeps STUB_SLEEP and exits STUB_EXIT.
func stubOdb(name, version string) string {
	return fmt.Sprintf(`#!/usr/bin/env bash
if [ "${1:-}" = version ]; then printf '{"version": "%s"}\n'; exit 0; fi
echo "%s $*" >>"${STUB_LOG:-/dev/null}"
if [ -n "${STUB_SLEEP:-}" ]; then sleep "$STUB_SLEEP"; fi
exit "${STUB_EXIT:-0}"
`, version, name)
}

func tarball(t *testing.T, odb string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "odb", Mode: 0o755, Size: int64(len(odb))}))
	_, err := tw.Write([]byte(odb))
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

func batsPath(t *testing.T) string {
	if p := os.Getenv("BATS"); p != "" {
		return p
	}
	if p, err := exec.LookPath("bats"); err == nil {
		return p
	}
	// Only the jobs that exist to run these suites (make test-scripts) require bats; plain
	// backend-test in CI skips them.
	if os.Getenv("ODB_REQUIRE_BATS") != "" {
		t.Fatal("bats is required (ODB_REQUIRE_BATS is set)")
	}
	t.Skip("bats isn't installed (set BATS or install bats-core)")
	return ""
}

// TestMigrateScript_Bats runs the migrate.sh suites (OC-053 AC1, AC3–AC6) with generated scripts,
// stub binaries and a local download server. Set ODB_TEST_BASH to run them under another bash (3.2 on
// macOS, 5 on Linux).
func TestMigrateScript_Bats(t *testing.T) {
	bats := batsPath(t)
	dir := t.TempDir()
	plat := runtime.GOOS + "_" + runtime.GOARCH
	name := "odb_2.0.0_" + plat + ".tar.gz"

	stubs := filepath.Join(dir, "stubs")
	require.NoError(t, os.MkdirAll(stubs, 0o755))
	for file, body := range map[string]string{
		"odb-bin": stubOdb("bin", "2.0.0"), "odb-path": stubOdb("path", "2.0.0"), "odb-old": stubOdb("old", "1.9.0"),
		"docker": "#!/usr/bin/env bash\necho \"docker $*\" >>\"${STUB_LOG:-/dev/null}\"\nexit \"${STUB_EXIT:-0}\"\n",
	} {
		require.NoError(t, os.WriteFile(filepath.Join(stubs, file), []byte(body), 0o755)) //nolint:gosec // G306: test stubs must be executable
	}

	// The download server: /good serves the real tarball, /bad a tampered one.
	good := tarball(t, stubOdb("download", "2.0.0"))
	sum := sha256.Sum256(good)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/good/" + name:
			_, _ = w.Write(good)
		case "/bad/" + name:
			_, _ = w.Write(append(append([]byte(nil), good...), 0))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	distDownload := filepath.Join(dir, "dist-download")
	require.NoError(t, os.MkdirAll(distDownload, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(distDownload, "SHA256SUMS"), []byte(hex.EncodeToString(sum[:])+"  "+name+"\n"), 0o600))
	distEmbedded := filepath.Join(dir, "dist-embedded")
	require.NoError(t, os.MkdirAll(distEmbedded, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(distEmbedded, name), tarball(t, stubOdb("embedded", "2.0.0")), 0o600))

	b := exporttest.Bundle(t, exporttest.Options{TargetPath: "/data/out"})
	raw, err := export.Encode(b)
	require.NoError(t, err)
	write := func(file string, s export.Script, cfg export.ScriptConfig) string {
		script, _, err := export.RenderScriptFiles(b, raw, s, cfg, "2.0.0")
		require.NoError(t, err)
		p := filepath.Join(dir, file)
		require.NoError(t, os.WriteFile(p, script, 0o755)) //nolint:gosec // G306: the script must be executable
		return p
	}
	cfg := func(url, distDir string) export.ScriptConfig {
		return export.ScriptConfig{Image: "omni-data-bus:2.0.0", DownloadURL: url, Dist: export.Dist{Dir: distDir, Version: "2.0.0"}}
	}
	env := append(os.Environ(),
		"SCRIPT_EMBEDDED="+write("embedded.sh", export.Script{Runtime: "auto", OS: runtime.GOOS, Arch: runtime.GOARCH}, cfg(srv.URL+"/good", distEmbedded)),
		"SCRIPT_PLAIN="+write("plain.sh", export.Script{Runtime: "auto"}, cfg(srv.URL+"/good", distDownload)),
		"SCRIPT_TAMPERED="+write("tampered.sh", export.Script{Runtime: "binary"}, cfg(srv.URL+"/bad", distDownload)),
		"STUBS="+stubs, "BUNDLE_SHA="+b.SHA256(),
	)
	env = append(env, endToEndEnv(t, dir)...)

	args := []string{"--tap"}
	if f := os.Getenv("ODB_TEST_BATS_FILTER"); f != "" {
		args = append(args, "--filter", f) // e.g. "docker runtime"
	}
	for _, f := range []string{"migrate_runtime.bats", "migrate_payload.bats", "migrate_run.bats"} {
		args = append(args, filepath.Join("testdata", "bats", f))
	}
	cmd := exec.Command(bats, args...)
	if sh := os.Getenv("ODB_TEST_BASH"); sh != "" {
		// bats runs the scripts through their #!/usr/bin/env bash: put the chosen bash first.
		link := filepath.Join(dir, "bash-bin")
		require.NoError(t, os.MkdirAll(link, 0o755))
		require.NoError(t, os.Symlink(sh, filepath.Join(link, "bash")))
		env = append(env, "PATH="+link+":"+os.Getenv("PATH"))
	}
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	t.Log("\n" + string(out))
	require.NoError(t, err)
}

// endToEndEnv builds a real sealed script for shop.orders on the dev stack's PostgreSQL, and
// the odb binary to run it (AC1). Without OC_IT_PG_HOST it returns nothing and the AC1 tests skip.
func endToEndEnv(t *testing.T, dir string) []string {
	t.Helper()
	host := os.Getenv("OC_IT_PG_HOST")
	if host == "" {
		return nil
	}
	port := 5432
	if v := os.Getenv("OC_IT_PG_PORT"); v != "" {
		port, _ = strconv.Atoi(v)
	}
	src, _ := json.Marshal(map[string]any{"host": host, "port": port, "database": "shop", "username": "shop_owner", "sslMode": "disable"})
	spec := `{"specVersion":2,"mode":"canvas","docSchema":{"root":{"id":"root","name":"","type":"object","children":[` +
		`{"id":"f1","name":"orderId","type":"integer","required":true},{"id":"f2","name":"status","type":"string"}]},"source":"generated"},` +
		`"key":{"template":"orders::{order_id}"},"typeField":{"enabled":true,"name":"type","value":"order"},"nullHandling":"include","fieldMappings":[` +
		`{"id":"m1","targetFieldId":"f1","sources":[{"column":"order_id"}],"transforms":[],"origin":"auto","state":"active"},` +
		`{"id":"m2","targetFieldId":"f2","sources":[{"column":"status"}],"transforms":[],"origin":"auto","state":"active"}],` +
		`"relations":[],"aggregates":[],"ignoredColumns":[]}`
	const pass = "correct-horse-battery-staple-orbit"
	b := exporttest.Bundle(t, exporttest.Options{
		Source: src, SourceSecrets: export.Secrets{"password": "shop_owner"}, Passphrase: pass, TargetPath: "/data/out",
		Mappings: []export.Mapping{{
			ID: "map-orders", Name: "orders", Source: export.TableRef{Schema: "shop", Table: "orders"},
			Location: store.Location{Kind: store.KindDirectory, Folder: "orders", CreateIfMissing: true},
			Spec:     json.RawMessage(spec), PlanHash: "0000000000000000000000000000000000000000000000000000000000000000",
		}},
	})
	raw, err := export.Encode(b)
	require.NoError(t, err)
	script, _, err := export.RenderScriptFiles(b, raw, export.Script{Runtime: "auto"}, export.ScriptConfig{Image: os.Getenv("ODB_IT_IMAGE")}, "dev")
	require.NoError(t, err)
	p := filepath.Join(dir, "e2e.sh")
	require.NoError(t, os.WriteFile(p, script, 0o755)) //nolint:gosec // G306: the script must be executable
	bin := filepath.Join(dir, "odb-real", "odb")
	build := exec.Command("go", "build", "-o", bin, "omnidatabus/cmd/odb")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	out, err := build.CombinedOutput()
	require.NoError(t, err, string(out))
	env := []string{"IT_SCRIPT=" + p, "IT_ODB=" + bin, "IT_PASSPHRASE=" + pass, "IT_ROWS=10000"}
	if docker, err := exec.LookPath("docker"); err == nil && os.Getenv("ODB_IT_IMAGE") != "" {
		env = append(env, "IT_SCRIPT_DOCKER="+p, "IT_DOCKER="+docker)
	}
	return env
}
