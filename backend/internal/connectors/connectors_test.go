package connectors

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/auth"
	"omnidatabus/internal/httpapi"
	"omnidatabus/internal/problem"
)

type stub struct{ d Descriptor }

func (s stub) Descriptor() Descriptor { return s.d }

func TestRegistry_AC1_GetRegistered(t *testing.T) {
	r := NewRegistry[stub](RoleSource)
	r.Register(stub{Descriptor{Type: "postgres", DisplayName: "PostgreSQL"}})
	got, err := r.Get("postgres")
	require.NoError(t, err)
	require.Equal(t, "PostgreSQL", got.Descriptor().DisplayName)
}

func TestRegistry_AC1_UnknownType(t *testing.T) {
	r := NewRegistry[stub](RoleSource)
	_, err := r.Get("mysql")
	var ute *UnsupportedTypeError
	require.ErrorAs(t, err, &ute)
	p := problem.From(err)
	require.Equal(t, 422, p.Status)
	require.Equal(t, problem.CodeUnsupportedSource, p.Code)
	_, err = NewRegistry[stub](RoleTarget).Get("mongodb")
	require.Equal(t, problem.CodeTargetKindUnknown, problem.From(err).Code)
}

func TestRegistry_AC1_DuplicatePanics(t *testing.T) {
	r := NewRegistry[stub](RoleSource)
	r.Register(stub{Descriptor{Type: "postgres"}})
	require.Panics(t, func() { r.Register(stub{Descriptor{Type: "postgres"}}) })
}

func TestConnectorsHandler_AC2_ListsDescriptors(t *testing.T) {
	src := NewRegistry[stub](RoleSource)
	src.Register(stub{Descriptor{Type: "postgres", Role: RoleSource, DisplayName: "PostgreSQL"}})
	src.Register(stub{Descriptor{Type: "mysql", Role: RoleSource, DisplayName: "MySQL"}})
	tgt := NewRegistry[stub](RoleTarget)
	tgt.Register(stub{Descriptor{Type: "couchbase", Role: RoleTarget, DisplayName: "Couchbase"}})
	withUser := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), auth.Principal{UserID: "u"})))
		})
	}
	h := httpapi.NewRouter(httpapi.Options{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Middleware: []func(http.Handler) http.Handler{withUser},
		Features: []httpapi.Registrar{CatalogHandlers{Sources: src, Targets: tgt}},
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/connectors", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	var body map[string][]Descriptor
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, []string{"MySQL", "PostgreSQL"}, []string{body["sources"][0].DisplayName, body["sources"][1].DisplayName}, "sorted by display name")
	require.Equal(t, "couchbase", body["targets"][0].Type)
}

type pgErr struct{ code string }

func (e pgErr) Error() string    { return "pg error " + e.code }
func (e pgErr) SQLState() string { return e.code }

type netTimeout struct{}

func (netTimeout) Error() string   { return "i/o timeout" }
func (netTimeout) Timeout() bool   { return true }
func (netTimeout) Temporary() bool { return true }

func TestClassify_AC3_TableDriven(t *testing.T) {
	cases := []struct {
		name string
		err  error
		kind Kind
		code string
	}{
		{"dial refused", &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}, KindUnreachable, "SOURCE_UNREACHABLE"},
		{"dns", &net.DNSError{Err: "no such host", Name: "db.invalid"}, KindDNS, "SOURCE_DNS"},
		{"deadline", fmt.Errorf("connect: %w", context.DeadlineExceeded), KindTimeout, "SOURCE_TIMEOUT"},
		{"net timeout", netTimeout{}, KindTimeout, "SOURCE_TIMEOUT"},
		{"x509", x509.UnknownAuthorityError{}, KindTLS, "SOURCE_TLS"},
		{"tls text", errors.New("tls: handshake failure"), KindTLS, "SOURCE_TLS"},
		{"bad password", fmt.Errorf("auth: %w", pgErr{"28P01"}), KindAuth, "SOURCE_AUTH_FAILED"},
		{"no database", pgErr{"3D000"}, KindDatabaseNotFound, "SOURCE_DB_NOT_FOUND"},
		{"no privilege", pgErr{"42501"}, KindPermission, "SOURCE_PERMISSION_DENIED"},
		{"connection exception", pgErr{"08006"}, KindUnreachable, "SOURCE_UNREACHABLE"},
		{"other", errors.New("weird"), KindUnknown, "SOURCE_UNKNOWN"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ce := Classify(RoleSource, c.err)
			require.Equal(t, c.kind, ce.Kind)
			require.Equal(t, c.code, ce.Code())
			require.ErrorIs(t, ce, c.err)
		})
	}
	require.Equal(t, "TARGET_AUTH_FAILED", Classify(RoleTarget, pgErr{"28P01"}).Code())
}

func TestConnErrorProblem_AC3_HasHintAndDetail(t *testing.T) {
	p := problem.From(Classify(RoleSource, pgErr{"28P01"}))
	require.Equal(t, "SOURCE_AUTH_FAILED", p.Code)
	require.Contains(t, p.Detail, "Check the username and password")
	require.Contains(t, p.Detail, "pg error 28P01", "raw driver text kept")
	ce := Classify(RoleSource, pgErr{"x"})
	require.Same(t, ce, Classify(RoleSource, fmt.Errorf("wrapped: %w", ce)), "already-classified errors keep their kind")
}

func TestConnectorCodes_AC3_AllRegistered(t *testing.T) {
	for _, role := range []Role{RoleSource, RoleTarget} {
		for kind := range kindInfo {
			code := (&ConnError{Role: role, Kind: kind, Cause: errors.New("x")}).Code()
			require.Contains(t, problem.All, code, "add %s to problem.ConnectorCodes and the OpenAPI enum", code)
		}
	}
}

func TestStepRunner_SkipsAfterFailure(t *testing.T) {
	now := time.Unix(0, 0)
	clock := func() time.Time { now = now.Add(5 * time.Millisecond); return now }
	r := NewStepRunner(clock)
	require.True(t, r.Run("resolve", "Resolve host", func() TestStep { return TestStep{Status: StepOK} }))
	require.False(t, r.Run("auth", "Authenticate", func() TestStep { return TestStep{Status: StepFail, Code: "SOURCE_AUTH_FAILED"} }))
	called := false
	require.False(t, r.Run("version", "Server version", func() TestStep { called = true; return TestStep{Status: StepOK} }))
	require.False(t, called)
	res := r.Result()
	require.False(t, res.OK)
	require.Equal(t, []StepStatus{StepOK, StepFail, StepSkipped}, []StepStatus{res.Steps[0].Status, res.Steps[1].Status, res.Steps[2].Status})
	require.Equal(t, "Authenticate", res.Steps[1].Label)
	require.Positive(t, res.LatencyMs)
}
