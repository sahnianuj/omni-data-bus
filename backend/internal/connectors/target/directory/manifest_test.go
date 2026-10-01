package directory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors/target"
)

func TestManifest_AC4_MergeDedupeSort(t *testing.T) {
	root := filepath.Join(t.TempDir(), "out")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "orders"), 0o750))
	c := New([]string{root})
	cfg, abs, err := c.parse(cfgJSON(t, map[string]any{"path": root, "durability": "none"}))
	require.NoError(t, err)
	// 3 writers (e.g. a job resumed twice) write 10,000 documents between them; 5 keys twice.
	const n = 10_000
	var writers []*writer
	for range 3 {
		w, err := c.openWriter(osFS{}, cfg, abs, target.WriteOptions{Location: target.Location{Folder: "orders"}, Mode: "upsert", MappingID: "m1", JobID: "j1", KeyTemplate: "{id}"})
		require.NoError(t, err)
		writers = append(writers, w)
	}
	for i := range n {
		w := writers[i%3]
		require.Equal(t, target.Written, w.WriteBatch(context.Background(), []target.Doc{doc(fmt.Sprintf("%05d", i), map[string]int{"i": i})})[0].Outcome)
	}
	for i := range 5 { // retries: rewritten by another writer, later
		require.Equal(t, target.Written, writers[2].WriteBatch(context.Background(), []target.Doc{doc(fmt.Sprintf("%05d", i), map[string]int{"i": i, "retry": 1})})[0].Outcome)
	}
	for _, w := range writers {
		require.NoError(t, w.Close())
	}
	lines, err := ReadManifest(abs, "m1")
	require.NoError(t, err)
	require.Len(t, lines, n)
	require.True(t, sort.SliceIsSorted(lines, func(i, j int) bool { return lines[i].Key < lines[j].Key }))
	for i, l := range lines {
		b, err := os.ReadFile(filepath.Join(abs, filepath.FromSlash(l.Path))) //nolint:gosec // test
		require.NoError(t, err)
		sum := sha256.Sum256(b)
		require.Equal(t, hex.EncodeToString(sum[:]), l.SHA256, l.Key)
		require.EqualValues(t, len(b), l.Bytes)
		if i < 5 {
			require.Contains(t, string(b), `"retry":1`, "the last write wins")
		}
	}
	frags, _ := filepath.Glob(filepath.Join(abs, "_odb", "manifest", "m1-*.jsonl"))
	require.Empty(t, frags, "fragments are merged away")

	info, err := ReadRunInfo(abs)
	require.NoError(t, err)
	require.Equal(t, "flat", info.Layout)
	require.Equal(t, RunMapping{MappingID: "m1", JobID: "j1", KeyTemplate: "{id}", UpdatedAt: info.Mappings["orders"].UpdatedAt}, info.Mappings["orders"])

	// Merging again with nothing new changes nothing.
	count, err := MergeManifest(abs, "m1")
	require.NoError(t, err)
	require.Equal(t, n, count)
}
