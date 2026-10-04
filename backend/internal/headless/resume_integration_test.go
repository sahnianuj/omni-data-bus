//go:build integration

package headless

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/export"
	"omnidatabus/internal/mapping"
)

// odbBinary builds cmd/odb once per test binary.
func odbBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "odb")
	cmd := exec.Command("go", "build", "-o", bin, "omnidatabus/cmd/odb")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	return bin
}

// startRun starts `odb run` on bundlePath; events gets every stdout event.
func startRun(t *testing.T, bin, bundlePath, stateDir string, extra ...string) (*exec.Cmd, <-chan Event, *bytes.Buffer) {
	t.Helper()
	args := append([]string{"run", "--bundle", bundlePath, "--state-dir", stateDir, "--progress-interval", "100ms"}, extra...)
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), PassphraseEnv+"="+passphrase)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	pipe, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	events := make(chan Event, 1024)
	go func() {
		defer close(events)
		sc := bufio.NewScanner(pipe)
		for sc.Scan() {
			var e Event
			if json.Unmarshal(sc.Bytes(), &e) == nil {
				events <- e
			}
		}
		_, _ = io.Copy(io.Discard, pipe)
	}()
	return cmd, events, &stderr
}

func writeBundle(t *testing.T, b export.Bundle) string {
	t.Helper()
	raw, err := export.Encode(b)
	require.NoError(t, err)
	p := filepath.Join(t.TempDir(), "bundle.odb.json")
	require.NoError(t, os.WriteFile(p, raw, 0o600))
	return p
}

// waitWritten waits until a progress event reports at least n written documents.
func waitWritten(t *testing.T, events <-chan Event, n int64) {
	t.Helper()
	deadline := time.After(60 * time.Second)
	for {
		select {
		case e, ok := <-events:
			require.True(t, ok, "the run ended before writing %d documents", n)
			if e.Event == EventProgress && e.Written >= n {
				return
			}
		case <-deadline:
			t.Fatalf("no progress past %d documents", n)
		}
	}
}

func exitCode(err error) int {
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode()
	}
	if err != nil {
		return -1
	}
	return 0
}

// TestRun_AC3_ResumeAfterKill: a run killed with SIGKILL halfway resumes from its checkpoint
// and the output is complete.
func TestRun_AC3_ResumeAfterKill(t *testing.T) {
	tl := newTool(t, t.TempDir())
	out := t.TempDir()
	bundle := writeBundle(t, tl.export(t, out, export.Request{}))
	bin := odbBinary(t)
	state := t.TempDir()
	total := countRows(t, "shop.orders")

	cmd, events, stderr := startRun(t, bin, bundle, state, "--throttle", "2000")
	waitWritten(t, events, int64(total/2))
	require.NoError(t, cmd.Process.Signal(syscall.SIGKILL))
	_ = cmd.Wait()
	require.FileExists(t, filepath.Join(state, CheckpointFile), stderr.String())
	require.Less(t, len(documents(t, out)), total, "killed before the end")

	cmd, events, stderr = startRun(t, bin, bundle, state)
	var start, done Event
	for e := range events {
		switch e.Event {
		case EventStart:
			start = e
		case EventDone:
			done = e
		}
	}
	require.Equal(t, 0, exitCode(cmd.Wait()), stderr.String())
	require.True(t, start.Resumed)
	require.Equal(t, StatusCompleted, done.Status)
	require.Len(t, documents(t, out), total)
	rep, err := StateDir{Path: state}.ReadReport()
	require.NoError(t, err)
	require.EqualValues(t, total, rep.Totals.Written+rep.Totals.Skipped)

	// A different bundle on the same state: exit 4 STATE_PLAN_MISMATCH; --restart archives.
	other := writeBundle(t, tl.export(t, t.TempDir(), export.Request{JobOptions: json.RawMessage(`{"batchSize":500}`)}))
	cmd, events, stderr = startRun(t, bin, other, state)
	for range events {
	}
	require.Equal(t, 4, exitCode(cmd.Wait()))
	require.Contains(t, stderr.String(), CodeStatePlanMismatch)
	cmd, events, stderr = startRun(t, bin, other, state, "--restart")
	for range events {
	}
	require.Equal(t, 0, exitCode(cmd.Wait()), stderr.String())
	archived, err := filepath.Glob(filepath.Join(state, ArchiveDir, "*", CheckpointFile))
	require.NoError(t, err)
	require.Len(t, archived, 1)
}

// TestRun_AC4_SigtermDrains: SIGTERM mid-run drains, writes the checkpoint and the report, and
// exits 5; running again finishes the job.
func TestRun_AC4_SigtermDrains(t *testing.T) {
	tl := newTool(t, t.TempDir())
	out := t.TempDir()
	bundle := writeBundle(t, tl.export(t, out, export.Request{}))
	bin := odbBinary(t)
	state := t.TempDir()

	cmd, events, stderr := startRun(t, bin, bundle, state, "--throttle", "2000")
	waitWritten(t, events, 2000)
	require.NoError(t, cmd.Process.Signal(syscall.SIGTERM))
	var done Event
	for e := range events {
		if e.Event == EventDone {
			done = e
		}
	}
	require.Equal(t, 5, exitCode(cmd.Wait()), stderr.String())
	require.Equal(t, StatusCancelled, done.Status)
	rep, err := StateDir{Path: state}.ReadReport()
	require.NoError(t, err)
	require.Equal(t, StatusCancelled, rep.Status)
	st, ok, err := StateDir{Path: state}.Load()
	require.NoError(t, err)
	require.True(t, ok)
	require.NotEmpty(t, st.Checkpoint)

	cmd, events, stderr = startRun(t, bin, bundle, state)
	for range events {
	}
	require.Equal(t, 0, exitCode(cmd.Wait()), stderr.String())
	require.Len(t, documents(t, out), countRows(t, "shop.orders"))
}

// TestRun_AC4_ErrorThresholds: half the orders fail a transform (modulo by zero on even IDs):
// below the error threshold the run completes with errors (2), above it it fails (3).
func TestRun_AC4_ErrorThresholds(t *testing.T) {
	tl := newTool(t, t.TempDir())
	var spec mapping.Spec
	require.NoError(t, json.Unmarshal(tl.orders.Spec, &spec))
	statusID := ""
	for _, f := range spec.DocSchema.Root.Children {
		if f.Name == "status" {
			statusID = f.ID
		}
	}
	for i, fm := range spec.FieldMappings {
		if fm.TargetFieldID == statusID {
			spec.FieldMappings[i].Transforms = append(fm.Transforms, mapping.Step{Op: "expression", Args: map[string]any{
				"expr": "row.order_id % (row.order_id % 2) == 0 ? value : value",
			}})
		}
	}
	tl.orders.Spec, _ = json.Marshal(spec)
	var err error
	tl.orders, err = tl.st.Mappings().Update(context.Background(), itWS, tl.orders)
	require.NoError(t, err)

	run := func(policy string) (Report, int) {
		b := tl.export(t, t.TempDir(), export.Request{JobOptions: json.RawMessage(`{"errorPolicy":` + policy + `}`)})
		var stdout, stderr bytes.Buffer
		rep, code, err := Run(context.Background(), runConfig(b, t.TempDir(), &stdout, &stderr))
		require.NoError(t, err, stderr.String())
		return rep, code
	}
	rep, code := run(`{"maxErrors":-1,"maxErrorRatio":1,"minRowsForRatio":0}`)
	require.Equal(t, ExitWithErrors, code)
	require.Equal(t, StatusCompletedWithErrors, rep.Status)
	total := int64(countRows(t, "shop.orders"))
	require.EqualValues(t, total/2, rep.Totals.Failed)
	require.EqualValues(t, total/2, rep.Totals.Written)
	require.EqualValues(t, total/2, rep.Errors.Total)
	require.Equal(t, "transform", rep.Errors.ByCode[0].Stage)
	require.Len(t, rep.Errors.Samples, maxReportSamples)

	rep, code = run(`{"maxErrors":5,"maxErrorRatio":1,"minRowsForRatio":0}`)
	require.Equal(t, ExitFailed, code)
	require.Equal(t, "ERROR_THRESHOLD_EXCEEDED", rep.Reason)
}
