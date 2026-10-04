package directory

import (
	"bufio"
	"container/heap"
	"encoding/json"
	"errors"
	"hash/fnv"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// mergeRunLines is how many manifest lines are sorted in memory at a time: bigger manifests are
// sorted in runs on disk and merged, so merging a mapping of millions of documents stays within
// the worker's memory budget (architecture §7, OC-056).
var mergeRunLines = 100_000

// rawLine is a manifest line with its key, kept as written.
type rawLine struct {
	key string
	raw []byte
}

// scanManifest calls fn with the key and bytes of each valid line of path (raw is only valid
// during the call).
func scanManifest(path string, fn func(key string, raw []byte) error) error {
	f, err := os.Open(path) //nolint:gosec // inside the allowed target folder
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		var k struct {
			Key string `json:"key"`
		}
		if json.Unmarshal(sc.Bytes(), &k) != nil || k.Key == "" {
			continue
		}
		if err := fn(k.Key, sc.Bytes()); err != nil {
			return err
		}
	}
	return sc.Err()
}

// sortRun sorts a run by key and keeps the last line of each key (lines are in write order).
func sortRun(run []rawLine) []rawLine {
	sort.SliceStable(run, func(i, j int) bool { return run[i].key < run[j].key })
	out := run[:0]
	for i, l := range run {
		if i+1 < len(run) && run[i+1].key == l.key {
			continue
		}
		out = append(out, l)
	}
	return out
}

// mergeSorted writes the deduplicated, key-sorted union of inputs (in write order: the first
// one oldest) to out and returns the number of lines; it holds one run in memory at a time.
func mergeSorted(inputs []string, tmpDir string, out io.Writer) (int, error) {
	var runs []string
	defer func() {
		for _, r := range runs {
			_ = os.Remove(r)
		}
	}()
	var run []rawLine
	var single []rawLine // the only run, kept in memory when everything fits
	spill := func() error {
		if len(run) == 0 {
			return nil
		}
		f, err := os.CreateTemp(tmpDir, ".merge-*.jsonl")
		if err != nil {
			return err
		}
		runs = append(runs, f.Name())
		w := bufio.NewWriterSize(f, 64<<10)
		for _, l := range sortRun(run) {
			_, _ = w.Write(l.raw)
			_ = w.WriteByte('\n')
		}
		run = run[:0]
		return errors.Join(w.Flush(), f.Close())
	}
	for _, in := range inputs {
		err := scanManifest(in, func(key string, raw []byte) error {
			run = append(run, rawLine{key: key, raw: append([]byte(nil), raw...)})
			if len(run) >= mergeRunLines {
				return spill()
			}
			return nil
		})
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return 0, err
		}
	}
	bw := bufio.NewWriterSize(out, 64<<10)
	if len(runs) == 0 {
		single = sortRun(run)
		for _, l := range single {
			_, _ = bw.Write(l.raw)
			_ = bw.WriteByte('\n')
		}
		return len(single), bw.Flush()
	}
	if err := spill(); err != nil {
		return 0, err
	}
	n, err := kMerge(runs, bw)
	return n, errors.Join(err, bw.Flush())
}

// cursor reads one sorted run.
type cursor struct {
	sc   *bufio.Scanner
	f    *os.File
	idx  int // the run's order: a later run's line wins a tie
	line rawLine
}

func (c *cursor) next() bool {
	for c.sc.Scan() {
		var k struct {
			Key string `json:"key"`
		}
		if json.Unmarshal(c.sc.Bytes(), &k) == nil && k.Key != "" {
			c.line = rawLine{key: k.Key, raw: c.sc.Bytes()}
			return true
		}
	}
	return false
}

type cursorHeap []*cursor

func (h cursorHeap) Len() int { return len(h) }
func (h cursorHeap) Less(i, j int) bool {
	if h[i].line.key != h[j].line.key {
		return h[i].line.key < h[j].line.key
	}
	return h[i].idx > h[j].idx // the newest first
}
func (h cursorHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *cursorHeap) Push(x any)   { *h = append(*h, x.(*cursor)) }
func (h *cursorHeap) Pop() any {
	old := *h
	c := old[len(old)-1]
	*h = old[:len(old)-1]
	return c
}

// kMerge merges sorted, per-run deduplicated runs: for a key in several runs, the latest wins.
func kMerge(runs []string, w *bufio.Writer) (int, error) {
	h := make(cursorHeap, 0, len(runs))
	defer func() {
		for _, c := range h {
			_ = c.f.Close()
		}
	}()
	for i, r := range runs {
		f, err := os.Open(r) //nolint:gosec // our own temp run
		if err != nil {
			return 0, err
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64<<10), 4<<20)
		c := &cursor{sc: sc, f: f, idx: i}
		if c.next() {
			h = append(h, c)
		} else {
			_ = f.Close()
		}
	}
	heap.Init(&h)
	n := 0
	last := ""
	for h.Len() > 0 {
		c := h[0]
		if n == 0 || c.line.key != last {
			last = c.line.key
			_, _ = w.Write(c.line.raw)
			_ = w.WriteByte('\n')
			n++
		}
		if c.next() {
			heap.Fix(&h, 0)
		} else {
			_ = c.f.Close()
			heap.Pop(&h)
		}
	}
	for _, c := range h {
		if err := c.sc.Err(); err != nil {
			return n, err
		}
	}
	return n, nil
}

// MergeManifest folds a mapping's fragments into its manifest: deduplicated by key (the last
// write wins: the merged file, then fragments in writer order, since writer IDs sort by time),
// sorted by key, replaced atomically; the fragments are then removed (OC-039 AC4). Memory is
// bounded by mergeRunLines, not by the number of documents.
func MergeManifest(root, mapping string) (int, error) {
	dir := filepath.Join(root, odbDir, manifestDir)
	frags, err := filepath.Glob(filepath.Join(dir, mapping+"-*.jsonl"))
	if err != nil {
		return 0, err
	}
	sort.Strings(frags)
	merged := manifestPath(root, mapping)
	if len(frags) == 0 {
		n := 0
		err := scanManifest(merged, func(string, []byte) error { n++; return nil })
		if errors.Is(err, os.ErrNotExist) {
			err = nil
		}
		return n, err
	}
	tmp := merged + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640) //nolint:gosec // shared with the output's readers
	if err != nil {
		return 0, err
	}
	n, err := mergeSorted(append([]string{merged}, frags...), dir, f)
	if err = errors.Join(err, f.Close()); err != nil {
		_ = os.Remove(tmp)
		return 0, err
	}
	if err := os.Rename(tmp, merged); err != nil {
		return 0, err
	}
	for _, f := range frags {
		_ = os.Remove(f)
	}
	return n, nil
}

// keyHash is a 64-bit hash of a key, to count distinct keys in 8 bytes each.
func keyHash(key string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	return h.Sum64()
}
