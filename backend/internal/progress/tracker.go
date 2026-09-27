package progress

import (
	"math"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// EWMA is an exponentially weighted moving average of a rate, sampled every tick. With a 1 s
// tick, alpha = 1 − 2^(−1/10) gives a ~10 s half-life (OC-027).
type EWMA struct {
	alpha float64
	value float64
	init  bool
}

// NewEWMA returns an EWMA with the given half-life in samples.
func NewEWMA(halfLifeSamples float64) *EWMA {
	return &EWMA{alpha: 1 - math.Pow(2, -1/halfLifeSamples)}
}

// Update adds a sample.
func (e *EWMA) Update(sample float64) {
	if !e.init {
		e.value, e.init = sample, true
		return
	}
	e.value += e.alpha * (sample - e.value)
}

// Value is the current average.
func (e *EWMA) Value() float64 { return e.value }

// ETA smooths the remaining-time estimate: it's unknown during warm-up, and between ticks it
// moves at most 20% unless the rate changed by more than 50%.
type ETA struct {
	last     *float64
	lastRate float64
}

// Next returns the ETA in seconds, or nil.
func (e *ETA) Next(remaining int64, rate float64, elapsed time.Duration) *int64 {
	if elapsed < 5*time.Second || rate <= 0 || remaining < 0 {
		e.last = nil
		return nil
	}
	raw := float64(remaining) / rate
	if e.last != nil && e.lastRate > 0 && math.Abs(rate-e.lastRate)/e.lastRate <= 0.5 {
		lo, hi := *e.last*0.8, *e.last*1.2
		raw = math.Min(math.Max(raw, lo), hi)
	}
	e.last, e.lastRate = &raw, rate
	n := int64(math.Round(raw))
	return &n
}

type mappingCounters struct {
	info                               MappingProgress
	written, skipped, failed, rowsRead atomic.Int64
	total                              atomic.Int64
	exact                              atomic.Bool
}

// Tracker counts a job's progress; the counters are safe for concurrent use and Snapshot is
// called once per tick by the publisher (OC-027).
type Tracker struct {
	jobID   string
	started time.Time
	now     func() time.Time
	// base holds counts from before a resume, so progress continues instead of restarting.
	baseElapsed time.Duration

	rowsRead, written, skipped, failed, bytes atomic.Int64

	mu       sync.Mutex
	mappings []*mappingCounters
	byID     map[string]*mappingCounters
	codes    map[string]int64
	samples  []ErrorSample
	ewma     *EWMA
	eta      ETA
	peak     float64
	lastTick time.Time
	lastProc int64
	seq      int64
	final    bool
}

// NewTracker starts tracking; mappings are in display order.
func NewTracker(jobID string, mappings []MappingProgress, now func() time.Time) *Tracker {
	if now == nil {
		now = time.Now
	}
	t := &Tracker{jobID: jobID, now: now, started: now(), byID: map[string]*mappingCounters{}, codes: map[string]int64{}, ewma: NewEWMA(10)}
	t.lastTick = t.started
	for _, m := range mappings {
		c := &mappingCounters{info: m}
		if c.info.Phase == "" {
			c.info.Phase = PhaseQueued
		}
		c.total.Store(m.Total)
		c.exact.Store(m.TotalKind == TotalExact)
		t.mappings = append(t.mappings, c)
		t.byID[m.MappingID] = c
	}
	return t
}

// Resume seeds the counters from a previous run of the same job.
func (t *Tracker) Resume(prev Snapshot) {
	t.bytes.Store(prev.BytesWritten)
	t.baseElapsed = time.Duration(prev.ElapsedSeconds) * time.Second
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, pm := range prev.Mappings {
		if c, ok := t.byID[pm.MappingID]; ok {
			c.written.Store(pm.Written)
			c.skipped.Store(pm.Skipped)
			c.failed.Store(pm.Failed)
			c.rowsRead.Store(pm.Written + pm.Skipped + pm.Failed)
			if pm.Phase == PhaseDone {
				c.info.Phase = PhaseDone
			}
		}
	}
	for _, cc := range prev.ErrorsByCode {
		t.codes[cc.Code] = cc.Count
	}
	// Job totals are the sum of the mappings, so ResumeMapping can adjust them consistently.
	var w, sk, f int64
	for _, c := range t.mappings {
		w, sk, f = w+c.written.Load(), sk+c.skipped.Load(), f+c.failed.Load()
	}
	t.written.Store(w)
	t.skipped.Store(sk)
	t.failed.Store(f)
	t.rowsRead.Store(w + sk + f)
	t.lastProc = w + sk + f
	t.peak = prev.PeakThroughput
}

// ResumeMapping sets one mapping's counts from the checkpoint and adjusts the job totals.
func (t *Tracker) ResumeMapping(id string, written, skipped, failed int64, done bool) {
	c := t.mapping(id)
	if c == nil {
		return
	}
	t.written.Add(written - c.written.Swap(written))
	t.skipped.Add(skipped - c.skipped.Swap(skipped))
	t.failed.Add(failed - c.failed.Swap(failed))
	t.rowsRead.Add(written + skipped + failed - c.rowsRead.Swap(written+skipped+failed))
	t.mu.Lock()
	t.lastProc = t.written.Load() + t.skipped.Load() + t.failed.Load()
	if done {
		c.info.Phase = PhaseDone
	}
	t.mu.Unlock()
}

// ResetMapping zeroes a mapping that restarts from the beginning (a cursor read).
func (t *Tracker) ResetMapping(id string) {
	c := t.mapping(id)
	if c == nil {
		return
	}
	t.written.Add(-c.written.Swap(0))
	t.skipped.Add(-c.skipped.Swap(0))
	t.failed.Add(-c.failed.Swap(0))
	t.rowsRead.Add(-c.rowsRead.Swap(0))
}

func (t *Tracker) mapping(id string) *mappingCounters {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.byID[id]
}

// SetPhase moves a mapping to phase.
func (t *Tracker) SetPhase(id, phase string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if c := t.byID[id]; c != nil {
		c.info.Phase = phase
	}
}

// SetTotal sets a mapping's total (the estimate, then the exact count when known).
func (t *Tracker) SetTotal(id string, total int64, exact bool) {
	if c := t.mapping(id); c != nil {
		c.total.Store(total)
		c.exact.Store(exact)
	}
}

// Read counts rows read from the source.
func (t *Tracker) Read(id string, n int) {
	t.rowsRead.Add(int64(n))
	if c := t.mapping(id); c != nil {
		c.rowsRead.Add(int64(n))
	}
}

// Acked counts a written batch.
func (t *Tracker) Acked(id string, written, skipped, failed, bytes int64) {
	t.written.Add(written)
	t.skipped.Add(skipped)
	t.failed.Add(failed)
	t.bytes.Add(bytes)
	if c := t.mapping(id); c != nil {
		c.written.Add(written)
		c.skipped.Add(skipped)
		c.failed.Add(failed)
	}
}

// Error counts a row error by code and keeps it as a live-feed sample.
func (t *Tracker) Error(s ErrorSample) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.codes[s.Code]++
	if c := t.byID[s.MappingID]; c != nil && s.Table == "" {
		s.Table = c.info.Table
	}
	t.samples = append(t.samples, s)
	if len(t.samples) > 20 {
		t.samples = t.samples[len(t.samples)-20:]
	}
}

// Totals returns processed and failed so far (the error policy reads them).
func (t *Tracker) Totals() (processed, failed int64) {
	f := t.failed.Load()
	return t.written.Load() + t.skipped.Load() + f, f
}

// MappingTotals returns one mapping's written, skipped and failed counts.
func (t *Tracker) MappingTotals(id string) (written, skipped, failed int64) {
	if c := t.mapping(id); c != nil {
		return c.written.Load(), c.skipped.Load(), c.failed.Load()
	}
	return 0, 0, 0
}

// Finish marks the job final: percent may reach 100.
func (t *Tracker) Finish() {
	t.mu.Lock()
	t.final = true
	t.mu.Unlock()
}

func percent(processed, total int64, final bool) float64 {
	if total <= 0 {
		if final {
			return 100
		}
		return 0
	}
	p := math.Round(float64(processed)/float64(total)*10000) / 100
	if !final && p > 99.9 {
		p = 99.9 // tails may still be flushing (OC-027 AC1)
	}
	return math.Min(p, 100)
}

// Snapshot builds the progress document at now (called once per tick).
func (t *Tracker) Snapshot(status string) Snapshot {
	now := t.now()
	t.mu.Lock()
	defer t.mu.Unlock()
	written, skipped, failed := t.written.Load(), t.skipped.Load(), t.failed.Load()
	processed := written + skipped + failed
	if dt := now.Sub(t.lastTick).Seconds(); dt >= 0.5 {
		rate := float64(processed-t.lastProc) / dt
		t.ewma.Update(rate)
		t.lastTick, t.lastProc = now, processed
		// The peak is the highest measured rate: the smoothed one lags on short runs.
		t.peak = math.Max(t.peak, rate)
	}
	t.seq++
	s := Snapshot{
		JobID: t.jobID, Status: status, Seq: t.seq, TS: now.UTC(), Written: written, Skipped: skipped, Failed: failed,
		Processed: processed, RowsRead: t.rowsRead.Load(), BytesWritten: t.bytes.Load(),
		Throughput: math.Round(t.ewma.Value()*10) / 10, PeakThroughput: math.Round(t.peak*10) / 10,
		ElapsedSeconds: int64((t.baseElapsed + now.Sub(t.started)).Seconds()), TotalKind: TotalExact,
		Mappings: make([]MappingProgress, 0, len(t.mappings)),
	}
	for _, c := range t.mappings {
		m := c.info
		m.Written, m.Skipped, m.Failed = c.written.Load(), c.skipped.Load(), c.failed.Load()
		m.Processed = m.Written + m.Skipped + m.Failed
		m.Total, m.TotalKind = c.total.Load(), TotalEstimate
		if c.exact.Load() {
			m.TotalKind = TotalExact
		}
		if m.Processed > m.Total { // a stale estimate: raise it, still an estimate
			m.Total, m.TotalKind = m.Processed, TotalEstimate
		}
		m.Percent = percent(m.Processed, m.Total, t.final || m.Phase == PhaseDone)
		if m.TotalKind == TotalEstimate {
			s.TotalKind = TotalEstimate
		}
		s.Total += m.Total
		s.Mappings = append(s.Mappings, m)
	}
	if s.Processed > s.Total {
		s.Total, s.TotalKind = s.Processed, TotalEstimate
	}
	s.Percent = percent(processed, s.Total, t.final)
	s.EtaSeconds = t.eta.Next(s.Total-processed, t.ewma.Value(), now.Sub(t.started))
	if t.final {
		s.EtaSeconds = nil
	}
	// Top 5 codes keep the payload small (≤ 6 KB, OC-027 AC4).
	for code, n := range t.codes {
		s.ErrorsByCode = append(s.ErrorsByCode, CodeCount{Code: code, Count: n})
	}
	sort.Slice(s.ErrorsByCode, func(i, j int) bool {
		if s.ErrorsByCode[i].Count != s.ErrorsByCode[j].Count {
			return s.ErrorsByCode[i].Count > s.ErrorsByCode[j].Count
		}
		return s.ErrorsByCode[i].Code < s.ErrorsByCode[j].Code
	})
	if len(s.ErrorsByCode) > 5 {
		s.ErrorsByCode = s.ErrorsByCode[:5]
	}
	if s.ErrorsByCode == nil {
		s.ErrorsByCode = []CodeCount{}
	}
	// New samples since the last snapshot, newest last, at most 5.
	if n := len(t.samples); n > 0 {
		s.ErrorSamples = append([]ErrorSample(nil), t.samples[max(0, n-5):]...)
		t.samples = t.samples[:0]
	}
	return s
}
