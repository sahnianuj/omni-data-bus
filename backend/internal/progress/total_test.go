package progress

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTotals_AC3_AutoPolicy(t *testing.T) {
	require.True(t, CountExactly("auto", 1<<30), "1 GB is counted")
	require.False(t, CountExactly("auto", 50<<30), "50 GB stays an estimate")
	require.True(t, CountExactly("always", 50<<30))
	require.False(t, CountExactly("never", 1))
}

func TestWithStatus_AdvancesSeq(t *testing.T) {
	raw := WithStatus("j1", json.RawMessage(`{"jobId":"j1","status":"running","seq":7,"written":5,"errorSamples":[{"code":"X"}]}`), "pausing", time.Now())
	s := Parse("j1", raw)
	require.Equal(t, "pausing", s.Status)
	require.EqualValues(t, 8, s.Seq)
	require.EqualValues(t, 5, s.Written)
	require.Empty(t, s.ErrorSamples)
	require.Equal(t, "j1", Parse("j1", nil).JobID)
}
