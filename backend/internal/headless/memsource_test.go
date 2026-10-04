package headless

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/buildinfo"
	"omnidatabus/internal/connectors"
	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/export"
	"omnidatabus/internal/export/exporttest"
	"omnidatabus/internal/schema"
)

// memSource is an in-memory "postgres" whose orders table has no key, so the pipeline reads it
// with one cursor: enough to run a whole bundle in a unit test.
type memSource struct {
	mu   sync.Mutex
	rows [][]any
}

func (m *memSource) snapshot() *schema.Snapshot {
	n := int64(len(m.rows))
	return &schema.Snapshot{Database: "shop", Schemas: []schema.Schema{{Name: "public", Tables: []schema.Table{{
		Name: "orders", RowEstimate: &n, Readable: true, Columns: []schema.Column{
			{Name: "id", Ordinal: 1, UDTName: "int4", DataType: "integer", JSONType: schema.TypeInteger, Nullable: true},
			{Name: "customer_id", Ordinal: 2, UDTName: "int4", DataType: "integer", JSONType: schema.TypeInteger},
			{Name: "status", Ordinal: 3, UDTName: "text", DataType: "text", JSONType: schema.TypeString},
		},
	}}}}}
}

func (m *memSource) Descriptor() connectors.Descriptor {
	return connectors.Descriptor{Type: "postgres", Role: connectors.RoleSource, DisplayName: "Memory"}
}
func (m *memSource) Validate(json.RawMessage) error { return nil }
func (m *memSource) Test(context.Context, json.RawMessage, connectors.Credentials) connectors.TestResult {
	return connectors.TestResult{OK: true}
}

func (m *memSource) Open(context.Context, json.RawMessage, connectors.Credentials) (source.Session, error) {
	return &memSession{m: m}, nil
}

type memSession struct{ m *memSource }

func (s *memSession) Ping(context.Context) (source.ServerInfo, error) {
	return source.ServerInfo{}, nil
}

func (s *memSession) CheckPrivileges(context.Context) (source.PrivilegeReport, error) {
	return source.PrivilegeReport{}, nil
}

func (s *memSession) EstimateRelations(context.Context, source.DiscoverOptions) (int, error) {
	return 1, nil
}

func (s *memSession) Discover(context.Context, source.DiscoverOptions) (*schema.Snapshot, error) {
	return s.m.snapshot(), nil
}

func (s *memSession) Preview(context.Context, source.PreviewRequest) ([][]any, error) {
	return nil, nil
}
func (s *memSession) Close() error { return nil }
func (s *memSession) BeginRead(context.Context, string, bool) (source.ReadSession, error) {
	return &memRead{m: s.m}, nil
}

type memRead struct{ m *memSource }

func (r *memRead) SnapshotID() string { return "" }
func (r *memRead) KeyBounds(context.Context, source.TableRead) (any, any, error) {
	return nil, nil, nil
}

func (r *memRead) Count(context.Context, source.TableRead) (int64, error) {
	return int64(len(r.m.rows)), nil
}

func (r *memRead) OpenPartition(context.Context, source.TableRead, any, any) (source.PartitionReader, error) {
	return nil, os.ErrInvalid
}

func (r *memRead) OpenCursor(context.Context, source.TableRead) (source.CursorReader, error) {
	return &memCursor{m: r.m}, nil
}

func (r *memRead) ReadKeys(context.Context, source.TableRead, [][]any) ([][]any, error) {
	return nil, nil
}
func (r *memRead) Close() error { return nil }

type memCursor struct {
	m   *memSource
	pos int
}

func (c *memCursor) Fetch(_ context.Context, n int) ([][]any, error) {
	c.m.mu.Lock()
	defer c.m.mu.Unlock()
	end := min(c.pos+n, len(c.m.rows))
	out := c.m.rows[c.pos:end]
	c.pos = end
	return out, nil
}
func (c *memCursor) Close() error { return nil }

// memConfig runs exporttest's orders bundle against mem, writing to a temp folder.
func memConfig(t *testing.T, mem *memSource, stdout, stderr *bytes.Buffer) (*Config, string) {
	t.Helper()
	out := t.TempDir()
	fp := export.ComputeFingerprint(mem.snapshot(), export.References{"public.orders": {"id": true, "customer_id": true, "status": true}})
	b := exporttest.Bundle(t, exporttest.Options{TargetPath: out, Fingerprint: fp, SourceSecrets: export.Secrets{"password": pgSecret}, Passphrase: passphrase})
	sources := source.NewRegistry()
	sources.Register(mem)
	return &Config{
		Bundle: &b, State: StateDir{Path: filepath.Join(t.TempDir(), "state")}, Sources: sources, Stdout: stdout,
		Log: slog.New(slog.NewJSONHandler(stderr, nil)), Build: buildinfo.Info{Version: "2.0.0", Commit: "abc1234"},
		ProgressInterval: time.Millisecond, PersistEvery: 10 * time.Millisecond,
		Unlock: Unlock{Getenv: func(k string) string {
			if k == PassphraseEnv {
				return passphrase
			}
			return ""
		}},
	}, out
}

func ordersRows() [][]any {
	return [][]any{{int32(1), int32(10), "paid"}, {int32(2), int32(11), "new"}, {int32(3), int32(12), "shipped"}}
}

// TestRun_Unit_EndToEnd runs a sealed bundle with the in-memory source: files, checkpoint,
// report, the stdout events, then status, verify and preflight on the same state.
func TestRun_Unit_EndToEnd(t *testing.T) {
	var stdout, stderr bytes.Buffer
	cfg, out := memConfig(t, &memSource{rows: ordersRows()}, &stdout, &stderr)
	rep, code, err := Run(context.Background(), cfg)
	require.NoError(t, err, stderr.String())
	require.Equal(t, ExitOK, code, stderr.String())
	require.Equal(t, StatusCompleted, rep.Status)
	require.EqualValues(t, 3, rep.Totals.Written)
	files, err := filepath.Glob(filepath.Join(out, "orders", "*.json"))
	require.NoError(t, err)
	require.Len(t, files, 3)
	raw, err := os.ReadFile(filepath.Join(out, "orders", "orders%3A%3A1.json"))
	require.NoError(t, err)
	require.JSONEq(t, `{"id":1,"customerId":10,"status":"paid","type":"order"}`, string(raw))
	require.Contains(t, stdout.String(), `"event":"mapping_done","jobId":"`+rep.RunID+`","mapping":"orders"`)
	require.Contains(t, stdout.String(), `"event":"done"`)
	st, ok, err := cfg.State.Load()
	require.NoError(t, err)
	require.True(t, ok)
	require.Contains(t, string(st.Checkpoint), `"state": "done"`)
	require.True(t, st.TargetEmpty["map-orders"])
	require.NotEmpty(t, rep.Verification, "the pipeline's verification results")

	var status bytes.Buffer
	require.Equal(t, ExitOK, Status(cfg.State, &status))
	require.Contains(t, status.String(), "completed (exit 0)")
	require.Contains(t, status.String(), "3 written")

	res, code, err := Verify(context.Background(), cfg)
	require.NoError(t, err)
	require.Equal(t, ExitOK, code)
	require.Equal(t, []VerifyResult{{Mapping: "orders", Target: "orders/", Expected: 3, Found: 3, OK: true}}, res)
	// A file deleted afterwards: verify notices.
	require.NoError(t, os.Remove(files[0]))
	res, code, err = Verify(context.Background(), cfg)
	require.NoError(t, err)
	require.Equal(t, ExitWithErrors, code)
	require.False(t, res[0].OK)

	pre, code := Preflight(context.Background(), cfg)
	require.Equal(t, ExitOK, code, "%+v", pre.Error)
	require.True(t, pre.OK)
	require.EqualValues(t, 3, pre.TotalRows)

	// Running again resumes a finished checkpoint: nothing to do, still completed.
	stdout.Reset()
	rep, code, err = Run(context.Background(), cfg)
	require.NoError(t, err)
	require.Equal(t, ExitOK, code)
	require.Contains(t, stdout.String(), `"resumed":true`)
}

// TestRun_Unit_RowErrors: a row without an id can't get a key: it goes to errors.jsonl and the
// run completes with errors (exit 2).
func TestRun_Unit_RowErrors(t *testing.T) {
	var stdout, stderr bytes.Buffer
	rows := append(ordersRows(), []any{nil, int32(13), "lost"})
	cfg, out := memConfig(t, &memSource{rows: rows}, &stdout, &stderr)
	rep, code, err := Run(context.Background(), cfg)
	require.NoError(t, err, stderr.String())
	require.Equal(t, ExitWithErrors, code, stderr.String())
	require.Equal(t, StatusCompletedWithErrors, rep.Status)
	require.EqualValues(t, 1, rep.Totals.Failed)
	require.EqualValues(t, 1, rep.Errors.Total)
	lines, err := os.ReadFile(filepath.Join(cfg.State.Path, ErrorsFile))
	require.NoError(t, err)
	require.Equal(t, 1, strings.Count(string(lines), "\n"))
	files, _ := filepath.Glob(filepath.Join(out, "orders", "*.json"))
	require.Len(t, files, 3)
}

func TestRun_ConfigErrors(t *testing.T) {
	var stdout, stderr bytes.Buffer
	cfg, _ := memConfig(t, &memSource{rows: ordersRows()}, &stdout, &stderr)
	// A relative --target-path, or an unknown --mapping: 4.
	cfg.TargetPath = "relative/out"
	_, code, err := Run(context.Background(), cfg)
	require.Equal(t, ExitPreflight, code)
	require.ErrorContains(t, err, "--target-path")
	cfg.TargetPath, cfg.Mappings = "", []string{"nope"}
	_, code, err = Run(context.Background(), cfg)
	require.Equal(t, ExitPreflight, code)
	require.ErrorContains(t, err, "No mapping matches")
	// --resume without a checkpoint.
	cfg.Mappings, cfg.Resume = nil, true
	_, code, err = Run(context.Background(), cfg)
	require.Equal(t, ExitPreflight, code)
	require.ErrorContains(t, err, CodeNothingToResume)
	// Verify needs a finished run; status of an empty directory says so.
	_, code, err = Verify(context.Background(), cfg)
	require.Equal(t, ExitPreflight, code)
	require.Error(t, err)
	var status bytes.Buffer
	require.Equal(t, ExitPreflight, Status(StateDir{Path: t.TempDir()}, &status))
	require.Contains(t, status.String(), "No run in")
	// Preflight with a wrong passphrase: 4 with the code.
	cfg.Resume = false
	cfg.Unlock = Unlock{Getenv: func(string) string { return "wrong passphrase!!" }}
	pre, code := Preflight(context.Background(), cfg)
	require.Equal(t, ExitPreflight, code)
	require.Equal(t, export.CodePassphraseInvalid, pre.Error.Code)
}

func TestRun_DrainCancels(t *testing.T) {
	var stdout, stderr bytes.Buffer
	cfg, _ := memConfig(t, &memSource{rows: ordersRows()}, &stdout, &stderr)
	drain := make(chan struct{})
	close(drain) // a signal before the first batch
	cfg.Drain = drain
	rep, code, err := Run(context.Background(), cfg)
	require.NoError(t, err)
	if code == ExitCancelled {
		require.Equal(t, StatusCancelled, rep.Status)
		var status bytes.Buffer
		require.Equal(t, ExitOK, Status(cfg.State, &status))
		require.Contains(t, status.String(), "cancelled")
	} else {
		require.Equal(t, ExitOK, code, "a tiny table may finish before the drain is seen")
	}
}
