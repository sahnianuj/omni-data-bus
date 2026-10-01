package directory

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors/target"
)

// newTestWriter opens a writer on a fresh target folder with one mapping folder, "orders".
func newTestWriter(t *testing.T, mode string, cfgOver map[string]any, fsys fsys) (*writer, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "out")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "orders"), 0o750))
	c := New([]string{root})
	v := map[string]any{"path": root}
	for k, x := range cfgOver {
		v[k] = x
	}
	cfg, abs, err := c.parse(cfgJSON(t, v))
	require.NoError(t, err)
	if fsys == nil {
		fsys = osFS{}
	}
	w, err := c.openWriter(fsys, cfg, abs, target.WriteOptions{
		Location: target.Location{Kind: "directory", Folder: "orders"}, Mode: mode, MappingID: "m1", JobID: "j1", KeyTemplate: "{order_id}",
	})
	require.NoError(t, err)
	w.sleep = func(context.Context, time.Duration) error { return nil }
	return w, abs
}

func doc(key string, v any) target.Doc {
	b, _ := json.Marshal(v)
	return target.Doc{Key: key, Value: b, SourceKey: []any{key}}
}

// faultFS fails a share of operations at random points, like a crash or an I/O error mid-write.
type faultFS struct {
	osFS
	mu   sync.Mutex
	rng  *rand.Rand
	rate float64
	err  error
}

func (f *faultFS) roll() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rng.Float64() < f.rate
}

type faultFile struct {
	file
	fs *faultFS
}

func (f faultFile) Write(p []byte) (int, error) {
	if f.fs.roll() {
		n, _ := f.file.Write(p[:len(p)/2]) // a torn write
		return n, f.fs.err
	}
	return f.file.Write(p)
}

func (f faultFile) Sync() error {
	if f.fs.roll() {
		return f.fs.err
	}
	return f.file.Sync()
}

func (f *faultFS) Create(name string, m fs.FileMode) (file, error) {
	if f.roll() {
		return nil, f.err
	}
	fl, err := f.osFS.Create(name, m)
	if err != nil {
		return nil, err
	}
	return faultFile{fl, f}, nil
}

func (f *faultFS) Rename(o, n string) error {
	if f.roll() {
		return f.err
	}
	return f.osFS.Rename(o, n)
}

func TestWriter_AC1_AtomicUnderFaults(t *testing.T) {
	ffs := &faultFS{rng: rand.New(rand.NewPCG(1, 2)), rate: 0.1, err: syscall.EIO} //nolint:gosec // deterministic test faults
	// durability none: macOS's full fsync takes milliseconds a file (Sync faults are covered in
	// TestWriter_SyncFaultNeverPublishes).
	w, root := newTestWriter(t, "upsert", map[string]any{"durability": "none"}, ffs)
	const n = 10_000
	docs := make([]target.Doc, n)
	for i := range docs {
		docs[i] = doc(fmt.Sprint(i), map[string]any{"id": i, "pad": strings.Repeat("x", i%300)})
	}
	written := map[string]bool{}
	for i := 0; i < n; i += 500 {
		for _, r := range w.WriteBatch(context.Background(), docs[i:i+500]) {
			if r.Outcome == target.Written {
				written[r.Key] = true
			}
		}
	}
	require.NoError(t, w.Close())
	require.Greater(t, len(written), n*95/100, "retries recover most faults")
	files := 0
	require.NoError(t, filepath.WalkDir(filepath.Join(root, "orders"), func(p string, d fs.DirEntry, err error) error {
		require.NoError(t, err)
		if d.IsDir() || strings.Contains(p, tmpDirName) {
			return nil
		}
		files++
		b, err := os.ReadFile(p) //nolint:gosec // test
		require.NoError(t, err)
		var got map[string]any
		require.NoError(t, json.Unmarshal(b, &got), "every published file is complete JSON: %s", p)
		key := strings.TrimSuffix(filepath.Base(p), ".json")
		require.EqualValues(t, key, fmt.Sprint(got["id"]), "and it's that key's document")
		require.True(t, written[key], "only documents reported Written are published")
		return nil
	}))
	require.Equal(t, len(written), files)
}

func TestWriter_AC1_GzipRoundTrip(t *testing.T) {
	w, root := newTestWriter(t, "upsert", map[string]any{"compression": "gzip", "pretty": true}, nil)
	res := w.WriteBatch(context.Background(), []target.Doc{doc("1001", map[string]any{"a": 1})})
	require.Equal(t, target.Written, res[0].Outcome)
	raw, err := os.ReadFile(filepath.Join(root, "orders", "1001.json.gz"))
	require.NoError(t, err)
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	require.NoError(t, err)
	body, err := io.ReadAll(zr)
	require.NoError(t, err)
	require.Equal(t, "{\n  \"a\": 1\n}", string(body), "pretty-printed, then gzipped")
	got, err := w.GetDocs(context.Background(), []string{"1001", "404"})
	require.NoError(t, err)
	require.Equal(t, map[string][]byte{"1001": body}, got)
	require.EqualValues(t, len(raw), w.BytesWritten())
	require.NoError(t, w.Close())
}

func TestWriter_AC2_Modes(t *testing.T) {
	for _, tc := range []struct {
		mode              string
		existing          bool
		outcome           target.Outcome
		contentAfterByNew bool
	}{
		{"upsert", true, target.Written, true},
		{"insert", true, target.Skipped, false},
		{"replace", true, target.Written, true},
		{"replace", false, target.Skipped, false},
		{"insert", false, target.Written, true},
		{"upsert", false, target.Written, true},
	} {
		t.Run(fmt.Sprintf("%s/existing=%v", tc.mode, tc.existing), func(t *testing.T) {
			w, root := newTestWriter(t, tc.mode, nil, nil)
			final := filepath.Join(root, "orders", "1001.json")
			if tc.existing {
				require.NoError(t, os.WriteFile(final, []byte(`{"v":"old"}`), 0o600))
			}
			res := w.WriteBatch(context.Background(), []target.Doc{doc("1001", map[string]string{"v": "new"})})
			require.Equal(t, tc.outcome, res[0].Outcome, "%+v", res[0])
			b, err := os.ReadFile(final) //nolint:gosec // test
			switch {
			case tc.contentAfterByNew:
				require.JSONEq(t, `{"v":"new"}`, string(b))
			case tc.existing:
				require.JSONEq(t, `{"v":"old"}`, string(b))
			default:
				require.ErrorIs(t, err, os.ErrNotExist)
			}
			entries, _ := os.ReadDir(w.tmpDir)
			require.Empty(t, entries, "no temp file is left")
		})
	}
}

func TestWriter_AC2_InsertNoReplaceRace(t *testing.T) {
	w, _ := newTestWriter(t, "insert", nil, nil)
	var wg sync.WaitGroup
	results := make([]target.Outcome, 8)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = w.WriteBatch(context.Background(), []target.Doc{doc("1001", map[string]int{"writer": i})})[0].Outcome
		}()
	}
	wg.Wait()
	written := 0
	for _, o := range results {
		if o == target.Written {
			written++
		} else {
			require.Equal(t, target.Skipped, o)
		}
	}
	require.Equal(t, 1, written, "exactly one insert wins")
}

func TestWriter_AC6_CaseCollision(t *testing.T) {
	w, root := newTestWriter(t, "upsert", nil, nil)
	w.caseFold, w.collisions = true, newCollisionGuard(10) // as on a case-insensitive file system
	res := w.WriteBatch(context.Background(), []target.Doc{doc("ABC", map[string]int{"n": 1})})
	require.Equal(t, target.Written, res[0].Outcome)
	res = w.WriteBatch(context.Background(), []target.Doc{doc("abc", map[string]int{"n": 2})})
	require.Equal(t, target.Failed, res[0].Outcome)
	require.Equal(t, target.CodeKeyPathCollision, res[0].Code)
	require.False(t, res[0].Fatal)
	b, err := os.ReadFile(filepath.Join(root, "orders", "ABC.json"))
	require.NoError(t, err)
	require.JSONEq(t, `{"n":1}`, string(b), "the first file is untouched")
	// The same key again (a retry or a resumed run) is fine.
	require.Equal(t, target.Written, w.WriteBatch(context.Background(), []target.Doc{doc("ABC", map[string]int{"n": 3})})[0].Outcome)
}

func TestCollisionGuard_SwitchesToBloom(t *testing.T) {
	g := newCollisionGuard(3)
	for _, p := range []string{"/o/A.json", "/o/B.json", "/o/C.json"} {
		_, clash := g.check(p)
		require.False(t, clash)
	}
	require.NotNil(t, g.bloom)
	other, clash := g.check("/o/a.json")
	require.True(t, clash)
	require.Contains(t, other, "false positive")
	_, clash = g.check("/o/zzz-new.json")
	require.False(t, clash)
}

func TestWriter_LimitsAndEmptyKey(t *testing.T) {
	w, _ := newTestWriter(t, "upsert", map[string]any{"maxDocBytes": 1024}, nil)
	res := w.WriteBatch(context.Background(), []target.Doc{
		{Key: "", Value: []byte(`{}`)},
		doc("big", map[string]string{"x": strings.Repeat("y", 2000)}),
	})
	require.Equal(t, target.CodeKeyInvalid, res[0].Code)
	require.Equal(t, target.CodeDocTooLarge, res[1].Code)
	require.False(t, res[1].Fatal)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res = w.WriteBatch(ctx, []target.Doc{doc("1", 1), doc("2", 2)})
	for _, r := range res {
		require.Equal(t, target.Failed, r.Outcome, "nothing is published after cancellation")
	}
}

func TestWriter_OpenErrors(t *testing.T) {
	root := t.TempDir()
	c := New([]string{root})
	cfg, abs, err := c.parse(cfgJSON(t, map[string]any{"path": root}))
	require.NoError(t, err)
	_, err = c.openWriter(osFS{}, cfg, abs, target.WriteOptions{Location: target.Location{Folder: "missing"}})
	require.Equal(t, target.CodeKeyspaceMissing, codeOf(err))
	_, err = c.openWriter(osFS{}, cfg, filepath.Join(abs, "gone"), target.WriteOptions{})
	require.Equal(t, target.CodeLocationMissing, codeOf(err))
	_, err = c.OpenWriter(context.Background(), cfgJSON(t, map[string]any{"path": "/etc"}), connNoCreds, target.WriteOptions{})
	require.Equal(t, CodeOutsideRoots, codeOf(err))
}

func TestWriter_DirSyncFailureFailsTheBatch(t *testing.T) {
	w, _ := newTestWriter(t, "upsert", map[string]any{"durability": "file+dir"}, &syncFailFS{})
	res := w.WriteBatch(context.Background(), []target.Doc{doc("1", 1), doc("2", 2)})
	for _, r := range res {
		require.Equal(t, target.CodeDiskFull, r.Code)
		require.True(t, r.Fatal)
	}
}

type syncFailFS struct{ osFS }

func (syncFailFS) SyncDir(string) error { return syscall.ENOSPC }

// failSyncFS fails every fsync: the document must not be published.
type failSyncFS struct{ osFS }

type failSyncFile struct{ file }

func (failSyncFile) Sync() error { return syscall.EIO }

func (f failSyncFS) Create(name string, m fs.FileMode) (file, error) {
	fl, err := f.osFS.Create(name, m)
	if err != nil {
		return nil, err
	}
	return failSyncFile{fl}, nil
}

func TestWriter_SyncFaultNeverPublishes(t *testing.T) {
	w, root := newTestWriter(t, "upsert", nil, failSyncFS{})
	r := w.WriteBatch(context.Background(), []target.Doc{doc("1001", 1)})[0]
	require.Equal(t, target.Failed, r.Outcome)
	require.NoFileExists(t, filepath.Join(root, "orders", "1001.json"))
	entries, _ := os.ReadDir(w.tmpDir)
	require.Empty(t, entries)
}
