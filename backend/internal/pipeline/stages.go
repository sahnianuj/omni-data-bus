package pipeline

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/connectors/target"
	"omnidatabus/internal/observability"
	"omnidatabus/internal/related"
	"omnidatabus/internal/transform"
)

// Stages of a row error.
const (
	StageRead      = "read"
	StageTransform = "transform"
	StageWrite     = "write"
)

// RowError is one row that didn't make it (dead-lettered by the error sink, OC-026).
type RowError struct {
	MappingID string
	Stage     string
	Code      string
	Message   string
	FieldPath string
	SourceKey []any
	// Row is the source row (the sink truncates and masks it for storage).
	Row []any
}

// DocBatch is a transformed batch: its documents plus the rows that failed, with the source
// batch's position so the write can be acknowledged and checkpointed.
type DocBatch struct {
	Seq       uint64
	Partition int
	LastKey   []any
	Docs      []target.Doc
	RowErrors []RowError
	// Rows is the source batch's row count (Docs + RowErrors).
	Rows int
}

// Transformer turns row batches into document batches with one compiled Program per worker
// (Programs aren't safe for concurrent use). Each batch is transformed by exactly one worker
// in row order, so the output is the same for any worker count (OC-025 AC1).
type Transformer struct {
	MappingID string
	Workers   int
	// NewProgram compiles the mapping; called once per worker.
	NewProgram func() (*transform.Program, error)
	KeyIdx     []int
}

// Run transforms until in closes (then closes out) or ctx ends.
func (tr Transformer) Run(ctx context.Context, in <-chan *source.Batch, out chan<- *DocBatch) error {
	workers := max(tr.Workers, 1)
	progs := make([]*transform.Program, workers)
	for i := range progs {
		p, err := tr.NewProgram()
		if err != nil {
			return err
		}
		progs[i] = p
	}
	var wg sync.WaitGroup
	for i := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for b := range in {
				db := tr.apply(progs[i], b)
				select {
				case out <- db:
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	wg.Wait()
	close(out)
	return ctx.Err()
}

func (tr Transformer) apply(p *transform.Program, b *source.Batch) *DocBatch {
	db := &DocBatch{Seq: b.Seq, Partition: b.Partition, LastKey: b.LastKey, Rows: len(b.Rows), Docs: make([]target.Doc, 0, len(b.Rows))}
	for i, row := range b.Rows {
		var sk []any
		if len(tr.KeyIdx) > 0 {
			sk = keyOf(row, tr.KeyIdx)
		}
		key, doc, _, rerr := p.ApplyRelated(row, related.Row(b.Related, i), nil)
		if rerr != nil {
			db.RowErrors = append(db.RowErrors, RowError{
				MappingID: tr.MappingID, Stage: StageTransform, Code: rerr.Code, Message: rerr.Message, FieldPath: rerr.FieldPath, SourceKey: sk, Row: row,
			})
			continue
		}
		db.Docs = append(db.Docs, target.Doc{Key: key, Value: doc, SourceKey: sk})
	}
	return db
}

// EmbedWorkers is how many batches have their embeds looked up at once (OC-021).
const EmbedWorkers = 2

// ResolveEmbeds attaches each batch's embedded rows (one lookup per embed and batch) and
// passes it on; it closes out when in closes. The first failed lookup stops it.
func ResolveEmbeds(ctx context.Context, r *related.Resolver, workers int, in <-chan *source.Batch, out chan<- *source.Batch) error {
	var wg sync.WaitGroup
	var once sync.Once
	var failed error
	stop := make(chan struct{})
	for range max(workers, 1) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for b := range in {
				rel, err := r.Resolve(ctx, b.Rows)
				if err != nil {
					once.Do(func() { failed = err; close(stop) })
					return
				}
				b.Related = rel
				select {
				case out <- b:
				case <-ctx.Done():
					return
				case <-stop:
					return
				}
			}
		}()
	}
	wg.Wait()
	close(out)
	return failed
}

// Ack reports a written batch (OC-025 AC7): row errors count as failed, so the checkpoint can
// move past bad rows.
type Ack struct {
	Partition int
	Seq       uint64
	LastKey   []any
	Written   int64
	Skipped   int64
	Failed    int64
	Bytes     int64
	Errors    []RowError
	// WrittenDocs are the written documents' keys (kept when the job verifies, OC-030).
	WrittenDocs []WrittenDoc
	// Fatal is set when a write error makes continuing pointless.
	Fatal *RowError
}

// WrittenDoc is one written document: its key and its source row's key.
type WrittenDoc struct {
	Key       string
	SourceKey []any
}

var outcomeLabel = map[target.Outcome]string{target.Written: "written", target.Skipped: "skipped", target.Failed: "failed"}

// Writer writes document batches; several batches are in flight at once, all sharing the
// writer's concurrency limit. KeepKeys reports the written keys in each Ack.
type Writer struct {
	MappingID string
	DocWriter target.DocWriter
	Workers   int
	KeepKeys  bool
}

// Run writes until in closes (then closes acks) or ctx ends. Exactly one Ack is sent per
// batch, after every document in it has a final outcome.
func (w Writer) Run(ctx context.Context, in <-chan *DocBatch, acks chan<- Ack) {
	var wg sync.WaitGroup
	for range max(w.Workers, 1) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for b := range in {
				a := w.write(ctx, b)
				if ctx.Err() != nil {
					// Cut short by a cancel: its outcomes aren't final, so it isn't acknowledged
					// and a resume writes it again (OC-029 AC5).
					return
				}
				select {
				case acks <- a:
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	wg.Wait()
	close(acks)
}

func (w Writer) write(ctx context.Context, b *DocBatch) Ack {
	a := Ack{Partition: b.Partition, Seq: b.Seq, LastKey: b.LastKey, Failed: int64(len(b.RowErrors)), Errors: b.RowErrors}
	if len(b.Docs) == 0 {
		return a
	}
	observability.WriterInflight.Inc()
	start := time.Now()
	results := w.DocWriter.WriteBatch(ctx, b.Docs)
	observability.WriterLatency.Observe(time.Since(start).Seconds())
	observability.WriterInflight.Dec()
	for i, r := range results {
		observability.WriterOps.WithLabelValues(outcomeLabel[r.Outcome], r.Code).Inc()
		if r.Attempts > 1 {
			observability.WriterRetries.WithLabelValues(r.Code).Add(float64(r.Attempts - 1))
		}
		switch r.Outcome {
		case target.Written:
			a.Written++
			a.Bytes += int64(len(b.Docs[i].Value))
			if w.KeepKeys {
				a.WrittenDocs = append(a.WrittenDocs, WrittenDoc{Key: b.Docs[i].Key, SourceKey: b.Docs[i].SourceKey})
			}
		case target.Skipped:
			a.Skipped++
		default:
			a.Failed++
			re := RowError{
				MappingID: w.MappingID, Stage: StageWrite, Code: r.Code, Message: r.Message, SourceKey: b.Docs[i].SourceKey,
				Row: []any{json.RawMessage(b.Docs[i].Value)},
			}
			a.Errors = append(a.Errors, re)
			if r.Fatal && a.Fatal == nil {
				a.Fatal = &re
			}
		}
	}
	return a
}
