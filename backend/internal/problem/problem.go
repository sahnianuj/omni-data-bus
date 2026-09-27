// Package problem renders RFC 9457 problem+json error responses.
package problem

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"reflect"
	"strings"
	"unicode"

	"github.com/go-playground/validator/v10"
)

// ContentType is the media type for problem responses.
const ContentType = "application/problem+json"

// FieldError describes one invalid input field.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// Problem is the RFC 9457 error body plus a stable machine-readable code.
type Problem struct {
	Type        string       `json:"type"`
	Title       string       `json:"title"`
	Status      int          `json:"status"`
	Detail      string       `json:"detail,omitempty"`
	Code        string       `json:"code"`
	Instance    string       `json:"instance,omitempty"`
	FieldErrors []FieldError `json:"fieldErrors,omitempty"`
}

// Error lets a Problem be returned as an error.
func (p Problem) Error() string { return p.Code + ": " + p.Detail }

// Problemer is implemented by domain errors that know their HTTP representation.
type Problemer interface{ Problem() Problem }

// New builds a problem with the standard type URI.
func New(status int, code, title, detail string) Problem {
	return Problem{
		Type:  "https://omni-data-bus.dev/problems/" + strings.ToLower(strings.ReplaceAll(code, "_", "-")),
		Title: title, Status: status, Code: code, Detail: detail,
	}
}

// From converts any error into a Problem. Unknown errors become an opaque 500.
func From(err error) Problem {
	var p Problem
	if errors.As(err, &p) {
		return p
	}
	var pr Problemer
	if errors.As(err, &pr) {
		return pr.Problem()
	}
	var verrs validator.ValidationErrors
	if errors.As(err, &verrs) {
		out := New(http.StatusUnprocessableEntity, CodeValidation, "Invalid request", "One or more fields are invalid.")
		for _, fe := range verrs {
			out.FieldErrors = append(out.FieldErrors, FieldError{Field: jsonPath(fe.Namespace()), Message: message(fe)})
		}
		return out
	}
	return New(http.StatusInternalServerError, CodeInternal, "Internal server error", "Something went wrong. Quote the request ID when reporting this.")
}

// Write renders err as problem+json. 5xx causes are logged; details are never leaked.
func Write(w http.ResponseWriter, r *http.Request, err error) {
	p := From(err)
	if p.Instance == "" && r != nil {
		p.Instance = RequestIDFrom(r)
	}
	if p.Status >= 500 && r != nil {
		slog.ErrorContext(r.Context(), "request failed", "error", err.Error(), "code", p.Code)
	}
	w.Header().Set("Content-Type", ContentType)
	w.WriteHeader(p.Status)
	_ = json.NewEncoder(w).Encode(p)
}

// RequestIDFrom reads the request ID set by the RequestID middleware.
func RequestIDFrom(r *http.Request) string { return r.Header.Get("X-Request-ID") }

// jsonPath turns a validator namespace "Req.Source.Port" into "source.port".
func jsonPath(ns string) string {
	parts := strings.Split(ns, ".")
	if len(parts) > 1 {
		parts = parts[1:]
	}
	for i, p := range parts {
		parts[i] = lowerFirst(p)
	}
	return strings.Join(parts, ".")
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	r[0] = unicode.ToLower(r[0])
	return string(r)
}

func message(fe validator.FieldError) string {
	switch fe.Tag() {
	case "required":
		return "is required"
	case "min":
		return "must be at least " + fe.Param()
	case "max":
		return "must be at most " + fe.Param()
	case "oneof":
		return "must be one of: " + fe.Param()
	case "email":
		return "must be a valid email address"
	default:
		return "is invalid (" + fe.Tag() + ")"
	}
}

// HasCode reports whether err is (or wraps) a problem with the given code. Use it instead of
// errors.Is: Problem holds a slice, so problem values aren't comparable.
func HasCode(err error, code string) bool {
	if err == nil {
		return false
	}
	var p Problem
	if errors.As(err, &p) {
		return p.Code == code
	}
	var pr Problemer
	return errors.As(err, &pr) && pr.Problem().Code == code
}

// NewValidator returns a validator whose errors use JSON field names (e.g. "sslMode", not
// "SSLMode"), so field errors match request bodies and UI form fields.
func NewValidator() *validator.Validate {
	v := validator.New()
	v.RegisterTagNameFunc(func(f reflect.StructField) string {
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" {
			return ""
		}
		if name == "" {
			return f.Name
		}
		return name
	})
	return v
}
