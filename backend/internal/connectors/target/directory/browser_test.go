package directory

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors/target"
	"omnidatabus/internal/problem"
)

func conn(t *testing.T, root string) target.Conn {
	return target.Conn{ID: "p::target", Version: "1", Config: cfgJSON(t, map[string]any{"path": root})}
}

func TestListLocations_AC3_FoldersOnly(t *testing.T) {
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "out")
	for _, d := range []string{"orders/2026", "customers", ".hidden", "_odb/manifest"} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, d), 0o750))
	}
	require.NoError(t, os.WriteFile(filepath.Join(root, "x.json"), []byte("{}"), 0o600))
	for _, f := range []string{"1.json", "2.json", ".tmp-3"} {
		require.NoError(t, os.WriteFile(filepath.Join(root, "customers", f), []byte("{}"), 0o600))
	}
	c := New([]string{root})
	nodes, err := c.ListLocations(ctx, conn(t, root), target.Location{})
	require.NoError(t, err)
	require.Len(t, nodes, 2)
	require.Equal(t, "customers", nodes[0].Name)
	require.EqualValues(t, 2, *nodes[0].DocCount, "hidden temp files aren't counted")
	require.Equal(t, target.Location{Kind: "directory", Folder: "customers"}, nodes[0].Location)
	require.True(t, nodes[0].Leaf)
	require.True(t, nodes[0].CanCreate)
	require.Equal(t, "orders", nodes[1].Name)

	nodes, err = c.ListLocations(ctx, conn(t, root), target.Location{Folder: "orders"})
	require.NoError(t, err)
	require.Equal(t, "orders/2026", nodes[0].Path)

	_, err = c.ListLocations(ctx, conn(t, root), target.Location{Folder: "nope"})
	require.True(t, problem.HasCode(err, problem.CodeNotFound))

	n, err := c.StatLocation(ctx, conn(t, root), target.Location{Folder: "customers"})
	require.NoError(t, err)
	require.EqualValues(t, 2, *n.DocCount)
	_, err = c.StatLocation(ctx, conn(t, root), target.Location{Folder: "gone"})
	require.True(t, problem.HasCode(err, problem.CodeNotFound))
	_, err = c.StatLocation(ctx, conn(t, root), target.Location{Folder: "x.json"})
	require.True(t, problem.HasCode(err, problem.CodeLocationInvalid))
	_, err = c.StatLocation(ctx, conn(t, root), target.Location{})
	require.True(t, problem.HasCode(err, problem.CodeLocationInvalid))
}

func TestListLocations_CountIsCapped(t *testing.T) {
	root := t.TempDir()
	big := filepath.Join(root, "big")
	require.NoError(t, os.Mkdir(big, 0o750))
	for i := range countCap + 5 {
		require.NoError(t, os.WriteFile(filepath.Join(big, string(rune('a'+i%26))+itoa(i)), nil, 0o600))
	}
	n, more := count(big)
	require.EqualValues(t, countCap, n)
	require.True(t, more)
	require.Equal(t, "10000+", countText(n, more))
}

func itoa(i int) string {
	b := []byte{}
	for {
		b = append([]byte{byte('0' + i%10)}, b...)
		if i /= 10; i == 0 {
			return string(b)
		}
	}
}

func TestCreateLocation_AC3_ValidatesNames(t *testing.T) {
	ctx := context.Background()
	root, c := rootFixture(t)
	require.NoError(t, c.CreateLocation(ctx, conn(t, root), target.Location{Folder: "archive/2026"}))
	require.DirExists(t, filepath.Join(root, "archive", "2026"))
	st, err := os.Stat(filepath.Join(root, "archive", "2026"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o750), st.Mode().Perm())

	err = c.CreateLocation(ctx, conn(t, root), target.Location{Folder: "archive/2026"})
	require.True(t, problem.HasCode(err, problem.CodeKeyspaceExists))
	for _, bad := range []string{"../x", "a/b/c/d", "sp ace", ""} {
		err := c.CreateLocation(ctx, conn(t, root), target.Location{Folder: bad})
		require.True(t, problem.HasCode(err, problem.CodeLocationInvalid), bad)
	}
	// Through a symlink that points out of the target folder: refused.
	err = c.CreateLocation(ctx, conn(t, root), target.Location{Folder: "escape/x"})
	require.True(t, problem.HasCode(err, problem.CodeLocationInvalid))
	require.NoFileExists(t, "/etc/x")
	// A connection outside the roots can't browse at all.
	_, err = c.ListLocations(ctx, conn(t, "/etc"), target.Location{})
	require.Equal(t, CodeOutsideRoots, problem.From(err).Code)
	require.NoError(t, os.WriteFile(filepath.Join(root, "file"), nil, 0o600))
	require.True(t, problem.HasCode(c.CreateLocation(ctx, conn(t, root), target.Location{Folder: "file"}), problem.CodeKeyspaceExists))
}

func TestPreflight_FoldersExistOrAreCreated(t *testing.T) {
	ctx := context.Background()
	root, c := rootFixture(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "shop", "1.json"), nil, 0o600))
	loc := func(f string) target.Location { return target.Location{Kind: "directory", Folder: f} }
	res := c.Preflight(ctx, conn(t, root), []target.PreflightLocation{
		{MappingID: "m1", Location: loc("shop"), WriteMode: "insert"},
		{MappingID: "m2", Location: loc("fresh"), CreateIfMissing: true},
		{MappingID: "m3", Location: loc("absent")},
		{MappingID: "m4", Location: loc("../bad")},
	})
	require.Equal(t, "ok", res[0].Checks[0].Status)
	require.Equal(t, "FOLDER_NOT_EMPTY", res[0].Checks[1].Code)
	require.Contains(t, res[0].Checks[1].Detail, "Insert mode")
	require.False(t, res[0].Empty)
	require.Equal(t, "Created fresh/.", res[1].Checks[0].Detail)
	require.True(t, res[1].Empty)
	require.DirExists(t, filepath.Join(root, "fresh"))
	require.Equal(t, target.CodeKeyspaceMissing, res[2].Checks[0].Code)
	require.Equal(t, "fail", res[3].Checks[0].Status)
	require.Equal(t, "m4", res[3].Checks[0].MappingID)
}
