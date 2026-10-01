package directory

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/problem"
)

func TestDefaultRoots_HomeAndData(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	roots := DefaultRoots()
	require.Contains(t, roots, home)
	if st, err := os.Stat(DataRoot); err == nil && st.IsDir() {
		require.Contains(t, roots, DataRoot)
	} else {
		require.NotContains(t, roots, DataRoot)
	}
	c := New(nil)
	ok, _ := c.Available()
	require.True(t, ok, "enabled with no configuration")
	t.Setenv("HOME", "/")
	require.NotContains(t, DefaultRoots(), "/", "never the whole file system")
}

func TestResolve_HiddenFoldersRefused(t *testing.T) {
	root, c := rootFixture(t)
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".ssh"), 0o700))
	require.NoError(t, os.Symlink(filepath.Join(root, ".ssh"), filepath.Join(root, "keys")))
	for _, p := range []string{filepath.Join(root, ".ssh"), filepath.Join(root, ".ssh", "x"), filepath.Join(root, "keys")} {
		_, err := c.resolve(p)
		require.Equal(t, CodeOutsideRoots, problem.From(err).Code, p)
	}
	_, err := c.resolve(filepath.Join(root, "shop"))
	require.NoError(t, err)
}

func TestFolders_BrowseAndCreate(t *testing.T) {
	root, c := rootFixture(t)
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".hidden"), 0o750))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "Archive"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(root, "file.txt"), nil, 0o600))
	absRoot, _ := realPath(root)

	areas, err := c.Folders("")
	require.NoError(t, err)
	require.Equal(t, []string{root}, []string{areas.Folders[0].Path})
	require.False(t, areas.CanCreate)

	l, err := c.Folders(root)
	require.NoError(t, err)
	var names []string
	for _, f := range l.Folders {
		names = append(names, f.Name)
	}
	// Sorted by name ignoring case; no files, no hidden folders; a symlink that stays inside
	// shows ("alias"), one that leads out is listed but can't be opened ("escape").
	require.Equal(t, []string{"alias", "Archive", "escape", "shop"}, names)
	require.Empty(t, l.Parent, "up from an area goes back to the areas")
	require.True(t, l.CanCreate)

	sub, err := c.Folders(filepath.Join(root, "shop"))
	require.NoError(t, err)
	require.Equal(t, absRoot, sub.Parent)
	_, err = c.Folders(filepath.Join(root, "escape"))
	require.Equal(t, CodeOutsideRoots, problem.From(err).Code)
	_, err = c.Folders(filepath.Join(root, "missing"))
	require.True(t, problem.HasCode(err, problem.CodeNotFound))
	_, err = c.Folders("/etc")
	require.Equal(t, CodeOutsideRoots, problem.From(err).Code)

	path, err := c.MakeFolder(root, "Odb Output 2026")
	require.NoError(t, err)
	require.DirExists(t, path)
	_, err = c.MakeFolder(root, "Odb Output 2026")
	require.True(t, problem.HasCode(err, problem.CodeKeyspaceExists))
	for _, bad := range []string{".secret", "a/b", "..", "bad*name", ""} {
		_, err := c.MakeFolder(root, bad)
		require.True(t, problem.HasCode(err, problem.CodeValidation), bad)
	}
	_, err = c.MakeFolder("/etc", "x")
	require.Equal(t, CodeOutsideRoots, problem.From(err).Code)
}
