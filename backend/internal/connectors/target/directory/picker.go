package directory

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"omnidatabus/internal/connectors/target"
	"omnidatabus/internal/problem"
)

// pickerMax bounds one folder listing.
const pickerMax = 1000

// folderName is what a folder created from the picker may be called.
var folderName = regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9 ._-]{0,63}$`)

// Folders implements target.FolderPicker: the allowed areas for path "", otherwise the visible
// sub-folders of path (never hidden ones), sorted by name.
func (c *Connector) Folders(path string) (target.FolderListing, error) {
	if strings.TrimSpace(path) == "" {
		out := target.FolderListing{Folders: []target.FolderEntry{}}
		for _, r := range c.roots {
			out.Folders = append(out.Folders, target.FolderEntry{Name: r, Path: r})
		}
		return out, nil
	}
	abs, err := c.resolve(filepath.Clean(path))
	if err != nil {
		return target.FolderListing{}, err
	}
	entries, err := os.ReadDir(abs)
	if errors.Is(err, os.ErrNotExist) {
		return target.FolderListing{}, problem.New(http.StatusNotFound, problem.CodeNotFound, "Folder not found", path+" doesn't exist.")
	}
	if err != nil {
		return target.FolderListing{}, problem.New(http.StatusUnprocessableEntity, problem.CodeValidation, "Can't open folder", err.Error())
	}
	out := target.FolderListing{Path: abs, Folders: []target.FolderEntry{}, CanCreate: true}
	if !c.isRoot(abs) {
		out.Parent = filepath.Dir(abs)
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") || !isDir(abs, e) {
			continue
		}
		out.Folders = append(out.Folders, target.FolderEntry{Name: name, Path: filepath.Join(abs, name)})
		if len(out.Folders) == pickerMax {
			break
		}
	}
	sort.Slice(out.Folders, func(i, j int) bool {
		return strings.ToLower(out.Folders[i].Name) < strings.ToLower(out.Folders[j].Name)
	})
	return out, nil
}

// isDir follows a symlink, so a linked folder shows (resolve still checks where it leads).
func isDir(dir string, e os.DirEntry) bool {
	if e.IsDir() {
		return true
	}
	if e.Type()&os.ModeSymlink == 0 {
		return false
	}
	st, err := os.Stat(filepath.Join(dir, e.Name()))
	return err == nil && st.IsDir()
}

func (c *Connector) isRoot(abs string) bool {
	for _, r := range c.roots {
		if rr, err := realPath(r); err == nil && rr == abs {
			return true
		}
	}
	return false
}

// MakeFolder implements target.FolderPicker: creates name inside parent and returns its path.
func (c *Connector) MakeFolder(parent, name string) (string, error) {
	name = strings.TrimSpace(name)
	if !folderName.MatchString(name) {
		p := problem.New(http.StatusUnprocessableEntity, problem.CodeValidation, "Invalid folder name",
			"Use letters, digits, spaces, dots, dashes and underscores (up to 64), not starting with a dot or space.")
		p.FieldErrors = []problem.FieldError{{Field: "name", Message: "is not a valid folder name"}}
		return "", p
	}
	realParent, err := c.resolve(filepath.Clean(parent))
	if err != nil {
		return "", err
	}
	path := filepath.Join(realParent, name)
	if _, err := os.Stat(path); err == nil {
		return "", problem.New(http.StatusConflict, problem.CodeKeyspaceExists, "Folder exists", name+" already exists here.")
	}
	if err := os.Mkdir(path, 0o750); err != nil {
		return "", problem.New(http.StatusUnprocessableEntity, problem.CodeValidation, "Can't create folder", err.Error())
	}
	return path, nil
}
