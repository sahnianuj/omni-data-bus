package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"omnidatabus/internal/config"
	"omnidatabus/internal/store/cbstore"
	"omnidatabus/internal/store/memstore"
	"omnidatabus/internal/store/provision"

	"github.com/stretchr/testify/require"
)

func TestNewHandler_AC4_ServesHealthAndVersion(t *testing.T) {
	h := NewHandler(config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), BuildInfo{Version: "1.2.3", Commit: "abc"}, Deps{})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/version", nil))
	var v map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &v))
	require.Equal(t, map[string]string{"version": "1.2.3", "commit": "abc"}, v)
}

func TestServe_AC6_StopsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, config.Config{HTTPAddr: "127.0.0.1:0", ShutdownTimeout: time.Second},
			slog.New(slog.NewTextHandler(io.Discard, nil)), BuildInfo{}, Deps{Store: memstore.New(nil)})
	}()
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop after cancel")
	}
}

func TestMetaConfig_AC1_MapsSettings(t *testing.T) {
	cfg, err := config.Load(map[string]string{"OC_DEV": "true", "OC_META_CAPELLA": "true"})
	require.NoError(t, err)
	mc := MetaConfig(cfg)
	require.Equal(t, "couchbase://localhost", mc.ConnStr)
	require.Equal(t, "omniconnect", mc.Bucket)
	require.Equal(t, "meta", mc.Scope)
	require.True(t, mc.Capella)
}

func TestRun_AC4_FailsFastWhenMetadataUnreachable(t *testing.T) {
	cfg, err := config.Load(map[string]string{"OC_DEV": "true", "OC_META_CONNSTR": "couchbase://127.0.0.1:1"})
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err = Run(ctx, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), BuildInfo{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "metadata bucket")
}

func TestRun_AC2_RejectsInvalidMasterKey(t *testing.T) {
	cfg, err := config.Load(map[string]string{"OC_DEV": "true", "OC_MASTER_KEY": "dG9vLXNob3J0"})
	require.NoError(t, err)
	err = Run(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), BuildInfo{})
	require.ErrorContains(t, err, "OC_MASTER_KEY")
}

type fakeMeta struct {
	*memstore.Store
	res    provision.Result
	err    error
	closed bool
}

func (f *fakeMeta) Provision(context.Context) (provision.Result, error) { return f.res, f.err }
func (f *fakeMeta) Close() error                                        { f.closed = true; return nil }

func devCfg(t *testing.T, extra map[string]string) config.Config {
	t.Helper()
	env := map[string]string{"OC_DEV": "true"}
	for k, v := range extra {
		env[k] = v
	}
	cfg, err := config.Load(env)
	require.NoError(t, err)
	return cfg
}

func openerFor(m *fakeMeta) Opener {
	return func(context.Context, cbstore.Config) (MetaStore, error) { return m, nil }
}

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestOpenMetadata_AC1_ProvisionsWhenEnabled(t *testing.T) {
	m := &fakeMeta{Store: memstore.New(nil), res: provision.Result{From: 0, To: 2, Applied: []string{"1 collections", "2 indexes"}}}
	got, err := OpenMetadata(context.Background(), devCfg(t, nil), quiet, openerFor(m))
	require.NoError(t, err)
	require.Same(t, m, got)
}

func TestOpenMetadata_AC8_NeedsAdminIsActionable(t *testing.T) {
	m := &fakeMeta{Store: memstore.New(nil), err: &provision.NeedsAdminError{Items: []string{"collection meta.projects"}}}
	_, err := OpenMetadata(context.Background(), devCfg(t, nil), quiet, openerFor(m))
	require.ErrorContains(t, err, "collection meta.projects")
	require.ErrorContains(t, err, "admin provision --print")
	require.True(t, m.closed)
}

func TestOpenMetadata_AC1_ProvisionFailure(t *testing.T) {
	m := &fakeMeta{Store: memstore.New(nil), err: errors.New("boom")}
	_, err := OpenMetadata(context.Background(), devCfg(t, nil), quiet, openerFor(m))
	require.ErrorContains(t, err, "provision metadata store: boom")
	require.True(t, m.closed)
}

func TestOpenMetadata_AC1_SkipsProvisionWhenDisabled(t *testing.T) {
	m := &fakeMeta{Store: memstore.New(nil), err: errors.New("must not be called")}
	_, err := OpenMetadata(context.Background(), devCfg(t, map[string]string{"OC_META_AUTO_PROVISION": "false"}), quiet, openerFor(m))
	require.NoError(t, err)
}

func TestOpenMetadata_AC4_OpenErrorPropagates(t *testing.T) {
	_, err := OpenMetadata(context.Background(), devCfg(t, nil), quiet, func(context.Context, cbstore.Config) (MetaStore, error) {
		return nil, errors.New("unreachable")
	})
	require.ErrorContains(t, err, "unreachable")
}
