package couchbase

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/couchbase/gocb/v2"
	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors"
	"omnidatabus/internal/connectors/target"
	"omnidatabus/internal/netguard"
)

// readableKV is a fakeKV that can also read documents back (the verifier's kvGetter).
type readableKV struct {
	*fakeKV
	getErr map[string]error
}

func (r readableKV) Get(_ context.Context, key string) ([]byte, error) {
	if err := r.getErr[key]; err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if b, ok := r.docs[key]; ok {
		return b, nil
	}
	return nil, gocb.ErrDocumentNotFound
}

func TestWriter_GetDocs_ReadsFoundSkipsMissing(t *testing.T) {
	kv := readableKV{fakeKV: newFakeKV("a", "b")}
	w, _ := testWriter(kv, "upsert")
	got, err := w.GetDocs(context.Background(), []string{"a", "b", "missing"})
	require.NoError(t, err)
	require.Equal(t, map[string][]byte{"a": []byte(`{"old":true}`), "b": []byte(`{"old":true}`)}, got)
}

func TestWriter_GetDocs_ReturnsFirstErrorKeepsTheRest(t *testing.T) {
	boom := errors.New("timeout")
	kv := readableKV{fakeKV: newFakeKV("a"), getErr: map[string]error{"bad": boom}}
	w, _ := testWriter(kv, "upsert")
	got, err := w.GetDocs(context.Background(), []string{"a", "bad"})
	require.ErrorIs(t, err, boom)
	require.Contains(t, got, "a")
}

func TestWriter_GetDocs_NeedsAReader(t *testing.T) {
	w, _ := testWriter(newFakeKV(), "upsert")
	_, err := w.GetDocs(context.Background(), []string{"a"})
	require.ErrorContains(t, err, "can't read documents")
}

func TestWriter_CloseRunsTheConnectionCloser(t *testing.T) {
	w, _ := testWriter(newFakeKV(), "upsert")
	require.NoError(t, w.Close(), "no connection to close")
	closed := false
	w.close = func() error { closed = true; return nil }
	require.NoError(t, w.Close())
	require.True(t, closed)
}

func TestOpenWriter_RejectsBadConfigAndDeniedHosts(t *testing.T) {
	c := New()
	o := target.WriteOptions{Location: target.Location{Kind: target.KindCouchbase, Bucket: "b", Scope: "s", Collection: "c"}, Mode: "upsert"}
	_, err := c.OpenWriter(context.Background(), json.RawMessage(`{"connectionString":""}`), connectors.Credentials{Password: "pw"}, o)
	require.Error(t, err)

	restricted(t)
	raw := json.RawMessage(`{"connectionString":"couchbase://metadata.evil.example","username":"u"}`)
	_, err = c.OpenWriter(context.Background(), raw, connectors.Credentials{Password: "pw"}, o)
	require.ErrorIs(t, err, netguard.ErrDenied, "the bootstrap host is checked before the SDK connects")
}

func TestConnector_ValidateNormalizeKeyspaces_RejectBadConfig(t *testing.T) {
	c := New()
	bad := json.RawMessage(`{"connectionString":"http://nope"}`)
	require.Error(t, c.Validate(bad))
	_, _, err := c.Normalize(bad)
	require.Error(t, err)
	_, err = c.Keyspaces(context.Background(), "p1::target", "1", bad, connectors.Credentials{Password: "pw"})
	require.Error(t, err)
}

func TestSleepCtx(t *testing.T) {
	require.NoError(t, sleepCtx(context.Background(), time.Millisecond))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, sleepCtx(ctx, time.Hour), context.Canceled)
}

func TestParseConfig_RejectsUnknownFieldsAndBadParams(t *testing.T) {
	_, err := ParseConfig(json.RawMessage(`{"connectionString":"couchbase://cb.example.com","nope":1}`))
	require.Error(t, err)
	_, err = ParseConfig(json.RawMessage(`{"connectionString":"couchbase://cb.example.com?a=%zz","username":"u"}`))
	require.Error(t, err)
}

func TestCheckBootstrap_IgnoresUnparsableConnectionStrings(t *testing.T) {
	require.NoError(t, checkBootstrap(context.Background(), netguard.Default(), "::::"), "ParseConfig reports those")
}

func TestNewAuthProber_DialsThroughTheEgressGuard(t *testing.T) {
	restricted(t)
	p := newAuthProber()
	require.NotNil(t, p.lookupSRV)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "http://metadata.evil.example:8093/query/service", nil)
	require.NoError(t, err)
	resp, err := p.do(req, nil)
	if resp != nil {
		_ = resp.Body.Close()
	}
	require.ErrorIs(t, err, netguard.ErrDenied)
}

func TestCapabilitiesFromRoles_ScopeAdminManagesItsBucket(t *testing.T) {
	c := CapabilitiesFromRoles([]Role{{Role: "scope_admin", Bucket: "app"}}, SelfManaged)
	require.True(t, c.CanManageScopes)
	require.False(t, c.CanCreateBucket)
}
