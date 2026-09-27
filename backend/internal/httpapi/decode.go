package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"

	"omnidatabus/internal/problem"
)

var validate = problem.NewValidator()

// DecodeJSON strictly decodes (unknown fields rejected) and validates a request body, up to the
// route's body limit (1 MiB, or 6 MiB for large routes; see BodyLimit).
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	return DecodeJSONMax(w, r, dst, bodyLimit(r.Context()))
}

// DecodeJSONMax is DecodeJSON with an explicit limit; a larger body is 413 PAYLOAD_TOO_LARGE.
func DecodeJSONMax(w http.ResponseWriter, r *http.Request, dst any, limit int64) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		if l, ok := IsTooLarge(err); ok {
			return tooLarge(l)
		}
		if errors.Is(err, io.EOF) {
			return problem.New(http.StatusBadRequest, problem.CodeBadRequest, "Invalid request", "A JSON body is required.")
		}
		return problem.New(http.StatusBadRequest, problem.CodeBadRequest, "Invalid request", "The request body isn't valid JSON: "+err.Error())
	}
	return validate.Struct(dst)
}

// URLParam returns a decoded path parameter. chi matches against the raw (escaped) path when a
// client percent-encodes characters such as ':' (e.g. "p1%3A%3Asource"), so handlers must decode.
func URLParam(r *http.Request, name string) string {
	v := chi.URLParam(r, name)
	if u, err := url.PathUnescape(v); err == nil {
		return u
	}
	return v
}
