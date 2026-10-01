package directory

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"omnidatabus/internal/pathcodec"
)

// The output's own files live in _odb/ under the target folder (OC-039): run.json says how
// paths were built, manifest/ lists every document.
const (
	odbDir      = "_odb"
	manifestDir = "manifest"
	runFile     = "run.json"
)

// ManifestLine is one written document.
type ManifestLine struct {
	Key    string `json:"key"`
	Path   string `json:"path"` // relative to the target folder, "/"-separated
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
	// Hashed: the file name is a hash (a key too long to keep), so the manifest is the only
	// way from this key to its file.
	Hashed   bool  `json:"hashed,omitempty"`
	SourcePK []any `json:"sourcePk,omitempty"`
}

// manifestPath is a mapping's merged manifest; fragments are <mapping>-<writer>.jsonl.
func manifestPath(root, mapping string) string {
	return filepath.Join(root, odbDir, manifestDir, mapping+".jsonl")
}

// fragment is one writer's manifest file: appended without locks (one per writer).
type fragment struct {
	mu sync.Mutex
	f  *os.File
	w  *bufio.Writer
}

func openFragment(root, mapping, writerID string, mode fs.FileMode) (*fragment, error) {
	dir := filepath.Join(root, odbDir, manifestDir)
	if err := os.MkdirAll(dir, mode|0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, mapping+"-"+writerID+".jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, mode) //nolint:gosec // inside the allowed target folder
	if err != nil {
		return nil, err
	}
	return &fragment{f: f, w: bufio.NewWriterSize(f, 64<<10)}, nil
}

func (m *fragment) add(l ManifestLine) error {
	b, err := json.Marshal(l)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.w.Write(append(b, '\n')); err != nil {
		return err
	}
	return nil
}

// flush makes appended lines durable enough to survive the writer (not the machine).
func (m *fragment) flush() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.w.Flush()
}

func (m *fragment) close() error {
	err := m.flush()
	return errors.Join(err, m.f.Close())
}

// MergeManifest folds a mapping's fragments into its manifest: deduplicated by key (the last
// write wins: the merged file, then fragments in writer order, since writer IDs sort by time),
// sorted by key, replaced atomically; the fragments are then removed (OC-039 AC4).
func MergeManifest(root, mapping string) (int, error) {
	dir := filepath.Join(root, odbDir, manifestDir)
	frags, err := filepath.Glob(filepath.Join(dir, mapping+"-*.jsonl"))
	if err != nil {
		return 0, err
	}
	sort.Strings(frags)
	merged := manifestPath(root, mapping)
	byKey := map[string]ManifestLine{}
	for _, f := range append([]string{merged}, frags...) {
		if err := readLines(f, func(l ManifestLine) { byKey[l.Key] = l }); err != nil && !errors.Is(err, os.ErrNotExist) {
			return 0, err
		}
	}
	if len(frags) == 0 {
		return len(byKey), nil
	}
	keys := make([]string, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var buf bytes.Buffer
	for _, k := range keys {
		b, _ := json.Marshal(byKey[k])
		buf.Write(b)
		buf.WriteByte('\n')
	}
	tmp := merged + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o640); err != nil { //nolint:gosec // shared with the output's readers
		return 0, err
	}
	if err := os.Rename(tmp, merged); err != nil {
		return 0, err
	}
	for _, f := range frags {
		_ = os.Remove(f)
	}
	return len(keys), nil
}

// readLines calls fn for each line of a manifest file.
func readLines(path string, fn func(ManifestLine)) error {
	f, err := os.Open(path) //nolint:gosec // inside the allowed target folder
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		var l ManifestLine
		if json.Unmarshal(sc.Bytes(), &l) == nil && l.Key != "" {
			fn(l)
		}
	}
	return sc.Err()
}

// ReadManifest returns a mapping's merged manifest lines (sorted by key).
func ReadManifest(root, mapping string) ([]ManifestLine, error) {
	var out []ManifestLine
	err := readLines(manifestPath(root, mapping), func(l ManifestLine) { out = append(out, l) })
	return out, err
}

// RunInfo is _odb/run.json: how the files under the target folder were built.
type RunInfo struct {
	PathcodecVersion int                   `json:"pathcodecVersion"`
	Layout           string                `json:"layout"`
	Compression      string                `json:"compression"`
	Mappings         map[string]RunMapping `json:"mappings"` // by folder
}

// RunMapping describes one mapping's folder.
type RunMapping struct {
	MappingID   string    `json:"mappingId"`
	JobID       string    `json:"jobId"`
	PlanHash    string    `json:"planHash,omitempty"`
	KeyTemplate string    `json:"keyTemplate"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

var runMu sync.Map // root → *sync.Mutex: mappings of one job update run.json concurrently

// writeRunInfo records the mapping in _odb/run.json (read, update, replace).
func writeRunInfo(root string, cfg Config, folder string, m RunMapping) error {
	mu, _ := runMu.LoadOrStore(root, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	defer mu.(*sync.Mutex).Unlock()
	path := filepath.Join(root, odbDir, runFile)
	info, err := ReadRunInfo(root)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	info.PathcodecVersion, info.Layout, info.Compression = pathcodec.Version, cfg.Layout, cfg.Compression
	if info.Mappings == nil {
		info.Mappings = map[string]RunMapping{}
	}
	info.Mappings[strings.Trim(folder, "/")] = m
	b, _ := json.MarshalIndent(info, "", "  ")
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.%d.tmp", path, time.Now().UnixNano())
	if err := os.WriteFile(tmp, append(b, '\n'), 0o640); err != nil { //nolint:gosec // shared with the output's readers
		return err
	}
	return os.Rename(tmp, path)
}

// ReadRunInfo reads _odb/run.json under root.
func ReadRunInfo(root string) (RunInfo, error) {
	var info RunInfo
	b, err := os.ReadFile(filepath.Join(root, odbDir, runFile)) //nolint:gosec // inside the allowed target folder
	if err != nil {
		return info, err
	}
	return info, json.Unmarshal(b, &info)
}
