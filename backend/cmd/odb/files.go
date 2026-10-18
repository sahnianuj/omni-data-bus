package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// executable is the running odb's path (a variable for tests).
var executable = os.Executable

// runFile is one of the files an export's commands read (OC-180).
type runFile struct {
	name, flag, env, holds string
}

var (
	configFile   = runFile{name: "config.json", flag: "--config", env: "ODB_CONFIG", holds: "the source and target connection details"}
	mappingsFile = runFile{name: "mappings.json", flag: "--mappings", env: "ODB_MAPPINGS", holds: "the mappings to run"}
)

// exportDirs are the folders a command looks in for the export's files, in order: the current
// directory, then the folder odb is in, or that folder's parent when it's named bin and holds an
// export (odb included in an export, OC-182; not /usr/local for /usr/local/bin/odb).
// Duplicates are dropped.
func exportDirs() []string {
	var dirs []string
	add := func(d string) {
		if d == "" {
			return
		}
		if abs, err := filepath.Abs(d); err == nil {
			d = abs
		}
		for _, have := range dirs {
			if have == d {
				return
			}
		}
		dirs = append(dirs, d)
	}
	if wd, err := os.Getwd(); err == nil {
		add(wd)
	}
	if exe, err := executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
		dir := filepath.Dir(exe)
		if parent := filepath.Dir(dir); strings.EqualFold(filepath.Base(dir), "bin") && holdsExport(parent) {
			add(parent)
		}
		add(dir)
	}
	return dirs
}

// holdsExport reports whether dir has an export's mappings.json or config.json.
func holdsExport(dir string) bool {
	for _, f := range []runFile{mappingsFile, configFile} {
		if st, err := os.Stat(filepath.Join(dir, f.name)); err == nil && !st.IsDir() {
			return true
		}
	}
	return false
}

// resolve picks the file to use: the flag, else the environment variable, else the first of
// exportDirs that has it. found is false when nothing was named and no folder has the file;
// path is then "" and missing says where odb looked.
func (f runFile) resolve(flagVal string) (path string, found bool) {
	if flagVal != "" {
		return flagVal, true
	}
	if v := os.Getenv(f.env); v != "" {
		return v, true
	}
	for _, d := range exportDirs() {
		p := filepath.Join(d, f.name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, true
		}
	}
	return "", false
}

// missing explains a file that resolve didn't find.
func (f runFile) missing() string {
	dirs := exportDirs()
	where := "the current directory"
	if len(dirs) > 0 {
		where = dirs[0]
	}
	if len(dirs) > 1 {
		where += " or beside odb (" + strings.Join(dirs[1:], ", ") + ")"
	}
	return fmt.Sprintf("%s not found in %s: it holds %s. Run odb in the export folder, or use %s or %s.", f.name, where, f.holds, f.flag, f.env)
}

// stateDir is the state directory: the flag, else ODB_STATE_DIR, else odb-state in the export
// folder odb found mappings.json in (so a run started from another directory keeps its
// checkpoint with the export, as migrate.sh did), else ./odb-state.
func stateDir(flagVal, exportDir string) string {
	switch {
	case flagVal != "":
		return flagVal
	case os.Getenv("ODB_STATE_DIR") != "":
		return os.Getenv("ODB_STATE_DIR")
	case exportDir != "":
		return filepath.Join(exportDir, "odb-state")
	}
	return "odb-state"
}

// exportDir is the folder of a file resolve found in exportDirs ("" when it was named by a flag
// or the environment).
func (f runFile) exportDir(flagVal, path string) string {
	if path == "" || flagVal != "" || os.Getenv(f.env) != "" {
		return ""
	}
	return filepath.Dir(path)
}
