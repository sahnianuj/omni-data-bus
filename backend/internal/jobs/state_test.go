package jobs

import (
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/store"
)

func TestTransition_AC1_FullMatrix(t *testing.T) {
	// Every allowed (state, event) pair; everything else must be ErrInvalidTransition.
	want := map[string]map[Event]string{
		store.JobQueued:  {EvClaim: store.JobRunning, EvCancel: store.JobCancelled},
		store.JobRunning: {EvPause: store.JobPausing, EvCancel: store.JobCancelling, EvComplete: store.JobCompleted, EvCompleteWithErrors: store.JobCompletedWithErrors, EvFail: store.JobFailed, EvRequeue: store.JobQueued, EvReap: store.JobQueued},
		store.JobPausing: {
			EvDrained: store.JobPaused, EvCancel: store.JobCancelling, EvFail: store.JobFailed, EvComplete: store.JobCompleted,
			EvCompleteWithErrors: store.JobCompletedWithErrors, EvReap: store.JobPaused,
		},
		store.JobPaused:     {EvResume: store.JobQueued, EvCancel: store.JobCancelled},
		store.JobCancelling: {EvStopped: store.JobCancelled, EvFail: store.JobCancelled, EvReap: store.JobCancelled},
		store.JobFailed:     {EvResume: store.JobQueued},
	}
	for _, s := range States {
		for _, ev := range Events {
			to, err := Transition(s, ev)
			if exp, ok := want[s][ev]; ok {
				require.NoErrorf(t, err, "%s + %s", s, ev)
				require.Equalf(t, exp, to, "%s + %s", s, ev)
			} else {
				require.ErrorIsf(t, err, ErrInvalidTransition, "%s + %s should be invalid", s, ev)
			}
		}
	}
	// The story's examples.
	_, err := Transition(store.JobCompleted, EvPause)
	require.ErrorIs(t, err, ErrInvalidTransition)
	to, _ := Transition(store.JobPaused, EvResume)
	require.Equal(t, store.JobQueued, to)
	to, _ = Transition(store.JobQueued, EvCancel)
	require.Equal(t, store.JobCancelled, to)
}

func TestFinalStatus_AC4_Table(t *testing.T) {
	cases := []struct {
		name string
		o    Outcome
		want string
	}{
		{"no failures", Outcome{Finished: true}, store.JobCompleted},
		{"7 failures under the thresholds", Outcome{Finished: true, Failed: 7}, store.JobCompletedWithErrors},
		{"fatal", Outcome{Failed: 1, Fatal: &Fatal{Code: ReasonErrorThreshold}}, store.JobFailed},
	}
	for _, c := range cases {
		to, err := Transition(store.JobRunning, FinalEvent(c.o))
		require.NoError(t, err, c.name)
		require.Equal(t, c.want, to, c.name)
	}
}
