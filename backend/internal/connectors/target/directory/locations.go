package directory

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"omnidatabus/internal/connectors/target"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/problem"
)

// countCap bounds the file count of a folder listing ("10,000+").
const countCap = 10_000

// folderPath resolves a folder under the connection's root, refusing names that break the
// folder rules and anything whose abs path leaves the root (a symlink pointing out).
func (c *Connector) folderPath(conn target.Conn, folder string) (Config, string, error) {
	cfg, root, err := c.parse(conn.Config)
	if err != nil {
		return cfg, "", err
	}
	folder = strings.Trim(folder, "/")
	if folder == "" {
		return cfg, root, nil
	}
	if err := mapping.ValidateFolder(folder); err != nil {
		return cfg, "", err
	}
	abs, err := realPath(filepath.Join(root, filepath.FromSlash(folder)))
	if err != nil || !within(abs, root) {
		return cfg, "", problem.New(http.StatusUnprocessableEntity, problem.CodeLocationInvalid, "Invalid location", "Folder "+folder+" leads outside the target folder.")
	}
	return cfg, abs, nil
}

func notFound(folder string) problem.Problem {
	return problem.New(http.StatusNotFound, problem.CodeNotFound, "Folder not found", "Folder "+folder+"/ doesn't exist.")
}

// count counts a folder's entries up to countCap; more is true past it.
func count(dir string) (n int64, more bool) {
	f, err := os.Open(dir) //nolint:gosec // resolved inside an allowed root
	if err != nil {
		return 0, false
	}
	defer func() { _ = f.Close() }()
	for {
		names, err := f.Readdirnames(1000)
		for _, name := range names {
			if !strings.HasPrefix(name, ".") && !strings.HasPrefix(name, reservedPrefix) {
				n++
			}
		}
		if n > countCap {
			return countCap, true
		}
		if err != nil || len(names) == 0 {
			return n, false
		}
	}
}

func (c *Connector) node(folder, abs string) target.LocationNode {
	n, more := count(abs)
	return target.LocationNode{
		Name: filepath.Base(folder), Level: "folder", Path: folder, Leaf: true, DocCount: &n, DocCountMore: more,
		Location:  target.Location{Kind: target.KindDirectory, Folder: folder},
		CanCreate: strings.Count(folder, "/") < 2, // folders nest up to 3 levels
	}
}

// ListLocations implements target.LocationBrowser: the sub-folders of parent (not files),
// without hidden and _odb folders, each with an approximate file count (OC-038 AC3).
func (c *Connector) ListLocations(_ context.Context, conn target.Conn, parent target.Location) ([]target.LocationNode, error) {
	_, dir, err := c.folderPath(conn, parent.Folder)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, notFound(parent.Folder)
	}
	if err != nil {
		return nil, err
	}
	out := []target.LocationNode{}
	prefix := strings.Trim(parent.Folder, "/")
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || strings.HasPrefix(name, ".") || strings.HasPrefix(name, reservedPrefix) {
			continue
		}
		folder := name
		if prefix != "" {
			folder = prefix + "/" + name
		}
		out = append(out, c.node(folder, filepath.Join(dir, name)))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// StatLocation implements target.LocationBrowser.
func (c *Connector) StatLocation(_ context.Context, conn target.Conn, loc target.Location) (target.LocationNode, error) {
	folder := strings.Trim(loc.Folder, "/")
	if folder == "" {
		return target.LocationNode{}, mapping.ValidateFolder(folder)
	}
	_, abs, err := c.folderPath(conn, folder)
	if err != nil {
		return target.LocationNode{}, err
	}
	st, err := os.Stat(abs)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return target.LocationNode{}, notFound(folder)
	case err != nil:
		return target.LocationNode{}, err
	case !st.IsDir():
		return target.LocationNode{}, problem.New(http.StatusUnprocessableEntity, problem.CodeLocationInvalid, "Not a folder", folder+" is a file.")
	}
	return c.node(folder, abs), nil
}

// CreateLocation implements target.LocationBrowser: the folder and its missing parents.
func (c *Connector) CreateLocation(_ context.Context, conn target.Conn, loc target.Location) error {
	folder := strings.Trim(loc.Folder, "/")
	if err := mapping.ValidateFolder(folder); err != nil {
		return err
	}
	cfg, abs, err := c.folderPath(conn, folder)
	if err != nil {
		return err
	}
	if st, err := os.Stat(abs); err == nil {
		if !st.IsDir() {
			return problem.New(http.StatusConflict, problem.CodeKeyspaceExists, "Name taken", folder+" exists and is a file.")
		}
		return problem.New(http.StatusConflict, problem.CodeKeyspaceExists, "Folder exists", "Folder "+folder+"/ already exists.")
	}
	if err := os.MkdirAll(abs, cfg.DirMode()); err != nil {
		return fmt.Errorf("create folder %s: %w", folder, err)
	}
	return nil
}

// Preflight implements target.Preflighter: each mapping's folder exists (or is created when the
// mapping allows it), with a warning when it already holds files.
func (c *Connector) Preflight(ctx context.Context, conn target.Conn, locs []target.PreflightLocation) []target.LocationPreflight {
	out := c.preflightFolders(ctx, conn, locs)
	if len(out) == 0 {
		return out
	}
	// The whole job lands on one file system: one space check, on the first mapping (OC-040 AC5).
	if chk, ok := c.spaceCheck(conn, locs); ok {
		out[0].Checks = append(out[0].Checks, chk)
	}
	for i, l := range locs {
		if n := c.cleanOrphans(conn, l.Location.Folder, time.Now()); n > 0 {
			out[i].Checks = append(out[i].Checks, target.PreflightCheck{
				ID: "orphans", Label: "leftover temporary files removed", Status: "ok", MappingID: l.MappingID,
				Detail: fmt.Sprintf("Removed %d temporary file(s) left by an interrupted run.", n),
			})
		}
	}
	return out
}

// spaceNeeded estimates a job's bytes (rows × average size × 1.2 for JSON overhead) and files.
func spaceNeeded(locs []target.PreflightLocation) (bytes, files uint64) {
	for _, l := range locs {
		avg := l.AvgDocBytes
		if avg <= 0 {
			avg = 1024
		}
		if l.EstimatedDocs > 0 {
			bytes += uint64(float64(l.EstimatedDocs*avg) * 1.2) //nolint:gosec // estimates are positive
			files += uint64(l.EstimatedDocs)                    //nolint:gosec // positive
		}
	}
	return bytes, files
}

// spaceCheck warns when free space or inodes are below the estimate, and fails below 10% of it.
func (c *Connector) spaceCheck(conn target.Conn, locs []target.PreflightLocation) (target.PreflightCheck, bool) {
	need, files := spaceNeeded(locs)
	_, root, err := c.parse(conn.Config)
	if err != nil || need == 0 {
		return target.PreflightCheck{}, false
	}
	info, err := c.statFS(root)
	if err != nil {
		return target.PreflightCheck{}, false
	}
	chk := target.PreflightCheck{
		ID: "space", Label: "target has room for the files", Status: "ok", MappingID: locs[0].MappingID,
		Detail: fmt.Sprintf("About %s needed for %d files; %s free.", gib(need), files, gib(info.FreeBytes)),
	}
	switch {
	case info.FreeBytes < need/10 || (info.FreeInodes > 0 && info.FreeInodes < files/10):
		chk.Status, chk.Code = "fail", target.CodeDiskFull
		chk.Hint = "Free up space, or choose a folder on a bigger disk."
	case info.FreeBytes < need || (info.FreeInodes > 0 && info.FreeInodes < files):
		chk.Status, chk.Code = "warn", CodeLowSpace
		chk.Hint = "The migration may run out of space before it finishes (it can be resumed once space is freed)."
	}
	if info.FreeInodes > 0 && info.FreeInodes < files {
		chk.Detail += fmt.Sprintf(" Only %d inodes (files) free.", info.FreeInodes)
	}
	return chk, true
}

// cleanOrphans removes temp files older than before from a mapping folder's .odb-tmp (left by a
// crashed run; a running writer's files are newer).
func (c *Connector) cleanOrphans(conn target.Conn, folder string, before time.Time) int {
	_, dir, err := c.folderPath(conn, folder)
	if err != nil {
		return 0
	}
	tmps, _ := filepath.Glob(filepath.Join(dir, tmpDirName, "*.tmp"))
	n := 0
	for _, p := range tmps {
		if st, err := os.Stat(p); err == nil && st.ModTime().Before(before) && os.Remove(p) == nil {
			n++
		}
	}
	return n
}

func (c *Connector) preflightFolders(ctx context.Context, conn target.Conn, locs []target.PreflightLocation) []target.LocationPreflight {
	out := make([]target.LocationPreflight, len(locs))
	for i, l := range locs {
		name := l.Location.String()
		chk := target.PreflightCheck{ID: "keyspace", Label: "target " + name, Status: "ok", MappingID: l.MappingID}
		n, err := c.StatLocation(ctx, conn, l.Location)
		switch {
		case err == nil:
		case problem.HasCode(err, problem.CodeNotFound) && l.CreateIfMissing:
			if cerr := c.CreateLocation(ctx, conn, l.Location); cerr != nil && !problem.HasCode(cerr, problem.CodeKeyspaceExists) {
				chk.Status, chk.Detail = "fail", "Couldn't create the folder: "+message(cerr)
				out[i] = target.LocationPreflight{Checks: []target.PreflightCheck{chk}}
				continue
			}
			chk.Detail = "Created " + name + "."
			out[i] = target.LocationPreflight{Checks: []target.PreflightCheck{chk}, Empty: true}
			continue
		case problem.HasCode(err, problem.CodeNotFound):
			chk.Status, chk.Code = "fail", target.CodeKeyspaceMissing
			chk.Detail, chk.Hint = "The folder doesn't exist.", "Create it, or turn on \"Create when migration starts\" in the Tables step."
			out[i] = target.LocationPreflight{Checks: []target.PreflightCheck{chk}}
			continue
		default:
			chk.Status, chk.Detail = "fail", message(err)
			out[i] = target.LocationPreflight{Checks: []target.PreflightCheck{chk}}
			continue
		}
		res := target.LocationPreflight{Checks: []target.PreflightCheck{chk}, Empty: *n.DocCount == 0}
		if *n.DocCount > 0 {
			w := target.PreflightCheck{
				ID: "empty", Label: "target folder is empty", Status: "warn", Code: "FOLDER_NOT_EMPTY", MappingID: l.MappingID,
				Detail: fmt.Sprintf("%s already holds %s files.", name, countText(*n.DocCount, n.DocCountMore)),
			}
			if l.WriteMode == "insert" {
				w.Detail += " Insert mode skips documents whose files already exist."
			}
			res.Checks = append(res.Checks, w)
		}
		out[i] = res
	}
	return out
}

func countText(n int64, more bool) string {
	if more {
		return fmt.Sprintf("%d+", n)
	}
	return fmt.Sprint(n)
}

func message(err error) string {
	var pr problem.Problemer
	if errors.As(err, &pr) {
		return pr.Problem().Detail
	}
	var p problem.Problem
	if errors.As(err, &p) && p.Detail != "" {
		return p.Detail
	}
	return err.Error()
}
