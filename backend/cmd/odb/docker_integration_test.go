//go:build integration && !windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/headless"
)

// OC-183 AC1: the README's Docker command, as written, runs an export folder with no odb
// installed: preflight then run, files owned by the caller. Needs ODB_IT_IMAGE (an image of
// this build, e.g. make image) and the dev stack's PostgreSQL (OC_IT_PG_HOST/PORT).
func TestDocker_OC183_AC1_ReadmeCommand(t *testing.T) {
	image := os.Getenv("ODB_IT_IMAGE")
	docker, err := exec.LookPath("docker")
	if image == "" || err != nil || os.Getenv("OC_IT_PG_HOST") == "" {
		t.Skip("set ODB_IT_IMAGE (an image of this build) and OC_IT_PG_HOST/PORT, with docker installed")
	}
	// From inside the container, the host's PostgreSQL is host.docker.internal (Docker Desktop)
	// or, with --network host (Linux), localhost: the README says the same.
	host, extra := "host.docker.internal", []string{}
	if runtime.GOOS == "linux" {
		host, extra = "localhost", []string{"--network", "host"}
	}
	dir := e2eExportAt(t, host)
	dir, err = filepath.EvalSymlinks(dir) // Docker Desktop shares /private/var, not the /var link
	require.NoError(t, err)
	readme := func(cmd string) *exec.Cmd {
		// docker run --rm -it --user "$(id -u):$(id -g)" -v "$PWD:$PWD" -w "$PWD" <image> <cmd>, without -t (no terminal here).
		args := append([]string{"run", "--rm", "-i", "--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()), "-v", dir + ":" + dir, "-w", dir}, extra...)
		c := exec.Command(docker, append(args, image, cmd)...) //nolint:gosec // G204: the test's own docker command
		c.Dir = dir
		return c
	}
	out, err := readme("preflight").CombinedOutput()
	require.Equal(t, 0, exitCode(err), string(out))
	require.Contains(t, string(out), "ready to run")
	out, err = readme("run").CombinedOutput()
	require.Equal(t, 0, exitCode(err), string(out))

	rep, err := headless.StateDir{Path: filepath.Join(dir, "odb-state")}.ReadReport()
	require.NoError(t, err)
	require.Equal(t, "completed", rep.Status)
	require.EqualValues(t, 10000, rep.Totals.Written)
	for _, p := range []string{"odb-state", filepath.Join("odb-state", "report.json"), filepath.Join("out", "orders")} {
		st, err := os.Stat(filepath.Join(dir, p))
		require.NoError(t, err, p)
		require.EqualValues(t, os.Getuid(), st.Sys().(*syscall.Stat_t).Uid, "%s is the caller's, not root's", p)
	}
}
