package directory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors/target"
)

func writeManifest(t *testing.T, path string, lines ...ManifestLine) {
	t.Helper()
	var b bytes.Buffer
	for _, l := range lines {
		raw, _ := json.Marshal(l)
		b.Write(append(raw, '\n'))
	}
	b.WriteString("{torn\n")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, b.Bytes(), 0o640))
}

// TestMergeManifest_OC056_SpillsRunsLastWriteWins: a manifest bigger than one in-memory run is
// sorted in runs on disk and merged; the newest line of a key wins across runs, and no run
// file is left behind.
func TestMergeManifest_OC056_SpillsRunsLastWriteWins(t *testing.T) {
	defer func(n int) { mergeRunLines = n }(mergeRunLines)
	mergeRunLines = 7
	root := t.TempDir()
	dir := filepath.Join(root, odbDir, manifestDir)
	line := func(k, sha string) ManifestLine {
		return ManifestLine{Key: k, Path: "orders/" + k + ".json", SHA256: sha}
	}
	var old, frag1, frag2 []ManifestLine
	for i := 0; i < 30; i++ {
		old = append(old, line(fmt.Sprintf("k%02d", 29-i), "old"))
	}
	for i := 0; i < 20; i += 2 {
		frag1 = append(frag1, line(fmt.Sprintf("k%02d", i), "frag1"), line(fmt.Sprintf("k%02d", i), "frag1b"))
	}
	for i := 25; i < 35; i++ {
		frag2 = append(frag2, line(fmt.Sprintf("k%02d", i), "frag2"))
	}
	writeManifest(t, manifestPath(root, "m"), old...)
	writeManifest(t, filepath.Join(dir, "m-01.jsonl"), frag1...)
	writeManifest(t, filepath.Join(dir, "m-02.jsonl"), frag2...)
	writeManifest(t, filepath.Join(dir, "other-01.jsonl"), line("x", "other"))

	n, err := MergeManifest(root, "m")
	require.NoError(t, err)
	require.Equal(t, 35, n)
	got, err := ReadManifest(root, "m")
	require.NoError(t, err)
	require.Len(t, got, 35)
	for i, l := range got {
		want := "old"
		switch {
		case i >= 25:
			want = "frag2"
		case i < 20 && i%2 == 0:
			want = "frag1b"
		}
		require.Equal(t, fmt.Sprintf("k%02d", i), l.Key)
		require.Equal(t, want, l.SHA256, l.Key)
	}
	left, _ := filepath.Glob(filepath.Join(dir, "*"))
	require.ElementsMatch(t, []string{manifestPath(root, "m"), filepath.Join(dir, "other-01.jsonl")}, left, "fragments and runs are removed; other mappings untouched")

	n, err = MergeManifest(root, "m")
	require.NoError(t, err)
	require.Equal(t, 35, n, "nothing new: counted, not rewritten")
}

// TestCheckIntegrity_OC056_SampleSeesLatestWrite: rewritten keys are checked against their
// latest manifest line, whether all lines or a small sample is checked; Listed counts keys.
func TestCheckIntegrity_OC056_SampleSeesLatestWrite(t *testing.T) {
	root := filepath.Join(t.TempDir(), "out")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "orders"), 0o750))
	c := New([]string{root})
	cfg, abs, err := c.parse(cfgJSON(t, map[string]any{"path": root, "durability": "none"}))
	require.NoError(t, err)
	w, err := c.openWriter(osFS{}, cfg, abs, target.WriteOptions{Location: target.Location{Folder: "orders"}, Mode: "upsert", MappingID: "m1", JobID: "j1", KeyTemplate: "{id}"})
	require.NoError(t, err)
	ctx := context.Background()
	for round := range 3 {
		for i := range 200 {
			require.Equal(t, target.Written, w.WriteBatch(ctx, []target.Doc{doc(fmt.Sprintf("%03d", i), map[string]int{"i": i, "round": round})})[0].Outcome)
		}
	}
	for _, sample := range []int{0, 5, 1000} {
		rep, err := w.CheckIntegrity(ctx, sample)
		require.NoError(t, err)
		require.Equal(t, 200, rep.Listed, "distinct keys")
		want := min(sample, 200)
		if sample <= 0 {
			want = 200
		}
		require.Equal(t, want, rep.Checked)
		require.Zero(t, rep.Mismatched+rep.Missing, "sample %d: %+v", sample, rep.Issues)
	}
	require.NoError(t, w.Close())
}
