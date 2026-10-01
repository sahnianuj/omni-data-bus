package directory

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"omnidatabus/internal/connectors/target"
	"omnidatabus/internal/pathcodec"
	"omnidatabus/internal/problem"
)

// PathRejected is a lookup whose file would be outside the mapping's folder (a crafted key, or
// a symlink placed in the folder). It reads as 404 and is audited (OC-041 AC5).
type PathRejected struct{ Key string }

func (e *PathRejected) Error() string { return "document path rejected for key " + e.Key }

// Problem implements problem.Problemer: the caller learns nothing about the file system.
func (e *PathRejected) Problem() problem.Problem { return docNotFound(e.Key) }

func docNotFound(key string) problem.Problem {
	return problem.New(http.StatusNotFound, problem.CodeDocumentNotFound, "Document not found", "No document with key "+key+".")
}

// FindDocument implements target.DocumentFinder: the file at the key's computed path (OC-039).
func (c *Connector) FindDocument(_ context.Context, conn target.Conn, loc target.Location, key string) (target.Document, error) {
	cfg, dir, err := c.folderPath(conn, loc.Folder)
	if err != nil {
		return target.Document{}, err
	}
	_, root, _ := c.parse(conn.Config)
	rel, _ := cfg.DocPath(loc.Folder, key)
	abs, err := realPath(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil || !within(abs, dir) {
		return target.Document{}, &PathRejected{Key: key}
	}
	st, err := os.Lstat(abs)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return target.Document{}, docNotFound(key)
	case err != nil:
		return target.Document{}, err
	case !st.Mode().IsRegular():
		return target.Document{}, &PathRejected{Key: key}
	}
	body, err := readDoc(abs, cfg)
	if err != nil {
		return target.Document{}, err
	}
	mod := st.ModTime().UTC()
	return target.Document{Key: key, Path: rel, Bytes: st.Size(), ModifiedAt: &mod, Body: body}, nil
}

// ListOutput implements target.OutputLister: a page of the mapping's documents sorted by key,
// after cursor (a key), from the merged manifest, or from the folder when there's none.
func (c *Connector) ListOutput(ctx context.Context, conn target.Conn, loc target.Location, mappingID, cursor string, limit int) ([]target.OutputItem, string, error) {
	cfg, dir, err := c.folderPath(conn, loc.Folder)
	if err != nil {
		return nil, "", err
	}
	_, root, _ := c.parse(conn.Config)
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var all []target.OutputItem
	lines, err := ReadManifest(root, mappingID)
	switch {
	case err == nil:
		for _, l := range lines {
			all = append(all, target.OutputItem{Key: l.Key, Path: l.Path, Bytes: l.Bytes})
		}
	case errors.Is(err, os.ErrNotExist):
		if all, err = walkOutput(ctx, cfg, root, dir); err != nil {
			return nil, "", err
		}
	default:
		return nil, "", err
	}
	i := sort.Search(len(all), func(i int) bool { return all[i].Key > cursor })
	if cursor == "" {
		i = 0
	}
	page := all[i:min(i+limit, len(all))]
	next := ""
	if i+limit < len(all) {
		next = page[len(page)-1].Key
	}
	return append([]target.OutputItem{}, page...), next, nil
}

// walkOutput lists a folder's document files, keys decoded from their names.
func walkOutput(ctx context.Context, cfg Config, root, dir string) ([]target.OutputItem, error) {
	ext := cfg.Ext()
	var out []target.OutputItem
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || ctx.Err() != nil {
			return errors.Join(err, ctx.Err())
		}
		name := d.Name()
		if d.IsDir() {
			if p != dir && (strings.HasPrefix(name, ".") || strings.HasPrefix(name, reservedPrefix)) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ext) || strings.HasPrefix(name, ".") {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		key, ok := keyOf(cfg, dir, p, ext)
		if !ok {
			key = strings.TrimSuffix(name, ext) // a hashed name: only the manifest knows its key
		}
		info, _ := d.Info()
		var size int64
		if info != nil {
			size = info.Size()
		}
		out = append(out, target.OutputItem{Key: key, Path: filepath.ToSlash(rel), Bytes: size})
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, err
}

// keyOf decodes a file's key from its path under the mapping folder, per layout.
func keyOf(cfg Config, dir, path, ext string) (string, bool) {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return "", false
	}
	segs := strings.Split(filepath.ToSlash(strings.TrimSuffix(rel, ext)), "/")
	switch cfg.Layout {
	case "key-path":
		keys := make([]string, len(segs))
		for i, s := range segs {
			if s == "%00" {
				continue
			}
			k, ok := pathcodec.Decode(s)
			if !ok {
				return "", false
			}
			keys[i] = k
		}
		return strings.Join(keys, pathcodec.Separator), true
	default:
		return pathcodec.Decode(segs[len(segs)-1])
	}
}
