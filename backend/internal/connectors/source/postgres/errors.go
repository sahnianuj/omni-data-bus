package postgres

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

// transientSQLStates are server errors worth retrying on a new connection: connection
// exceptions (08…), admin/crash shutdown and "cannot connect now" (57P01–57P03),
// serialization failure and deadlock (40001, 40P01).
var transientSQLStates = map[string]bool{
	"57P01": true, "57P02": true, "57P03": true, "40001": true, "40P01": true,
}

// ClassifyPgTransient reports whether a read error should be retried (OC-024 AC7).
// Cancellation is never transient: the job is stopping.
func ClassifyPgTransient(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		return transientSQLStates[pg.Code] || strings.HasPrefix(pg.Code, "08")
	}
	var ne net.Error
	if errors.As(err, &ne) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, net.ErrClosed) {
		return true
	}
	if pgconn.SafeToRetry(err) || pgconn.Timeout(err) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "conn closed") || strings.Contains(msg, "connection reset") || strings.Contains(msg, "broken pipe")
}
