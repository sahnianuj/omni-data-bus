package app

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"omnidatabus/internal/connectors/target"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/config"
	"omnidatabus/internal/crypto"
	"omnidatabus/internal/store"
	"omnidatabus/internal/store/memstore"
)

func TestPprofServer_OffByDefault(t *testing.T) {
	require.Nil(t, PprofServer(config.Config{}))
}

func TestPprofServer_OC034_ServesProfilesOnItsOwnListener(t *testing.T) {
	cfg := config.Config{Observe: config.ObservabilityConfig{PprofAddr: "127.0.0.1:0"}}
	// The public port never serves pprof.
	code, _ := get(t, NewHandler(cfg, quiet, BuildInfo{}, Deps{}), "/debug/pprof/")
	require.NotEqual(t, http.StatusOK, code)

	ps := PprofServer(cfg)
	require.NotNil(t, ps)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- ps.Run(ctx, ln) }()
	for _, path := range []string{"/debug/pprof/", "/debug/pprof/heap?debug=1", "/debug/pprof/cmdline", "/debug/pprof/symbol"} {
		resp, err := http.Get("http://" + ln.Addr().String() + path)
		require.NoError(t, err)
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode, path)
	}
	cancel()
	require.NoError(t, <-done)
}

func TestServe_StartsMetricsAndPprofListeners(t *testing.T) {
	cfg := config.Config{
		HTTPAddr: "127.0.0.1:0", ShutdownTimeout: time.Second,
		Observe: config.ObservabilityConfig{MetricsAddr: "127.0.0.1:0", PprofAddr: "127.0.0.1:0"},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, cfg, quiet, BuildInfo{}, Deps{Store: memstore.New(nil)}) }()
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("Serve did not stop after cancel")
	}
}

func TestServe_ReportsSideListenerFailures(t *testing.T) {
	// Side listeners on an address that can't be bound only log; the public server still runs.
	cfg := config.Config{
		HTTPAddr: "127.0.0.1:0", ShutdownTimeout: time.Second,
		Observe: config.ObservabilityConfig{MetricsAddr: "256.0.0.1:1", PprofAddr: "256.0.0.1:1"},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	require.NoError(t, Serve(ctx, cfg, quiet, BuildInfo{}, Deps{Store: memstore.New(nil)}))
}

func TestRun_RejectsBadEgressCIDRs(t *testing.T) {
	// Never reachable: Run must fail before it opens the metadata store.
	cfg := devCfg(t, map[string]string{"OC_META_CONNSTR": "couchbase://127.0.0.1:1"})
	cfg.Guard.EgressAllowCIDRs = "not-a-cidr"
	require.ErrorContains(t, Run(context.Background(), cfg, quiet, BuildInfo{}), "OC_EGRESS_ALLOW_CIDRS")
}

func TestRun_WiresEveryRoleAndClosesTheStore(t *testing.T) {
	for _, role := range []string{"all", "api"} {
		t.Run(role, func(t *testing.T) {
			cfg := devCfg(t, map[string]string{"OC_ROLE": role, "OC_HTTP_ADDR": "127.0.0.1:0", "OC_META_AUTO_PROVISION": "false"})
			meta := &fakeMeta{Store: memstore.New(nil)}
			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			defer cancel()
			require.NoError(t, run(ctx, cfg, quiet, BuildInfo{Version: "test"}, openerFor(meta)))
			require.True(t, meta.closed)
		})
	}
}

func TestNewWorker_WiresThePipeline(t *testing.T) {
	cfg := devCfg(t, nil)
	keyring, err := crypto.ParseKeyring(cfg.MasterKey, cfg.MasterKeys, cfg.MasterKeyActive)
	require.NoError(t, err)
	sources, targets := Registries(nil)
	w := NewWorker(cfg, memstore.New(nil), crypto.NewLocalSealer(keyring), sources, targets, quiet)
	require.NotEmpty(t, w.ID())
}

func TestJobCounts_ByStatus(t *testing.T) {
	ctx := context.Background()
	st := memstore.New(nil)
	ws := store.WorkspaceID("ws1")
	for i := 0; i < 3; i++ {
		p, err := st.Projects().Create(ctx, ws, store.NewProject{Name: "p" + string(rune('a'+i))})
		require.NoError(t, err)
		_, err = st.Jobs().Create(ctx, ws, store.Job{ProjectID: p.ID, Options: json.RawMessage(`{}`)})
		require.NoError(t, err)
	}
	got, err := jobCounts(st)(ctx, []string{store.JobQueued, store.JobRunning}, 100)
	require.NoError(t, err)
	require.Equal(t, map[string]int{store.JobQueued: 3}, got)
}

func TestDeps_DefaultHubAndMiddleware(t *testing.T) {
	d := newTestDeps(t)
	require.NotNil(t, d.hub(), "a hub is created when none is injected")
	require.NotEmpty(t, APIMiddleware(d))
	require.Nil(t, APIMiddleware(Deps{}), "no session middleware without auth")
}

// OC-036 AC1: the app's own registry lists both target kinds (the Target step's picker).
func TestRegistries_AC1_BothTargetKinds(t *testing.T) {
	_, targets := Registries(nil)
	var kinds []string
	for _, k := range target.Kinds(targets) {
		kinds = append(kinds, k.Type)
	}
	require.ElementsMatch(t, []string{"couchbase", "directory"}, kinds)
}
