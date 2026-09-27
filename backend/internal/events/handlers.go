package events

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"omnidatabus/internal/auth"
	"omnidatabus/internal/httpapi"
	"omnidatabus/internal/problem"
	"omnidatabus/internal/progress"
	"omnidatabus/internal/store"
)

// MaxMultiplexJobs caps GET /events?jobs=… (OC-027).
const MaxMultiplexJobs = 20

// Handlers serve the SSE endpoints.
type Handlers struct {
	Store     store.Store
	Hub       *Hub
	Heartbeat time.Duration // 15 s in production
}

// Routes implements httpapi.Registrar.
func (h Handlers) Routes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireAuth)
		r.Get("/jobs/{jid}/events", h.one)
		r.Get("/events", h.many)
	})
}

type stream struct {
	w  http.ResponseWriter
	rc *http.ResponseController
}

func open(w http.ResponseWriter) stream {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	s := stream{w: w, rc: http.NewResponseController(w)}
	_ = s.rc.Flush()
	return s
}

func (s stream) send(e Event) error {
	if _, err := s.w.Write(Format(e)); err != nil {
		return err
	}
	return s.rc.Flush()
}

func (s stream) ping() error {
	if _, err := s.w.Write([]byte(": ping\n\n")); err != nil {
		return err
	}
	return s.rc.Flush()
}

// current is the job's snapshot now: the progress document, with the job's status.
func (h Handlers) current(r *http.Request, j store.Job) Event {
	raw, _, err := h.Store.Jobs().GetProgress(r.Context(), j.ID)
	if err != nil {
		raw = j.Progress
	}
	snap := progress.Parse(j.ID, raw)
	if store.JobTerminal(j.Status) || snap.Status == "" {
		snap.Status = j.Status
	}
	data, _ := json.Marshal(snap)
	return Event{ID: snap.Seq, Type: TypeProgress, Data: data}
}

func (h Handlers) heartbeat() time.Duration {
	if h.Heartbeat > 0 {
		return h.Heartbeat
	}
	return 15 * time.Second
}

// one streams a job: the current snapshot first, then progress as it changes, a comment
// heartbeat every 15 s, and "completed" before closing when the job ends (OC-027 AC6).
// Progress is state, not a log, so a reconnect (Last-Event-ID) simply gets the latest.
func (h Handlers) one(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.PrincipalFrom(r.Context())
	j, err := h.Store.Jobs().Get(r.Context(), p.WorkspaceID, httpapi.URLParam(r, "jid"))
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	s := open(w)
	first := h.current(r, j)
	if err := s.send(first); err != nil {
		return
	}
	if store.JobTerminal(j.Status) {
		_ = s.send(Event{ID: first.ID, Type: TypeCompleted, Data: first.Data})
		return
	}
	last := first.ID
	sub := h.Hub.Subscribe(j.ID) //nolint:contextcheck // the job's poller is shared by all subscribers and outlives this request
	defer sub.Close()
	beat := time.NewTicker(h.heartbeat())
	defer beat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-beat.C:
			if s.ping() != nil {
				return
			}
		case <-sub.Notify():
			for _, e := range sub.Drain() {
				// Progress is state: skip what the client already has (the first poll repeats
				// the snapshot sent on connect).
				if e.Type == TypeProgress && e.ID <= last {
					continue
				}
				if s.send(e) != nil {
					return
				}
				if e.Type == TypeProgress {
					last = e.ID
				}
				if e.Type == TypeCompleted {
					return
				}
			}
		}
	}
}

// many multiplexes several jobs over one connection (the Dashboard and Jobs list): each
// event's data carries its jobId. It doesn't close when a job ends.
func (h Handlers) many(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.PrincipalFrom(r.Context())
	var ids []string
	for _, id := range strings.Split(r.URL.Query().Get("jobs"), ",") {
		if id = strings.TrimSpace(id); id != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 || len(ids) > MaxMultiplexJobs {
		problem.Write(w, r, problem.New(http.StatusBadRequest, problem.CodeBadRequest, "Invalid jobs", "Pass 1 to 20 job IDs in ?jobs=a,b,c."))
		return
	}
	var subs []*Subscription
	var first []Event
	notify := make(chan struct{}, 1)
	for _, id := range ids {
		j, err := h.Store.Jobs().Get(r.Context(), p.WorkspaceID, id)
		if err != nil {
			continue // not this workspace's job: silently skipped
		}
		first = append(first, h.current(r, j))
		subs = append(subs, h.Hub.SubscribeWith(id, notify)) //nolint:contextcheck // shared poller, see one
	}
	defer func() {
		for _, s := range subs {
			s.Close()
		}
	}()
	s := open(w)
	for _, e := range first {
		if s.send(e) != nil {
			return
		}
	}
	beat := time.NewTicker(h.heartbeat())
	defer beat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-beat.C:
			if s.ping() != nil {
				return
			}
		case <-notify:
			for _, sub := range subs {
				for _, e := range sub.Drain() {
					if s.send(e) != nil {
						return
					}
				}
			}
		}
	}
}
