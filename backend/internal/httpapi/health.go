package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"time"
)

// Pinger is a readiness dependency (e.g. the metadata bucket).
type Pinger interface {
	Ping(ctx context.Context) error
}

// PingFunc adapts a function to Pinger.
type PingFunc func(ctx context.Context) error

// Ping implements Pinger.
func (f PingFunc) Ping(ctx context.Context) error { return f(ctx) }

type readyBody struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks"`
}

// Healthz is liveness: 200 whenever the process is serving.
func Healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// Readyz returns 200 when every dependency answers within one second, else 503.
func Readyz(checks map[string]Pinger) http.HandlerFunc {
	names := make([]string, 0, len(checks))
	for n := range checks {
		names = append(names, n)
	}
	sort.Strings(names)
	return func(w http.ResponseWriter, r *http.Request) {
		body := readyBody{Status: "ok", Checks: map[string]string{}}
		status := http.StatusOK
		for _, n := range names {
			ctx, cancel := context.WithTimeout(r.Context(), time.Second)
			err := checks[n].Ping(ctx)
			cancel()
			if err != nil {
				body.Checks[n] = errorClass(err)
				body.Status = "unavailable"
				status = http.StatusServiceUnavailable
				continue
			}
			body.Checks[n] = "ok"
		}
		writeJSON(w, status, body)
	}
}

func errorClass(err error) string {
	if err == context.DeadlineExceeded {
		return "timeout"
	}
	return "unavailable"
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// WriteJSON is the shared JSON responder for feature handlers.
func WriteJSON(w http.ResponseWriter, status int, v any) { writeJSON(w, status, v) }
