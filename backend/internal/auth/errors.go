package auth

import (
	"fmt"
	"math"
	"net/http"
	"time"

	"omnidatabus/internal/problem"
)

// Auth errors, rendered as problem+json.
var (
	ErrInvalidCredentials = problem.New(http.StatusUnauthorized, problem.CodeInvalidCreds, "Sign-in failed", "The email or password is incorrect.")
	ErrWrongPassword      = problem.New(http.StatusUnauthorized, problem.CodeInvalidCreds, "Wrong password", "The password is incorrect.")
	ErrUnauthenticated    = problem.New(http.StatusUnauthorized, problem.CodeUnauthenticated, "Sign in required", "Please sign in to continue.")
	ErrSessionExpired     = problem.New(http.StatusUnauthorized, problem.CodeSessionExpired, "Session expired", "Your session has expired. Please sign in again.")
	ErrEmailTaken         = func() problem.Problem {
		p := problem.New(http.StatusConflict, problem.CodeEmailTaken, "Email already registered", "An account with this email already exists. Try signing in.")
		p.FieldErrors = []problem.FieldError{{Field: "email", Message: "is already registered"}}
		return p
	}()
	ErrSignupDisabled = problem.New(http.StatusForbidden, problem.CodeSignupDisabled, "Sign-up disabled", "Self sign-up is disabled on this server. Ask an administrator for an account.")
	ErrCSRF           = problem.New(http.StatusForbidden, problem.CodeCSRFFailed, "Request blocked", "The request's security token is missing or invalid. Reload the page and try again.")
	ErrForbiddenRole  = problem.New(http.StatusForbidden, problem.CodeForbiddenRole, "Not allowed", "Your role in this workspace is read-only.")
)

// RateLimitedError is returned when too many sign-in attempts were made.
type RateLimitedError struct{ RetryAfter time.Duration }

func (e *RateLimitedError) Error() string { return "rate limited" }

// Problem implements problem.Problemer.
func (e *RateLimitedError) Problem() problem.Problem {
	return problem.New(http.StatusTooManyRequests, problem.CodeRateLimited, "Too many attempts",
		fmt.Sprintf("Too many sign-in attempts. Try again in %d seconds.", e.Seconds()))
}

// Seconds is the Retry-After value (at least 1).
func (e *RateLimitedError) Seconds() int {
	return int(math.Max(1, math.Ceil(e.RetryAfter.Seconds())))
}

func validationProblem(field, message string) problem.Problem {
	p := problem.New(http.StatusUnprocessableEntity, problem.CodeValidation, "Invalid request", "One or more fields are invalid.")
	p.FieldErrors = []problem.FieldError{{Field: field, Message: message}}
	return p
}
