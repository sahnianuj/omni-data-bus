package directory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"

	"omnidatabus/internal/connectors/target"
)

// CountDocuments implements target.Counter: the document files under a mapping's folder, at
// any depth (sharded and key-path layouts nest), without hidden folders (.odb-tmp) or _odb.
func (c *Connector) CountDocuments(ctx context.Context, conn target.Conn, loc target.Location) (int64, error) {
	cfg, dir, err := c.folderPath(conn, loc.Folder)
	if err != nil {
		return 0, err
	}
	ext := cfg.Ext()
	var n int64
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		name := d.Name()
		if d.IsDir() {
			if p != dir && (strings.HasPrefix(name, ".") || strings.HasPrefix(name, reservedPrefix)) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(name, ext) && !strings.HasPrefix(name, ".") {
			n++
		}
		return nil
	})
	return n, err
}

// CheckIntegrity implements target.IntegrityChecker: a random sample of the manifest (up to
// sample lines; all when sample <= 0) is checked against the files: each exists, and its
// sha256 matches what was written (OC-041 AC3).
func (w *writer) CheckIntegrity(ctx context.Context, sample int) (target.IntegrityReport, error) {
	rep := target.IntegrityReport{Issues: []target.IntegrityIssue{}}
	if w.manifest == nil {
		return rep, nil
	}
	if err := w.manifest.flush(); err != nil {
		return rep, err
	}
	byKey := map[string]ManifestLine{}
	frags, _ := filepath.Glob(filepath.Join(w.root, odbDir, manifestDir, w.mapping+"-*.jsonl"))
	for _, f := range append([]string{manifestPath(w.root, w.mapping)}, frags...) {
		if err := readLines(f, func(l ManifestLine) { byKey[l.Key] = l }); err != nil && !errors.Is(err, os.ErrNotExist) {
			return rep, err
		}
	}
	lines := make([]ManifestLine, 0, len(byKey))
	for _, l := range byKey {
		lines = append(lines, l)
	}
	rep.Listed = len(lines)
	if sample > 0 && len(lines) > sample {
		rand.Shuffle(len(lines), func(i, j int) { lines[i], lines[j] = lines[j], lines[i] }) //nolint:gosec // sampling
		lines = lines[:sample]
	}
	for _, l := range lines {
		if ctx.Err() != nil {
			return rep, ctx.Err()
		}
		rep.Checked++
		b, err := os.ReadFile(filepath.Join(w.root, filepath.FromSlash(l.Path))) //nolint:gosec // a manifest path inside the target folder
		switch {
		case errors.Is(err, os.ErrNotExist):
			rep.Missing++
			rep.Issues = append(rep.Issues, target.IntegrityIssue{Key: l.Key, Path: l.Path, Problem: "missing"})
		case err != nil:
			return rep, err
		default:
			sum := sha256.Sum256(b)
			if hex.EncodeToString(sum[:]) != l.SHA256 {
				rep.Mismatched++
				rep.Issues = append(rep.Issues, target.IntegrityIssue{Key: l.Key, Path: l.Path, Problem: "hash_mismatch"})
			}
		}
	}
	if len(rep.Issues) > 20 {
		rep.Issues = rep.Issues[:20]
	}
	return rep, nil
}
