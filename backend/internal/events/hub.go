// Package events streams job progress to browsers over Server-Sent Events (OC-027). Workers
// write each job's progress document; every API node's Hub polls the documents its
// subscribers watch and fans them out, so a browser can be served by any node.
package events

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"omnidatabus/internal/observability"
	"omnidatabus/internal/store"
)

// Event types.
const (
	TypeProgress    = "progress"
	TypeStatus      = "status"
	TypeErrorSample = "error-sample"
	TypeCompleted   = "completed"
)

// Event is one SSE message.
type Event struct {
	ID   int64
	Type string
	Data json.RawMessage
}

// ProgressReader reads a job's progress document (store.Jobs implements it).
type ProgressReader interface {
	GetProgress(ctx context.Context, jobID string) (json.RawMessage, uint64, error)
}

// Subscription receives a job's events: the latest progress (a slow subscriber skips
// intermediate ones) plus a small queue of status, error-sample and completed events.
type Subscription struct {
	jobID  string
	notify chan struct{}
	mu     sync.Mutex
	latest *Event
	queue  []Event
	hub    *Hub
}

// Notify is signalled when there is something to read.
func (s *Subscription) Notify() <-chan struct{} { return s.notify }

// Drain returns queued events, then the latest progress (if any), then "completed" (which
// ends the stream, so it always comes last), and clears them.
func (s *Subscription) Drain() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out, done []Event
	for _, e := range s.queue {
		if e.Type == TypeCompleted {
			done = append(done, e)
		} else {
			out = append(out, e)
		}
	}
	s.queue = nil
	if s.latest != nil {
		out = append(out, *s.latest)
		s.latest = nil
	}
	return append(out, done...)
}

func (s *Subscription) push(e Event) {
	s.mu.Lock()
	if e.Type == TypeProgress {
		s.latest = &e
	} else {
		if len(s.queue) >= 16 {
			s.queue = s.queue[1:]
		}
		s.queue = append(s.queue, e)
	}
	s.mu.Unlock()
	select {
	case s.notify <- struct{}{}:
	default:
	}
}

// Close unsubscribes; the job's poller stops with its last subscriber.
func (s *Subscription) Close() { s.hub.unsubscribe(s) }

type poller struct {
	subs   map[*Subscription]bool
	cancel context.CancelFunc
	// replay is the latest progress (and completed, once terminal), sent to subscribers who
	// join after the poller has read the document: it only pushes on a new revision.
	replay []Event
}

// Hub runs one poller per job that has subscribers on this node.
type Hub struct {
	reader   ProgressReader
	interval time.Duration
	mu       sync.Mutex
	jobs     map[string]*poller
}

// NewHub polls every interval (1 s in production).
func NewHub(r ProgressReader, interval time.Duration) *Hub {
	if interval <= 0 {
		interval = time.Second
	}
	return &Hub{reader: r, interval: interval, jobs: map[string]*poller{}}
}

// Subscribe starts receiving a job's events.
func (h *Hub) Subscribe(jobID string) *Subscription {
	return h.SubscribeWith(jobID, make(chan struct{}, 1))
}

// SubscribeWith is Subscribe with a notification channel shared by several subscriptions
// (one multiplexed stream watching many jobs). notify should have a buffer of 1.
func (h *Hub) SubscribeWith(jobID string, notify chan struct{}) *Subscription {
	s := &Subscription{jobID: jobID, notify: notify, hub: h}
	observability.SSESubscribers.Inc()
	h.mu.Lock()
	defer h.mu.Unlock()
	p := h.jobs[jobID]
	if p == nil {
		ctx, cancel := context.WithCancel(context.Background())
		p = &poller{subs: map[*Subscription]bool{}, cancel: cancel}
		h.jobs[jobID] = p
		go h.poll(ctx, jobID)
	}
	p.subs[s] = true
	for _, e := range p.replay {
		s.push(e)
	}
	return s
}

func (h *Hub) unsubscribe(s *Subscription) {
	h.mu.Lock()
	defer h.mu.Unlock()
	p := h.jobs[s.jobID]
	if p == nil || !p.subs[s] {
		return
	}
	observability.SSESubscribers.Dec()
	delete(p.subs, s)
	if len(p.subs) == 0 {
		p.cancel()
		delete(h.jobs, s.jobID)
	}
}

// Pollers reports how many jobs are being polled (for tests and metrics).
func (h *Hub) Pollers() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.jobs)
}

type progressHead struct {
	Status       string            `json:"status"`
	Seq          int64             `json:"seq"`
	ErrorSamples []json.RawMessage `json:"errorSamples"`
}

// poll reads the progress document every interval, skips unchanged revisions, keeps going
// through read errors, and fans new snapshots out.
func (h *Hub) poll(ctx context.Context, jobID string) {
	t := time.NewTicker(h.interval)
	defer t.Stop()
	var lastRev uint64
	lastStatus := ""
	for {
		raw, rev, err := h.reader.GetProgress(ctx, jobID)
		if err != nil && ctx.Err() == nil && !errors.Is(err, store.ErrNotFound) {
			observability.EventsPollErrors.Inc() // a job that hasn't started has no progress yet
		}
		if err == nil && rev != lastRev {
			lastRev = rev
			var head progressHead
			_ = json.Unmarshal(raw, &head)
			var evs []Event
			if head.Status != lastStatus && lastStatus != "" {
				d, _ := json.Marshal(map[string]string{"status": head.Status})
				evs = append(evs, Event{ID: head.Seq, Type: TypeStatus, Data: d})
			}
			lastStatus = head.Status
			for i, s := range head.ErrorSamples {
				if i >= 5 {
					break
				}
				evs = append(evs, Event{ID: head.Seq, Type: TypeErrorSample, Data: s})
			}
			evs = append(evs, Event{ID: head.Seq, Type: TypeProgress, Data: raw})
			if store.JobTerminal(head.Status) {
				evs = append(evs, Event{ID: head.Seq, Type: TypeCompleted, Data: raw})
			}
			replay := []Event{evs[len(evs)-1]}
			if store.JobTerminal(head.Status) {
				replay = evs[len(evs)-2:]
			}
			h.mu.Lock()
			if p := h.jobs[jobID]; p != nil {
				p.replay = replay
				for s := range p.subs {
					for _, e := range evs {
						s.push(e)
					}
				}
			}
			h.mu.Unlock()
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Format frames one SSE message: "id: <seq>\nevent: <type>\ndata: <json>\n\n". Every line of
// a multi-line payload gets its own "data:" prefix.
func Format(e Event) []byte {
	var b strings.Builder
	if e.ID > 0 {
		b.WriteString("id: ")
		b.WriteString(itoa(e.ID))
		b.WriteByte('\n')
	}
	if e.Type != "" {
		b.WriteString("event: ")
		b.WriteString(e.Type)
		b.WriteByte('\n')
	}
	data := strings.ReplaceAll(strings.ReplaceAll(string(e.Data), "\r\n", "\n"), "\r", "\n")
	for _, line := range strings.Split(data, "\n") {
		b.WriteString("data: ")
		b.WriteString(line)
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	return []byte(b.String())
}

func itoa(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}
