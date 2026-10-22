package postgres

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors"
	"omnidatabus/internal/connectors/source"
)

type fakeSession struct {
	source.Session // methods the tester doesn't use (discovery) are left unimplemented
	info           source.ServerInfo
	rep            source.PrivilegeReport
	pingErr        error
	privErr        error
	closed         bool
}

func (f *fakeSession) Ping(context.Context) (source.ServerInfo, error) { return f.info, f.pingErr }
func (f *fakeSession) CheckPrivileges(context.Context) (source.PrivilegeReport, error) {
	return f.rep, f.privErr
}
func (f *fakeSession) Close() error { f.closed = true; return nil }

type fakeConn struct{ net.Conn }

func (fakeConn) Close() error { return nil }

func testConnector(sess *fakeSession, connectErr error) *Connector {
	return &Connector{
		lookupHost: func(context.Context, string) ([]string, error) { return []string{"10.0.0.5"}, nil },
		dialTCP:    func(context.Context, string) (net.Conn, error) { return fakeConn{}, nil },
		connect: func(context.Context, *pgx.ConnConfig) (session, error) {
			if connectErr != nil {
				return nil, connectErr
			}
			return sess, nil
		},
		now: time.Now,
	}
}

func statuses(res connectors.TestResult) map[string]connectors.StepStatus {
	out := map[string]connectors.StepStatus{}
	for _, s := range res.Steps {
		out[s.ID] = s.Status
	}
	return out
}

var creds = connectors.Credentials{Password: "pw"}

func TestTester_AC4_AllOK(t *testing.T) {
	sess := &fakeSession{info: source.ServerInfo{Product: "PostgreSQL", Version: "16.3"}}
	res := testConnector(sess, nil).Test(context.Background(), cfgJSON(nil), creds)
	require.True(t, res.OK)
	require.Equal(t, map[string]connectors.StepStatus{"resolve": "ok", "connect": "ok", "auth": "ok", "version": "ok", "privileges": "ok"}, statuses(res))
	require.Equal(t, "PostgreSQL 16.3", res.Steps[3].Detail)
	require.Equal(t, "Authenticate as omni_ro", res.Steps[2].Label)
	require.True(t, sess.closed, "session closed after the test")
}

func TestTester_AC4_AuthFailSkipsRest(t *testing.T) {
	pgErr := &pgconn.PgError{Code: "28P01", Message: "password authentication failed for user \"omni_ro\""}
	res := testConnector(&fakeSession{}, fmt.Errorf("failed to connect: %w", pgErr)).Test(context.Background(), cfgJSON(nil), creds)
	require.False(t, res.OK)
	require.Equal(t, map[string]connectors.StepStatus{"resolve": "ok", "connect": "ok", "auth": "fail", "version": "skipped", "privileges": "skipped"}, statuses(res))
	require.Equal(t, "SOURCE_AUTH_FAILED", res.Steps[2].Code)
	require.Contains(t, res.Steps[2].Hint, "username and password")
}

func TestTester_AC4_UnreachableAndDNS(t *testing.T) {
	c := testConnector(&fakeSession{}, nil)
	c.dialTCP = func(context.Context, string) (net.Conn, error) {
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
	}
	res := c.Test(context.Background(), cfgJSON(nil), creds)
	require.Equal(t, connectors.StepFail, statuses(res)["connect"])
	require.Equal(t, "SOURCE_UNREACHABLE", res.Steps[1].Code)

	c = testConnector(&fakeSession{}, nil)
	c.lookupHost = func(context.Context, string) ([]string, error) {
		return nil, &net.DNSError{Err: "no such host", Name: "db"}
	}
	res = c.Test(context.Background(), cfgJSON(nil), creds)
	require.Equal(t, "SOURCE_DNS", res.Steps[0].Code)
	require.Equal(t, connectors.StepSkipped, statuses(res)["connect"])
}

func TestTester_AC4_IPHostSkipsDNS(t *testing.T) {
	c := testConnector(&fakeSession{}, nil)
	c.lookupHost = func(context.Context, string) ([]string, error) { panic("must not resolve an IP") }
	res := c.Test(context.Background(), cfgJSON(map[string]any{"host": "10.1.2.3"}), creds)
	require.True(t, res.OK)
}

func TestTester_AC4_PrivilegeWarning(t *testing.T) {
	sess := &fakeSession{rep: source.PrivilegeReport{UnreadableTables: []string{"billing.invoice", "billing.payment", "billing.refund"}}}
	res := testConnector(sess, nil).Test(context.Background(), cfgJSON(nil), creds)
	require.True(t, res.OK, "a warning doesn't fail the test")
	priv := res.Steps[4]
	require.Equal(t, connectors.StepWarn, priv.Status)
	require.Contains(t, priv.Detail, "No SELECT on 3 table(s) in schema 'billing'")
	require.Equal(t, "GRANT USAGE ON SCHEMA billing TO omni_ro; GRANT SELECT ON ALL TABLES IN SCHEMA billing TO omni_ro;", priv.Hint)
}

func TestTester_AC4_SuperuserAndWriterWarnings(t *testing.T) {
	res := testConnector(&fakeSession{rep: source.PrivilegeReport{Superuser: true}}, nil).Test(context.Background(), cfgJSON(nil), creds)
	require.Contains(t, res.Steps[4].Detail, "superuser")
	res = testConnector(&fakeSession{rep: source.PrivilegeReport{CanWrite: true}}, nil).Test(context.Background(), cfgJSON(nil), creds)
	require.Contains(t, res.Steps[4].Detail, "can modify data")
	res = testConnector(&fakeSession{privErr: errors.New("boom")}, nil).Test(context.Background(), cfgJSON(nil), creds)
	require.Equal(t, connectors.StepWarn, res.Steps[4].Status)
	require.True(t, res.OK)
}

func TestTester_AC1_InvalidConfigFailsFast(t *testing.T) {
	res := testConnector(&fakeSession{}, nil).Test(context.Background(), cfgJSON(map[string]any{"port": 0, "sslMode": "bad"}), creds)
	require.False(t, res.OK)
	require.Equal(t, "config", res.Steps[0].ID)
}

func TestPrivilegeHint_QuotesUnusualNames(t *testing.T) {
	s := privilegeStep(source.PrivilegeReport{UnreadableTables: []string{"Sales Data.t1", "b.t2", "Sales Data.t3"}}, "Omni RO")
	require.Contains(t, s.Hint, `GRANT USAGE ON SCHEMA "Sales Data" TO "Omni RO";`)
	require.Contains(t, s.Detail, "across 2 schemas (most in 'Sales Data')")
}

func TestConnector_Descriptor(t *testing.T) {
	c := New()
	require.Equal(t, "postgres", c.Descriptor().Type)
	require.NoError(t, c.Validate(cfgJSON(nil)))
	norm, name, err := c.Normalize(cfgJSON(nil))
	require.NoError(t, err)
	require.Equal(t, "shop@db.example.com", name)
	require.Contains(t, string(norm), `"sslMode":"require"`)
}

func TestPostgresTest_OC190_AC2_Refused(t *testing.T) {
	c := testConnector(&fakeSession{}, nil)
	c.dialTCP = func(context.Context, string) (net.Conn, error) {
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connect: connection refused")}
	}
	res := c.Test(context.Background(), cfgJSON(nil), creds)
	step := res.Steps[1]
	require.Equal(t, "SOURCE_UNREACHABLE", step.Code)
	require.Contains(t, step.Detail, "refused the connection on port 5432: nothing is listening there.")
	require.Contains(t, step.Hint, "Check the port")
	require.Contains(t, step.Cause, "connection refused", "the driver's text is kept")

	c = testConnector(&fakeSession{}, nil)
	c.lookupHost = func(context.Context, string) ([]string, error) {
		return nil, &net.DNSError{Err: "no such host", Name: "db.example.com", IsNotFound: true}
	}
	res = c.Test(context.Background(), cfgJSON(nil), creds)
	require.Equal(t, "The host name db.example.com doesn't resolve.", res.Steps[0].Detail)
}
