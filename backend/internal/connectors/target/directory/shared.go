package directory

import (
	"path/filepath"
	"strings"

	"omnidatabus/internal/connectors/target"
)

// Where a picker area is, as users should read it (OC-080).
const (
	// WhereComputer: a folder of the user's computer shared into the all-in-one image's container.
	WhereComputer = "computer"
	// WhereContainer: inside the all-in-one image's container only; invisible on the computer.
	WhereContainer = "container"
)

// Shared is a folder of the user's computer mounted into the container: Path inside it, HostPath
// on the computer (OC-080).
type Shared struct {
	Path     string
	HostPath string
}

// Options are the all-in-one image's settings for the directory connector; the zero value is a
// server whose folders are the machine's own (the regular image, Helm, make dev).
type Options struct {
	// Shared folders are the only areas a target may be in when set.
	Shared []Shared
	// ContainerOnly marks the areas as inside the container (no folder is shared).
	ContainerOnly bool
}

// hostPath maps a path inside the container to the user's computer ("" when it isn't shared).
func (c *Connector) hostPath(abs string) string {
	for _, s := range c.opts.Shared {
		root, err := realPath(s.Path)
		if err != nil || !within(abs, root) {
			continue
		}
		rel, err := filepath.Rel(root, abs)
		if err != nil {
			continue
		}
		if rel == "." {
			return s.HostPath
		}
		sep := "/"
		if strings.Contains(s.HostPath, `\`) { // a Windows computer
			sep = `\`
			rel = strings.ReplaceAll(rel, "/", `\`)
		}
		return strings.TrimSuffix(s.HostPath, sep) + sep + rel
	}
	return ""
}

// where says where abs is for users: their computer, the container only, or "" (the server's
// own folders, outside the all-in-one image).
func (c *Connector) where(abs string) string {
	switch {
	case c.hostPath(abs) != "":
		return WhereComputer
	case c.opts.ContainerOnly:
		return WhereContainer
	}
	return ""
}

// area describes one allowed area for the picker.
func (c *Connector) area(root string) target.FolderEntry {
	e := target.FolderEntry{Name: root, Path: root}
	abs, err := realPath(root)
	if err != nil {
		abs = root
	}
	e.Where, e.HostPath = c.where(abs), c.hostPath(abs)
	switch e.Where {
	case WhereComputer:
		e.Label = "Your computer: " + e.HostPath
	case WhereContainer:
		e.Label = "Inside the app's container (not visible on your computer)"
	}
	return e
}

// SharedFolder implements target.SharedFolderer: the first shared folder, for the Target step's
// hints (OC-080).
func (c *Connector) SharedFolder() *target.SharedFolder {
	if len(c.opts.Shared) == 0 {
		return nil
	}
	s := c.opts.Shared[0]
	return &target.SharedFolder{Path: s.Path, HostPath: s.HostPath}
}

// HostPath implements target.SharedFolderer for one path ("" when it isn't shared).
func (c *Connector) HostPath(path string) string {
	abs, err := realPath(path)
	if err != nil {
		return ""
	}
	return c.hostPath(abs)
}
