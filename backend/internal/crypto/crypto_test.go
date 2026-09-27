package crypto

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"omnidatabus/internal/store"
	"omnidatabus/internal/store/memstore"

	"github.com/stretchr/testify/require"
)

func b64Key(fill byte) string {
	return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{fill}, KeySize))
}

func singleSealer(t *testing.T) *LocalSealer {
	t.Helper()
	kr, err := ParseKeyring(b64Key(1), "", "")
	require.NoError(t, err)
	return NewLocalSealer(kr)
}

func TestSealer_AC1_RoundTrip(t *testing.T) {
	s := singleSealer(t)
	aad := ConnectionAAD("ws", "p", "p::source")
	sealed, err := s.Seal([]byte(`{"password":"hunter2"}`), aad)
	require.NoError(t, err)
	require.Equal(t, "k1", sealed.KeyID)
	require.NotContains(t, string(sealed.Ciphertext), "hunter2")
	pt, err := s.Open(sealed, aad)
	require.NoError(t, err)
	require.Equal(t, `{"password":"hunter2"}`, string(pt))
}

func TestSealer_AC1_WrongAADFails(t *testing.T) {
	s := singleSealer(t)
	sealed, err := s.Seal([]byte("x"), ConnectionAAD("ws-a", "p", "p::source"))
	require.NoError(t, err)
	for _, aad := range [][]byte{
		ConnectionAAD("ws-b", "p", "p::source"), // another tenant
		ConnectionAAD("ws-a", "q", "q::source"), // another project
		ConnectionAAD("ws-a", "p", "p::target"), // another connection
	} {
		_, err := s.Open(sealed, aad)
		require.ErrorIs(t, err, ErrDecrypt)
	}
}

func TestSealer_AC1_TamperedCiphertextFails(t *testing.T) {
	s := singleSealer(t)
	aad := []byte("aad")
	sealed, err := s.Seal([]byte("secret"), aad)
	require.NoError(t, err)

	bad := sealed
	bad.Ciphertext = append([]byte(nil), sealed.Ciphertext...)
	bad.Ciphertext[0] ^= 0x01
	_, err = s.Open(bad, aad)
	require.ErrorIs(t, err, ErrDecrypt)

	bad = sealed
	bad.WrappedDEK = append([]byte(nil), sealed.WrappedDEK...)
	bad.WrappedDEK[3] ^= 0x80
	_, err = s.Open(bad, aad)
	require.ErrorIs(t, err, ErrDecrypt)

	bad = sealed
	bad.Nonce = []byte{1, 2}
	_, err = s.Open(bad, aad)
	require.ErrorIs(t, err, ErrDecrypt)
}

func TestSealer_AC1_UniqueNoncesPerSeal(t *testing.T) {
	s := singleSealer(t)
	seen := map[string]bool{}
	for i := 0; i < 10000; i++ {
		sealed, err := s.Seal([]byte("x"), nil)
		require.NoError(t, err)
		for _, n := range [][]byte{sealed.Nonce, sealed.DEKNonce} {
			require.False(t, seen[string(n)], "nonce reused")
			seen[string(n)] = true
		}
	}
}

func TestKeyring_AC2_ParseMultipleKeys(t *testing.T) {
	kr, err := ParseKeyring("", fmt.Sprintf("k2:%s, k1:%s", b64Key(2), b64Key(1)), "k2")
	require.NoError(t, err)
	require.Equal(t, "k2", kr.Active())
	require.Equal(t, []string{"k1", "k2"}, kr.IDs())
}

func TestKeyring_AC2_RejectsBadInput(t *testing.T) {
	short := base64.StdEncoding.EncodeToString([]byte("too short"))
	cases := map[string][3]string{
		"short single":      {short, "", ""},
		"not base64":        {"%%%", "", ""},
		"missing active":    {"", "k1:" + b64Key(1), ""},
		"active not listed": {"", "k1:" + b64Key(1), "k9"},
		"bad entry":         {"", "k1" + b64Key(1), "k1"},
		"duplicate id":      {"", "k1:" + b64Key(1) + ",k1:" + b64Key(2), "k1"},
		"short in multi":    {"", "k1:" + short, "k1"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParseKeyring(c[0], c[1], c[2])
			require.Error(t, err)
		})
	}
}

type rotationFixture struct {
	st  *memstore.Store
	ids []string
}

func seedConnections(t *testing.T, sealer *LocalSealer, n int) rotationFixture {
	t.Helper()
	ctx := context.Background()
	st := memstore.New(nil)
	fx := rotationFixture{st: st}
	for i := 0; i < n; i++ {
		ws := store.WorkspaceID(fmt.Sprintf("ws-%d", i))
		p, err := st.Projects().Create(ctx, ws, store.NewProject{Name: "p"})
		require.NoError(t, err)
		id := store.ConnectionID(p.ID, store.RoleSource)
		sealed, err := sealer.Seal([]byte(fmt.Sprintf("secret-%d", i)), ConnectionAAD(string(ws), p.ID, id))
		require.NoError(t, err)
		_, err = st.Connections().Upsert(ctx, ws, p.ID, store.RoleSource, store.ConnectionInput{Type: "postgres", Secret: sealed})
		require.NoError(t, err)
		fx.ids = append(fx.ids, id)
	}
	return fx
}

func TestRotate_AC2_RewrapsAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	kr1, err := ParseKeyring("", "k1:"+b64Key(1), "k1")
	require.NoError(t, err)
	fx := seedConnections(t, NewLocalSealer(kr1), 7)

	kr2, err := ParseKeyring("", fmt.Sprintf("k1:%s,k2:%s", b64Key(1), b64Key(2)), "k2")
	require.NoError(t, err)
	sealer2 := NewLocalSealer(kr2)

	report, err := Rotate(ctx, fx.st.Connections(), sealer2, 3) // small batches exercise paging
	require.NoError(t, err)
	require.Equal(t, 7, report.Rewrapped)
	require.Empty(t, report.Failed)

	left, err := fx.st.Connections().ListForRewrap(ctx, "k2", "", 100)
	require.NoError(t, err)
	require.Empty(t, left, "every secret is on k2")

	// Still decrypts to the same plaintext with only the new key... and the old key id is gone from records.
	for i := 0; i < 7; i++ {
		ws := store.WorkspaceID(fmt.Sprintf("ws-%d", i))
		c, err := fx.st.Connections().Get(ctx, ws, fx.ids[i])
		require.NoError(t, err)
		require.Equal(t, "k2", c.Secret.KeyID)
		pt, err := sealer2.Open(c.Secret, ConnectionAAD(string(ws), c.ProjectID, c.ID))
		require.NoError(t, err)
		require.Equal(t, fmt.Sprintf("secret-%d", i), string(pt))
	}

	again, err := Rotate(ctx, fx.st.Connections(), sealer2, 3)
	require.NoError(t, err)
	require.Zero(t, again.Rewrapped, "second run changes nothing")
}

func TestRotate_AC2_ReportsUnknownKeyAndContinues(t *testing.T) {
	ctx := context.Background()
	// One record sealed with k0, which the new keyring doesn't have...
	kr0, err := ParseKeyring("", "k0:"+b64Key(9), "k0")
	require.NoError(t, err)
	fx := seedConnections(t, NewLocalSealer(kr0), 1)

	// ...and one sealed with kold, which it does have.
	krOld, err := ParseKeyring("", "kold:"+b64Key(5), "kold")
	require.NoError(t, err)
	p, err := fx.st.Projects().Create(ctx, "ws-x", store.NewProject{Name: "x"})
	require.NoError(t, err)
	id := store.ConnectionID(p.ID, store.RoleTarget)
	sealed, err := NewLocalSealer(krOld).Seal([]byte("ok"), ConnectionAAD("ws-x", p.ID, id))
	require.NoError(t, err)
	_, err = fx.st.Connections().Upsert(ctx, "ws-x", p.ID, store.RoleTarget, store.ConnectionInput{Secret: sealed})
	require.NoError(t, err)

	krNew, err := ParseKeyring("", fmt.Sprintf("k1:%s,kold:%s", b64Key(1), b64Key(5)), "k1")
	require.NoError(t, err)
	report, err := Rotate(ctx, fx.st.Connections(), NewLocalSealer(krNew), 10)
	require.NoError(t, err, "a bad record doesn't abort the batch")
	require.Equal(t, 1, report.Rewrapped, "the known-key record is rotated")
	require.Len(t, report.Failed, 1)
	require.Equal(t, fx.ids[0], report.Failed[0].ConnectionID)
	require.ErrorIs(t, report.Failed[0].Err, ErrUnknownKey)
}

func TestFingerprint_AC3_SHA256Colon(t *testing.T) {
	fp, err := CertFingerprint(testCertPEM)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(fp, "SHA256:"))
	require.Len(t, strings.Split(strings.TrimPrefix(fp, "SHA256:"), ":"), 32)
	fp2, err := CertFingerprint(testCertPEM)
	require.NoError(t, err)
	require.Equal(t, fp, fp2, "deterministic")
	_, err = CertFingerprint("-----BEGIN PRIVATE KEY-----\nAAAA\n-----END PRIVATE KEY-----\n")
	require.ErrorIs(t, err, ErrNotCertificate)
	_, err = CertFingerprint("not pem")
	require.ErrorIs(t, err, ErrNotCertificate)
}

func TestSecret_AC4_JSONMasked(t *testing.T) {
	out, err := json.Marshal(struct {
		Password Secret `json:"password"`
	}{"hunter2"})
	require.NoError(t, err)
	require.JSONEq(t, `{"password":"***"}`, string(out))
}

func TestSecret_AC4_SlogMasked(t *testing.T) {
	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("x", "anything", Secret("hunter2"))
	require.NotContains(t, buf.String(), "hunter2")
	require.Contains(t, buf.String(), "***")
}

func TestSecret_AC4_FmtVerbsMasked(t *testing.T) {
	s := Secret("hunter2")
	wrapper := struct{ P Secret }{s}
	for _, out := range []string{
		fmt.Sprintf("%v", s),
		fmt.Sprintf("%s", s), //nolint:staticcheck // S1025: the %s verb itself is under test
		fmt.Sprintf("%+v", wrapper), fmt.Sprintf("%#v", s), fmt.Sprint(s),
	} {
		require.NotContains(t, out, "hunter2", out)
	}
	require.Equal(t, "hunter2", s.Reveal())
}
