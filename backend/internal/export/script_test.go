package export

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"flag"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/store"
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

// variants builds every runtime × credential mode × target kind.
func variants(t *testing.T) map[string]struct {
	b      Bundle
	raw    []byte
	script Script
} {
	t.Helper()
	f := newFixture(t)
	out := map[string]struct {
		b      Bundle
		raw    []byte
		script Script
	}{}
	for _, kind := range []string{store.KindCouchbase, store.KindDirectory} {
		if kind == store.KindDirectory {
			f.toDirectory(t, "/data/out dir")
		}
		for _, mode := range []string{ModeSealed, ModePlaintext, ModeExternal, ModeConfig} {
			req := Request{CredentialMode: mode, Passphrase: passphrase}
			b := f.build(t, req).Bundle
			raw := encode(t, b)
			for _, rt := range []string{"auto", "binary", "docker", "embedded"} {
				s := Script{Runtime: rt}
				if rt == "embedded" {
					s.OS, s.Arch = runtime.GOOS, runtime.GOARCH
				}
				out[mode+"/"+kind+"/"+rt] = struct {
					b      Bundle
					raw    []byte
					script Script
				}{b, raw, s}
			}
		}
	}
	return out
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

func TestScriptTemplate_AC2_ShellcheckAllVariants(t *testing.T) {
	cfg := testScriptConfig(t)
	sc := shellcheckPath(t)
	dir := t.TempDir()
	for name, v := range variants(t) {
		script, _, err := RenderScriptFiles(v.b, v.raw, v.script, cfg, "2.0.0")
		require.NoError(t, err, name)
		path := filepath.Join(dir, strings.ReplaceAll(name, "/", "_")+".sh")
		require.NoError(t, os.WriteFile(path, script, 0o755))
		out, err := exec.Command("bash", "-n", path).CombinedOutput()
		require.NoError(t, err, "%s: bash -n: %s", name, out)
		if sc != "" {
			out, err := exec.Command(sc, "-s", "bash", "-S", "style", path).CombinedOutput()
			require.NoError(t, err, "%s: shellcheck:\n%s", name, out)
		}
		if v.b.Source.Credentials.Mode != ModePlaintext {
			require.Empty(t, ScanForSecrets(script, pgPassword, cbPassword, passphrase), name)
		} else {
			require.Contains(t, string(script), "# WARNING: contains plaintext database credentials", name)
		}
	}
	if sc == "" {
		t.Log("shellcheck not installed: only bash -n ran (set SHELLCHECK or install it)")
	}
}

func TestScript_HeaderAndPayload(t *testing.T) {
	cfg := testScriptConfig(t)
	f := newFixture(t)
	cb := f.build(t, externalReq()).Bundle
	f.toDirectory(t, "/data/out")
	b := f.build(t, externalReq()).Bundle
	raw := encode(t, b)
	script, _, err := RenderScriptFiles(b, raw, Script{}, cfg, "2.0.0")
	cbScript, _, err2 := RenderScriptFiles(cb, encode(t, cb), Script{}, cfg, "2.0.0")
	require.NoError(t, err2)
	require.Contains(t, string(cbScript), "-e ODB_SOURCE_PASSWORD -e ODB_TARGET_PASSWORD", "external env refs reach the container")
	require.NoError(t, err)
	s := string(script)
	require.True(t, strings.HasPrefix(s, "#!/usr/bin/env bash\n"))
	require.Contains(t, s, "\n# Omni-Data-Bus migration: Shop → docs — exported 2026-10-01T10:00:00Z by user@example.com\n")
	require.Contains(t, s, "bundle sha256: "+b.SHA256())
	require.Contains(t, s, "ODB_SHA256_linux_amd64='"+strings.Repeat("a", 64)+"'")
	require.Contains(t, s, "ODB_SHA256_linux_arm64=''")
	require.Contains(t, s, `: "${ODB_TARGET_PATH:=/data/out}"`)
	require.Contains(t, s, "-e ODB_BUNDLE_PASSPHRASE -e ODB_SOURCE_PASSWORD)", "a directory has no credentials")
	require.Contains(t, s, "ODB_DOWNLOAD_URL='https://example.com/releases/v2.0.0'")
	// The payload decodes to the bundle exactly.
	_, payload, ok := strings.Cut(s, "\n__ODB_BUNDLE_BELOW__\n")
	require.True(t, ok)
	b64, _, _ := strings.Cut(payload, "\n__ODB_BINARY_BELOW__\n")
	got, err := decodeWrapped(b64)
	require.NoError(t, err)
	require.Equal(t, raw, got)
	require.True(t, strings.HasSuffix(s, "__ODB_BINARY_BELOW__\n\n__ODB_PAYLOAD_END__\n"), "no embedded binary")
}

func TestScript_ZipContents(t *testing.T) {
	cfg := testScriptConfig(t)
	f := newFixture(t)
	built := f.build(t, Request{CredentialMode: ModeConfig})
	b := built.Bundle
	raw := encode(t, b)
	art, err := RenderScript(b, raw, built.RunConfig, Script{IncludeBundle: true}, cfg, "2.0.0")
	require.NoError(t, err)
	require.Equal(t, "odb-export-shop-docs-20261001.zip", art.FileName)
	zr, err := zip.NewReader(bytes.NewReader(art.Body), int64(len(art.Body)))
	require.NoError(t, err)
	names := map[string]os.FileMode{}
	for _, zf := range zr.File {
		names[zf.Name] = zf.Mode()
	}
	// Everything sits in one folder named like the zip.
	const dir = "odb-export-shop-docs-20261001/"
	require.True(t, names[dir].IsDir())
	require.Equal(t, os.FileMode(0o755), names[dir+"migrate.sh"])
	require.Contains(t, names, dir+"README.md")
	require.Contains(t, names, dir+"bundle.odb.json")
	require.Equal(t, os.FileMode(0o600), names[dir+"config.json"], "the credentials: owner only")
	require.Len(t, names, 5)
	for _, zf := range zr.File {
		rc, _ := zf.Open()
		data, _ := io.ReadAll(rc)
		_ = rc.Close()
		switch zf.Name {
		case dir + "bundle.odb.json":
			require.Equal(t, raw, data)
		case dir + "config.json":
			require.Equal(t, built.RunConfig, data)
		}
	}
}

// TestReadmeTemplate_ConfigFile: a config-mode export explains config.json, its base64
// passwords and ODB_CONFIG, and its script passes the file to odb (OC-155, OC-156).
func TestReadmeTemplate_ConfigFile(t *testing.T) {
	f := newFixture(t)
	b := f.build(t, Request{CredentialMode: ModeConfig}).Bundle
	script, readme, err := RenderScriptFiles(b, encode(t, b), Script{Runtime: "auto"}, testScriptConfig(t), "2.0.0")
	require.NoError(t, err)
	for _, s := range []string{
		"**Credentials:** in config.json beside migrate.sh (base64-encoded, not encrypted)",
		"### Run it against another environment", "encodes it and saves `config.json` again",
		"| `ODB_CONFIG` | `config.json` in this folder |", "is not encryption",
	} {
		require.Contains(t, string(readme), s)
	}
	require.NotContains(t, string(readme), "ODB_BUNDLE_PASSPHRASE", "nothing to unseal")
	for _, s := range []string{`: "${ODB_CONFIG:=$HERE/config.json}"`, `--config "$cfgdir/$cfgname"`, `--config "/config/$cfgname"`, "Connection details and credentials: config.json"} {
		require.Contains(t, string(script), s)
	}

	// Other modes have no config file.
	b = f.build(t, externalReq()).Bundle
	script, readme, err = RenderScriptFiles(b, encode(t, b), Script{Runtime: "auto"}, testScriptConfig(t), "2.0.0")
	require.NoError(t, err)
	require.NotContains(t, string(script), "ODB_CONFIG")
	require.NotContains(t, string(readme), "config.json")
}

func TestReadmeTemplate_AC7_Golden(t *testing.T) {
	cfg := testScriptConfig(t)
	f := newFixture(t)
	f.toDirectory(t, "/data/out")
	b := f.build(t, externalReq()).Bundle
	_, readme, err := RenderScriptFiles(b, encode(t, b), Script{Runtime: "auto"}, cfg, "2.0.0")
	require.NoError(t, err)
	// The bundle hash and mapping IDs vary per fixture; the golden file has placeholders.
	got := strings.ReplaceAll(string(readme), b.SHA256(), "<sha256>")
	golden := filepath.Join("testdata", "README.golden.md")
	if *update {
		require.NoError(t, os.WriteFile(golden, []byte(got), 0o600))
	}
	want, err := os.ReadFile(golden)
	require.NoError(t, err)
	require.Equal(t, string(want), got)
	for _, s := range []string{"| orders | `public.orders` | `orders/` |", "Exit codes", "| 5 | Cancelled", "ODB_SOURCE_PASSWORD", "./migrate.sh preflight", "JSON files go to `/data/out`"} {
		require.Contains(t, got, s)
	}
	// The target folder is the script's default already: no export step for it, nor chmod.
	require.NotContains(t, got, "export ODB_TARGET_PATH")
	require.NotContains(t, got, "chmod")
}

func TestScript_AutoEmbedsWhenNothingDownloads(t *testing.T) {
	f := newFixture(t)
	b := f.build(t, externalReq()).Bundle
	raw := encode(t, b)
	self := runtime.GOOS + "_" + runtime.GOARCH
	dir := t.TempDir()
	writeTarball(t, filepath.Join(dir, "odb_2.0.0_"+self+".tar.gz"), "#!/bin/sh\necho fake odb\n")
	noDownload := ScriptConfig{DownloadURL: "https://example.com/r", Dist: Dist{Dir: dir, Version: "2.0.0"}, EmbedWhenNoDownload: true}

	// No release checksums: an auto script carries this server's odb.
	script, _, err := RenderScriptFiles(b, raw, Script{Runtime: "auto"}, noDownload, "2.0.0")
	require.NoError(t, err)
	require.Contains(t, string(script), "ODB_EMBEDDED_PLATFORM='"+self+"'")
	require.Contains(t, string(script), "ODB_RUNTIME:=auto")

	// Other runtimes, the setting off, or downloads available: nothing embedded.
	for name, c := range map[string]struct {
		s   Script
		cfg ScriptConfig
	}{
		"binary":       {Script{Runtime: "binary"}, noDownload},
		"off":          {Script{Runtime: "auto"}, ScriptConfig{Dist: noDownload.Dist}},
		"downloadable": {Script{Runtime: "auto"}, func() ScriptConfig { c := testScriptConfig(t); c.EmbedWhenNoDownload = true; return c }()},
	} {
		script, _, err := RenderScriptFiles(b, raw, c.s, c.cfg, "2.0.0")
		require.NoError(t, err, name)
		require.Contains(t, string(script), "ODB_EMBEDDED_PLATFORM=''", name)
	}
}

func decodeWrapped(s string) ([]byte, error) {
	return base64Std.DecodeString(strings.ReplaceAll(strings.TrimSpace(s), "\n", ""))
}

var base64Std = base64.StdEncoding

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
