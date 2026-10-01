package directory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors/target"
	"omnidatabus/internal/problem"
)

func TestFindDocument_ByKeyAndConfinement(t *testing.T) {
	ctx := context.Background()
	for _, layout := range []string{"flat", "sharded", "key-path"} {
		w, root := newTestWriter(t, "upsert", map[string]any{"layout": layout, "durability": "none"}, nil)
		keys := []string{"1001", "2026::10::1002", "a/b:c"}
		for _, k := range keys {
			require.Equal(t, target.Written, w.WriteBatch(ctx, []target.Doc{doc(k, map[string]string{"k": k})})[0].Outcome)
		}
		require.NoError(t, w.Close())
		c := New([]string{root})
		conn := target.Conn{Config: cfgJSON(t, map[string]any{"path": root, "layout": layout})}
		loc := target.Location{Kind: "directory", Folder: "orders"}
		for _, k := range keys {
			d, err := c.FindDocument(ctx, conn, loc, k)
			require.NoError(t, err, "%s %s", layout, k)
			require.JSONEq(t, fmt.Sprintf(`{"k":%q}`, k), string(d.Body))
			require.NotEmpty(t, d.Path)
			require.NotNil(t, d.ModifiedAt)
		}
		_, err := c.FindDocument(ctx, conn, loc, "404")
		require.Equal(t, problem.CodeDocumentNotFound, problem.From(err).Code)

		items, next, err := c.ListOutput(ctx, conn, loc, "m1", "", 2)
		require.NoError(t, err, layout)
		require.Len(t, items, 2)
		require.NotEmpty(t, next)
		rest, next2, err := c.ListOutput(ctx, conn, loc, "m1", next, 2)
		require.NoError(t, err)
		require.Len(t, rest, 1)
		require.Empty(t, next2)

		// Without a manifest, the folder is walked and keys decoded from names.
		require.NoError(t, os.RemoveAll(filepath.Join(root, "_odb")))
		items, _, err = c.ListOutput(ctx, conn, loc, "m1", "", 50)
		require.NoError(t, err)
		var got []string
		for _, it := range items {
			got = append(got, it.Key)
		}
		require.ElementsMatch(t, keys, got, layout)
	}
}

func TestFindDocument_AC5_SymlinkOutIsRejected(t *testing.T) {
	root, c := rootFixture(t)
	outside := filepath.Join(t.TempDir(), "secret.json")
	require.NoError(t, os.WriteFile(outside, []byte(`{"secret":true}`), 0o600))
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "shop", "1001.json")))
	_, err := c.FindDocument(context.Background(), conn(t, root), target.Location{Folder: "shop"}, "1001")
	var rej *PathRejected
	require.True(t, errors.As(err, &rej))
	require.Equal(t, problem.CodeDocumentNotFound, problem.From(err).Code, "reads as not found")
	_, err = c.FindDocument(context.Background(), conn(t, root), target.Location{Folder: "../x"}, "1")
	require.Error(t, err)
}
