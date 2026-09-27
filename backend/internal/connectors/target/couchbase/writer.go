package couchbase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/couchbase/gocb/v2"

	"omnidatabus/internal/connectors"
	"omnidatabus/internal/connectors/target"
	"omnidatabus/internal/netguard"
	"omnidatabus/internal/pipeline/flowctl"
	"omnidatabus/internal/problem"
)

// kvOptions are what every write carries.
type kvOptions struct {
	Timeout    time.Duration
	Durability gocb.DurabilityLevel
	Expiry     time.Duration
}

// kvAPI is the collection subset the writer uses (faked in tests). Values are raw JSON bytes.
type kvAPI interface {
	Upsert(ctx context.Context, key string, value []byte, o kvOptions) error
	Insert(ctx context.Context, key string, value []byte, o kvOptions) error
	Replace(ctx context.Context, key string, value []byte, o kvOptions) error
}

type gocbKV struct{ c *gocb.Collection }

// raw passes the document bytes through untouched (no re-encoding, OC-025 AC3).
var raw = gocb.NewRawJSONTranscoder()

func (g gocbKV) Upsert(ctx context.Context, key string, v []byte, o kvOptions) error {
	_, err := g.c.Upsert(key, v, &gocb.UpsertOptions{Context: ctx, Transcoder: raw, Timeout: o.Timeout, DurabilityLevel: o.Durability, Expiry: o.Expiry})
	return err
}

func (g gocbKV) Insert(ctx context.Context, key string, v []byte, o kvOptions) error {
	_, err := g.c.Insert(key, v, &gocb.InsertOptions{Context: ctx, Transcoder: raw, Timeout: o.Timeout, DurabilityLevel: o.Durability, Expiry: o.Expiry})
	return err
}

func (g gocbKV) Replace(ctx context.Context, key string, v []byte, o kvOptions) error {
	_, err := g.c.Replace(key, v, &gocb.ReplaceOptions{Context: ctx, Transcoder: raw, Timeout: o.Timeout, DurabilityLevel: o.Durability, Expiry: o.Expiry})
	return err
}

// Get reads one document's bytes (ErrDocumentNotFound when absent).
func (g gocbKV) Get(ctx context.Context, key string) ([]byte, error) {
	res, err := g.c.Get(key, &gocb.GetOptions{Context: ctx, Transcoder: raw})
	if err != nil {
		return nil, err
	}
	var b []byte
	err = res.Content(&b)
	return b, err
}

// kvGetter reads documents back (the verifier).
type kvGetter interface {
	Get(ctx context.Context, key string) ([]byte, error)
}

// GetDocs implements target.DocReader, 16 reads at a time.
func (w *writer) GetDocs(ctx context.Context, keys []string) (map[string][]byte, error) {
	g, ok := w.kv.(kvGetter)
	if !ok {
		return nil, errors.New("this writer can't read documents")
	}
	out := make(map[string][]byte, len(keys))
	var mu sync.Mutex
	var first error
	sem := make(chan struct{}, 16)
	var wg sync.WaitGroup
	for _, k := range keys {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			b, err := g.Get(ctx, k)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				out[k] = b
			case errors.Is(err, gocb.ErrDocumentNotFound):
			case first == nil:
				first = err
			}
		}()
	}
	wg.Wait()
	return out, first
}

func durabilityLevel(s string) gocb.DurabilityLevel {
	switch s {
	case "majority":
		return gocb.DurabilityLevelMajority
	case "majorityAndPersistActive":
		return gocb.DurabilityLevelMajorityAndPersistOnMaster
	case "persistToMajority":
		return gocb.DurabilityLevelPersistToMajority
	}
	return gocb.DurabilityLevelNone
}

// writer writes documents with bounded, adaptive concurrency and retries (OC-025).
type writer struct {
	kv      kvAPI
	mode    string
	opts    kvOptions
	aimd    *flowctl.AIMD
	gate    *flowctl.Gate
	backoff flowctl.Backoff
	tries   int
	clock   flowctl.Clock
	close   func() error
}

func newWriter(kv kvAPI, o target.WriteOptions, kvTimeout time.Duration, clock flowctl.Clock) *writer {
	if clock == nil {
		clock = flowctl.Real
	}
	if o.Concurrency <= 0 {
		o.Concurrency = 64
	}
	a := flowctl.NewAIMD(o.Concurrency, clock)
	a.OnChange = o.OnConcurrency
	w := &writer{
		kv: kv, mode: o.Mode, aimd: a, clock: clock, tries: 6,
		opts:    kvOptions{Timeout: kvTimeout, Durability: durabilityLevel(o.Durability), Expiry: o.Expiry},
		backoff: flowctl.Backoff{Base: 50 * time.Millisecond, Factor: 2, Cap: 5 * time.Second},
	}
	w.gate = flowctl.NewGate(a.Limit)
	return w
}

// OpenWriter implements target.WriterOpener with a dedicated cluster connection for the job.
func (c *Connector) OpenWriter(ctx context.Context, rawCfg json.RawMessage, creds connectors.Credentials, o target.WriteOptions) (target.DocWriter, error) {
	cfg, err := ParseConfig(rawCfg)
	if err != nil {
		return nil, err
	}
	opts, _, err := buildClusterOptions(cfg, creds)
	if err != nil {
		return nil, err
	}
	guard := netguard.Default()
	if err := checkBootstrap(ctx, guard, cfg.ConnectionString); err != nil {
		return nil, classify(err)
	}
	cl, err := gocb.Connect(cfg.ConnectionString, opts)
	if err != nil {
		return nil, classify(err)
	}
	b := cl.Bucket(o.Bucket)
	if err := b.WaitUntilReady(time.Duration(cfg.ConnectTimeoutMs)*time.Millisecond, &gocb.WaitUntilReadyOptions{Context: ctx}); err != nil {
		_ = cl.Close(nil)
		return nil, classify(err)
	}
	nodes, err := diagnosticNodes(cl)
	if err == nil {
		err = verifyNodes(ctx, guard, nodes)
	}
	if err != nil {
		_ = cl.Close(nil)
		return nil, classify(err)
	}
	coll := b.Scope(o.Scope).Collection(o.Collection)
	// Probe the collection now: writing to a missing one would only time out, per document.
	_, err = coll.Get("__odb_probe__", &gocb.GetOptions{Context: ctx, Timeout: 5 * time.Second})
	if err != nil && !errors.Is(err, gocb.ErrDocumentNotFound) {
		_ = cl.Close(nil)
		if code, _ := ClassifyWriteError(err); code == target.CodeKeyspaceMissing {
			return nil, problem.New(http.StatusUnprocessableEntity, target.CodeKeyspaceMissing, "Target collection missing",
				fmt.Sprintf("%s.%s.%s doesn't exist.", o.Bucket, o.Scope, o.Collection))
		}
		return nil, classify(err)
	}
	w := newWriter(gocbKV{coll}, o, time.Duration(cfg.KVTimeoutMs)*time.Millisecond, nil)
	w.close = func() error { return cl.Close(nil) }
	return w, nil
}

func (w *writer) Close() error {
	if w.close != nil {
		return w.close()
	}
	return nil
}

// WriteBatch writes every document concurrently (bounded by the gate) and returns one result
// per document, in input order.
func (w *writer) WriteBatch(ctx context.Context, docs []target.Doc) []target.WriteResult {
	out := make([]target.WriteResult, len(docs))
	var wg sync.WaitGroup
	for i := range docs {
		if err := w.gate.Acquire(ctx); err != nil {
			for j := i; j < len(docs); j++ {
				out[j] = target.WriteResult{Key: docs[j].Key, Outcome: target.Failed, Code: target.CodeWriteFailed, Message: err.Error()}
			}
			break
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer w.gate.Release()
			out[i] = w.write(ctx, docs[i])
		}()
	}
	wg.Wait()
	return out
}

func (w *writer) op(ctx context.Context, d target.Doc) error {
	switch w.mode {
	case "insert":
		return w.kv.Insert(ctx, d.Key, d.Value, w.opts)
	case "replace":
		return w.kv.Replace(ctx, d.Key, d.Value, w.opts)
	}
	return w.kv.Upsert(ctx, d.Key, d.Value, w.opts)
}

// write retries transient failures with jittered backoff (up to 6 attempts).
func (w *writer) write(ctx context.Context, d target.Doc) target.WriteResult {
	res := target.WriteResult{Key: d.Key}
	ambiguous := false
	for attempt := 1; ; attempt++ {
		res.Attempts = attempt
		err := w.op(ctx, d)
		kind := classifyWrite(err)
		w.aimd.Observe(kind == kindBusy)
		switch {
		case err == nil:
			res.Outcome = target.Written
			return res
		case w.mode == "insert" && errors.Is(err, gocb.ErrDocumentExists):
			// After an ambiguous timeout, "exists" means our earlier attempt landed.
			if ambiguous {
				res.Outcome = target.Written
			} else {
				res.Outcome = target.Skipped
			}
			return res
		case w.mode == "replace" && errors.Is(err, gocb.ErrDocumentNotFound):
			res.Outcome = target.Skipped
			return res
		case kind == kindRetry || kind == kindBusy:
			ambiguous = ambiguous || errors.Is(err, gocb.ErrAmbiguousTimeout) || errors.Is(err, gocb.ErrTimeout)
			if attempt >= w.tries || ctx.Err() != nil {
				res.Outcome, res.Code, res.Message = target.Failed, target.CodeTemporaryExhausted, err.Error()
				return res
			}
			if w.clock.Sleep(ctx, w.backoff.Delay(attempt)) != nil {
				res.Outcome, res.Code, res.Message = target.Failed, target.CodeWriteFailed, ctx.Err().Error()
				return res
			}
		default:
			code, fatal := ClassifyWriteError(err)
			res.Outcome, res.Code, res.Message, res.Fatal = target.Failed, code, err.Error(), fatal
			return res
		}
	}
}

type writeKind int

const (
	kindOK writeKind = iota
	kindRetry
	kindBusy // temporary failure / overload: also slows AIMD down
	kindFinal
)

// collectionMissing: the SDK retries writes to an unknown collection until it times out, with
// "collection outdated" as the retry reason, instead of reporting it as not found.
func collectionMissing(err error) bool {
	if errors.Is(err, gocb.ErrCollectionNotFound) || errors.Is(err, gocb.ErrScopeNotFound) {
		return true
	}
	var te *gocb.TimeoutError
	if errors.As(err, &te) {
		for _, r := range te.RetryReasons {
			if r == gocb.KVCollectionOutdatedRetryReason {
				return true
			}
		}
	}
	return false
}

func classifyWrite(err error) writeKind {
	switch {
	case err == nil:
		return kindOK
	case collectionMissing(err):
		return kindFinal
	case errors.Is(err, gocb.ErrTemporaryFailure), errors.Is(err, gocb.ErrOverload):
		return kindBusy
	case errors.Is(err, gocb.ErrTimeout), errors.Is(err, gocb.ErrAmbiguousTimeout), errors.Is(err, gocb.ErrUnambiguousTimeout),
		errors.Is(err, gocb.ErrDurableWriteInProgress), errors.Is(err, gocb.ErrDurableWriteReCommitInProgress), errors.Is(err, gocb.ErrDurabilityAmbiguous):
		return kindRetry
	}
	return kindFinal
}

// ClassifyWriteError maps a non-retryable write error to its code, and whether it's fatal for
// the mapping or job (OC-025 AC5).
func ClassifyWriteError(err error) (code string, fatal bool) {
	switch {
	case errors.Is(err, gocb.ErrValueTooLarge):
		return target.CodeDocTooLarge, false
	case errors.Is(err, gocb.ErrInvalidArgument):
		return target.CodeKeyInvalid, false
	case collectionMissing(err), errors.Is(err, gocb.ErrBucketNotFound):
		return target.CodeKeyspaceMissing, true
	case errors.Is(err, gocb.ErrAuthenticationFailure) || forbidden(err) || noAccess(err):
		return target.CodePermissionDenied, true
	case errors.Is(err, gocb.ErrDurabilityImpossible), errors.Is(err, gocb.ErrDurabilityLevelNotAvailable):
		return target.CodeDurabilityImpossible, true
	}
	return target.CodeWriteFailed, false
}

func noAccess(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no access") || strings.Contains(msg, "access denied") || strings.Contains(msg, "not authorized")
}
