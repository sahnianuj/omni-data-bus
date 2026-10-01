// Package directory is the directory target kind: one JSON file per document under a folder the
// server is allowed to write to (OC-036 registers the kind; OC-038 adds the allowlist, the
// connection test and the folder browser; OC-039 – OC-041 the file names, writer and reader).
package directory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"omnidatabus/internal/connectors"
	"omnidatabus/internal/connectors/target"
	"omnidatabus/internal/problem"
)

// DisabledReason is shown when the server has no folder it may write to.
const DisabledReason = "This server has no folder it can write to (no home folder or /data)."

// Test step codes.
const (
	CodeNotFound    = "DIRECTORY_NOT_FOUND"
	CodeNotADir     = "DIRECTORY_NOT_A_DIRECTORY"
	CodeNotWritable = "DIRECTORY_NOT_WRITABLE"
	CodeLowSpace    = "DIRECTORY_LOW_SPACE"
	CodeNetworkFS   = "DIRECTORY_NETWORK_FS"
	lowFreeBytes    = 5 << 30
	lowFreeInodes   = 1_000_000
	probePrefix     = ".odb-probe-"
	reservedPrefix  = "_odb"
)

// Connector implements target.Connector for directories.
type Connector struct {
	roots  []string
	statFS func(path string) (FSInfo, error)
}

// New returns the directory connector. roots are the folders targets may write under; nil
// means DefaultRoots. There is no setting for it: users pick a folder in the Target step, inside
// these areas (OC-038).
func New(roots []string) *Connector {
	if roots == nil {
		roots = DefaultRoots()
	}
	var clean []string
	for _, r := range roots {
		if r = strings.TrimSpace(r); r != "" {
			clean = append(clean, filepath.Clean(r))
		}
	}
	return &Connector{roots: clean, statFS: statFS}
}

// Descriptor implements target.Connector.
func (*Connector) Descriptor() connectors.Descriptor {
	return connectors.Descriptor{Type: target.KindDirectory, Role: connectors.RoleTarget, DisplayName: "Directory of JSON files", Icon: "folder"}
}

// Capabilities implements target.Connector: no durability levels or expiry, and no credentials.
func (*Connector) Capabilities() target.Capabilities {
	return target.Capabilities{SupportsCreateLocation: true, MaxDocBytes: DefaultMaxDocBytes, LocationLevels: []string{"folder"}}
}

// Available implements target.Availability.
func (c *Connector) Available() (bool, string) {
	if len(c.roots) == 0 {
		return false, DisabledReason
	}
	return true, ""
}

// Roots are the allowed folders.
func (c *Connector) Roots() []string { return append([]string(nil), c.roots...) }

// Validate implements target.Connector: the settings, and the path inside an allowed root.
func (c *Connector) Validate(raw json.RawMessage) error {
	_, _, err := c.parse(raw)
	return err
}

// Normalize implements connections.Normalizer: the config with its defaults, named by its path.
func (c *Connector) Normalize(raw json.RawMessage) (json.RawMessage, string, error) {
	cfg, _, err := c.parse(raw)
	if err != nil {
		return nil, "", err
	}
	return cfg.JSON(), cfg.Path, nil
}

// parse checks the config and returns it with its path resolved inside an allowed root.
func (c *Connector) parse(raw json.RawMessage) (Config, string, error) {
	cfg, err := ParseConfig(raw)
	if err != nil {
		return cfg, "", err
	}
	abs, err := c.resolve(cfg.Path)
	return cfg, abs, err
}

// resolve returns path with symlinks resolved, or DIRECTORY_OUTSIDE_ALLOWED_ROOTS unless it's
// inside one of the (also resolved) roots. "root/../etc" and a symlink inside a root that points
// elsewhere are both outside.
func (c *Connector) resolve(path string) (string, error) {
	abs, err := realPath(path)
	if err != nil {
		return "", outside(path)
	}
	for _, r := range c.roots {
		rr, err := realPath(r)
		if err == nil && within(abs, rr) && !hiddenPart(abs, rr) {
			return abs, nil
		}
	}
	return "", outside(path)
}

// Close implements target.Connector.
func (*Connector) Close(context.Context) error { return nil }

func fail(code, detail, hint string) connectors.TestStep {
	return connectors.TestStep{Status: connectors.StepFail, Code: code, Detail: detail, Hint: hint}
}

func ok(detail string) connectors.TestStep {
	return connectors.TestStep{Status: connectors.StepOK, Detail: detail}
}

// Test implements connectors.Tester (OC-038 AC2): the path is allowed, exists (or is created),
// is a folder, can be written, read and deleted, has space, and its file system and case
// sensitivity are reported. Nothing is left behind.
func (c *Connector) Test(_ context.Context, raw json.RawMessage, _ connectors.Credentials) connectors.TestResult {
	r := connectors.NewStepRunner(nil)
	var cfg Config
	var abs string
	r.Run("enabled", "Directory targets are enabled", func() connectors.TestStep {
		if enabled, why := c.Available(); !enabled {
			return fail(problem.CodeTargetKindDisabled, why, "")
		}
		return ok("")
	})
	r.Run("path", "Folder is allowed", func() connectors.TestStep {
		var err error
		if cfg, abs, err = c.parse(raw); err != nil {
			p := problem.From(err)
			return fail(p.Code, p.Detail, "Allowed folders: "+strings.Join(c.roots, ", "))
		}
		return ok(abs)
	})
	r.Run("exists", "Folder exists", func() connectors.TestStep {
		st, err := os.Stat(abs)
		switch {
		case err == nil:
			_ = st
			return ok("")
		case !errors.Is(err, os.ErrNotExist):
			return fail(CodeNotFound, err.Error(), "")
		case !cfg.CreateIfMissing:
			return fail(CodeNotFound, abs+" doesn't exist.", "Create it, or turn on \"Create the folder if it's missing\".")
		}
		if err := os.Mkdir(abs, cfg.DirMode()); err != nil {
			return fail(CodeNotFound, "Couldn't create "+abs+": "+err.Error(), "The parent folder must exist and be writable.")
		}
		return ok("Created " + abs + ".")
	})
	r.Run("isdir", "It's a folder", func() connectors.TestStep {
		st, err := os.Stat(abs)
		if err != nil || !st.IsDir() {
			return fail(CodeNotADir, abs+" is a file, not a folder.", "Choose a folder.")
		}
		return ok("")
	})
	r.Run("probe", "Files can be written, read and deleted", func() connectors.TestStep {
		if err := probe(abs, cfg); err != nil {
			return fail(CodeNotWritable, err.Error(), "Give the server's user (uid 65532 in the image) write access to the folder.")
		}
		return ok("")
	})
	info, statErr := c.statFS(abs)
	r.Run("space", "Free space", func() connectors.TestStep {
		if statErr != nil {
			return connectors.TestStep{Status: connectors.StepWarn, Detail: "Free space is unknown: " + statErr.Error()}
		}
		detail := fmt.Sprintf("%s free, %d inodes free.", gib(info.FreeBytes), info.FreeInodes)
		if info.FreeBytes < lowFreeBytes || (info.FreeInodes > 0 && info.FreeInodes < lowFreeInodes) {
			return connectors.TestStep{
				Status: connectors.StepWarn, Code: CodeLowSpace, Detail: detail,
				Hint: "Each document is a file: make sure there's room for the whole migration.",
			}
		}
		return ok(detail)
	})
	r.Run("fstype", "File system", func() connectors.TestStep {
		switch {
		case statErr != nil || info.Type == "":
			return ok("Unknown file system.")
		case networkFS[info.Type] && cfg.Durability == "file+dir":
			return connectors.TestStep{
				Status: connectors.StepWarn, Code: CodeNetworkFS, Detail: info.Type + " is a network file system.",
				Hint: "Syncing the folder after every file (durability file+dir) can be slow here; consider durability file.",
			}
		}
		return ok(info.Type)
	})
	r.Run("case", "Case sensitivity", func() connectors.TestStep {
		sensitive, err := caseSensitive(abs)
		if err != nil {
			return connectors.TestStep{Status: connectors.StepWarn, Detail: "Couldn't tell: " + err.Error()}
		}
		if sensitive {
			return ok("Case-sensitive: Order-1.json and order-1.json are different files.")
		}
		return ok("Case-insensitive: keys differing only in case would share a file.")
	})
	return r.Result()
}

func gib(b uint64) string { return fmt.Sprintf("%.1f GiB", float64(b)/(1<<30)) }

// probe writes, reads back and deletes a small file.
func probe(dir string, cfg Config) error {
	name := filepath.Join(dir, probePrefix+uuid.Must(uuid.NewV7()).String())
	want := []byte(`{"probe":true}`)
	mode, _ := cfg.Mode()
	if err := os.WriteFile(name, want, mode); err != nil {
		return fmt.Errorf("can't write in %s: %w", dir, err)
	}
	defer func() { _ = os.Remove(name) }()
	got, err := os.ReadFile(name) //nolint:gosec // our own probe file inside an allowed root
	if err != nil {
		return fmt.Errorf("can't read back a file: %w", err)
	}
	if string(got) != string(want) {
		return errors.New("a file read back differs from what was written")
	}
	if err := os.Remove(name); err != nil {
		return fmt.Errorf("can't delete a file: %w", err)
	}
	return nil
}

// caseSensitive creates a file with an upper-case name and looks for it in lower case.
func caseSensitive(dir string) (bool, error) {
	base := probePrefix + "case-" + uuid.Must(uuid.NewV7()).String()
	upper := filepath.Join(dir, base+"-A")
	if err := os.WriteFile(upper, nil, 0o600); err != nil {
		return false, err
	}
	defer func() { _ = os.Remove(upper) }()
	_, err := os.Stat(filepath.Join(dir, base+"-a"))
	return errors.Is(err, os.ErrNotExist), nil
}

// CaseSensitive implements target.CaseProber on the target folder.
func (c *Connector) CaseSensitive(_ context.Context, conn target.Conn) (bool, error) {
	_, abs, err := c.parse(conn.Config)
	if err != nil {
		return true, err
	}
	return caseSensitive(abs)
}
