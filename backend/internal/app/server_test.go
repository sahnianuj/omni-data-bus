package app

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/config"
)

func TestServer_AC6_GracefulShutdownWaitsForInflight(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/slow", func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		w.WriteHeader(http.StatusOK)
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	srv := &Server{HTTP: &http.Server{Handler: mux, ReadHeaderTimeout: time.Second}, ShutdownTimeout: 5 * time.Second}

	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() { runErr <- srv.Run(ctx, ln) }()

	respCh := make(chan int, 1)
	go func() {
		resp, err := http.Get("http://" + ln.Addr().String() + "/slow")
		if err != nil {
			respCh <- -1
			return
		}
		_ = resp.Body.Close()
		respCh <- resp.StatusCode
	}()
	<-started
	cancel() // SIGTERM equivalent

	select {
	case <-runErr:
		t.Fatal("Run returned before the in-flight request finished")
	case <-time.After(200 * time.Millisecond):
	}
	close(release)
	require.Equal(t, http.StatusOK, <-respCh)
	require.NoError(t, <-runErr)

	_, err = net.DialTimeout("tcp", ln.Addr().String(), 200*time.Millisecond)
	require.Error(t, err, "no new connections after shutdown")
}

func get(t *testing.T, h http.Handler, path string) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec.Code, rec.Body.String()
}

func TestMetricsServer_AC5_SeparateAddr(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	cfg := config.Config{Observe: config.ObservabilityConfig{MetricsAddr: "127.0.0.1:0"}}

	// The public port doesn't serve metrics (and doesn't fall through to the web app).
	code, _ := get(t, NewHandler(cfg, logger, BuildInfo{}, Deps{}), "/metrics")
	require.Equal(t, http.StatusNotFound, code)

	// The metrics listener does.
	ms := MetricsServer(cfg)
	require.NotNil(t, ms)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- ms.Run(ctx, ln) }()
	resp, err := http.Get("http://" + ln.Addr().String() + "/metrics")
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Contains(t, string(body), "oc_writer_inflight")
	require.Contains(t, string(body), "go_goroutines", "runtime collectors are included")
	cancel()
	require.NoError(t, <-done)
}

func TestMetrics_AC5_PublicPortByDefault(t *testing.T) {
	require.Nil(t, MetricsServer(config.Config{}))
	code, body := get(t, NewHandler(config.Config{}, slog.New(slog.DiscardHandler), BuildInfo{}, Deps{}), "/metrics")
	require.Equal(t, http.StatusOK, code)
	require.Contains(t, body, "oc_sse_subscribers")
}
