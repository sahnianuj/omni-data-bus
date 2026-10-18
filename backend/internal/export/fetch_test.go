package export

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

// otherPlatform is a platform this test machine isn't, so its odb must come from the release.
func otherPlatform() string {
	for _, p := range []string{"linux_arm64", "windows_arm64"} {
		if p != selfPlatform() {
			return p
		}
	}
	return "linux_amd64"
}

// release serves one archive of platform p holding a stand-in odb, and counts the downloads. It
// returns the fetcher (checksums in its SumsDir, the release's) and the archive's bytes.
func release(t *testing.T, p string) (*Fetcher, *atomic.Int32, []byte) {
	t.Helper()
	dir := t.TempDir()
	archive := filepath.Join(dir, Archive("2.5.0", p))
	if isWindows(p) {
		writeZip(t, archive, "odb.exe", "fetched odb for "+p)
	} else {
		writeTarball(t, archive, "fetched odb for "+p)
	}
	body, err := os.ReadFile(archive) //nolint:gosec // the test's own file
	require.NoError(t, err)
	sum := sha256.Sum256(body)
	sums := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(sums, "SHA256SUMS"), []byte(hex.EncodeToString(sum[:])+"  "+Archive("2.5.0", p)+"\n"), 0o600))
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path != "/v2.5.0/"+Archive("2.5.0", p) {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return &Fetcher{BaseURL: srv.URL + "/v2.5.0/", SumsDir: sums, CacheDir: t.TempDir(), Client: srv.Client()}, &hits, body
}

func machine(ms []Machine, p string) Machine {
	for _, m := range ms {
		if m.Platform == p {
			return m
		}
	}
	return Machine{}
}

// OC-188 AC2: a machine's odb is downloaded from the release on first use, checked against the
// checksum built into the server, cached, and not downloaded again.
func TestDist_OC188_AC2_FetchVerifyCache(t *testing.T) {
	p := otherPlatform()
	f, hits, _ := release(t, p)
	d := Dist{Version: "2.5.0", Fetch: f}
	require.Equal(t, MachineOnDemand, machine(d.Machines(), p).State)
	require.Contains(t, d.Available(), p, "on demand counts as available to the dialog")

	bin, err := d.Binary(context.Background(), p)
	require.NoError(t, err)
	require.Equal(t, "fetched odb for "+p, string(bin))
	require.EqualValues(t, 1, hits.Load())
	require.Equal(t, MachineAvailable, machine(d.Machines(), p).State, "cached now")

	bin, err = d.Binary(context.Background(), p)
	require.NoError(t, err)
	require.Equal(t, "fetched odb for "+p, string(bin))
	require.EqualValues(t, 1, hits.Load(), "served from the cache")

	// The README links the same release, with the checksum the image carries.
	require.NotEmpty(t, d.Checksums()[p])
}

// OC-188 AC3: a download whose SHA-256 isn't the release's is refused and not cached; the
// machine says why, and the next export tries again.
func TestDist_OC188_AC3_ChecksumMismatch(t *testing.T) {
	p := otherPlatform()
	f, hits, _ := release(t, p)
	require.NoError(t, os.WriteFile(filepath.Join(f.SumsDir, "SHA256SUMS"), []byte(strings.Repeat("0", 64)+"  "+Archive("2.5.0", p)+"\n"), 0o600))
	d := Dist{Version: "2.5.0", Fetch: f}

	_, err := d.Binary(context.Background(), p)
	fe, ok := isFetchError(err)
	require.True(t, ok, "%v", err)
	require.Equal(t, CodeDownloadInvalid, fe.Code)
	require.Contains(t, fe.Message, "doesn't match its checksum")
	require.Contains(t, fe.Message, PlatformLabel(p))
	entries, _ := os.ReadDir(f.CacheDir)
	require.Empty(t, entries, "nothing cached, not even a temporary file")

	m := machine(d.Machines(), p)
	require.Equal(t, MachineUnavailable, m.State)
	require.Contains(t, m.Reason, "doesn't match its checksum")
	require.Contains(t, m.Reason, "Trying again after")

	_, err = d.Binary(context.Background(), p)
	require.Error(t, err)
	require.EqualValues(t, 2, hits.Load(), "an export asks again")
}

// A release that doesn't answer fails the export with the URL, and marks the machine.
func TestDist_OC188_DownloadFailed(t *testing.T) {
	p := otherPlatform()
	f, _, _ := release(t, p)
	f.BaseURL += "missing/"
	d := Dist{Version: "2.5.0", Fetch: f}
	_, err := d.Binary(context.Background(), p)
	fe, ok := isFetchError(err)
	require.True(t, ok, "%v", err)
	require.Equal(t, CodeDownloadFailed, fe.Code)
	require.Contains(t, fe.Message, "404")
	require.Contains(t, fe.Message, f.BaseURL)
	require.Equal(t, MachineUnavailable, machine(d.Machines(), p).State)
}

// OC-188 AC4: every machine says whether this server can add its odb, and why not.
func TestOptions_OC188_AC4_MachineStates(t *testing.T) {
	p := otherPlatform()
	f, _, _ := release(t, p)

	// Downloads off: only the server's own binary.
	off := Dist{Version: "2.5.0", Fetch: &Fetcher{SumsDir: f.SumsDir, CacheDir: f.CacheDir}}
	for _, m := range off.Machines() {
		if m.Platform == selfPlatform() {
			require.Equal(t, MachineAvailable, m.State)
			continue
		}
		require.Equal(t, MachineUnavailable, m.State, m.Platform)
		require.Contains(t, m.Reason, "OC_EXPORT_DIST_DIR", m.Platform)
	}

	// Downloads on: a machine with a known checksum is on demand; one without (a development
	// build) says so.
	on := Dist{Version: "2.5.0", Fetch: f}
	require.Equal(t, MachineOnDemand, machine(on.Machines(), p).State)
	var noSum string
	for _, q := range Platforms {
		if q != p && q != selfPlatform() {
			noSum = q
			break
		}
	}
	m := machine(on.Machines(), noSum)
	require.Equal(t, MachineUnavailable, m.State)
	require.Contains(t, m.Reason, "doesn't know the release's checksum")

	// The options endpoint carries them, and embeddable = available + on demand.
	svc := &Service{Script: ScriptConfig{Dist: on}, Version: "2.5.0"}
	o := svc.Describe()
	require.Len(t, o.Machines, len(Platforms))
	require.Contains(t, o.Embeddable, p)
	require.NotContains(t, o.Embeddable, noSum)
}

// OC-188 AC5: the dist folder comes first; no download is attempted for what it has.
func TestDist_OC188_AC5_DistDirFirst(t *testing.T) {
	p := otherPlatform()
	f, hits, body := release(t, p)
	dist := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dist, Archive("2.5.0", p)), body, 0o600))
	d := Dist{Dir: dist, Version: "2.5.0", Fetch: f}
	require.Equal(t, MachineAvailable, machine(d.Machines(), p).State)
	bin, err := d.Binary(context.Background(), p)
	require.NoError(t, err)
	require.Equal(t, "fetched odb for "+p, string(bin))
	require.Zero(t, hits.Load())
}

// writeZip writes a zip with one file, as a Windows release archive.
func writeZip(t *testing.T, path, name, body string) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(name)
	require.NoError(t, err)
	_, _ = w.Write([]byte(body))
	require.NoError(t, zw.Close())
	require.NoError(t, os.WriteFile(path, buf.Bytes(), 0o600))
}
