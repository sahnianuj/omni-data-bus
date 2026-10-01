package directory

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"sync/atomic"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors"
	"omnidatabus/internal/connectors/target"
	"omnidatabus/internal/problem"
)

var connNoCreds = connectors.Credentials{}

func codeOf(err error) string { return problem.From(err).Code }

// errFS fails every Create with err (after okFirst successful ones).
type errFS struct {
	osFS
	err     error
	failFor int32 // fail this many Creates, then succeed
	calls   atomic.Int32
}

func (e *errFS) Create(name string, m fs.FileMode) (file, error) {
	if e.failFor < 0 || e.calls.Add(1) <= e.failFor {
		return nil, e.err
	}
	return e.osFS.Create(name, m)
}

func TestWriter_AC3_ErrorTable(t *testing.T) {
	for _, tc := range []struct {
		err   error
		code  string
		fatal bool
	}{
		{syscall.ENOSPC, target.CodeDiskFull, true},
		{syscall.EDQUOT, target.CodeDiskFull, true},
		{syscall.EACCES, target.CodePermissionDenied, true},
		{syscall.EPERM, target.CodePermissionDenied, true},
		{syscall.EROFS, target.CodePermissionDenied, true},
		{&fs.PathError{Op: "open", Path: "x", Err: syscall.ENAMETOOLONG}, target.CodeKeyInvalid, false},
		{os.ErrNotExist, target.CodeLocationMissing, true},
		{syscall.EIO, target.CodeWriteFailed, false},
	} {
		w, _ := newTestWriter(t, "upsert", nil, &errFS{err: tc.err, failFor: -1})
		r := w.WriteBatch(context.Background(), []target.Doc{doc("1001", 1)})[0]
		require.Equal(t, target.Failed, r.Outcome, "%v", tc.err)
		require.Equal(t, tc.code, r.Code, "%v", tc.err)
		require.Equal(t, tc.fatal, r.Fatal, "%v", tc.err)
		if tc.code == target.CodeWriteFailed {
			require.Equal(t, writeTries, r.Attempts, "retried 3 times")
		} else {
			require.Equal(t, 1, r.Attempts, "not retried")
		}
	}
}

func TestWriter_AC3_RetryThenSucceed(t *testing.T) {
	w, _ := newTestWriter(t, "upsert", nil, &errFS{err: syscall.EIO, failFor: 2})
	r := w.WriteBatch(context.Background(), []target.Doc{doc("1001", 1)})[0]
	require.Equal(t, target.Written, r.Outcome)
	require.Equal(t, 3, r.Attempts)
}

func TestWriter_AC3_ConsecutiveFailuresBecomeFatal(t *testing.T) {
	w, _ := newTestWriter(t, "upsert", nil, &errFS{err: syscall.EIO, failFor: -1})
	w.workers = 1
	docs := make([]target.Doc, maxConsecutiveFail)
	for i := range docs {
		docs[i] = doc(string(rune('a'+i%26))+string(rune('0'+i/26)), i)
	}
	res := w.WriteBatch(context.Background(), docs)
	require.False(t, res[0].Fatal)
	require.True(t, res[len(res)-1].Fatal, "50 consecutive failures stop the job")
	require.True(t, errors.Is(syscall.EIO, syscall.EIO))
}
