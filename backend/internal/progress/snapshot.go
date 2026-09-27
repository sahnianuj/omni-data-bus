// Package progress tracks a job's counters, throughput and ETA, and defines the progress
// snapshot every API node streams to browsers (OC-027).
package progress

import (
	"encoding/json"
	"time"
)

// Phases of one mapping.
const (
	PhaseQueued    = "queued"
	PhasePreparing = "preparing" // snapshot, partition plan, count
	PhaseMigrating = "migrating"
	PhaseVerifying = "verifying"
	PhaseDone      = "done"
	PhaseFailed    = "failed"
)

// Total kinds.
const (
	TotalEstimate = "estimate"
	TotalExact    = "exact"
)

// MappingProgress is one table's line in the snapshot.
type MappingProgress struct {
	MappingID string  `json:"mappingId"`
	Table     string  `json:"table"`
	Target    string  `json:"target"`
	Phase     string  `json:"phase"`
	Processed int64   `json:"processed"`
	Total     int64   `json:"total"`
	TotalKind string  `json:"totalKind"`
	Percent   float64 `json:"percent"`
	Written   int64   `json:"written"`
	Skipped   int64   `json:"skipped"`
	Failed    int64   `json:"failed"`
}

// CodeCount is one entry of errorsByCode.
type CodeCount struct {
	Code  string `json:"code"`
	Count int64  `json:"count"`
}

// Snapshot is the progress document (job_progress::<jobId>) and the SSE `progress` payload.
type Snapshot struct {
	JobID          string            `json:"jobId"`
	Status         string            `json:"status"`
	Seq            int64             `json:"seq"`
	TS             time.Time         `json:"ts"`
	Total          int64             `json:"total"`
	TotalKind      string            `json:"totalKind"`
	Processed      int64             `json:"processed"`
	Percent        float64           `json:"percent"`
	Written        int64             `json:"written"`
	Skipped        int64             `json:"skipped"`
	Failed         int64             `json:"failed"`
	RowsRead       int64             `json:"rowsRead"`
	BytesWritten   int64             `json:"bytesWritten"`
	Throughput     float64           `json:"throughput"`
	PeakThroughput float64           `json:"peakThroughput"`
	EtaSeconds     *int64            `json:"etaSeconds"`
	ElapsedSeconds int64             `json:"elapsedSeconds"`
	Mappings       []MappingProgress `json:"mappings"`
	ErrorsByCode   []CodeCount       `json:"errorsByCode"`
	// ErrorSamples are the newest row errors since the last snapshot (at most 5), for the live feed.
	ErrorSamples []ErrorSample `json:"errorSamples,omitempty"`
}

// ErrorSample is one row error shown in the live error feed.
type ErrorSample struct {
	At        time.Time       `json:"at"`
	MappingID string          `json:"mappingId"`
	Table     string          `json:"table"`
	Stage     string          `json:"stage"`
	Code      string          `json:"code"`
	Message   string          `json:"message"`
	FieldPath string          `json:"fieldPath,omitempty"`
	SourceKey json.RawMessage `json:"sourceKey,omitempty"`
}

// Parse decodes a stored snapshot; a missing one is an empty snapshot for jobID.
func Parse(jobID string, raw json.RawMessage) Snapshot {
	s := Snapshot{JobID: jobID, Mappings: []MappingProgress{}, ErrorsByCode: []CodeCount{}}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &s)
	}
	if s.Mappings == nil {
		s.Mappings = []MappingProgress{}
	}
	if s.ErrorsByCode == nil {
		s.ErrorsByCode = []CodeCount{}
	}
	return s
}

// WithStatus returns raw with its status replaced and seq advanced, so subscribers see a
// status change made outside the worker (pause requested, cancelled while queued, …).
func WithStatus(jobID string, raw json.RawMessage, status string, now time.Time) json.RawMessage {
	s := Parse(jobID, raw)
	s.Status, s.Seq, s.TS = status, s.Seq+1, now
	s.ErrorSamples = nil
	out, _ := json.Marshal(s)
	return out
}
