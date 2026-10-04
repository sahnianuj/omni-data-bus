package headless

import (
	"encoding/json"
	"io"
	"sync"
	"time"

	"omnidatabus/internal/progress"
)

// Event kinds on stdout (architecture §6.4).
const (
	EventStart       = "start"
	EventProgress    = "progress"
	EventMappingDone = "mapping_done"
	EventWarning     = "warning"
	EventDone        = "done"
)

// Event is one stdout line. Every event has ts, event and jobId; counts are the run's totals,
// or the mapping's for mapping_done.
type Event struct {
	TS         time.Time `json:"ts"`
	Event      string    `json:"event"`
	JobID      string    `json:"jobId"`
	Mapping    string    `json:"mapping,omitempty"`
	Read       int64     `json:"read"`
	Written    int64     `json:"written"`
	Skipped    int64     `json:"skipped"`
	Failed     int64     `json:"failed"`
	DocsPerSec float64   `json:"docsPerSec"`
	EtaSec     *int64    `json:"etaSec"`
	// start
	Version      string   `json:"version,omitempty"`
	Commit       string   `json:"commit,omitempty"`
	BundleSHA256 string   `json:"bundleSha256,omitempty"`
	Mappings     []string `json:"mappings,omitempty"`
	Resumed      bool     `json:"resumed,omitempty"`
	// warning
	Message string `json:"message,omitempty"`
	// done
	Status   string `json:"status,omitempty"`
	ExitCode *int   `json:"exitCode,omitempty"`
}

// ProgressSink writes events as JSON lines: only events go to stdout, so `| jq` works.
type ProgressSink struct {
	mu       sync.Mutex
	w        io.Writer
	jobID    string
	now      func() time.Time
	quiet    bool          // only the done event
	interval time.Duration // 0 = no periodic progress
	last     time.Time
	done     map[string]bool // mappings already reported done
	names    map[string]string
}

// NewProgressSink returns a sink for one run; names maps mapping IDs to bundle names.
func NewProgressSink(w io.Writer, jobID string, interval time.Duration, quiet bool, names map[string]string, now func() time.Time) *ProgressSink {
	return &ProgressSink{w: w, jobID: jobID, now: now, quiet: quiet, interval: interval, done: map[string]bool{}, names: names}
}

// Emit writes one event (dropped in quiet mode unless it's done).
func (p *ProgressSink) Emit(e Event) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.quiet && e.Event != EventDone {
		return
	}
	if e.TS.IsZero() {
		e.TS = p.now()
	}
	e.JobID = p.jobID
	raw, err := json.Marshal(e)
	if err != nil {
		return
	}
	_, _ = p.w.Write(append(raw, '\n'))
}

// Snapshot reacts to a pipeline progress document: mapping_done for each newly finished
// mapping, and a progress event when the interval has passed.
func (p *ProgressSink) Snapshot(s progress.Snapshot) {
	p.MappingsDone(s)
	p.mu.Lock()
	periodic := p.interval > 0 && p.now().Sub(p.last) >= p.interval
	if periodic {
		p.last = p.now()
	}
	p.mu.Unlock()
	if periodic {
		p.Emit(Totals(EventProgress, s))
	}
}

// MappingsDone emits mapping_done once for each mapping s shows finished; the run calls it with
// the final snapshot too, since the last ones can finish between two progress documents.
func (p *ProgressSink) MappingsDone(s progress.Snapshot) {
	p.mu.Lock()
	var finished []progress.MappingProgress
	for _, m := range s.Mappings {
		if m.Phase == progress.PhaseDone && !p.done[m.MappingID] {
			p.done[m.MappingID] = true
			finished = append(finished, m)
		}
	}
	p.mu.Unlock()
	for _, m := range finished {
		p.Emit(Event{Event: EventMappingDone, Mapping: p.names[m.MappingID], Read: m.Processed, Written: m.Written, Skipped: m.Skipped, Failed: m.Failed})
	}
}

// Totals is an event with the snapshot's run totals.
func Totals(kind string, s progress.Snapshot) Event {
	return Event{Event: kind, Read: s.Processed, Written: s.Written, Skipped: s.Skipped, Failed: s.Failed, DocsPerSec: s.Throughput, EtaSec: s.EtaSeconds}
}
