package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"

	"omnidatabus/internal/buildinfo"

	"github.com/stretchr/testify/require"
)

func TestAdminCommands_AC2_Dispatch(t *testing.T) {
	t.Setenv("OC_ENV_FILE", t.TempDir()+"/none")
	for _, k := range []string{"OC_DEV", "OC_META_CONNSTR", "OC_META_USERNAME", "OC_META_PASSWORD", "OC_MASTER_KEY", "OC_MASTER_KEYS"} {
		t.Setenv(k, "")
	}
	var out, errOut bytes.Buffer
	require.Equal(t, 2, run([]string{"admin"}, &out, &errOut))
	for _, c := range []string{"gen-key", "provision [--print]", "migrate", "rotate-secrets"} {
		require.Contains(t, errOut.String(), c, "help lists %s", c)
	}
	// Commands that need the metadata store load the configuration first; without it they
	// exit 2 naming what's missing (so they were dispatched, not rejected as unknown).
	for _, cmd := range [][]string{{"admin", "provision"}, {"admin", "migrate"}, {"admin", "rotate-secrets"}} {
		errOut.Reset()
		require.Equal(t, 2, run(cmd, &out, &errOut), cmd)
		require.Contains(t, errOut.String(), "OC_META_CONNSTR", cmd)
		require.NotContains(t, errOut.String(), "unknown admin command")
	}
	errOut.Reset()
	require.Equal(t, 2, run([]string{"admin", "provision", "--bogus"}, &out, &errOut))
	errOut.Reset()
	require.Equal(t, 2, run([]string{"serve-me"}, &out, &errOut))
	require.Contains(t, errOut.String(), `unknown command "serve-me"`)
}

func TestVersion_AC1_LdflagsInjected(t *testing.T) {
	t.Setenv("OC_ENV_FILE", t.TempDir()+"/none")
	t.Cleanup(buildinfo.Override("v2.0.0", "9f8e7d6c5b", "2026-11-02T14:05:11Z", ""))
	var out, errOut bytes.Buffer
	require.Equal(t, 0, run([]string{"--version"}, &out, &errOut))
	want := "Omni-Data-Bus 2.0.0 (release)\nCommit: 9f8e7d6  Built: 2026-11-02T14:05:11Z  Go: " + runtime.Version() +
		"  Platform: " + runtime.GOOS + "/" + runtime.GOARCH + "\n"
	require.Equal(t, want, out.String())

	out.Reset()
	require.Equal(t, 0, run([]string{"version"}, &out, &errOut))
	require.Equal(t, want, out.String())

	out.Reset()
	require.Equal(t, 0, run([]string{"version", "--json"}, &out, &errOut))
	var v map[string]any
	require.NoError(t, json.Unmarshal(out.Bytes(), &v))
	require.Equal(t, "2.0.0", v["version"])
	require.Equal(t, "release", v["channel"])
	require.Equal(t, "9f8e7d6c5b", v["commit"])
	require.NotContains(t, v, "nodes")

	t.Cleanup(buildinfo.Override("v2.0.0-rc.1", "abc", "", ""))
	out.Reset()
	require.Equal(t, 0, run([]string{"--version"}, &out, &errOut))
	require.Contains(t, out.String(), "Omni-Data-Bus 2.0.0-rc.1 (rc)")
}

func TestHealthcheck_ProbesReadiness(t *testing.T) {
	t.Setenv("OC_ENV_FILE", t.TempDir()+"/none")
	ready := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/readyz", r.URL.Path)
		if !ready {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"status":"unavailable"}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()
	t.Setenv("OC_HTTP_ADDR", srv.Listener.Addr().String())

	var out, errOut bytes.Buffer
	require.Equal(t, 0, run([]string{"healthcheck"}, &out, &errOut), errOut.String())
	require.Contains(t, out.String(), `"ok"`)
	ready = false
	errOut.Reset()
	require.Equal(t, 1, run([]string{"healthcheck"}, &out, &errOut))
	require.Contains(t, errOut.String(), "503")
	require.Equal(t, 1, run([]string{"healthcheck", "--url", "http://127.0.0.1:1/readyz"}, &out, &errOut), "nothing listening")
}

func TestLocalAddr(t *testing.T) {
	require.Equal(t, "127.0.0.1:8080", localAddr(""))
	require.Equal(t, "127.0.0.1:8080", localAddr(":8080"))
	require.Equal(t, "127.0.0.1:9000", localAddr("0.0.0.0:9000"))
	require.Equal(t, "10.0.0.5:8080", localAddr("10.0.0.5:8080"))
}
