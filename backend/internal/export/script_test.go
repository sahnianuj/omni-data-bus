package export

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"flag"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

var update = flag.Bool("update", false, "rewrite golden files")

func testScriptConfig(t *testing.T) ScriptConfig {
	t.Helper()
	dir := t.TempDir()
	sums := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  odb_2.0.0_linux_amd64.tar.gz\n" +
		"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb  odb_2.0.0_darwin_arm64.tar.gz\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(sums), 0o600))
	// A small stand-in for this platform's release tarball (only its bytes are embedded).
	writeTarball(t, filepath.Join(dir, "odb_2.0.0_"+runtime.GOOS+"_"+runtime.GOARCH+".tar.gz"), "#!/bin/sh\necho fake odb\n")
	return ScriptConfig{
		Image: "ghcr.io/example/omni-data-bus@sha256:" + strings.Repeat("c", 64), DownloadURL: "https://example.com/releases/v2.0.0/",
		CosignIdentity: "^https://github.com/example/omni-data-bus/", Dist: Dist{Dir: dir, Version: "2.0.0"},
	}
}

func shellcheckPath(t *testing.T) string {
	if p := os.Getenv("SHELLCHECK"); p != "" {
		return p
	}
	if p, err := exec.LookPath("shellcheck"); err == nil {
		return p
	}
	if os.Getenv("CI") != "" {
		t.Fatal("shellcheck is required in CI")
	}
	return ""
}

// unzipExport returns the export's files (without the folder prefix) and their modes.
func unzipExport(t *testing.T, art Artifact) (map[string][]byte, map[string]os.FileMode) {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(art.Body), int64(len(art.Body)))
	require.NoError(t, err)
	files, modes := map[string][]byte{}, map[string]os.FileMode{}
	folder := strings.TrimSuffix(art.FileName, ".zip") + "/"
	for _, zf := range zr.File {
		name, ok := strings.CutPrefix(zf.Name, folder)
		require.True(t, ok, "%s is inside %s", zf.Name, folder)
		modes[name] = zf.Mode()
		if zf.Mode().IsDir() {
			continue
		}
		rc, err := zf.Open()
		require.NoError(t, err)
		data, _ := io.ReadAll(rc)
		_ = rc.Close()
		files[name] = data
	}
	return files, modes
}

func TestScriptTemplate_AC2_ShellcheckAllVariants(t *testing.T) {
	sc := shellcheckPath(t)
	path := filepath.Join(t.TempDir(), "migrate.sh")
	for name, cfg := range map[string]ScriptConfig{"release": testScriptConfig(t), "no downloads": {}} {
		script, err := GenericScript(cfg, "2.0.0")
		require.NoError(t, err, name)
		require.NoError(t, os.WriteFile(path, script, 0o755))
		out, err := exec.Command("bash", "-n", path).CombinedOutput()
		require.NoError(t, err, "%s: bash -n: %s", name, out)
		if sc != "" {
			out, err := exec.Command(sc, "-s", "bash", "-S", "style", path).CombinedOutput()
			require.NoError(t, err, "%s: shellcheck:\n%s", name, out)
		}
	}
	if sc == "" {
		t.Log("shellcheck not installed: only bash -n ran (set SHELLCHECK or install it)")
	}
}

// OC-159 AC1: every export from one release has the same migrate.sh, with nothing about the
// export in it.
func TestScript_OC159_AC1_SameForEveryExport(t *testing.T) {
	cfg := testScriptConfig(t)
	f := newFixture(t)
	cb := f.build(t, Request{CredentialMode: ModeConfig})
	f.toDirectory(t, "/data/out")
	dir := f.build(t, Request{CredentialMode: ModeConfig})
	a, err := RenderScript(cb.Bundle, encode(t, cb.Bundle), cb.RunConfig, "", cfg, "2.0.0")
	require.NoError(t, err)
	b, err := RenderScript(dir.Bundle, encode(t, dir.Bundle), dir.RunConfig, runtime.GOOS+"_"+runtime.GOARCH, cfg, "2.0.0")
	require.NoError(t, err)
	fa, _ := unzipExport(t, a)
	fb, _ := unzipExport(t, b)
	require.Equal(t, string(fa["migrate.sh"]), string(fb["migrate.sh"]))
	script := string(fa["migrate.sh"])
	for _, s := range []string{"Shop", "orders", "/data/out", cb.Bundle.SHA256(), dir.Bundle.SHA256(), "__ODB_", "base64"} {
		require.NotContains(t, script, s)
	}
	require.Empty(t, ScanForSecrets(fa["migrate.sh"], pgPassword, cbPassword))
	for _, s := range []string{
		"ODB_SHA256_linux_amd64='" + strings.Repeat("a", 64) + "'", "ODB_SHA256_linux_arm64=''",
		"ODB_DOWNLOAD_URL='https://example.com/releases/v2.0.0'", `: "${ODB_CONFIG:=$HERE/config.json}"`, `: "${ODB_MAPPINGS:=$HERE/mappings.json}"`,
		`--mappings "$mappings" --config "$config" --state-dir "$state"`, `-w "$HERE" -v "$HERE:$HERE"`,
	} {
		require.Contains(t, script, s)
	}
	gen, err := GenericScript(cfg, "2.0.0")
	require.NoError(t, err)
	require.Equal(t, string(gen), script)
}

// OC-160 AC1: one folder with migrate.sh, README.md, config.json and mappings.json, plus odb
// when it's included.
func TestScript_ZipContents(t *testing.T) {
	cfg := testScriptConfig(t)
	f := newFixture(t)
	built := f.build(t, Request{CredentialMode: ModeConfig})
	b := built.Bundle
	raw := encode(t, b)
	art, err := RenderScript(b, raw, built.RunConfig, "", cfg, "2.0.0")
	require.NoError(t, err)
	require.Equal(t, "odb-export-shop-docs-20261001.zip", art.FileName)
	files, modes := unzipExport(t, art)
	require.True(t, modes[""].IsDir())
	require.Equal(t, os.FileMode(0o755), modes["migrate.sh"])
	require.Equal(t, os.FileMode(0o644), modes["README.md"])
	require.Equal(t, os.FileMode(0o600), modes["config.json"], "the credentials: owner only")
	require.Equal(t, os.FileMode(0o644), modes["mappings.json"], "no secrets")
	require.Len(t, modes, 5)
	require.Equal(t, raw, files["mappings.json"])
	require.Equal(t, built.RunConfig, files["config.json"])
	require.Empty(t, ScanForSecrets(files["mappings.json"], pgPassword, cbPassword))

	self := runtime.GOOS + "_" + runtime.GOARCH
	art, err = RenderScript(b, raw, built.RunConfig, self, cfg, "2.0.0")
	require.NoError(t, err)
	files, modes = unzipExport(t, art)
	require.Equal(t, os.FileMode(0o755), modes["odb"])
	require.Equal(t, "#!/bin/sh\necho fake odb\n", string(files["odb"]), "the binary itself, not compressed")
	require.Contains(t, string(files["README.md"]), "| `odb` | odb 2.0.0 for "+self)

	_, err = RenderScript(b, raw, built.RunConfig, "windows_amd64", cfg, "2.0.0")
	require.ErrorContains(t, err, "no odb binary for windows_amd64")
}

// TestReadmeTemplate_ConfigFile: the README explains the three files, config.json's base64
// passwords, ODB_CONFIG and ODB_MAPPINGS, and reads the connections from config.json.
func TestReadmeTemplate_ConfigFile(t *testing.T) {
	f := newFixture(t)
	built := f.build(t, Request{CredentialMode: ModeConfig})
	readme, err := RenderReadme(built.Bundle, built.RunConfig, "", testScriptConfig(t), "2.0.0")
	require.NoError(t, err)
	for _, s := range []string{
		"| `config.json` | **Where:**", "| `mappings.json` | **What:**", "The same script for every export",
		"encodes it and saves the file again", "| `ODB_CONFIG` | `config.json` in this folder |",
		"| `ODB_MAPPINGS` | `mappings.json` in this folder |", "is not encryption", "**Source:** PostgreSQL `migrator@db.internal",
		"odb run --mappings mappings.json --config config.json",
	} {
		require.Contains(t, string(readme), s)
	}
	require.NotContains(t, string(readme), "ODB_BUNDLE_PASSPHRASE")
	require.NotContains(t, string(readme), "| `odb` |", "no odb included")
}

func TestReadmeTemplate_AC7_Golden(t *testing.T) {
	cfg := testScriptConfig(t)
	f := newFixture(t)
	f.toDirectory(t, "/data/out")
	built := f.build(t, Request{CredentialMode: ModeConfig})
	readme, err := RenderReadme(built.Bundle, built.RunConfig, "", cfg, "2.0.0")
	require.NoError(t, err)
	got := strings.ReplaceAll(string(readme), built.Bundle.SHA256(), "<sha256>")
	golden := filepath.Join("testdata", "README.golden.md")
	if *update {
		require.NoError(t, os.WriteFile(golden, []byte(got), 0o600))
	}
	want, err := os.ReadFile(golden)
	require.NoError(t, err)
	require.Equal(t, string(want), got)
	for _, s := range []string{"| orders | `public.orders` | `orders/` |", "Exit codes", "| 5 | Cancelled", "./migrate.sh preflight", "JSON files go to `/data/out`"} {
		require.Contains(t, got, s)
	}
	require.NotContains(t, got, "chmod")
}

// With no release checksums (a self-built server), every export includes this server's odb,
// so the script runs as is; with downloads available or the setting off, none is included.
func TestScript_IncludesOdbWhenNothingDownloads(t *testing.T) {
	f := newFixture(t)
	built := f.build(t, Request{CredentialMode: ModeConfig})
	raw := encode(t, built.Bundle)
	self := runtime.GOOS + "_" + runtime.GOARCH
	dir := t.TempDir()
	writeTarball(t, filepath.Join(dir, "odb_2.0.0_"+self+".tar.gz"), "#!/bin/sh\necho fake odb\n")
	noDownload := ScriptConfig{DownloadURL: "https://example.com/r", Dist: Dist{Dir: dir, Version: "2.0.0"}, EmbedWhenNoDownload: true}

	art, err := RenderScript(built.Bundle, raw, built.RunConfig, "", noDownload, "2.0.0")
	require.NoError(t, err)
	files, _ := unzipExport(t, art)
	require.Contains(t, files, "odb")

	for name, cfg := range map[string]ScriptConfig{
		"off":          {Dist: noDownload.Dist},
		"downloadable": func() ScriptConfig { c := testScriptConfig(t); c.EmbedWhenNoDownload = true; return c }(),
	} {
		art, err := RenderScript(built.Bundle, raw, built.RunConfig, "", cfg, "2.0.0")
		require.NoError(t, err, name)
		files, _ := unzipExport(t, art)
		require.NotContains(t, files, "odb", name)
	}
}

func writeTarball(t *testing.T, path, odb string) {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "odb", Mode: 0o755, Size: int64(len(odb))}))
	_, err := tw.Write([]byte(odb))
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	require.NoError(t, zw.Close())
	require.NoError(t, os.WriteFile(path, buf.Bytes(), 0o600))
}
