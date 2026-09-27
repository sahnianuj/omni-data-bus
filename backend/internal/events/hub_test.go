package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fakeDocs is a progress store; reads can be made to fail.
type fakeDocs struct {
	mu    sync.Mutex
	docs  map[string]json.RawMessage
	revs  map[string]uint64
	fail  int
	reads int
}

func newFakeDocs() *fakeDocs {
	return &fakeDocs{docs: map[string]json.RawMessage{}, revs: map[string]uint64{}}
}

func (f *fakeDocs) put(job, status string, seq int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.docs[job] = json.RawMessage(fmt.Sprintf(`{"jobId":%q,"status":%q,"seq":%d}`, job, status, seq))
	f.revs[job]++
}

func (f *fakeDocs) GetProgress(_ context.Context, job string) (json.RawMessage, uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	if f.fail > 0 {
		f.fail--
		return nil, 0, errors.New("kv timeout")
	}
	d, ok := f.docs[job]
	if !ok {
		return nil, 0, errors.New("not found")
	}
	return d, f.revs[job], nil
}

func (f *fakeDocs) readCount() int { f.mu.Lock(); defer f.mu.Unlock(); return f.reads }

func next(t *testing.T, s *Subscription) []Event {
	t.Helper()
	select {
	case <-s.Notify():
		return s.Drain()
	case <-time.After(2 * time.Second):
		t.Fatal("no event within 2 s")
		return nil
	}
}

func seqOf(e Event) int64 {
	var h progressHead
	_ = json.Unmarshal(e.Data, &h)
	return h.Seq
}

func TestHub_AC5_FanOutByJob(t *testing.T) {
	docs := newFakeDocs()
	docs.put("a", "running", 1)
	docs.put("b", "running", 1)
	h := NewHub(docs, 5*time.Millisecond)
	a1, a2, b := h.Subscribe("a"), h.Subscribe("a"), h.Subscribe("b")
	defer a1.Close()
	defer a2.Close()
	defer b.Close()
	require.Equal(t, 2, h.Pollers(), "one poller per job, shared by its subscribers")
	for _, s := range []*Subscription{a1, a2, b} {
		evs := next(t, s)
		require.Equal(t, TypeProgress, evs[len(evs)-1].Type)
	}
	docs.put("a", "running", 2)
	for _, s := range []*Subscription{a1, a2} {
		evs := next(t, s)
		require.EqualValues(t, 2, seqOf(evs[len(evs)-1]))
	}
	select {
	case <-b.Notify():
		t.Fatal("b's subscribers don't get a's events")
	case <-time.After(30 * time.Millisecond):
	}
	// Status change, then completion.
	docs.put("a", "completed", 3)
	evs := next(t, a1)
	var types []string
	for _, e := range evs {
		types = append(types, e.Type)
	}
	require.Equal(t, []string{TypeStatus, TypeProgress, TypeCompleted}, types)
}

func TestHub_AC5_PollerRetriesAndStops(t *testing.T) {
	docs := newFakeDocs()
	docs.put("a", "running", 1)
	docs.fail = 3
	h := NewHub(docs, 5*time.Millisecond)
	s := h.Subscribe("a")
	evs := next(t, s)
	require.Equal(t, TypeProgress, evs[0].Type, "kept polling through KV errors")
	s.Close()
	require.Zero(t, h.Pollers(), "stops with the last subscriber")
	n := docs.readCount()
	time.Sleep(30 * time.Millisecond)
	require.LessOrEqual(t, docs.readCount(), n+1)
}

func TestHub_AC5_SlowSubscriberKeepsLatestOnly(t *testing.T) {
	docs := newFakeDocs()
	docs.put("a", "running", 1)
	h := NewHub(docs, 2*time.Millisecond)
	s := h.Subscribe("a")
	defer s.Close()
	for seq := int64(2); seq <= 20; seq++ {
		docs.put("a", "running", seq)
		time.Sleep(3 * time.Millisecond) // the subscriber reads nothing meanwhile
	}
	time.Sleep(10 * time.Millisecond)
	evs := s.Drain()
	var progress []Event
	for _, e := range evs {
		if e.Type == TypeProgress {
			progress = append(progress, e)
		}
	}
	require.Len(t, progress, 1, "only the latest progress is kept")
	require.EqualValues(t, 20, seqOf(progress[0]))
}
