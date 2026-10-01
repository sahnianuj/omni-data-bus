package directory

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/google/uuid"

	"omnidatabus/internal/connectors"
	"omnidatabus/internal/connectors/target"
	"omnidatabus/internal/problem"
)

// Writer tuning (OC-040).
const (
	defaultWriters     = 16
	writeTries         = 4 // the first attempt and 3 retries
	maxConsecutiveFail = 50
	tmpDirName         = ".odb-tmp"
	collisionSetMax    = 5_000_000
)

// writer writes one file per document into a mapping's folder: temp file on the same file
// system, fsync, then rename (upsert), link (insert) or rename-if-present (replace), so the
// folder never holds a partial document (OC-040).
type writer struct {
	fs       fsys
	cfg      Config
	root     string // the target folder (resolved)
	folder   string // the mapping's folder, relative to root
	tmpDir   string
	mode     string
	workers  int
	writerID string
	seq      atomic.Int64
	dirs     sync.Map // created directories
	manifest *fragment
	mapping  string
	sleep    func(context.Context, time.Duration) error

	caseFold   bool
	collisions *collisionGuard

	consecutive atomic.Int64
	bytes       atomic.Int64
}

// OpenWriter implements target.WriterOpener for a directory target.
func (c *Connector) OpenWriter(_ context.Context, raw json.RawMessage, _ connectors.Credentials, o target.WriteOptions) (target.DocWriter, error) {
	cfg, root, err := c.parse(raw)
	if err != nil {
		return nil, err
	}
	return c.openWriter(osFS{}, cfg, root, o)
}

func locationMissing(detail string) problem.Problem {
	return problem.New(http.StatusUnprocessableEntity, target.CodeLocationMissing, "Target folder missing", detail)
}

func (c *Connector) openWriter(fsys fsys, cfg Config, root string, o target.WriteOptions) (*writer, error) {
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		return nil, locationMissing(root + " isn't there (an unmounted volume?).")
	}
	folder := strings.Trim(o.Location.Folder, "/")
	if folder != "" {
		if _, _, err := c.folderPath(target.Conn{Config: cfg.JSON()}, folder); err != nil {
			return nil, err
		}
	}
	dir := filepath.Join(root, filepath.FromSlash(folder))
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return nil, problem.New(http.StatusUnprocessableEntity, target.CodeKeyspaceMissing, "Target folder missing", folder+"/ doesn't exist.")
	}
	w := &writer{
		fs: fsys, cfg: cfg, root: root, folder: folder, tmpDir: filepath.Join(dir, tmpDirName), mode: o.Mode,
		workers: o.Concurrency, writerID: uuid.Must(uuid.NewV7()).String(), mapping: o.MappingID, sleep: sleepCtx,
	}
	if w.workers <= 0 {
		w.workers = defaultWriters
	}
	if w.mapping == "" {
		w.mapping = strings.ReplaceAll(folder, "/", "_")
	}
	if err := fsys.MkdirAll(w.tmpDir, cfg.DirMode()); err != nil {
		return nil, fmt.Errorf("create %s: %w", w.tmpDir, err)
	}
	if sensitive, err := caseSensitive(dir); err == nil && !sensitive {
		w.caseFold, w.collisions = true, newCollisionGuard(collisionSetMax)
	}
	if cfg.Manifest {
		m, err := openFragment(root, w.mapping, w.writerID, 0o640)
		if err != nil {
			return nil, err
		}
		w.manifest = m
		_ = writeRunInfo(root, cfg, folder, RunMapping{
			MappingID: o.MappingID, JobID: o.JobID, PlanHash: o.PlanHash, KeyTemplate: o.KeyTemplate, UpdatedAt: time.Now().UTC(),
		})
	}
	return w, nil
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// WriteBatch implements target.DocWriter: one result per document, in order.
// MaxDocBytes implements target.DocLimiter.
func (w *writer) MaxDocBytes() int64 { return w.cfg.MaxDocBytes }

func (w *writer) WriteBatch(ctx context.Context, docs []target.Doc) []target.WriteResult {
	out := make([]target.WriteResult, len(docs))
	parents := make([]string, len(docs))
	var wg sync.WaitGroup
	sem := make(chan struct{}, w.workers)
	for i := range docs {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			for j := i; j < len(docs); j++ {
				out[j] = target.WriteResult{Key: docs[j].Key, Outcome: target.Failed, Code: target.CodeWriteFailed, Message: ctx.Err().Error()}
			}
			wg.Wait()
			return out
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			out[i], parents[i] = w.write(ctx, docs[i])
		}()
	}
	wg.Wait()
	if w.cfg.Durability == "file+dir" {
		// Renames are durable once their folders are synced: once per folder per batch.
		seen := map[string]bool{}
		for i, p := range parents {
			if p == "" || seen[p] || out[i].Outcome != target.Written {
				continue
			}
			seen[p] = true
			if err := w.fs.SyncDir(p); err != nil {
				code, fatal := classify(err)
				for j := range out {
					if parents[j] == p && out[j].Outcome == target.Written {
						out[j] = target.WriteResult{Key: out[j].Key, Outcome: target.Failed, Code: code, Fatal: fatal, Message: err.Error(), Attempts: out[j].Attempts}
					}
				}
			}
		}
	}
	if w.manifest != nil {
		_ = w.manifest.flush()
	}
	return out
}

// payload is what goes into the file: pretty-printed and/or gzipped as configured.
func (w *writer) payload(v []byte) ([]byte, error) {
	if w.cfg.Pretty {
		var b bytes.Buffer
		if err := json.Indent(&b, v, "", "  "); err == nil {
			v = b.Bytes()
		}
	}
	if w.cfg.Compression == "gzip" {
		var b bytes.Buffer
		zw := gzip.NewWriter(&b)
		if _, err := zw.Write(v); err != nil {
			return nil, err
		}
		if err := zw.Close(); err != nil {
			return nil, err
		}
		v = b.Bytes()
	}
	return v, nil
}

// write writes one document, retrying transient failures; parent is its folder (for the
// batch's directory sync).
func (w *writer) write(ctx context.Context, d target.Doc) (res target.WriteResult, parent string) {
	res = target.WriteResult{Key: d.Key}
	fail := func(code, msg string, fatal bool) (target.WriteResult, string) {
		res.Outcome, res.Code, res.Message, res.Fatal = target.Failed, code, msg, fatal
		return res, ""
	}
	if d.Key == "" {
		return fail(target.CodeKeyInvalid, "The document key is empty.", false)
	}
	data, err := w.payload(d.Value)
	if err != nil {
		return fail(target.CodeWriteFailed, err.Error(), false)
	}
	if int64(len(data)) > w.cfg.MaxDocBytes {
		return fail(target.CodeDocTooLarge, fmt.Sprintf("The document is %d bytes; the target allows %d.", len(data), w.cfg.MaxDocBytes), false)
	}
	rel, hashed := w.cfg.DocPath(w.folder, d.Key)
	final := filepath.Join(w.root, filepath.FromSlash(rel))
	parent = filepath.Dir(final)
	if w.caseFold && w.mode != "insert" {
		if other, clash := w.collisions.check(final); clash {
			return fail(target.CodeKeyPathCollision, "Key "+d.Key+" maps to the same file as "+other+" on this case-insensitive folder.", false)
		}
	}
	for attempt := 1; ; attempt++ {
		res.Attempts = attempt
		outcome, err := w.commit(ctx, data, final, parent)
		if err == nil {
			w.consecutive.Store(0)
			res.Outcome = outcome
			if outcome == target.Written {
				w.bytes.Add(int64(len(data)))
				if w.manifest != nil {
					sum := sha256.Sum256(data)
					_ = w.manifest.add(ManifestLine{Key: d.Key, Path: rel, Bytes: int64(len(data)), SHA256: hex.EncodeToString(sum[:]), Hashed: hashed, SourcePK: d.SourceKey})
				}
			}
			return res, parent
		}
		code, fatal := classify(err)
		if ctx.Err() != nil {
			return fail(target.CodeWriteFailed, ctx.Err().Error(), false)
		}
		if code != target.CodeWriteFailed || attempt >= writeTries {
			if code == target.CodeWriteFailed && w.consecutive.Add(1) >= maxConsecutiveFail {
				fatal = true
			}
			return fail(code, err.Error(), fatal)
		}
		if w.sleep(ctx, time.Duration(10<<(2*(attempt-1)))*time.Millisecond) != nil {
			return fail(target.CodeWriteFailed, ctx.Err().Error(), false)
		}
	}
}

// commit writes data to a temp file and moves it into place by write mode.
func (w *writer) commit(ctx context.Context, data []byte, final, parent string) (target.Outcome, error) {
	if _, done := w.dirs.Load(parent); !done {
		if err := w.fs.MkdirAll(parent, w.cfg.DirMode()); err != nil {
			return target.Failed, err
		}
		w.dirs.Store(parent, true)
	}
	tmp := filepath.Join(w.tmpDir, fmt.Sprintf("%s-%d.tmp", w.writerID, w.seq.Add(1)))
	f, err := w.fs.Create(tmp, w.fileMode())
	if err != nil {
		return target.Failed, err
	}
	_, err = f.Write(data)
	if err == nil && w.cfg.Durability != "none" {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err == nil && ctx.Err() != nil {
		err = ctx.Err() // cancelled: never publish after cancellation is observed
	}
	if err != nil {
		_ = w.fs.Remove(tmp)
		return target.Failed, err
	}
	switch w.mode {
	case "insert":
		err := w.fs.Link(tmp, final)
		_ = w.fs.Remove(tmp)
		if errors.Is(err, os.ErrExist) {
			return target.Skipped, nil
		}
		return target.Written, err
	case "replace":
		if _, err := w.fs.Lstat(final); errors.Is(err, os.ErrNotExist) {
			_ = w.fs.Remove(tmp)
			return target.Skipped, nil
		}
	}
	if err := w.fs.Rename(tmp, final); err != nil {
		_ = w.fs.Remove(tmp)
		return target.Failed, err
	}
	return target.Written, nil
}

func (w *writer) fileMode() os.FileMode {
	m, err := w.cfg.Mode()
	if err != nil {
		return 0o640
	}
	return m
}

// classify maps a write error to its code and whether it's fatal (OC-040 error table).
// WRITE_FAILED is the retryable class.
func classify(err error) (code string, fatal bool) {
	switch {
	case errors.Is(err, syscall.ENOSPC), errors.Is(err, syscall.EDQUOT):
		return target.CodeDiskFull, true
	case errors.Is(err, syscall.EACCES), errors.Is(err, syscall.EPERM), errors.Is(err, syscall.EROFS), errors.Is(err, os.ErrPermission):
		return target.CodePermissionDenied, true
	case errors.Is(err, syscall.ENAMETOOLONG):
		return target.CodeKeyInvalid, false
	case errors.Is(err, os.ErrNotExist):
		return target.CodeLocationMissing, true // the folder vanished under us (an unmounted volume)
	}
	return target.CodeWriteFailed, false
}

// Close implements target.DocWriter: flushes and merges the manifest, and removes the temp
// folder when it's empty.
func (w *writer) Close() error {
	var err error
	if w.manifest != nil {
		err = w.manifest.close()
		if _, merr := MergeManifest(w.root, w.mapping); merr != nil {
			err = errors.Join(err, merr)
		}
	}
	_ = os.Remove(w.tmpDir) // only succeeds when empty
	return err
}

// BytesWritten is the size of the files written so far.
func (w *writer) BytesWritten() int64 { return w.bytes.Load() }

// GetDocs implements target.DocReader: the documents found at their computed paths, as JSON
// (decompressed), by key (OC-041).
func (w *writer) GetDocs(_ context.Context, keys []string) (map[string][]byte, error) {
	out := make(map[string][]byte, len(keys))
	for _, k := range keys {
		rel, _ := w.cfg.DocPath(w.folder, k)
		b, err := readDoc(filepath.Join(w.root, filepath.FromSlash(rel)), w.cfg)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return out, err
		}
		out[k] = b
	}
	return out, nil
}

// readDoc reads a document file, decompressing gzip.
func readDoc(path string, cfg Config) ([]byte, error) {
	b, err := os.ReadFile(path) //nolint:gosec // a computed path inside the allowed target folder
	if err != nil || cfg.Compression != "gzip" {
		return b, err
	}
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	defer func() { _ = zr.Close() }()
	var out bytes.Buffer
	if _, err := out.ReadFrom(zr); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// collisionGuard remembers the lower-cased paths written on a case-insensitive folder: a
// second key with the same lower-cased path is KEY_PATH_COLLISION. Past max entries it
// switches to a Bloom filter (about 1% false positives).
type collisionGuard struct {
	mu    sync.Mutex
	max   int
	exact map[string]string // lower-cased path → the path first written
	bloom *bloom
}

func newCollisionGuard(limit int) *collisionGuard {
	return &collisionGuard{max: limit, exact: map[string]string{}}
}

// check records path and reports the clashing path, if another one folds to the same name.
func (g *collisionGuard) check(path string) (string, bool) {
	lower := strings.ToLower(path)
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.bloom != nil {
		if g.bloom.has(lower) {
			return filepath.Base(path) + " (possibly a false positive)", true
		}
		g.bloom.add(lower)
		return "", false
	}
	if prev, ok := g.exact[lower]; ok {
		if prev == path {
			return "", false // the same key again (a retry or a resumed run)
		}
		return filepath.Base(prev), true
	}
	g.exact[lower] = path
	if len(g.exact) >= g.max {
		g.bloom = newBloom(g.max*4, 0.01)
		for k := range g.exact {
			g.bloom.add(k)
		}
		g.exact = nil
	}
	return "", false
}
