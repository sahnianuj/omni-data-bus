package export

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"flag"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/legal"
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

// OC-159 AC1: migrate.sh (no longer exported, kept for the 2.0 compatibility test until 3.0,
// OC-182) holds nothing about any export.
func TestScript_OC159_AC1_SameForEveryExport(t *testing.T) {
	cfg := testScriptConfig(t)
	gen, err := GenericScript(cfg, "2.0.0")
	require.NoError(t, err)
	script := string(gen)
	for _, s := range []string{"Shop", "orders", "/data/out", "__ODB_", "base64"} {
		require.NotContains(t, script, s)
	}
	for _, s := range []string{
		"ODB_SHA256_linux_amd64='" + strings.Repeat("a", 64) + "'", "ODB_SHA256_linux_arm64=''",
		"ODB_DOWNLOAD_URL='https://example.com/releases/v2.0.0'", `: "${ODB_CONFIG:=$HERE/config.json}"`, `: "${ODB_MAPPINGS:=$HERE/mappings.json}"`,
		`--mappings "$mappings" --config "$config" --state-dir "$state"`, `-w "$HERE" -v "$HERE:$HERE"`,
	} {
		require.Contains(t, script, s)
	}
}

// OC-182 AC1: the export is one folder with README.md, config.json and mappings.json, and
// nothing OS-specific: no script, no binary.
func TestExport_OC182_AC1_Files(t *testing.T) {
	cfg := testScriptConfig(t)
	f := newFixture(t)
	built := f.build(t, Request{CredentialMode: ModeConfig})
	b := built.Bundle
	raw := encode(t, b)
	art, err := RenderScript(context.Background(), b, raw, built.RunConfig, "", cfg, "2.0.0")
	require.NoError(t, err)
	require.Equal(t, "odb-export-shop-docs-20261001.zip", art.FileName)
	require.Empty(t, art.IncludedOdb)
	files, modes := unzipExport(t, art)
	require.True(t, modes[""].IsDir())
	require.Equal(t, os.FileMode(0o644), modes["README.md"])
	require.Equal(t, os.FileMode(0o600), modes["config.json"], "the credentials: owner only")
	require.Equal(t, os.FileMode(0o644), modes["mappings.json"], "no secrets")
	require.Len(t, modes, 4, "the folder and three files: no migrate.sh, no odb")
	require.Equal(t, raw, files["mappings.json"])
	require.Equal(t, built.RunConfig, files["config.json"])
	require.Empty(t, ScanForSecrets(files["mappings.json"], pgPassword, cbPassword))
}

// OC-182 AC2: without odb, even on a server with no downloads, nothing is added, and the README
// says how to get odb.
func TestExport_OC182_AC2_NoBinary(t *testing.T) {
	f := newFixture(t)
	built := f.build(t, Request{CredentialMode: ModeConfig})
	raw := encode(t, built.Bundle)
	self := runtime.GOOS + "_" + runtime.GOARCH
	dir := t.TempDir()
	writeTarball(t, filepath.Join(dir, "odb_2.0.0_"+self+".tar.gz"), "#!/bin/sh\necho fake odb\n")
	for name, cfg := range map[string]ScriptConfig{
		"no downloads": {Dist: Dist{Dir: dir, Version: "2.0.0"}, Image: "ghcr.io/example/odb:2.0.0"},
		"downloads":    testScriptConfig(t),
	} {
		art, err := RenderScript(context.Background(), built.Bundle, raw, built.RunConfig, "", cfg, "2.0.0")
		require.NoError(t, err, name)
		files, _ := unzipExport(t, art)
		require.Len(t, files, 3, name)
		readme := string(files["README.md"])
		require.Contains(t, readme, "odb preflight", name)
		require.Contains(t, readme, "docker run --rm -it", name)
		if name == "no downloads" {
			require.Contains(t, readme, "doesn't publish odb downloads", name)
		} else {
			require.Contains(t, readme, "| Linux x86-64 | [odb_2.0.0_linux_amd64.tar.gz](https://example.com/releases/v2.0.0/odb_2.0.0_linux_amd64.tar.gz) | `"+strings.Repeat("a", 64)+"` |", name)
			require.Contains(t, readme, "| macOS (Apple silicon) | [odb_2.0.0_darwin_arm64.tar.gz]", name)
			require.NotContains(t, readme, "Linux ARM64", "no checksum, no link")
		}
	}
}

// OC-182 AC3: odb for a machine without internet access goes in bin/, 0755.
func TestExport_OC182_AC3_OfflineBinary(t *testing.T) {
	cfg := testScriptConfig(t)
	f := newFixture(t)
	built := f.build(t, Request{CredentialMode: ModeConfig})
	self := runtime.GOOS + "_" + runtime.GOARCH
	art, err := RenderScript(context.Background(), built.Bundle, encode(t, built.Bundle), built.RunConfig, self, cfg, "2.0.0")
	require.NoError(t, err)
	require.Equal(t, self, art.IncludedOdb)
	files, modes := unzipExport(t, art)
	require.True(t, modes["bin/"].IsDir())
	require.Equal(t, os.FileMode(0o755), modes["bin/odb"])
	require.Equal(t, "#!/bin/sh\necho fake odb\n", string(files["bin/odb"]), "the binary itself, not compressed")
	readme := string(files["README.md"])
	require.Contains(t, readme, "| `bin/odb` | odb 2.0.0 for "+PlatformLabel(self)+", for a machine without internet access |")
	require.Contains(t, readme, "`bin/odb preflight`")

	_, err = RenderScript(context.Background(), built.Bundle, encode(t, built.Bundle), built.RunConfig, "windows_arm64", cfg, "2.0.0")
	require.ErrorContains(t, err, "no odb binary for windows_arm64")
}

// TestReadmeTemplate_ConfigFile: the README explains the files, config.json's base64
// passwords, the settings, and reads the connections from config.json.
func TestReadmeTemplate_ConfigFile(t *testing.T) {
	f := newFixture(t)
	built := f.build(t, Request{CredentialMode: ModeConfig})
	readme, err := RenderReadme(built.Bundle, built.RunConfig, "", testScriptConfig(t), "2.0.0")
	require.NoError(t, err)
	for _, s := range []string{
		"| `config.json` | **Where:**", "| `mappings.json` | **What:**", "Nothing in it is specific to one operating system",
		"encodes it and saves the file again", "| `--config` | `ODB_CONFIG` | `config.json` here |",
		"| `--mappings` | `ODB_MAPPINGS` | `mappings.json` here |", "is not encryption", "**Source:** PostgreSQL `migrator@db.internal",
		"This export needs odb 2.0.0 or a later 2.x release.",
	} {
		require.Contains(t, string(readme), s)
	}
	require.NotContains(t, string(readme), "ODB_BUNDLE_PASSPHRASE")
	require.NotContains(t, string(readme), "migrate.sh")
	require.NotContains(t, string(readme), "bin/odb", "no odb included")
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
	for _, s := range []string{"| orders | `public.orders` | `orders/` |", "Exit codes", "| 5 | Cancelled", "odb preflight", "JSON files go to `/data/out`"} {
		require.Contains(t, got, s)
	}
	require.NotContains(t, got, "chmod")
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

// OC-172 AC4: the README and migrate.sh name the release and the build that produced them.
func TestExport_OC172_AC4_VersionHeader(t *testing.T) {
	cfg := testScriptConfig(t)
	cfg.Build = "7f6cea2+418"
	f := newFixture(t)
	built := f.build(t, Request{CredentialMode: ModeConfig})
	readme, err := RenderReadme(built.Bundle, built.RunConfig, "", cfg, "2.5.0")
	require.NoError(t, err)
	require.Contains(t, string(readme), "with Omni-Data-Bus 2.5.0 (build 7f6cea2+418).")
	script, err := GenericScript(cfg, "2.5.0")
	require.NoError(t, err)
	require.Contains(t, string(script), "# Omni-Data-Bus migration runner, odb 2.5.0 (build 7f6cea2+418).")
	cfg.Build = ""
	script, err = GenericScript(cfg, "2.5.0")
	require.NoError(t, err)
	require.Contains(t, string(script), "odb 2.5.0.\n", "no build: the release only")
}

// OC-185 AC4: one macOS binary for every Mac, preferred over the per-architecture ones; a
// development build's README says its odb isn't signed, a release build's never does.
func TestExport_OC185_AC4_DarwinUniversal(t *testing.T) {
	dir := t.TempDir()
	writeTarball(t, filepath.Join(dir, "odb_2.5.0_darwin_universal.tar.gz"), "universal odb")
	writeTarball(t, filepath.Join(dir, "odb_2.5.0_darwin_arm64.tar.gz"), "arm64 odb")
	sums := strings.Repeat("u", 64) + "  odb_2.5.0_darwin_universal.tar.gz\n" + strings.Repeat("a", 64) + "  odb_2.5.0_darwin_arm64.tar.gz\n" +
		strings.Repeat("l", 64) + "  odb_2.5.0_linux_amd64.tar.gz\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(sums), 0o600))
	cfg := ScriptConfig{DownloadURL: "https://example.com/r", Dist: Dist{Dir: dir, Version: "2.5.0"}}

	for _, p := range []string{"darwin_arm64", "darwin_amd64", "darwin_universal"} {
		require.Equal(t, "darwin_universal", cfg.Dist.Preferred(p), p)
	}
	require.Equal(t, "linux_amd64", cfg.Dist.Preferred("linux_amd64"))
	require.Contains(t, cfg.Dist.Available(), "darwin_universal")

	f := newFixture(t)
	built := f.build(t, Request{CredentialMode: ModeConfig})
	render := func() (map[string][]byte, string) {
		art, err := RenderScript(context.Background(), built.Bundle, encode(t, built.Bundle), built.RunConfig, "darwin_universal", cfg, "2.5.0")
		require.NoError(t, err)
		files, _ := unzipExport(t, art)
		return files, string(files["README.md"])
	}
	files, readme := render()
	require.Equal(t, "universal odb", string(files["bin/odb"]))
	require.Contains(t, readme, "odb 2.5.0 for macOS (Intel or Apple silicon)")
	require.Contains(t, readme, "| macOS (Intel or Apple silicon) | [odb_2.5.0_darwin_universal.tar.gz]")
	require.NotContains(t, readme, "darwin_arm64.tar.gz", "one download for every Mac")
	require.Contains(t, readme, "xattr -d com.apple.quarantine bin/odb", "not signed: a development build")
	require.Empty(t, cfg.Dist.Signed())

	require.NoError(t, os.WriteFile(filepath.Join(dir, SignedFile), []byte("odb_2.5.0_darwin_universal.tar.gz\n"), 0o600))
	require.Equal(t, []string{"darwin_universal"}, cfg.Dist.Signed())
	_, readme = render()
	require.NotContains(t, readme, "xattr", "a release build's odb is notarized")
}

// OC-186: Windows odb comes from the release .zip and goes in as bin/odb.exe; the README says how
// to run it in PowerShell and links the zip.
func TestExport_OC186_WindowsZip(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("odb.exe")
	require.NoError(t, err)
	_, _ = w.Write([]byte("MZ fake odb.exe"))
	require.NoError(t, zw.Close())
	require.NoError(t, os.WriteFile(filepath.Join(dir, "odb_2.5.0_windows_amd64.zip"), buf.Bytes(), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(strings.Repeat("w", 64)+"  odb_2.5.0_windows_amd64.zip\n"), 0o600))
	cfg := ScriptConfig{DownloadURL: "https://example.com/r", Dist: Dist{Dir: dir, Version: "2.5.0"}}
	require.Contains(t, cfg.Dist.Available(), "windows_amd64")
	require.Equal(t, strings.Repeat("w", 64), cfg.Dist.Checksums()["windows_amd64"])

	f := newFixture(t)
	built := f.build(t, Request{CredentialMode: ModeConfig})
	art, err := RenderScript(context.Background(), built.Bundle, encode(t, built.Bundle), built.RunConfig, "windows_amd64", cfg, "2.5.0")
	require.NoError(t, err)
	files, modes := unzipExport(t, art)
	require.Equal(t, "MZ fake odb.exe", string(files["bin/odb.exe"]))
	require.Equal(t, os.FileMode(0o755), modes["bin/odb.exe"])
	readme := string(files["README.md"])
	require.Contains(t, readme, "| `bin/odb.exe` | odb 2.5.0 for Windows x86-64, for a machine without internet access |")
	require.Contains(t, readme, "`.\\bin\\odb.exe run`")
	require.Contains(t, readme, "| Windows x86-64 | [odb_2.5.0_windows_amd64.zip](https://example.com/r/odb_2.5.0_windows_amd64.zip)")
	require.Contains(t, readme, "Expand-Archive odb_2.5.0_windows_amd64.zip")
	require.Contains(t, readme, "Windows SmartScreen", "not in SIGNED: a development build")
}

// OC-184 AC4: no stale instructions. The UI strings, the export README and the user guide talk
// about the odb commands; only the guide's "Exports from 2.0" section mentions migrate.sh.
func TestDocs_OC184_AC4_NoMigrateSh(t *testing.T) {
	read := func(p string) string {
		raw, err := os.ReadFile(filepath.Join("..", "..", "..", p))
		require.NoError(t, err, p)
		return string(raw)
	}
	guide := read("docs/user-guide/running-outside-the-tool.md")
	if i := strings.Index(guide, "## Exports from 2.0"); i >= 0 {
		guide = guide[:i]
	}
	for name, text := range map[string]string{
		"web/src/i18n/en.json":                    read("web/src/i18n/en.json"),
		"README.md.tmpl":                          read("backend/internal/export/templates/README.md.tmpl"),
		"running-outside-the-tool.md (2.5 parts)": guide,
	} {
		require.NotContains(t, text, "migrate.sh", name)
	}
}

// OC-189 AC4: an export with odb carries odb's licence, NOTICE and its dependencies' notices.
func TestExport_OC189_AC4_OdbLicenses(t *testing.T) {
	f := newFixture(t)
	built := f.build(t, Request{CredentialMode: ModeConfig})
	art, err := RenderScript(context.Background(), built.Bundle, encode(t, built.Bundle), built.RunConfig, runtime.GOOS+"_"+runtime.GOARCH, testScriptConfig(t), "2.0.0")
	require.NoError(t, err)
	files, modes := unzipExport(t, art)
	require.True(t, modes["bin/LICENSES/"].IsDir())
	require.Equal(t, string(legal.License), string(files["bin/LICENSES/LICENSE"]))
	require.Contains(t, string(files["bin/LICENSES/NOTICE"]), "Copyright 2026 Couchbase, Inc.")
	require.Contains(t, string(files["bin/LICENSES/THIRD_PARTY_NOTICES.md"]), "# Third-party notices")
	require.Contains(t, string(files["README.md"]), "| `bin/LICENSES/` |")

	// Without odb, the folder holds only what the migration needs.
	art, err = RenderScript(context.Background(), built.Bundle, encode(t, built.Bundle), built.RunConfig, "", testScriptConfig(t), "2.0.0")
	require.NoError(t, err)
	files, _ = unzipExport(t, art)
	require.NotContains(t, files, "bin/LICENSES/LICENSE")
}
