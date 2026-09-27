package problem

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-playground/validator/v10"
	"github.com/stretchr/testify/require"
)

func decode(t *testing.T, rec *httptest.ResponseRecorder) Problem {
	t.Helper()
	require.Equal(t, ContentType, rec.Header().Get("Content-Type"))
	var p Problem
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	return p
}

type req struct {
	Source struct {
		Port int `validate:"min=1"`
	}
	Name string `validate:"required"`
}

func TestWrite_AC3_ValidationErrorTo422(t *testing.T) {
	err := validator.New().Struct(req{})
	rec := httptest.NewRecorder()
	Write(rec, httptest.NewRequest(http.MethodPost, "/", nil), err)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	p := decode(t, rec)
	require.Equal(t, CodeValidation, p.Code)
	require.ElementsMatch(t, []FieldError{
		{Field: "source.port", Message: "must be at least 1"},
		{Field: "name", Message: "is required"},
	}, p.FieldErrors)
}

type notFoundErr struct{}

func (notFoundErr) Error() string    { return "nope" }
func (notFoundErr) Problem() Problem { return New(404, CodeNotFound, "Not found", "No such project.") }

func TestWrite_AC3_DomainErrorUsesItsProblem(t *testing.T) {
	rec := httptest.NewRecorder()
	Write(rec, httptest.NewRequest(http.MethodGet, "/", nil), fmt.Errorf("wrapped: %w", notFoundErr{}))
	require.Equal(t, 404, rec.Code)
	require.Equal(t, CodeNotFound, decode(t, rec).Code)
}

func TestWrite_AC3_ProblemValueIsReturnedAsIs(t *testing.T) {
	rec := httptest.NewRecorder()
	Write(rec, nil, New(409, CodeConflict, "Conflict", "dup"))
	require.Equal(t, 409, rec.Code)
	require.Equal(t, "dup", decode(t, rec).Detail)
}

func TestWrite_AC3_UnknownErrorIsOpaque500(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Request-ID", "req-1")
	rec := httptest.NewRecorder()
	Write(rec, r, errors.New("pq: password authentication failed for user admin"))
	require.Equal(t, 500, rec.Code)
	p := decode(t, rec)
	require.Equal(t, CodeInternal, p.Code)
	require.Equal(t, "req-1", p.Instance)
	require.NotContains(t, rec.Body.String(), "password authentication")
}

func TestHasCode(t *testing.T) {
	p := New(401, CodeSessionExpired, "t", "d")
	require.True(t, HasCode(p, CodeSessionExpired))
	require.True(t, HasCode(fmt.Errorf("wrapped: %w", p), CodeSessionExpired))
	require.False(t, HasCode(p, CodeUnauthenticated))
	require.True(t, HasCode(notFoundErr{}, CodeNotFound))
	require.False(t, HasCode(nil, CodeNotFound))
	require.False(t, HasCode(errors.New("x"), CodeNotFound))
}

type jsonNamed struct {
	SSLMode string `json:"sslMode" validate:"oneof=a b"`
	Nested  struct {
		MaxTTL int `json:"maxTtl,omitempty" validate:"min=1"`
	} `json:"nested"`
	Plain string `validate:"required"`
}

func TestNewValidator_UsesJSONNames(t *testing.T) {
	err := NewValidator().Struct(jsonNamed{SSLMode: "c"})
	p := From(err)
	var fields []string
	for _, fe := range p.FieldErrors {
		fields = append(fields, fe.Field)
	}
	require.ElementsMatch(t, []string{"sslMode", "nested.maxTtl", "plain"}, fields)
}
