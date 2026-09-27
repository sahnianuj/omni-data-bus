package store

import (
	"errors"
	"net/http"

	"omnidatabus/internal/problem"
)

type notFoundError struct{}

func (notFoundError) Error() string { return "not found" }
func (notFoundError) Problem() problem.Problem {
	return problem.New(http.StatusNotFound, problem.CodeNotFound, "Not found", "The requested resource does not exist.")
}

type versionConflictError struct{}

func (versionConflictError) Error() string { return "version conflict" }
func (versionConflictError) Problem() problem.Problem {
	return problem.New(http.StatusPreconditionFailed, problem.CodeVersionConflict, "Changed by someone else",
		"This record changed since you loaded it. Reload and try again.")
}

type unavailableError struct{ cause error }

func (e unavailableError) Error() string { return "metadata store unavailable: " + e.cause.Error() }
func (e unavailableError) Unwrap() error { return e.cause }
func (unavailableError) Problem() problem.Problem {
	return problem.New(http.StatusServiceUnavailable, problem.CodeUnavailable, "Service unavailable",
		"The metadata store is temporarily unavailable. Try again shortly.")
}

// Is makes errors.Is(err, ErrUnavailable) match any unavailableError.
func (unavailableError) Is(target error) bool { _, ok := target.(unavailableError); return ok }

type invalidCursorError struct{}

func (invalidCursorError) Error() string { return "invalid cursor" }
func (invalidCursorError) Problem() problem.Problem {
	return problem.New(http.StatusBadRequest, problem.CodeInvalidCursor, "Invalid cursor", "The paging cursor is invalid.")
}

// Domain errors returned by every Store implementation.
var (
	ErrNotFound        error = notFoundError{}
	ErrVersionConflict error = versionConflictError{}
	ErrUnavailable     error = unavailableError{cause: errors.New("unavailable")}
	ErrInvalidCursor   error = invalidCursorError{}
)

// Unavailable wraps a transport error as ErrUnavailable.
func Unavailable(cause error) error { return unavailableError{cause: cause} }

// ConflictError reports a uniqueness violation on Field.
type ConflictError struct{ Field string }

func (e *ConflictError) Error() string { return "conflict on " + e.Field }

// Problem implements problem.Problemer.
func (e *ConflictError) Problem() problem.Problem {
	p := problem.New(http.StatusConflict, problem.CodeConflict, "Already exists", "A record with this "+e.Field+" already exists.")
	p.FieldErrors = []problem.FieldError{{Field: e.Field, Message: "is already taken"}}
	return p
}

// IsConflict reports whether err is a uniqueness conflict on field.
func IsConflict(err error, field string) bool {
	var c *ConflictError
	return errors.As(err, &c) && c.Field == field
}
