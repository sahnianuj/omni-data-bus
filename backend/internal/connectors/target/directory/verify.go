package directory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"omnidatabus/internal/connectors/target"
)

// CountDocuments implements target.Counter: the document files under a mapping's folder, at
// any depth (sharded and key-path layouts nest), without hidden folders (.odb-tmp) or _odb. It
// reads directories in batches, so a flat folder of millions of files isn't held in memory.
func (c *Connector) CountDocuments(ctx context.Context, conn target.Conn, loc target.Location) (int64, error) {
	cfg, dir, err := c.folderPath(conn, loc.Folder)
	if err != nil {
		return 0, err
	}
	ext := cfg.Ext()
	var n int64
	err = walkBatched(ctx, dir, func(name string) {
		if strings.HasSuffix(name, ext) && !strings.HasPrefix(name, ".") {
			n++
		}
	})
	return n, err
}

// walkBatched calls fn with the name of every file under dir, reading 1,024 entries at a time
// in directory order, and skips hidden and reserved folders below dir.
func walkBatched(ctx context.Context, dir string, fn func(name string)) error {
	f, err := os.Open(dir) //nolint:gosec // inside the allowed target folder
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		entries, err := f.ReadDir(1024)
		for _, e := range entries {
			name := e.Name()
			if !e.IsDir() {
				fn(name)
				continue
			}
			if strings.HasPrefix(name, ".") || strings.HasPrefix(name, reservedPrefix) {
				continue
			}
			if err := walkBatched(ctx, filepath.Join(dir, name), fn); err != nil {
				return err
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
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
	// One pass over the manifest (the merged file, then fragments in write order): a reservoir
	// sample of the keys, each with its latest line, and the distinct keys counted by 8-byte
	// hash, so memory doesn't grow with the documents (architecture §7).
	frags, _ := filepath.Glob(filepath.Join(w.root, odbDir, manifestDir, w.mapping+"-*.jsonl"))
	sort.Strings(frags)
	seen := map[uint64]struct{}{}
	var lines []ManifestLine
	slot := map[string]int{} // a sampled key → its index in lines
	occurrences := 0
	for _, f := range append([]string{manifestPath(w.root, w.mapping)}, frags...) {
		err := readLines(f, func(l ManifestLine) {
			seen[keyHash(l.Key)] = struct{}{}
			if i, ok := slot[l.Key]; ok {
				lines[i] = l // the last write wins
				return
			}
			occurrences++
			switch {
			case sample <= 0 || len(lines) < sample:
				slot[l.Key] = len(lines)
				lines = append(lines, l)
			default:
				if j := rand.IntN(occurrences); j < sample { //nolint:gosec // sampling
					delete(slot, lines[j].Key)
					slot[l.Key] = j
					lines[j] = l
				}
			}
		})
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return rep, err
		}
	}
	rep.Listed = len(seen)
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
