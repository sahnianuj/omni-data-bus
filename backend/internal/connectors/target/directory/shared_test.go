package directory

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors"
	"omnidatabus/internal/connectors/target"
	"omnidatabus/internal/problem"
)

// OC-080 AC2: with a folder shared from the user's computer, it's the only area, labeled with
// its path on the computer, and every folder in it says where it is on the computer.
func TestFolders_OC080_AC2_SharedAreaOnly(t *testing.T) {
	data := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home) // the container-only home must not be offered
	require.NoError(t, os.MkdirAll(filepath.Join(data, "orders"), 0o750))
	c := New(nil, Options{Shared: []Shared{{Path: data, HostPath: "/Users/ana/omni-data-bus-output"}}})

	areas, err := c.Folders("")
	require.NoError(t, err)
	require.Len(t, areas.Folders, 1)
	a := areas.Folders[0]
	require.Equal(t, data, a.Path)
	require.Equal(t, WhereComputer, a.Where)
	require.Equal(t, "/Users/ana/omni-data-bus-output", a.HostPath)
	require.Equal(t, "Your computer: /Users/ana/omni-data-bus-output", a.Label)

	in, err := c.Folders(data)
	require.NoError(t, err)
	require.Equal(t, WhereComputer, in.Where)
	require.Equal(t, "/Users/ana/omni-data-bus-output", in.HostPath)
	require.Equal(t, "/Users/ana/omni-data-bus-output/orders", in.Folders[0].HostPath)

	_, err = c.resolve(filepath.Join(home, "x"))
	require.Equal(t, CodeOutsideRoots, problem.From(err).Code, "the container's home isn't an area any more")

	require.Equal(t, &target.SharedFolder{Path: data, HostPath: "/Users/ana/omni-data-bus-output"}, c.SharedFolder())
	require.Equal(t, target.Info(c).SharedFolder, c.SharedFolder(), "GET /target-kinds carries it")

	// Test connection shows the computer path next to the container path.
	raw, _ := json.Marshal(map[string]any{"path": filepath.Join(data, "orders")})
	res := c.Test(context.Background(), raw, connectors.Credentials{})
	var detail string
	for _, s := range res.Steps {
		if s.ID == "path" {
			detail = s.Detail
		}
	}
	require.Contains(t, detail, "on your computer: /Users/ana/omni-data-bus-output/orders")
}

// A Windows computer's paths keep backslashes.
func TestHostPath_OC080_WindowsComputer(t *testing.T) {
	data := t.TempDir()
	c := New(nil, Options{Shared: []Shared{{Path: data, HostPath: `C:\Users\ana\odb-output`}}})
	require.Equal(t, `C:\Users\ana\odb-output\orders\2026`, c.HostPath(filepath.Join(data, "orders", "2026")))
	require.Empty(t, c.HostPath(t.TempDir()), "outside the share")
}

// OC-080 AC2: nothing shared in the all-in-one image: the container's home is offered, marked.
func TestFolders_OC080_AC2_ContainerOnlyWarning(t *testing.T) {
	root, _ := rootFixture(t)
	c := New([]string{root}, Options{ContainerOnly: true})
	areas, err := c.Folders("")
	require.NoError(t, err)
	require.Equal(t, WhereContainer, areas.Folders[0].Where)
	require.Contains(t, areas.Folders[0].Label, "not visible on your computer")
	require.Empty(t, areas.Folders[0].HostPath)
	require.Nil(t, c.SharedFolder())
}

// OC-080 AC5: outside the all-in-one image nothing is labeled and nothing changes.
func TestFolders_OC080_AC5_RegularServerUnchanged(t *testing.T) {
	root, _ := rootFixture(t)
	c := New([]string{root})
	areas, err := c.Folders("")
	require.NoError(t, err)
	require.Equal(t, []target.FolderEntry{{Name: root, Path: root}}, areas.Folders)
	require.Nil(t, target.Info(c).SharedFolder)
}
