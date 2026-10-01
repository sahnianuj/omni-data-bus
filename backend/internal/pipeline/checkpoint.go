package pipeline

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"
)

// CheckpointVersion is the stored checkpoint format (OC-029).
const CheckpointVersion = 1

// Mapping checkpoint states and read strategies (OC-029).
const (
	StatePending    = "pending"
	StateInProgress = "in_progress"
	StateDone       = "done"
	StrategyKeyset  = "keyset"
	StrategyCursor  = "cursor" // no key: a resume re-reads the table
)

// Checkpoint persistence cadence (OC-029): every 5 s or every 50 contiguous batches.
const (
	PersistEvery   = 5 * time.Second
	PersistBatches = 50
)

// ErrLostOwnership means another worker claimed the job: this run must stop (fencing).
var ErrLostOwnership = errors.New("the job is owned by another worker")

// CodeLostOwnership is the fatal code of a run that was fenced out.
const CodeLostOwnership = "LOST_OWNERSHIP"

// PartitionState is where one partition's read stands: batches 1..Seq are fully written, the
// last of them ending at LastKey.
type PartitionState struct {
	Lo      any    `json:"-"`
	Hi      any    `json:"-"`
	Seq     uint64 `json:"seq"`
	LastKey []any  `json:"-"`
	Done    bool   `json:"done"`
}

// MappingCheckpoint is one mapping's resumable position (OC-023 pause/resume; OC-029 builds
// on it). A mapping read with a cursor (no key) has no position: it restarts.
type MappingCheckpoint struct {
	Partitions []PartitionState `json:"partitions"`
	Done       bool             `json:"done"`
	Cursor     bool             `json:"cursor,omitempty"`
	// Written etc. are the mapping's totals so far, so a resumed run's progress continues.
	Written int64 `json:"written"`
	Skipped int64 `json:"skipped"`
	Failed  int64 `json:"failed"`
	// Pages are extra page documents of split arrays (OC-046), counted apart from rows.
	Pages int64 `json:"pages,omitempty"`
	// State and Strategy describe the position for readers of the stored document.
	State    string `json:"state,omitempty"`
	Strategy string `json:"strategy,omitempty"`
}

// Processed is how many rows the checkpoint covers.
func (m *MappingCheckpoint) Processed() int64 { return m.Written + m.Skipped + m.Failed }

func (m *MappingCheckpoint) describe() {
	m.Strategy = StrategyKeyset
	if m.Cursor {
		m.Strategy = StrategyCursor
	}
	m.State = StateInProgress
	switch {
	case m.Done:
		m.State = StateDone
	case m.Processed() == 0:
		m.State = StatePending
	}
}

// Checkpoint is the whole job's position.
type Checkpoint struct {
	Version   int                           `json:"version,omitempty"`
	UpdatedAt *time.Time                    `json:"updatedAt,omitempty"`
	Mappings  map[string]*MappingCheckpoint `json:"mappings"`
	// ErrorSeq is the last stored error's number, so a resumed run continues the numbering.
	ErrorSeq int64 `json:"errorSeq"`
}

// Key values keep their type through JSON: int64 as {"i":"…"}, uuid as {"u":"hex"},
// time as {"t":RFC3339Nano}, float as {"f":…}, bytes as {"b":"hex"}, else a string.
type keyValue struct{ v any }

func (k keyValue) MarshalJSON() ([]byte, error) {
	switch x := k.v.(type) {
	case nil:
		return []byte("null"), nil
	case int64:
		return json.Marshal(map[string]string{"i": strconv.FormatInt(x, 10)})
	case int32:
		return json.Marshal(map[string]string{"i": strconv.FormatInt(int64(x), 10)})
	case int16:
		return json.Marshal(map[string]string{"i": strconv.FormatInt(int64(x), 10)})
	case [16]byte:
		return json.Marshal(map[string]string{"u": hex.EncodeToString(x[:])})
	case []byte:
		return json.Marshal(map[string]string{"b": hex.EncodeToString(x)})
	case time.Time:
		return json.Marshal(map[string]string{"t": x.Format(time.RFC3339Nano)})
	case float64:
		return json.Marshal(map[string]float64{"f": x})
	case string:
		return json.Marshal(x)
	}
	return json.Marshal(fmt.Sprint(k.v))
}

func (k *keyValue) UnmarshalJSON(b []byte) error {
	if bytes.Equal(b, []byte("null")) {
		k.v = nil
		return nil
	}
	if len(b) > 0 && b[0] == '"' {
		var s string
		err := json.Unmarshal(b, &s)
		k.v = s
		return err
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	for tag, raw := range m {
		var s string
		_ = json.Unmarshal(raw, &s)
		switch tag {
		case "i":
			n, err := strconv.ParseInt(s, 10, 64)
			k.v = n
			return err
		case "u":
			var u [16]byte
			_, err := hex.Decode(u[:], []byte(s))
			k.v = u
			return err
		case "b":
			v, err := hex.DecodeString(s)
			k.v = v
			return err
		case "t":
			t, err := time.Parse(time.RFC3339Nano, s)
			k.v = t
			return err
		case "f":
			var f float64
			err := json.Unmarshal(raw, &f)
			k.v = f
			return err
		}
	}
	return fmt.Errorf("checkpoint: unknown key value %s", b)
}

func wrapKey(vs []any) []keyValue {
	if vs == nil {
		return nil
	}
	out := make([]keyValue, len(vs))
	for i, v := range vs {
		out[i] = keyValue{v}
	}
	return out
}

func unwrapKey(ks []keyValue) []any {
	if ks == nil {
		return nil
	}
	out := make([]any, len(ks))
	for i, k := range ks {
		out[i] = k.v
	}
	return out
}

type partitionJSON struct {
	Lo      *keyValue  `json:"lo"`
	Hi      *keyValue  `json:"hi"`
	Seq     uint64     `json:"seq"`
	LastKey []keyValue `json:"lastKey"`
	Done    bool       `json:"done"`
}

func optKey(v any) *keyValue {
	if v == nil {
		return nil
	}
	return &keyValue{v}
}

// MarshalJSON keeps typed bounds and keys.
func (p PartitionState) MarshalJSON() ([]byte, error) {
	return json.Marshal(partitionJSON{Lo: optKey(p.Lo), Hi: optKey(p.Hi), Seq: p.Seq, LastKey: wrapKey(p.LastKey), Done: p.Done})
}

// UnmarshalJSON restores typed bounds and keys.
func (p *PartitionState) UnmarshalJSON(b []byte) error {
	var j partitionJSON
	if err := json.Unmarshal(b, &j); err != nil {
		return err
	}
	*p = PartitionState{Seq: j.Seq, LastKey: unwrapKey(j.LastKey), Done: j.Done}
	if j.Lo != nil {
		p.Lo = j.Lo.v
	}
	if j.Hi != nil {
		p.Hi = j.Hi.v
	}
	return nil
}

// ParseCheckpoint decodes a stored checkpoint (empty = start fresh).
func ParseCheckpoint(raw json.RawMessage) (Checkpoint, error) {
	cp := Checkpoint{Mappings: map[string]*MappingCheckpoint{}}
	if len(raw) == 0 || string(raw) == "null" {
		return cp, nil
	}
	err := json.Unmarshal(raw, &cp)
	if cp.Mappings == nil {
		cp.Mappings = map[string]*MappingCheckpoint{}
	}
	return cp, err
}

// checkpointer advances each partition's position only over contiguous acknowledged batches,
// so a resume never skips a batch that was still being written (acks arrive out of order).
type checkpointer struct {
	mu      sync.Mutex
	mapping *MappingCheckpoint
	pending []map[uint64]pendingAck // partition → seq → acked but not yet contiguous
}

type pendingAck struct {
	lastKey                         []any
	written, skipped, failed, pages int64
}

func newCheckpointer(m *MappingCheckpoint) *checkpointer {
	c := &checkpointer{mapping: m, pending: make([]map[uint64]pendingAck, len(m.Partitions))}
	for i := range c.pending {
		c.pending[i] = map[uint64]pendingAck{}
	}
	return c
}

// ack records that batch seq of partition part is fully written. The mapping's counts only
// include batches the checkpoint covers, so a resume neither loses nor double-counts rows.
// It returns how many batches the position advanced over (0 while an earlier one is missing).
//
// Invariant: every row with a key ≤ LastKey in the partition has a final outcome.
func (c *checkpointer) ack(part int, seq uint64, lastKey []any, written, skipped, failed int64) int {
	return c.ackPages(part, seq, lastKey, written, skipped, failed, 0)
}

// ackPages is ack with the batch's split page documents (OC-046).
func (c *checkpointer) ackPages(part int, seq uint64, lastKey []any, written, skipped, failed, pages int64) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if part < 0 || part >= len(c.pending) {
		return 0
	}
	p := &c.mapping.Partitions[part]
	c.pending[part][seq] = pendingAck{lastKey, written, skipped, failed, pages}
	advanced := 0
	for {
		a, ok := c.pending[part][p.Seq+1]
		if !ok {
			return advanced
		}
		advanced++
		delete(c.pending[part], p.Seq+1)
		p.Seq++
		if a.lastKey != nil {
			p.LastKey = a.lastKey
		}
		c.mapping.Written += a.written
		c.mapping.Skipped += a.skipped
		c.mapping.Failed += a.failed
		c.mapping.Pages += a.pages
	}
}

func (c *checkpointer) partitionDone(part int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if part >= 0 && part < len(c.mapping.Partitions) {
		c.mapping.Partitions[part].Done = true
	}
}

// snapshot copies the mapping's checkpoint.
func (c *checkpointer) snapshot() MappingCheckpoint {
	c.mu.Lock()
	defer c.mu.Unlock()
	cp := *c.mapping
	cp.Partitions = append([]PartitionState(nil), c.mapping.Partitions...)
	return cp
}

// persister stores the checkpoint durably every PersistEvery or PersistBatches contiguous
// batches, and at once when a partition completes (OC-029 AC2). Dead-lettered rows are flushed
// first, so the failed rows a checkpoint covers are always in job_errors.
type persister struct {
	every   time.Duration
	batches int
	now     func() time.Time
	// flush stores buffered row errors; save stores the checkpoint, fenced by ownership
	// (ErrLostOwnership when another worker owns the job); snapshot builds the checkpoint.
	flush    func(context.Context)
	save     func(context.Context, json.RawMessage) error
	snapshot func() json.RawMessage

	mu        sync.Mutex
	last      time.Time
	since     int
	persisted json.RawMessage
}

func newPersister(now func() time.Time, flush func(context.Context), save func(context.Context, json.RawMessage) error, snapshot func() json.RawMessage) *persister {
	return &persister{every: PersistEvery, batches: PersistBatches, now: now, flush: flush, save: save, snapshot: snapshot, last: now()}
}

// advanced notes that the position moved over n batches; it persists when the cadence is due,
// or at once when force is set (a partition finished).
func (p *persister) advanced(ctx context.Context, n int, force bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.since += n
	if !force && p.since < p.batches && p.now().Sub(p.last) < p.every {
		return nil
	}
	return p.persistLocked(ctx)
}

// persist stores the current position now.
func (p *persister) persist(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.persistLocked(ctx)
}

func (p *persister) persistLocked(ctx context.Context) error {
	// The position is taken before the flush: every error it covers is already in the sink.
	raw := p.snapshot()
	p.flush(ctx)
	if err := p.save(ctx, raw); err != nil {
		return err
	}
	p.last, p.since, p.persisted = p.now(), 0, raw
	return nil
}

// last persisted checkpoint (nil before the first).
func (p *persister) durable() json.RawMessage {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.persisted
}
