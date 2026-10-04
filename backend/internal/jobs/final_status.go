package jobs

import "omnidatabus/internal/jobs/jobspec"

// Stop reasons stored in job.reason.
const (
	ReasonErrorThreshold = jobspec.ReasonErrorThreshold
	ReasonCancelled      = jobspec.ReasonCancelled
	ReasonNotResumable   = jobspec.ReasonNotResumable
)

// The run contract lives in jobspec, which the pipeline imports without this package (OC-051).
type (
	// Fatal is an error that stops the whole job at once.
	Fatal = jobspec.Fatal
	// Outcome is what a run reports when it returns.
	Outcome = jobspec.Outcome
	// Execution is a running pipeline.
	Execution = jobspec.Execution
	// Control tells a run to stop.
	Control = jobspec.Control
	// Runner starts pipelines for claimed jobs.
	Runner = jobspec.Runner
)

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
