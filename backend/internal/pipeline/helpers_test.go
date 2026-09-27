package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/jobs"
	"omnidatabus/internal/problem"
	"omnidatabus/internal/schema"
)

type problemer struct{ p problem.Problem }

func (e problemer) Error() string            { return e.p.Title }
func (e problemer) Problem() problem.Problem { return e.p }

func TestErrCodeAndText(t *testing.T) {
	p := problem.New(http.StatusBadGateway, "SOURCE_UNREACHABLE", "Unreachable", "The host didn't answer.")
	require.Equal(t, "SOURCE_UNREACHABLE", errCode(p, "X"))
	require.Equal(t, "The host didn't answer.", errText(p))
	pr := problemer{p}
	require.Equal(t, "SOURCE_UNREACHABLE", errCode(pr, "X"))
	require.Equal(t, "The host didn't answer.", errText(pr))
	plain := errors.New("plain")
	require.Equal(t, "X", errCode(plain, "X"))
	require.Equal(t, "plain", errText(plain))
	require.True(t, contains([]string{"a", "b"}, "b"))
	require.False(t, contains(nil, "b"))
}

func TestReadKey_PrimaryThenUniqueThenCursor(t *testing.T) {
	tb := &schema.Table{Columns: []schema.Column{{Name: "id"}, {Name: "email"}, {Name: "nick", Nullable: true}}}
	require.Nil(t, readKey(tb))
	tb.UniqueConstraints = [][]string{{"nick"}, {"email"}}
	require.Equal(t, []string{"email"}, readKey(tb), "a nullable unique key can't order a keyset")
	tb.PrimaryKey = []string{"id"}
	require.Equal(t, []string{"id"}, readKey(tb))
}

func TestKeyValues_MarshalEveryType(t *testing.T) {
	ks := []any{nil, int32(1), int16(2), []byte{1, 2}, float64(1.5), struct{ A int }{3}}
	raw, err := json.Marshal(wrapKey(ks))
	require.NoError(t, err)
	var back []keyValue
	require.NoError(t, json.Unmarshal(raw, &back))
	got := unwrapKey(back)
	require.Nil(t, got[0])
	require.Equal(t, int64(1), got[1])
	require.Equal(t, int64(2), got[2])
	require.Equal(t, []byte{1, 2}, got[3])
	require.Equal(t, 1.5, got[4])
	require.Equal(t, "{3}", got[5])
	var k keyValue
	require.Error(t, json.Unmarshal([]byte(`{"z":"1"}`), &k))
	require.Nil(t, wrapKey(nil))
	require.Nil(t, unwrapKey(nil))

	v, ok := asInt64(int16(4))
	require.True(t, ok)
	require.EqualValues(t, 4, v)
	_, ok = asInt64("x")
	require.False(t, ok)
	v, _ = asInt64(7)
	require.EqualValues(t, 7, v)

	require.Equal(t, []any{"01000000-0000-0000-0000-000000000000", `\x0a`, "2026-01-02T00:00:00Z", 5}, jsonValues([]any{[16]byte{1}, []byte{10}, time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), 5}))
	re := &ReadError{Partition: 2, Err: errors.New("down")}
	require.ErrorContains(t, re, "partition 2")
	require.Equal(t, "down", errors.Unwrap(re).Error())
	require.Equal(t, "1,234,567", thousands(1234567))
	require.Equal(t, "-1,000", thousands(-1000))
}

func TestExecution_CheckpointIsTheDurableOne(t *testing.T) {
	f := newRunFixture(t, peopleRows(1, 3000), 3000, opts(100))
	f.runner.d.PersistBatches = 3
	f.src.delay = time.Millisecond
	drain := make(chan struct{})
	exec := f.runner.Start(context.Background(), f.job, jobs.Control{Drain: drain, Abort: make(chan struct{})})
	var once sync.Once
	require.Eventually(t, func() bool { return len(exec.Checkpoint()) > 0 }, 5*time.Second, time.Millisecond, "persisted while running")
	cp, err := ParseCheckpoint(exec.Checkpoint())
	require.NoError(t, err)
	stored, _ := f.st.Jobs().GetAny(context.Background(), f.job.ID)
	stcp, _ := ParseCheckpoint(stored.Checkpoint)
	require.LessOrEqual(t, cp.Mappings[f.job.MappingIDs[0]].Processed(), stcp.Mappings[f.job.MappingIDs[0]].Processed()+300,
		"never ahead of what is stored")
	once.Do(func() { close(drain) })
	o := exec.Wait()
	require.False(t, o.Finished)
}
