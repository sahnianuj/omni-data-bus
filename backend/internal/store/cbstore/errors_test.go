package cbstore

import (
	"errors"
	"fmt"
	"testing"

	"omnidatabus/internal/store"
	"omnidatabus/internal/store/provision"

	"github.com/couchbase/gocb/v2"
	"github.com/stretchr/testify/require"
)

// txWrapped imitates how a transaction wraps its cause.
type txWrapped struct{ cause error }

func (e txWrapped) Error() string { return "transaction failed | " + e.cause.Error() }
func (e txWrapped) Unwrap() error { return e.cause }

func TestTranslate_AC3_Table(t *testing.T) {
	cases := []struct {
		name  string
		in    error
		check func(t *testing.T, got error)
	}{
		{"not found", gocb.ErrDocumentNotFound, func(t *testing.T, got error) { require.ErrorIs(t, got, store.ErrNotFound) }},
		{"exists on unique", gocb.ErrDocumentExists, func(t *testing.T, got error) { require.True(t, store.IsConflict(got, "name")) }},
		{"cas mismatch", gocb.ErrCasMismatch, func(t *testing.T, got error) { require.ErrorIs(t, got, store.ErrVersionConflict) }},
		{"timeout", gocb.ErrUnambiguousTimeout, func(t *testing.T, got error) { require.ErrorIs(t, got, store.ErrUnavailable) }},
		{"tx wraps exists", txWrapped{fmt.Errorf("op: %w", gocb.ErrDocumentExists)}, func(t *testing.T, got error) { require.True(t, store.IsConflict(got, "name")) }},
		{"tx wraps domain not found", txWrapped{store.ErrNotFound}, func(t *testing.T, got error) { require.Equal(t, store.ErrNotFound, got) }},
		{"tx wraps version conflict", txWrapped{store.ErrVersionConflict}, func(t *testing.T, got error) { require.Equal(t, store.ErrVersionConflict, got) }},
		{"tx wraps domain conflict", txWrapped{&store.ConflictError{Field: "email"}}, func(t *testing.T, got error) { require.True(t, store.IsConflict(got, "email")) }},
		{"other passes through", errors.New("boom"), func(t *testing.T, got error) { require.EqualError(t, got, "boom") }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { c.check(t, Translate(c.in, "name")) })
	}
	require.NoError(t, Translate(nil, ""))
	require.True(t, store.IsConflict(Translate(gocb.ErrDocumentExists, ""), "id"))
}

func TestProvisionErr_AC8_Mapping(t *testing.T) {
	require.ErrorIs(t, provisionErr(gocb.ErrScopeExists), provision.ErrExists)
	require.ErrorIs(t, provisionErr(gocb.ErrCollectionExists), provision.ErrExists)
	// A concurrent CREATE INDEX from another node (seen when api and worker start together).
	require.ErrorIs(t, provisionErr(errors.New(`internal server failure | {"errors":[{"code":5000,"message":"GSI CreateIndex() - cause: Encountered transient error.  Index creation will be retried in background. Error: Index idx_jobs_ws will retry building in the background for reason: Build Already In Progress."}]}`)), provision.ErrExists)
	require.ErrorIs(t, provisionErr(gocb.ErrIndexExists), provision.ErrExists)
	require.ErrorIs(t, provisionErr(errors.New("User does not have credentials to run index operations")), provision.ErrForbidden)
	require.ErrorIs(t, provisionErr(errors.New("http status code: 403 Forbidden")), provision.ErrForbidden)
	require.EqualError(t, provisionErr(errors.New("disk full")), "disk full")
	require.NoError(t, provisionErr(nil))
}

func TestTimestamps_FixedWidthAndSortable(t *testing.T) {
	a := formatTS(parseTS("2026-09-25T10:00:00.000000000Z"))
	b := formatTS(parseTS("2026-09-25T10:00:00.100000000Z"))
	require.Len(t, a, len(b))
	require.Less(t, a, b)
	require.Nil(t, timePtr(""))
}
