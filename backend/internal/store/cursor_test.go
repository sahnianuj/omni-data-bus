package store

import (
	"errors"
	"testing"
	"time"

	"omnidatabus/internal/problem"

	"github.com/stretchr/testify/require"
)

func TestCursor_AC5_EncodeDecodeRoundTrip(t *testing.T) {
	at := time.Date(2026, 9, 25, 10, 11, 12, 123456789, time.UTC)
	at2, id, err := DecodeCursor(EncodeCursor(at, "01a0-id"))
	require.NoError(t, err)
	require.True(t, at.Equal(at2))
	require.Equal(t, "01a0-id", id)

	at3, id3, err := DecodeCursor("")
	require.NoError(t, err)
	require.True(t, at3.IsZero())
	require.Empty(t, id3)
}

func TestCursor_AC5_RejectsTampered(t *testing.T) {
	for _, c := range []string{"%%%", "bm9waXBl", EncodeCursor(time.Now(), "")[:4]} {
		_, _, err := DecodeCursor(c)
		require.ErrorIs(t, err, ErrInvalidCursor, c)
	}
}

func TestBefore_AC5_TieBreaksOnID(t *testing.T) {
	at := time.Now()
	require.True(t, Before(at, "a", at, "b"))
	require.False(t, Before(at, "c", at, "b"))
	require.True(t, Before(at.Add(-time.Second), "z", at, "a"))
}

func TestPage_AC5_Normalize(t *testing.T) {
	require.Equal(t, 20, Page{}.Normalize().Limit)
	require.Equal(t, 100, Page{Limit: 1000}.Normalize().Limit)
	require.Equal(t, 5, Page{Limit: 5}.Normalize().Limit)
}

func TestErrors_AC3_ProblemsAndMatching(t *testing.T) {
	require.ErrorIs(t, Unavailable(errors.New("timeout")), ErrUnavailable)
	require.True(t, IsConflict(&ConflictError{Field: "name"}, "name"))
	require.False(t, IsConflict(&ConflictError{Field: "email"}, "name"))
	require.Equal(t, 404, problem.From(ErrNotFound).Status)
	require.Equal(t, 412, problem.From(ErrVersionConflict).Status)
	require.Equal(t, 503, problem.From(Unavailable(errors.New("x"))).Status)
	require.Equal(t, 400, problem.From(ErrInvalidCursor).Status)
	c := problem.From(&ConflictError{Field: "name"})
	require.Equal(t, 409, c.Status)
	require.Equal(t, "name", c.FieldErrors[0].Field)
}

func TestErrors_AC3_Messages(t *testing.T) {
	require.Equal(t, "not found", ErrNotFound.Error())
	require.Equal(t, "version conflict", ErrVersionConflict.Error())
	require.Equal(t, "invalid cursor", ErrInvalidCursor.Error())
	require.Contains(t, Unavailable(errors.New("dial")).Error(), "dial")
	require.ErrorIs(t, errors.Unwrap(Unavailable(errors.ErrUnsupported)), errors.ErrUnsupported)
	require.Equal(t, "conflict on name", (&ConflictError{Field: "name"}).Error())
	require.True(t, RoleSource.Valid())
	require.False(t, Role("x").Valid())
	require.Equal(t, "p1::target", ConnectionID("p1", RoleTarget))
	require.Equal(t, "orders", NameKey("  Orders "))
	require.False(t, SystemClock{}.Now().IsZero())
}
