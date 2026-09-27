package postgres

import (
	"context"
	"errors"
	"fmt"
	"io"
	"syscall"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
)

func TestClassifyPgTransient_AC7(t *testing.T) {
	transient := []error{
		&pgconn.PgError{Code: "57P01", Message: "terminating connection due to administrator command"},
		&pgconn.PgError{Code: "40001", Message: "could not serialize access"},
		&pgconn.PgError{Code: "08006", Message: "connection failure"},
		fmt.Errorf("read batch: %w", io.ErrUnexpectedEOF),
		fmt.Errorf("read: %w", syscall.ECONNRESET),
		errors.New("conn closed"),
	}
	for _, err := range transient {
		require.Truef(t, ClassifyPgTransient(err), "%v should be transient", err)
	}
	permanent := []error{
		nil,
		&pgconn.PgError{Code: "42P01", Message: `relation "shop.gone" does not exist`},
		&pgconn.PgError{Code: "42501", Message: "permission denied for table secret"},
		&pgconn.PgError{Code: "22P02", Message: "invalid input syntax"},
		context.Canceled,
		fmt.Errorf("stop: %w", context.DeadlineExceeded),
	}
	for _, err := range permanent {
		require.Falsef(t, ClassifyPgTransient(err), "%v should not be transient", err)
	}
}
