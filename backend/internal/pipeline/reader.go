package pipeline

import (
	"context"
	"errors"
	"fmt"
	"time"

	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/observability"
	"omnidatabus/internal/pipeline/flowctl"
)

// CodeSourceReadFailed fails a mapping whose reads keep failing (OC-024 AC7).
const CodeSourceReadFailed = "SOURCE_READ_FAILED"

// ErrDrained is returned by a reader that stopped because the job is draining (pause or
// shutdown); what it emitted so far is complete and checkpointable.
var ErrDrained = errors.New("reader drained")

// ReadConfig tunes the readers.
type ReadConfig struct {
	BatchSize  int
	Limiter    *flowctl.TokenBucket // nil = unthrottled
	Retry      flowctl.Backoff
	MaxRetries int // retries after the first failure (default 5)
	Clock      flowctl.Clock
	// OnRetry is told about each retried read (metrics and the job log).
	OnRetry func(partition, attempt int, err error)
}

func (c ReadConfig) withDefaults() ReadConfig {
	if c.BatchSize <= 0 {
		c.BatchSize = 1000
	}
	if c.MaxRetries == 0 {
		c.MaxRetries = 5
	}
	if c.Retry.Base == 0 {
		c.Retry = flowctl.Backoff{Base: 200 * time.Millisecond, Factor: 2, Cap: 10 * time.Second}
	}
	if c.Clock == nil {
		c.Clock = flowctl.Real
	}
	return c
}

// ReadError is a read that failed for good; the job fails with its code.
type ReadError struct {
	Partition int
	Err       error
}

func (e *ReadError) Error() string {
	return fmt.Sprintf("%s (partition %d): %v", CodeSourceReadFailed, e.Partition, e.Err)
}
func (e *ReadError) Unwrap() error { return e.Err }

func stopped(stop <-chan struct{}) bool {
	select {
	case <-stop:
		return true
	default:
		return false
	}
}

func keyOf(row []any, keyIdx []int) []any {
	k := make([]any, len(keyIdx))
	for i, j := range keyIdx {
		k[i] = row[j]
	}
	return k
}

// ReadPartition streams one partition as batches with Seq startSeq+1, +2, … and LastKey set to
// the last row's key, continuing after `after` (nil = from the partition's start). Transient
// errors retry the same keyset query (same arguments), so nothing is skipped or repeated. It
// returns nil at the end, ErrDrained when stop closes, or a *ReadError.
func ReadPartition(ctx context.Context, r source.PartitionReader, part int, keyIdx []int, after []any, startSeq uint64,
	cfg ReadConfig, stop <-chan struct{}, out chan<- *source.Batch,
) error {
	cfg = cfg.withDefaults()
	seq := startSeq
	for {
		if stopped(stop) {
			return ErrDrained
		}
		rows, err := readWithRetry(ctx, cfg, part, func() ([][]any, error) { return r.ReadAfter(ctx, after, cfg.BatchSize) })
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		seq++
		b := &source.Batch{Seq: seq, Partition: part, Rows: rows, LastKey: keyOf(rows[len(rows)-1], keyIdx)}
		if err := emit(ctx, cfg, b, out); err != nil {
			return err
		}
		if len(rows) < cfg.BatchSize {
			return nil
		}
		after = b.LastKey
	}
}

// ReadCursor streams a keyless table once from the start; batches have no LastKey (no resume).
func ReadCursor(ctx context.Context, c source.CursorReader, cfg ReadConfig, stop <-chan struct{}, out chan<- *source.Batch) error {
	cfg = cfg.withDefaults()
	var seq uint64
	for {
		if stopped(stop) {
			return ErrDrained
		}
		rows, err := c.Fetch(ctx, cfg.BatchSize)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return &ReadError{Err: err}
		}
		if len(rows) == 0 {
			return nil
		}
		seq++
		if err := emit(ctx, cfg, &source.Batch{Seq: seq, Rows: rows}, out); err != nil {
			return err
		}
		if len(rows) < cfg.BatchSize {
			return nil
		}
	}
}

// ReadKeys streams the rows of explicit keys (retry of failed rows, OC-026), 1,000 keys per query.
func ReadKeys(ctx context.Context, rs source.ReadSession, t source.TableRead, keys [][]any, cfg ReadConfig, stop <-chan struct{}, out chan<- *source.Batch) error {
	cfg = cfg.withDefaults()
	var seq uint64
	for i := 0; i < len(keys); i += 1000 {
		if stopped(stop) {
			return ErrDrained
		}
		chunk := keys[i:min(i+1000, len(keys))]
		rows, err := readWithRetry(ctx, cfg, 0, func() ([][]any, error) { return rs.ReadKeys(ctx, t, chunk) })
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			continue
		}
		seq++
		if err := emit(ctx, cfg, &source.Batch{Seq: seq, Rows: rows}, out); err != nil {
			return err
		}
	}
	return nil
}

func readWithRetry(ctx context.Context, cfg ReadConfig, part int, read func() ([][]any, error)) ([][]any, error) {
	start := time.Now()
	defer func() { observability.ReaderBatchSeconds.Observe(time.Since(start).Seconds()) }()
	for attempt := 1; ; attempt++ {
		rows, err := read()
		if err == nil {
			return rows, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if !source.IsTransient(err) || attempt > cfg.MaxRetries {
			return nil, &ReadError{Partition: part, Err: err}
		}
		observability.ReaderRetries.Inc()
		if cfg.OnRetry != nil {
			cfg.OnRetry(part, attempt, err)
		}
		if err := cfg.Clock.Sleep(ctx, cfg.Retry.Delay(attempt)); err != nil {
			return nil, err
		}
	}
}

// emit throttles (when configured) and hands the batch downstream; a full channel blocks
// the reader, which is the pipeline's backpressure.
func emit(ctx context.Context, cfg ReadConfig, b *source.Batch, out chan<- *source.Batch) error {
	if cfg.Limiter != nil {
		if err := cfg.Limiter.WaitN(ctx, len(b.Rows)); err != nil {
			return err
		}
	}
	select {
	case out <- b:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
