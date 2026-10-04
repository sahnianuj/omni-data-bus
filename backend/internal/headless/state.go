// Package headless runs a portable bundle without the tool (OC-051): `odb run`. The pipeline is
// the server's own; its metadata store is an in-memory one loaded from the bundle, and the state
// a run must keep (checkpoint, errors, progress, report) goes to local files and stdout.
package headless

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// State directory files.
const (
	CheckpointFile = "checkpoint.json"
	ErrorsFile     = "errors.jsonl"
	ReportFile     = "report.json"
	LockFile       = ".lock"
	ArchiveDir     = "archive"
)

// Error codes of the runner's own checks (exit code 4).
const (
	CodeStateLocked       = "STATE_LOCKED"
	CodeStatePlanMismatch = "STATE_PLAN_MISMATCH"
	CodeNothingToResume   = "NOTHING_TO_RESUME"
	CodeTargetNotWritable = "TARGET_NOT_WRITABLE"
	CodePreflightFailed   = "PREFLIGHT_FAILED"
	CodeConfig            = "CONFIG_INVALID"
)

// ConfigError is a preflight or configuration problem: exit code 4.
type ConfigError struct {
	Code    string
	Message string
	Details []string
}

func (e *ConfigError) Error() string { return e.Code + ": " + e.Message }

// State is what a run keeps in checkpoint.json between attempts.
type State struct {
	Version      int       `json:"version"`
	RunID        string    `json:"runId"`
	PlanHash     string    `json:"planHash"`
	BundleSHA256 string    `json:"bundleSha256"`
	StartedAt    time.Time `json:"startedAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
	// TargetEmpty lists mappings whose location was empty when the run first started (OC-030):
	// a resumed run keeps it, since its own output is there by then.
	TargetEmpty map[string]bool `json:"targetEmpty,omitempty"`
	// Checkpoint is the pipeline's checkpoint (per-mapping and per-partition positions).
	Checkpoint json.RawMessage `json:"checkpoint,omitempty"`
}

// StateDir is a run's state directory.
type StateDir struct{ Path string }

func (s StateDir) file(name string) string { return filepath.Join(s.Path, name) }

// Ensure creates the directory (0700: the error samples hold source data).
func (s StateDir) Ensure() error { return os.MkdirAll(s.Path, 0o700) }

// Load reads checkpoint.json; ok is false when there is none.
func (s StateDir) Load() (State, bool, error) {
	raw, err := os.ReadFile(s.file(CheckpointFile))
	if errors.Is(err, os.ErrNotExist) {
		return State{}, false, nil
	}
	if err != nil {
		return State{}, false, err
	}
	var st State
	if err := json.Unmarshal(raw, &st); err != nil {
		return State{}, false, &ConfigError{Code: CodeConfig, Message: "The state directory's checkpoint.json is unreadable: " + err.Error() + ". Run with --restart to start over."}
	}
	return st, true, nil
}

// Save writes checkpoint.json atomically: a temp file, fsync, rename, then the directory fsync.
func (s StateDir) Save(st State) error {
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(s.file(CheckpointFile), raw)
}

func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if d, err := os.Open(filepath.Dir(path)); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

// Archive moves the previous run's files to archive/<ts>/ (--restart).
func (s StateDir) Archive(now time.Time) (string, error) {
	dst := filepath.Join(s.Path, ArchiveDir, now.UTC().Format("20060102T150405Z"))
	moved := false
	for _, name := range []string{CheckpointFile, ErrorsFile, ReportFile, ErrorsFile + ".1"} {
		src := s.file(name)
		if _, err := os.Stat(src); err != nil {
			continue
		}
		if err := os.MkdirAll(dst, 0o700); err != nil {
			return "", err
		}
		if err := os.Rename(src, filepath.Join(dst, name)); err != nil {
			return "", err
		}
		moved = true
	}
	if !moved {
		return "", nil
	}
	return dst, nil
}

// Plan checks the state against the bundle: resume when the plan matches, refuse when it
// doesn't, and archive first with restart.
func (s StateDir) Plan(planHash string, resume, restart bool, now time.Time) (State, bool, string, error) {
	st, ok, err := s.Load()
	if err != nil && !restart {
		return State{}, false, "", err
	}
	if restart {
		dst, err := s.Archive(now)
		return State{}, false, dst, err
	}
	switch {
	case !ok && resume:
		return State{}, false, "", &ConfigError{Code: CodeNothingToResume, Message: "There is no checkpoint in " + s.Path + " to resume from."}
	case !ok:
		return State{}, false, "", nil
	case st.PlanHash != planHash:
		return State{}, false, "", &ConfigError{Code: CodeStatePlanMismatch, Message: fmt.Sprintf(
			"The state directory %s belongs to a different migration (plan %.12s…, this bundle is %.12s…). Use another --state-dir, or --restart to archive it and start over.",
			s.Path, st.PlanHash, planHash)}
	}
	return st, true, "", nil
}
