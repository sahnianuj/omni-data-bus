//go:build integration

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/export"
	"omnidatabus/internal/export/exporttest"
	"omnidatabus/internal/headless"
	"omnidatabus/internal/store"
)

// e2eExport writes an export folder for shop.orders (10,000 rows in the dev seed) on the
// PostgreSQL at OC_IT_PG_HOST/PORT, writing JSON files to out/ inside the folder.
func e2eExport(t *testing.T) string {
	t.Helper()
	host := os.Getenv("OC_IT_PG_HOST")
	if host == "" {
		t.Skip("set OC_IT_PG_HOST/PORT (the dev stack, or the CI job's PostgreSQL)")
	}
	return e2eExportAt(t, host)
}

// e2eExportAt is e2eExport with the database host config.json names (as a container sees it).
func e2eExportAt(t *testing.T, host string) string {
	t.Helper()
	port := 5432
	if v := os.Getenv("OC_IT_PG_PORT"); v != "" {
		port, _ = strconv.Atoi(v)
	}
	spec := `{"specVersion":2,"mode":"canvas","docSchema":{"root":{"id":"root","name":"","type":"object","children":[` +
		`{"id":"f1","name":"orderId","type":"integer","required":true},{"id":"f2","name":"status","type":"string"}]},"source":"generated"},` +
		`"key":{"template":"orders::{order_id}"},"typeField":{"enabled":true,"name":"type","value":"order"},"nullHandling":"include","fieldMappings":[` +
		`{"id":"m1","targetFieldId":"f1","sources":[{"column":"order_id"}],"transforms":[],"origin":"auto","state":"active"},` +
		`{"id":"m2","targetFieldId":"f2","sources":[{"column":"status"}],"transforms":[],"origin":"auto","state":"active"}],` +
		`"relations":[],"aggregates":[],"ignoredColumns":[]}`
	dir := t.TempDir()
	exporttest.Write(t, dir, export.MappingsFile, exporttest.Bundle(t, exporttest.Options{
		Source: json.RawMessage(`{}`), ConfigMode: true, TargetPath: "out",
		Mappings: []export.Mapping{{
			ID: "map-orders", Name: "orders", Source: export.TableRef{Schema: "shop", Table: "orders"},
			Location: store.Location{Kind: store.KindDirectory, Folder: "orders", CreateIfMissing: true},
			Spec:     json.RawMessage(spec), PlanHash: strings.Repeat("0", 64),
		}},
	}))
	cfg, _ := json.Marshal(map[string]any{
		"source": map[string]any{"type": "postgres", "host": host, "port": port, "database": "shop", "username": "shop_owner", "sslMode": "disable", "password": "shop_owner"},
		"target": map[string]any{"type": "directory", "path": "out"},
	})
	require.NoError(t, os.WriteFile(filepath.Join(dir, export.RunConfigFile), cfg, 0o600))
	return dir
}

// lockedBuffer is a bytes.Buffer safe to read while the process still writes to it.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// odbCmd is this test binary run as odb (TestMain) in the export folder.
func odbCmd(t *testing.T, dir string, args ...string) *exec.Cmd {
	t.Helper()
	self, err := os.Executable()
	require.NoError(t, err)
	cmd := exec.Command(self, args...) //nolint:gosec // G204: the test binary itself
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "ODB_TEST_AS_ODB=1", "ODB_CONFIG=", "ODB_MAPPINGS=", "ODB_STATE_DIR=")
	newProcessGroup(cmd)
	return cmd
}

func exitCode(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	if err != nil {
		return -1
	}
	return 0
}

// OC-182 AC1, OC-184 AC1, OC-186 AC1: the same export folder runs with the odb commands alone —
// preflight, a run interrupted with the OS's interrupt (SIGINT; Ctrl+Break on Windows), status,
// resume, verify — with the same results on every OS.
func TestRunner_OC186_AC1_InterruptResume(t *testing.T) {
	dir := e2eExport(t)

	out, err := odbCmd(t, dir, "preflight").CombinedOutput()
	require.Equal(t, 0, exitCode(err), string(out))
	require.Contains(t, string(out), "ready to run")

	// A throttled run, interrupted once it's writing: the checkpoint is saved, exit 5.
	run := odbCmd(t, dir, "run", "--throttle", "1000", "--progress-interval", "200ms")
	var stdout, stderr lockedBuffer
	run.Stdout, run.Stderr = &stdout, &stderr
	require.NoError(t, run.Start())
	deadline := time.Now().Add(30 * time.Second)
	for !strings.Contains(stdout.String(), `"event":"progress"`) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	require.Contains(t, stdout.String(), `"event":"progress"`, stderr.String())
	require.NoError(t, interrupt(run))
	err = run.Wait()
	require.Equal(t, 5, exitCode(err), "stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	require.Contains(t, stdout.String(), `"status":"cancelled"`)

	out, err = odbCmd(t, dir, "status").CombinedOutput()
	require.Equal(t, 0, exitCode(err), string(out))

	out, err = odbCmd(t, dir, "resume").CombinedOutput()
	require.Equal(t, 0, exitCode(err), string(out))

	out, err = odbCmd(t, dir, "verify").CombinedOutput()
	require.Equal(t, 0, exitCode(err), string(out))
	require.Contains(t, string(out), "OK ")

	rep, err := headless.StateDir{Path: filepath.Join(dir, "odb-state")}.ReadReport()
	require.NoError(t, err)
	require.Equal(t, "completed", rep.Status)
	require.EqualValues(t, 10000, rep.Totals.Written+rep.Totals.Skipped, "every order once, across the interruption")
	files, err := os.ReadDir(filepath.Join(dir, "out", "orders"))
	require.NoError(t, err)
	n := 0
	for _, f := range files {
		if strings.HasSuffix(f.Name(), ".json") {
			n++
		}
	}
	require.Equal(t, 10000, n)
	_, err = os.Stat(filepath.Join(dir, "out", "orders", "orders%3A%3A1.json"))
	require.NoError(t, err, "the key's \":\" is encoded the same way on every OS (pathcodec)")
}
