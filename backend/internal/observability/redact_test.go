package observability

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"
)

func logJSON(t *testing.T, fn func(l *slog.Logger)) map[string]any {
	t.Helper()
	var buf bytes.Buffer
	fn(NewLogger(&buf, "json", "debug"))
	var m map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &m))
	return m
}

func TestRedactHandler_AC2_MasksKeys(t *testing.T) {
	cases := []string{"password", "Authorization", "dsn", "api_token", "OC_MASTER_KEY", "cookie"}
	for _, k := range cases {
		t.Run(k, func(t *testing.T) {
			m := logJSON(t, func(l *slog.Logger) { l.Info("x", k, "hunter2") })
			require.Equal(t, "***", m[k])
		})
	}
}

func TestRedactHandler_AC2_MasksNestedGroupsAndWithAttrs(t *testing.T) {
	m := logJSON(t, func(l *slog.Logger) {
		l.With("secret", "abc").Info("x", slog.Group("conn", slog.String("password", "p"), slog.String("host", "h")))
	})
	require.Equal(t, "***", m["secret"])
	conn := m["conn"].(map[string]any)
	require.Equal(t, "***", conn["password"])
	require.Equal(t, "h", conn["host"])
}

func TestRedactHandler_AC2_MasksDSNCredentials(t *testing.T) {
	m := logJSON(t, func(l *slog.Logger) {
		l.Info("connecting to postgres://u:p@h/db", "target", "couchbases://admin:pw@cb.example.com")
	})
	require.Equal(t, "connecting to postgres://u:***@h/db", m["msg"])
	require.Equal(t, "couchbases://admin:***@cb.example.com", m["target"])
}

func TestRedactHandler_AC2_LeavesSafeKeysAndValues(t *testing.T) {
	m := logJSON(t, func(l *slog.Logger) { l.Info("x", "keyspace", "b.s.c", "route", "/api/v1/projects/{id}", "n", 3) })
	require.Equal(t, "b.s.c", m["keyspace"])
	require.Equal(t, "/api/v1/projects/{id}", m["route"])
	require.EqualValues(t, 3, m["n"])
}

func TestNewLogger_AC2_TextFormat(t *testing.T) {
	var buf bytes.Buffer
	NewLogger(&buf, "text", "info").Info("hello", "password", "x")
	require.Contains(t, buf.String(), "password=***")
}
