package directory

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"omnidatabus/internal/problem"
)

// CodeOutsideRoots rejects a path outside the allowed areas, or in a hidden folder (after
// resolving symlinks).
const CodeOutsideRoots = problem.CodeDirectoryOutsideRoots

// Config defaults and limits (architecture §6.2, OC-038).
const (
	DefaultMaxDocBytes = 20 << 20
	MinMaxDocBytes     = 1 << 10
	MaxMaxDocBytes     = 1 << 30
	DefaultFileMode    = "0640"
)

// Layouts, compression and durability levels.
var (
	layouts      = []string{"flat", "sharded", "key-path"}
	compressions = []string{"none", "gzip"}
	durabilities = []string{"none", "file", "file+dir"}
)

// Config is a directory connection's config (architecture §6.2).
type Config struct {
	Path        string `json:"path"`
	Layout      string `json:"layout"`
	Pretty      bool   `json:"pretty"`
	Compression string `json:"compression"`
	FileMode    string `json:"fileMode"`
	Durability  string `json:"durability"`
	Manifest    bool   `json:"manifest"`
	MaxDocBytes int64  `json:"maxDocBytes"`
	// CreateIfMissing creates the folder (when its parent exists) on test and save.
	CreateIfMissing bool `json:"createIfMissing"`
}

// ParseConfig decodes a config, applies the defaults and checks every field. It doesn't touch
// the file system (see Connector.resolve for the allowlist).
func ParseConfig(raw json.RawMessage) (Config, error) {
	var in struct {
		Config
		Manifest *bool `json:"manifest"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return Config{}, invalid("config", "must be a JSON object")
	}
	c := in.Config
	c.Manifest = in.Manifest == nil || *in.Manifest
	c.Path = strings.TrimSpace(c.Path)
	switch {
	case c.Path == "":
		return Config{}, invalid("config.path", "is required")
	case !filepath.IsAbs(c.Path):
		return Config{}, invalid("config.path", "must be an absolute path")
	}
	c.Path = filepath.Clean(c.Path)
	def := func(v *string, d string) {
		if *v == "" {
			*v = d
		}
	}
	def(&c.Layout, "flat")
	def(&c.Compression, "none")
	def(&c.FileMode, DefaultFileMode)
	// No fsync by default: on macOS a per-file sync is a full device flush, ~220 files/s (OC-079).
	// Writes stay atomic (temp file + rename); only a power cut can lose the latest files.
	def(&c.Durability, "none")
	if c.MaxDocBytes == 0 {
		c.MaxDocBytes = DefaultMaxDocBytes
	}
	for field, pair := range map[string]struct {
		v    string
		opts []string
	}{"config.layout": {c.Layout, layouts}, "config.compression": {c.Compression, compressions}, "config.durability": {c.Durability, durabilities}} {
		if !oneOf(pair.v, pair.opts) {
			return Config{}, invalid(field, "must be one of "+strings.Join(pair.opts, ", "))
		}
	}
	if _, err := c.Mode(); err != nil {
		return Config{}, invalid("config.fileMode", err.Error())
	}
	if c.MaxDocBytes < MinMaxDocBytes || c.MaxDocBytes > MaxMaxDocBytes {
		return Config{}, invalid("config.maxDocBytes", "must be between 1 KiB and 1 GiB")
	}
	return c, nil
}

func oneOf(v string, opts []string) bool {
	for _, o := range opts {
		if v == o {
			return true
		}
	}
	return false
}

// Mode is the file mode: octal, 0600–0666.
func (c Config) Mode() (fs.FileMode, error) {
	n, err := strconv.ParseUint(c.FileMode, 8, 32)
	if err != nil || !strings.HasPrefix(c.FileMode, "0") || n < 0o600 || n > 0o666 {
		return 0, errors.New("must be an octal mode between 0600 and 0666")
	}
	return fs.FileMode(n), nil
}

// DirMode is the mode of created folders: the file mode plus x wherever it has r.
func (c Config) DirMode() fs.FileMode {
	m, err := c.Mode()
	if err != nil {
		m = 0o640
	}
	return m | (m&0o444)>>2
}

// JSON is the normalized config.
func (c Config) JSON() json.RawMessage {
	b, _ := json.Marshal(c)
	return b
}

func invalid(field, msg string) problem.Problem {
	p := problem.New(http.StatusUnprocessableEntity, problem.CodeValidation, "Invalid request", "The directory settings are invalid: "+field+" "+msg+".")
	p.FieldErrors = []problem.FieldError{{Field: field, Message: msg}}
	return p
}

func outside(path string) problem.Problem {
	p := problem.New(http.StatusUnprocessableEntity, CodeOutsideRoots, "Folder not allowed",
		fmt.Sprintf("%s isn't a folder this server may write to: choose one in your home folder or /data, not a hidden or system folder.", path))
	p.FieldErrors = []problem.FieldError{{Field: "config.path", Message: "is outside the allowed folders"}}
	return p
}

// DataRoot is where container images and the Helm chart mount output volumes.
const DataRoot = "/data"

// DefaultRoots are the areas users may pick a folder in: the home folder of the account running
// the server, and /data when it exists (the container volume). Hidden folders inside them
// (.ssh, .config…) are never allowed, see hiddenPart.
func DefaultRoots() []string {
	var out []string
	if home, err := os.UserHomeDir(); err == nil && home != "" && home != "/" {
		if st, err := os.Stat(home); err == nil && st.IsDir() {
			out = append(out, home)
		}
	}
	if st, err := os.Stat(DataRoot); err == nil && st.IsDir() {
		out = append(out, DataRoot)
	}
	return out
}

// hiddenPart reports whether path, below root, goes through a hidden folder (.ssh, .git…).
func hiddenPart(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." {
		return false
	}
	for _, seg := range strings.Split(rel, string(filepath.Separator)) {
		if strings.HasPrefix(seg, ".") {
			return true
		}
	}
	return false
}

// realPath resolves symlinks in path; for a path that doesn't exist yet, the nearest existing
// ancestor is resolved and the rest appended, so "root/new/dir" can be checked before creation.
func realPath(path string) (string, error) {
	path = filepath.Clean(path)
	var rest []string
	for cur := path; ; {
		r, err := filepath.EvalSymlinks(cur)
		if err == nil {
			return filepath.Join(append([]string{r}, rest...)...), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return path, nil
		}
		rest = append([]string{filepath.Base(cur)}, rest...)
		cur = parent
	}
}

// within reports whether path is dir or below it (both already resolved).
func within(path, dir string) bool {
	if path == dir {
		return true
	}
	return strings.HasPrefix(path, strings.TrimSuffix(dir, string(filepath.Separator))+string(filepath.Separator))
}
