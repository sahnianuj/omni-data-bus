package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/export/exporttest"
)

// exportFolder writes an export's two files into a new folder: a directory target writing to
// out/, with the connection details in config.json.
func exportFolder(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	exporttest.Write(t, dir, "mappings.json", exporttest.Bundle(t, exporttest.Options{ConfigMode: true, TargetPath: "out"}))
	cfg := `{"source": {"type": "postgres", "host": "127.0.0.1", "port": 1, "database": "shop", "username": "u", "password": "p"},
	         "target": {"type": "directory", "path": "out"}}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.json"), []byte(cfg), 0o600))
	return dir
}

// noExportEnv clears the ODB_* settings and puts odb somewhere without export files.
func noExportEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"ODB_CONFIG", "ODB_MAPPINGS", "ODB_STATE_DIR", "ODB_TARGET_PATH"} {
		t.Setenv(k, "")
	}
	bin := filepath.Join(t.TempDir(), "odb")
	executable = func() (string, error) { return bin, nil }
	t.Cleanup(func() { executable = os.Executable })
}

// realPathPrefix rewrites out's leading dir (as the OS may spell it, e.g. /var vs /private/var)
// to its real path.
func realPathPrefix(t *testing.T, out, dir string) string {
	t.Helper()
	resolved := realPath(t, dir)
	for _, d := range []string{dir, resolved} {
		if len(out) >= len(d) && out[:len(d)] == d {
			return resolved + out[len(d):]
		}
	}
	return out
}

func realPath(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	require.NoError(t, err)
	return r
}

// OC-180 AC1: run in the export folder, odb reads ./config.json and ./mappings.json.
func TestRun_OC180_AC1_DefaultsInCwd(t *testing.T) {
	noExportEnv(t)
	dir := exportFolder(t)
	t.Chdir(dir)
	p, ok := mappingsFile.resolve("")
	require.True(t, ok)
	require.Equal(t, realPath(t, filepath.Join(dir, "mappings.json")), realPath(t, p))
	p, ok = configFile.resolve("")
	require.True(t, ok)
	require.Equal(t, realPath(t, filepath.Join(dir, "config.json")), realPath(t, p))

	// target-path reads config.json with no --config; status reads ./odb-state.
	code, out, stderr := runCLI("run", "target-path")
	require.Equal(t, exitOK, code, stderr)
	require.Equal(t, filepath.Join(realPath(t, dir), "out")+"\n", realPathPrefix(t, out, dir))
	code, out, _ = runCLI("run", "status")
	require.Equal(t, exitPreflight, code)
	require.Contains(t, out, "No run in "+filepath.Join(filepath.Dir(p), "odb-state"))

	// preflight gets past the files to the connection.
	code, out, stderr = runCLI("run", "preflight")
	require.Equal(t, exitPreflight, code)
	require.NotContains(t, stderr, "not found")
	require.NotContains(t, stderr, "--mappings")
	require.Contains(t, out+stderr, "127.0.0.1")
}

// OC-180 AC2: from another directory, odb uses the files beside it, or above its bin/.
func TestRun_OC180_AC2_BesideBinary(t *testing.T) {
	noExportEnv(t)
	dir := exportFolder(t)
	t.Chdir(t.TempDir())
	for _, bin := range []string{filepath.Join(dir, "odb"), filepath.Join(dir, "bin", "odb")} {
		executable = func() (string, error) { return bin, nil }
		p, ok := mappingsFile.resolve("")
		require.True(t, ok, bin)
		require.Equal(t, filepath.Join(dir, "mappings.json"), p, bin)
		require.Equal(t, filepath.Join(dir, "odb-state"), stateDir("", mappingsFile.exportDir("", p)), bin)
		code, out, stderr := runCLI("run", "status")
		require.Equal(t, exitPreflight, code, stderr)
		require.Contains(t, out, "No run in "+filepath.Join(dir, "odb-state"), bin)
	}
}

// OC-180 AC3: a flag beats ODB_*, which beats the export folder.
func TestRun_OC180_AC3_FlagEnvDefault(t *testing.T) {
	noExportEnv(t)
	t.Chdir(exportFolder(t))
	t.Setenv("ODB_CONFIG", "/tmp/prod.json")
	p, _ := configFile.resolve("./dev.json")
	require.Equal(t, "./dev.json", p)
	p, _ = configFile.resolve("")
	require.Equal(t, "/tmp/prod.json", p)
	t.Setenv("ODB_CONFIG", "")
	p, _ = configFile.resolve("")
	require.Equal(t, "config.json", filepath.Base(p))

	t.Setenv("ODB_STATE_DIR", "/tmp/state")
	require.Equal(t, "/tmp/state", stateDir("", "/x"))
	require.Equal(t, "s", stateDir("s", "/x"))
	t.Setenv("ODB_STATE_DIR", "")
	require.Equal(t, "odb-state", stateDir("", ""), "a named mappings.json keeps ./odb-state")
}

// OC-180: an odb installed in a bin folder (/usr/local/bin) doesn't look in the folder above it
// unless that holds an export.
func TestRun_OC180_BinParentOnlyForAnExport(t *testing.T) {
	noExportEnv(t)
	t.Chdir(t.TempDir())
	usr := t.TempDir()
	executable = func() (string, error) { return filepath.Join(usr, "bin", "odb"), nil }
	require.Equal(t, filepath.Join(usr, "bin"), exportDirs()[1])
	require.Len(t, exportDirs(), 2)
	require.NotContains(t, mappingsFile.missing(), usr+",")
}

// OC-180 AC4: a missing file says where odb looked and how to point at it.
func TestRun_OC180_AC4_MissingFile(t *testing.T) {
	noExportEnv(t)
	empty := t.TempDir()
	t.Chdir(empty)
	code, _, stderr := runCLI("run", "preflight")
	require.Equal(t, exitPreflight, code)
	require.Contains(t, stderr, "mappings.json not found in ")
	require.Contains(t, stderr, " or beside odb (")
	require.Contains(t, stderr, "--mappings or ODB_MAPPINGS")

	// mappings.json alone, with its connection details in a config.json that isn't there.
	exporttest.Write(t, empty, "mappings.json", exporttest.Bundle(t, exporttest.Options{ConfigMode: true}))
	code, _, stderr = runCLI("run", "preflight")
	require.Equal(t, exitPreflight, code)
	require.Contains(t, stderr, "config.json not found in ")
	require.Contains(t, stderr, "--config or ODB_CONFIG")
}

// fakeTerminal makes odb treat stdout and stderr as a terminal (or not) for one test.
func fakeTerminal(t *testing.T, on bool) {
	t.Helper()
	prev := isTerminal
	isTerminal = func(io.Writer) bool { return on }
	t.Cleanup(func() { isTerminal = prev })
}

// OC-181 AC1: the operator commands give what the forms they stand for give.
func TestCLI_OC181_AC1_CommandParity(t *testing.T) {
	noExportEnv(t)
	fakeTerminal(t, false)
	dir := exportFolder(t)
	t.Chdir(dir)
	for _, c := range []struct{ name, legacy []string }{
		{[]string{"preflight"}, []string{"run", "preflight"}},
		{[]string{"status"}, []string{"run", "status"}},
		{[]string{"verify"}, []string{"run", "verify"}},
		{[]string{"resume"}, []string{"run", "--resume"}},
		{[]string{"inspect"}, []string{"bundle", "inspect", filepath.Join(dir, "mappings.json")}},
	} {
		code, out, _ := runCLI(c.name...)
		legacyCode, legacyOut, _ := runCLI(c.legacy...)
		require.Equal(t, legacyCode, code, c.name)
		require.Equal(t, stripTimes(legacyOut), stripTimes(out), c.name)
	}
	code, out, _ := runCLI("inspect")
	require.Equal(t, exitOK, code)
	require.Contains(t, out, "orders")
}

// stripTimes drops the timestamps two runs of the same command differ in.
func stripTimes(s string) string {
	return regexp.MustCompile(`"ts":"[^"]*"`).ReplaceAllString(s, "")
}

// OC-181 AC2: the 2.0 forms still work (2.0 migrate.sh scripts call them).
func TestCLI_OC181_AC2_LegacyForms(t *testing.T) {
	noExportEnv(t)
	fakeTerminal(t, false)
	dir := exportFolder(t)
	t.Chdir(t.TempDir())
	m, c, st := filepath.Join(dir, "mappings.json"), filepath.Join(dir, "config.json"), filepath.Join(dir, "state")
	code, _, stderr := runCLI("run", "preflight", "--mappings", m, "--config", c, "--state-dir", st)
	require.Equal(t, exitPreflight, code)
	require.NotContains(t, stderr, "not found")
	code, out, _ := runCLI("run", "status", "--state-dir", st)
	require.Equal(t, exitPreflight, code)
	require.Contains(t, out, "No run in "+st)
	code, _, stderr = runCLI("run", "--resume", "--bundle", m, "--config", c, "--state-dir", st)
	require.Equal(t, exitPreflight, code)
	require.Contains(t, stderr, "no checkpoint")
	code, out, _ = runCLI("bundle", "inspect", m)
	require.Equal(t, exitOK, code)
	require.Contains(t, out, "orders")
}

// OC-181 AC3: text and a closing sentence on a terminal; JSON events when piped.
func TestCLI_OC181_AC3_LogFormatByTTY(t *testing.T) {
	noExportEnv(t)
	t.Chdir(exportFolder(t))

	fakeTerminal(t, true)
	code, out, stderr := runCLI("run")
	require.Equal(t, exitPreflight, code)
	require.Contains(t, out, "done      failed (exit 4)")
	require.NotContains(t, out, `"event"`)
	require.NotContains(t, stderr, `"level"`, "text logs")
	require.Contains(t, stderr, "odb: not started: a preflight or configuration problem")

	fakeTerminal(t, false)
	code, out, stderr = runCLI("run")
	require.Equal(t, exitPreflight, code)
	require.Contains(t, out, `"event":"done"`)
	require.NotContains(t, stderr, "odb: not started")

	// An explicit --log-format wins.
	fakeTerminal(t, true)
	_, out, stderr = runCLI("run", "--log-format", "json")
	require.Contains(t, out, `"event":"done"`)
	require.Contains(t, stderr, "odb: not started")
}

// OC-181 AC4: resume needs a checkpoint, and a cancelled run says to run odb resume.
func TestCLI_OC181_AC4_CancelResume(t *testing.T) {
	noExportEnv(t)
	fakeTerminal(t, false)
	t.Chdir(exportFolder(t))
	code, _, stderr := runCLI("resume")
	require.Equal(t, exitPreflight, code)
	require.Contains(t, stderr, "no checkpoint")
	require.Equal(t, "cancelled after saving the checkpoint: odb resume continues", exitSummary(5, "s"))
	require.Equal(t, "failed: see "+filepath.Join("s", "report.json")+"; fix the cause, then run odb resume", exitSummary(3, "s"))
	require.Contains(t, exitSummary(2, "s"), filepath.Join("s", "errors.jsonl"))
	// The interrupt itself (drain, checkpoint, exit 5) is headless.Run's: TestWatch_AbortAndDrain and
	// the resume integration tests.
}

// OC-181 AC5: odb help lists the operator commands, the files and the exit codes first; odb on
// its own in an export folder prints it rather than trying to start a server.
func TestCLI_OC181_AC5_Help(t *testing.T) {
	code, out, _ := runCLI("help")
	require.Equal(t, 0, code)
	order := []string{"preflight", "run [--mapping M]", "resume", "status", "verify", "inspect", "config.json    where", "mappings.json  what", "exit codes", "Other commands", "admin"}
	at := -1
	for _, s := range order {
		i := strings.Index(out, s)
		require.Greater(t, i, at, s)
		at = i
	}

	noExportEnv(t)
	t.Setenv("OC_ENV_FILE", t.TempDir()+"/none")
	t.Setenv("OC_META_CONNSTR", "")
	t.Setenv("OC_DEV", "")
	t.Chdir(exportFolder(t))
	code, _, stderr := runCLI()
	require.Equal(t, exitUsage, code)
	require.Contains(t, stderr, "Run a migration")
}

// inDocker makes odb believe it runs in a container whose mounts are / and the given folders.
func inDocker(t *testing.T, mounts ...string) {
	t.Helper()
	prevIn, prevInfo := inContainer, mountInfo
	inContainer = func() bool { return true }
	info := "21 1 0:21 / / rw,relatime - overlay overlay rw\n"
	for i, m := range mounts {
		esc := strings.ReplaceAll(m, " ", `\040`) // mountinfo escapes spaces
		info += fmt.Sprintf("%d 21 254:1 /host%s %s rw,relatime - ext4 /dev/vda1 rw\n", 30+i, esc, esc)
	}
	mountInfo = func() ([]byte, error) { return []byte(info), nil }
	t.Cleanup(func() { inContainer, mountInfo = prevIn, prevInfo })
}

// OC-183 AC2: in a container, a directory target outside the mounted folder stops preflight and
// says which -v to add; inside it, odb goes on.
func TestRun_OC183_AC2_TargetNotMounted(t *testing.T) {
	noExportEnv(t)
	fakeTerminal(t, false)
	dir := exportFolder(t)
	t.Chdir(dir)
	inDocker(t, realPath(t, dir), dir)

	code, _, stderr := runCLI("preflight")
	require.Equal(t, exitPreflight, code)
	require.NotContains(t, stderr, "isn't visible", "out/ is inside the mounted export folder")

	for _, args := range [][]string{{"preflight", "--target-path", "/data/out"}, {"run", "--target-path", "/data/out"}, {"verify", "--target-path", "/data/out"}} {
		code, _, stderr = runCLI(args...)
		require.Equal(t, exitPreflight, code, args)
		require.Contains(t, stderr, "the directory target /data/out isn't visible outside this container", args)
		require.Contains(t, stderr, "add -v /data/out:/data/out to docker run", args)
	}
	inDocker(t, realPath(t, dir), dir, "/data")
	_, _, stderr = runCLI("preflight", "--target-path", "/data/out")
	require.NotContains(t, stderr, "isn't visible", "/data is mounted")

	// Outside a container, nothing changes.
	inContainer = func() bool { return false }
	_, _, stderr = runCLI("preflight", "--target-path", "/data/out")
	require.NotContains(t, stderr, "isn't visible")
}

// OC-183 AC3: in a container, a failing connection to localhost says what localhost means there.
func TestPreflight_OC183_AC3_LocalhostHint(t *testing.T) {
	noExportEnv(t)
	fakeTerminal(t, false)
	dir := exportFolder(t) // the source is 127.0.0.1:1: nothing listens
	t.Chdir(dir)
	inDocker(t, realPath(t, dir), dir)
	for _, cmd := range []string{"preflight", "run"} {
		code, _, stderr := runCLI(cmd)
		require.Equal(t, exitPreflight, code, cmd)
		require.Contains(t, stderr, "hint: odb runs in a container here, where localhost is the container itself", cmd)
		require.Contains(t, stderr, "--network host", cmd)
		require.Contains(t, stderr, "host.docker.internal", cmd)
	}
	inContainer = func() bool { return false }
	_, _, stderr := runCLI("preflight")
	require.NotContains(t, stderr, "hint:")
}

func TestMounted_OC183(t *testing.T) {
	inDocker(t, "/work", "/my data")
	require.True(t, mounted("/work"))
	require.True(t, mounted("/work/out/orders"))
	require.True(t, mounted("/my data/x"))
	require.False(t, mounted("/workshop"), "a prefix of a name is not a parent")
	require.False(t, mounted("/data/out"))
	require.False(t, mounted("/"))
}
