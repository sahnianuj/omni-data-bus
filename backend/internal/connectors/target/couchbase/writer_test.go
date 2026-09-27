package couchbase

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/couchbase/gocb/v2"
	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors/target"
	"omnidatabus/internal/pipeline/flowctl"
)

// fakeKV is an in-memory collection with scripted failures.
type fakeKV struct {
	mu       sync.Mutex
	docs     map[string][]byte
	opts     []kvOptions
	fail     map[string][]error // key → errors returned by the next calls, in order
	delay    time.Duration
	inflight atomic.Int32
	maxSeen  atomic.Int32
	calls    atomic.Int32
}

func newFakeKV(existing ...string) *fakeKV {
	f := &fakeKV{docs: map[string][]byte{}, fail: map[string][]error{}}
	for _, k := range existing {
		f.docs[k] = []byte(`{"old":true}`)
	}
	return f
}

func (f *fakeKV) do(key string, _ []byte, o kvOptions, apply func() error) error {
	n := f.inflight.Add(1)
	defer f.inflight.Add(-1)
	for {
		m := f.maxSeen.Load()
		if n <= m || f.maxSeen.CompareAndSwap(m, n) {
			break
		}
	}
	f.calls.Add(1)
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.opts = append(f.opts, o)
	if errs := f.fail[key]; len(errs) > 0 {
		f.fail[key] = errs[1:]
		if errs[0] != nil {
			return errs[0]
		}
	}
	return apply()
}

func (f *fakeKV) Upsert(_ context.Context, k string, v []byte, o kvOptions) error {
	return f.do(k, v, o, func() error { f.docs[k] = v; return nil })
}

func (f *fakeKV) Insert(_ context.Context, k string, v []byte, o kvOptions) error {
	return f.do(k, v, o, func() error {
		if _, ok := f.docs[k]; ok {
			return fmt.Errorf("insert %s: %w", k, gocb.ErrDocumentExists)
		}
		f.docs[k] = v
		return nil
	})
}

func (f *fakeKV) Replace(_ context.Context, k string, v []byte, o kvOptions) error {
	return f.do(k, v, o, func() error {
		if _, ok := f.docs[k]; !ok {
			return fmt.Errorf("replace %s: %w", k, gocb.ErrDocumentNotFound)
		}
		f.docs[k] = v
		return nil
	})
}

func docs(keys ...string) []target.Doc {
	out := make([]target.Doc, len(keys))
	for i, k := range keys {
		out[i] = target.Doc{Key: k, Value: []byte(`{"k":"` + k + `"}`)}
	}
	return out
}

func outcomes(rs []target.WriteResult) []target.Outcome {
	out := make([]target.Outcome, len(rs))
	for i, r := range rs {
		out[i] = r.Outcome
	}
	return out
}

func testWriter(kv kvAPI, mode string) (*writer, *flowctl.FakeClock) {
	clock := flowctl.NewFakeClock()
	return newWriter(kv, target.WriteOptions{Mode: mode, Concurrency: 16}, 2500*time.Millisecond, clock), clock
}

func TestWriter_AC2_Upsert(t *testing.T) {
	kv := newFakeKV("a")
	w, _ := testWriter(kv, "upsert")
	rs := w.WriteBatch(context.Background(), docs("a", "b"))
	require.Equal(t, []target.Outcome{target.Written, target.Written}, outcomes(rs))
	require.JSONEq(t, `{"k":"a"}`, string(kv.docs["a"]), "existing key overwritten")
}

func TestWriter_AC2_InsertSkipsExisting(t *testing.T) {
	kv := newFakeKV("a")
	w, _ := testWriter(kv, "insert")
	rs := w.WriteBatch(context.Background(), docs("a", "b"))
	require.Equal(t, []target.Outcome{target.Skipped, target.Written}, outcomes(rs))
	require.JSONEq(t, `{"old":true}`, string(kv.docs["a"]))
}

func TestWriter_AC2_ReplaceSkipsMissing(t *testing.T) {
	kv := newFakeKV("a")
	w, _ := testWriter(kv, "replace")
	rs := w.WriteBatch(context.Background(), docs("a", "b"))
	require.Equal(t, []target.Outcome{target.Written, target.Skipped}, outcomes(rs))
	_, created := kv.docs["b"]
	require.False(t, created)
}

func TestWriter_AC3_RawTranscoderAndOptions(t *testing.T) {
	kv := newFakeKV()
	w := newWriter(kv, target.WriteOptions{Mode: "upsert", Durability: "majority", Expiry: time.Hour, Concurrency: 4}, 2500*time.Millisecond, nil)
	value := []byte(`{"n":1.10,"s":"x"}`) // not canonical JSON: must survive byte for byte
	w.WriteBatch(context.Background(), []target.Doc{{Key: "k", Value: value}})
	require.Equal(t, value, kv.docs["k"])
	require.Equal(t, kvOptions{Timeout: 2500 * time.Millisecond, Durability: gocb.DurabilityLevelMajority, Expiry: time.Hour}, kv.opts[0])
	require.Equal(t, gocb.DurabilityLevelPersistToMajority, durabilityLevel("persistToMajority"))
	require.Equal(t, gocb.DurabilityLevelNone, durabilityLevel("none"))
}

func TestWriter_AC4_RetryThenSuccess(t *testing.T) {
	kv := newFakeKV()
	kv.fail["a"] = []error{gocb.ErrTemporaryFailure, gocb.ErrTemporaryFailure}
	w, clock := testWriter(kv, "upsert")
	rs := w.WriteBatch(context.Background(), docs("a"))
	require.Equal(t, target.Written, rs[0].Outcome)
	require.Equal(t, 3, rs[0].Attempts)
	require.Len(t, clock.Slept, 2)
	require.LessOrEqual(t, clock.Slept[0], 50*time.Millisecond)
	require.LessOrEqual(t, clock.Slept[1], 100*time.Millisecond)
}

func TestWriter_AC4_RetryExhausted(t *testing.T) {
	kv := newFakeKV()
	kv.fail["a"] = []error{gocb.ErrTemporaryFailure, gocb.ErrTemporaryFailure, gocb.ErrTemporaryFailure, gocb.ErrTemporaryFailure, gocb.ErrTemporaryFailure, gocb.ErrTemporaryFailure}
	w, _ := testWriter(kv, "upsert")
	rs := w.WriteBatch(context.Background(), docs("a"))
	require.Equal(t, target.Failed, rs[0].Outcome)
	require.Equal(t, target.CodeTemporaryExhausted, rs[0].Code)
	require.Equal(t, 6, rs[0].Attempts)
}

func TestWriter_AC4_InsertAmbiguousThenExists(t *testing.T) {
	kv := newFakeKV()
	// The first attempt times out ambiguously but lands; the retry then sees "exists".
	kv.fail["a"] = []error{fmt.Errorf("ambiguous: %w", gocb.ErrAmbiguousTimeout)}
	kv.docs["a"] = []byte(`{"k":"a"}`)
	w, _ := testWriter(kv, "insert")
	rs := w.WriteBatch(context.Background(), docs("a"))
	require.Equal(t, target.Written, rs[0].Outcome)
	require.Equal(t, 2, rs[0].Attempts)
}

func TestWriter_AC5_PerDocFailuresContinue(t *testing.T) {
	kv := newFakeKV()
	kv.fail["big"] = []error{gocb.ErrValueTooLarge}
	w, _ := testWriter(kv, "upsert")
	rs := w.WriteBatch(context.Background(), docs("a", "big", "c"))
	require.Equal(t, []target.Outcome{target.Written, target.Failed, target.Written}, outcomes(rs))
	require.Equal(t, target.CodeDocTooLarge, rs[1].Code)
	require.False(t, rs[1].Fatal)
}

func TestClassifyWriteError_AC5_Table(t *testing.T) {
	cases := []struct {
		err   error
		code  string
		fatal bool
	}{
		{gocb.ErrValueTooLarge, target.CodeDocTooLarge, false},
		{gocb.ErrInvalidArgument, target.CodeKeyInvalid, false},
		{gocb.ErrCollectionNotFound, target.CodeKeyspaceMissing, true},
		{gocb.ErrScopeNotFound, target.CodeKeyspaceMissing, true},
		{gocb.ErrAuthenticationFailure, target.CodePermissionDenied, true},
		{errors.New("no access to collection"), target.CodePermissionDenied, true},
		{gocb.ErrDurabilityImpossible, target.CodeDurabilityImpossible, true},
		{errors.New("mystery"), target.CodeWriteFailed, false},
	}
	for _, c := range cases {
		code, fatal := ClassifyWriteError(fmt.Errorf("op: %w", c.err))
		require.Equal(t, c.code, code, c.err.Error())
		require.Equal(t, c.fatal, fatal, c.err.Error())
	}
	require.Equal(t, kindBusy, classifyWrite(gocb.ErrOverload))
	require.Equal(t, kindRetry, classifyWrite(gocb.ErrDurableWriteInProgress))
	outdated := &gocb.TimeoutError{InnerError: gocb.ErrAmbiguousTimeout, RetryReasons: []gocb.RetryReason{gocb.KVCollectionOutdatedRetryReason}}
	require.Equal(t, kindFinal, classifyWrite(outdated), "a missing collection times out with this retry reason")
	code, fatal := ClassifyWriteError(outdated)
	require.Equal(t, target.CodeKeyspaceMissing, code)
	require.True(t, fatal)
}

func TestWriter_AC6_InflightNeverExceedsLimit(t *testing.T) {
	kv := newFakeKV()
	kv.delay = 200 * time.Microsecond
	w := newWriter(kv, target.WriteOptions{Mode: "upsert", Concurrency: 64}, time.Second, nil)
	keys := make([]string, 2000)
	for i := range keys {
		keys[i] = fmt.Sprintf("k%d", i)
	}
	var wg sync.WaitGroup
	for range 4 { // several batches at once share the same limit
		wg.Add(1)
		go func() { defer wg.Done(); w.WriteBatch(context.Background(), docs(keys...)) }()
	}
	wg.Wait()
	require.LessOrEqual(t, kv.maxSeen.Load(), int32(64))
	require.Greater(t, kv.maxSeen.Load(), int32(8), "it does run in parallel")
	require.Len(t, kv.docs, 2000)
}
