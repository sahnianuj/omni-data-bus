package jobs

import (
	"context"
	"encoding/json"

	"omnidatabus/internal/store"
)

// Stop reasons stored in job.reason.
const (
	ReasonErrorThreshold = "ERROR_THRESHOLD_EXCEEDED"
	ReasonCancelled      = "CANCELLED_BY_USER"
	// ReasonNotResumable marks a failure that a resume can't fix (the job has nothing to resume).
	ReasonNotResumable = "NOT_RESUMABLE"
)

// Fatal is an error that stops the whole job at once (auth, missing keyspace, dropped table,
// error threshold), regardless of the error policy (OC-026).
type Fatal struct {
	Code    string
	Message string
}

func (f *Fatal) Error() string { return f.Code + ": " + f.Message }

// Outcome is what a run reports when it returns.
type Outcome struct {
	// Finished is true when every mapping was read and written to the end.
	Finished bool
	Failed   int64
	Fatal    *Fatal
	// Checkpoint is where a resumed run continues (OC-029 builds on it).
	Checkpoint      json.RawMessage
	Progress        json.RawMessage
	ErrorsTruncated bool
}

// Execution is a running pipeline.
type Execution interface {
	// Checkpoint returns the current resumable position (persisted on every heartbeat).
	Checkpoint() json.RawMessage
	// Wait blocks until the run returns.
	Wait() Outcome
}

// Control tells a run to stop: Drain stops reading and lets in-flight batches finish and
// checkpoint (pause, graceful shutdown); Abort stops everything now (cancel, lost ownership).
type Control struct {
	Drain <-chan struct{}
	Abort <-chan struct{}
}

// Runner starts pipelines for claimed jobs.
type Runner interface {
	Start(ctx context.Context, j store.Job, ctl Control) Execution
}

// FinalEvent maps a finished run to the job's closing event (OC-026 AC4): no failures →
// complete, some failures under the thresholds → complete_with_errors, fatal → fail.
func FinalEvent(o Outcome) Event {
	switch {
	case o.Fatal != nil:
		return EvFail
	case o.Failed > 0:
		return EvCompleteWithErrors
	}
	return EvComplete
}
