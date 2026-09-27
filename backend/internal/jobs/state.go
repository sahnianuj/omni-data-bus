// Package jobs runs migrations: options, pre-flight checks, the job state machine, the
// worker claim loop with heartbeats and a reaper, and the jobs API (OC-023, OC-026).
package jobs

import (
	"errors"
	"net/http"

	"omnidatabus/internal/problem"
	"omnidatabus/internal/store"
)

// Event moves a job between states (architecture §6.3).
type Event string

// Events.
const (
	EvClaim              Event = "claim"                // a worker takes a queued job
	EvPause              Event = "pause"                // the user asks to pause
	EvDrained            Event = "drained"              // the worker finished draining after a pause
	EvResume             Event = "resume"               // the user resumes a paused (or resumable failed) job
	EvCancel             Event = "cancel"               // the user cancels
	EvStopped            Event = "stopped"              // the worker stopped after a cancel
	EvComplete           Event = "complete"             // every row processed, none failed
	EvCompleteWithErrors Event = "complete_with_errors" // every row processed, some failed
	EvFail               Event = "fail"                 // fatal error or error threshold
	EvRequeue            Event = "requeue"              // graceful worker shutdown hands the job back
	EvReap               Event = "reap"                 // the reaper found a stale heartbeat
)

// Events lists every event (for exhaustive tests).
var Events = []Event{EvClaim, EvPause, EvDrained, EvResume, EvCancel, EvStopped, EvComplete, EvCompleteWithErrors, EvFail, EvRequeue, EvReap}

// States lists every job status.
var States = []string{
	store.JobQueued, store.JobRunning, store.JobPausing, store.JobPaused, store.JobCancelling,
	store.JobCancelled, store.JobCompleted, store.JobCompletedWithErrors, store.JobFailed,
}

// ErrInvalidTransition is returned for an event that isn't allowed in the current state.
var ErrInvalidTransition = errors.New("invalid job state transition")

var transitions = map[string]map[Event]string{
	store.JobQueued: {
		EvClaim:  store.JobRunning,
		EvCancel: store.JobCancelled,
	},
	store.JobRunning: {
		EvPause:              store.JobPausing,
		EvCancel:             store.JobCancelling,
		EvComplete:           store.JobCompleted,
		EvCompleteWithErrors: store.JobCompletedWithErrors,
		EvFail:               store.JobFailed,
		EvRequeue:            store.JobQueued,
		EvReap:               store.JobQueued,
	},
	store.JobPausing: {
		EvDrained: store.JobPaused,
		EvCancel:  store.JobCancelling,
		EvFail:    store.JobFailed,
		// Finishing while draining still counts: the last batches were the whole table.
		EvComplete:           store.JobCompleted,
		EvCompleteWithErrors: store.JobCompletedWithErrors,
		EvReap:               store.JobPaused,
	},
	store.JobPaused: {
		EvResume: store.JobQueued,
		EvCancel: store.JobCancelled,
	},
	store.JobCancelling: {
		EvStopped: store.JobCancelled,
		EvFail:    store.JobCancelled,
		EvReap:    store.JobCancelled,
	},
	store.JobFailed: {
		// Only when the failure is resumable (checked by the service, not the table).
		EvResume: store.JobQueued,
	},
}

// Transition returns the state after ev in state from, or ErrInvalidTransition.
func Transition(from string, ev Event) (string, error) {
	if to, ok := transitions[from][ev]; ok {
		return to, nil
	}
	return "", ErrInvalidTransition
}

// Active reports whether status holds the project's active-job slot.
func Active(status string) bool { return !store.JobTerminal(status) }

func invalidTransition(from string, ev Event) problem.Problem {
	return problem.New(http.StatusConflict, problem.CodeJobInvalidTransition, "Not possible right now",
		"A "+string(from)+" job can't "+string(ev)+".")
}
