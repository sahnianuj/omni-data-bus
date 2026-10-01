package related

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"omnidatabus/internal/connectors/source"
)

// MergeableType reports whether a join column of this type can be sorted-merged (OC-046): Go
// must order its values exactly as the database does, byte-wise for text under COLLATE "C".
func MergeableType(udt string) bool {
	switch udt {
	case "int2", "int4", "int8", "text", "varchar", "bpchar", "name", "uuid", "date", "timestamp", "timestamptz":
		return true
	}
	return false
}

// Compare orders join key values the way the sorted-merge query does: integers numerically,
// text byte-wise (COLLATE "C"), UUIDs and bytes byte-wise, times chronologically.
func Compare(a, b any) int {
	if x, ok := asInt(a); ok {
		if y, ok := asInt(b); ok {
			switch {
			case x < y:
				return -1
			case x > y:
				return 1
			}
			return 0
		}
	}
	switch x := a.(type) {
	case string:
		if y, ok := b.(string); ok {
			return strings.Compare(x, y)
		}
	case [16]byte:
		if y, ok := b.([16]byte); ok {
			return bytes.Compare(x[:], y[:])
		}
	case []byte:
		if y, ok := b.([]byte); ok {
			return bytes.Compare(x, y)
		}
	case time.Time:
		if y, ok := b.(time.Time); ok {
			return x.Compare(y)
		}
	}
	return strings.Compare(fmt.Sprint(a), fmt.Sprint(b))
}

func asInt(v any) (int64, bool) {
	switch x := v.(type) {
	case int16:
		return int64(x), true
	case int32:
		return int64(x), true
	case int64:
		return x, true
	case int:
		return int64(x), true
	}
	return 0, false
}

// Merger groups a stream of child rows, ordered by their join key, under parent keys sorted
// the same way (OC-046). It holds one parent's rows at a time — at most keep of them (0 = all),
// the rest are only counted — and hands each finished group to emit. Rows whose key matches
// no parent are skipped.
type Merger struct {
	keys   []any
	pos    int
	cur    [][]any
	keep   int
	accept func(pos int, row []any) bool
	emit   func(pos int, rows [][]any)
	// MaxRetained is the most rows held at once (tests check the memory bound).
	MaxRetained int
}

// NewMerger merges under keys (sorted ascending by Compare, distinct). accept may drop a row
// before it's kept (nil keeps every row).
func NewMerger(keys []any, keep int, accept func(pos int, row []any) bool, emit func(pos int, rows [][]any)) *Merger {
	return &Merger{keys: keys, keep: keep, accept: accept, emit: emit}
}

// Push adds the next child row, whose join key is key.
func (m *Merger) Push(key any, row []any) {
	for m.pos < len(m.keys) && Compare(m.keys[m.pos], key) < 0 {
		m.flush()
		m.pos++
	}
	if m.pos >= len(m.keys) || Compare(m.keys[m.pos], key) > 0 {
		return // no parent has this key
	}
	if m.accept != nil && !m.accept(m.pos, row) {
		return
	}
	if m.keep <= 0 || len(m.cur) < m.keep {
		m.cur = append(m.cur, row)
		m.MaxRetained = max(m.MaxRetained, len(m.cur))
	}
}

// Close emits the last group.
func (m *Merger) Close() { m.flush() }

func (m *Merger) flush() {
	if len(m.cur) > 0 {
		m.emit(m.pos, m.cur)
		m.cur = nil
	}
}

// merge reads the relation's rows for the keys' range, ordered by the join key, and merges them
// with the sorted keys instead of matching an = ANY list (sorted-merge, OC-046).
func (r *Resolver) merge(ctx context.Context, p Plan, keys [][]any, b *budget) (map[string]*group, error) {
	sorted := make([]any, len(keys))
	for i, k := range keys {
		sorted[i] = k[0]
	}
	sort.Slice(sorted, func(i, j int) bool { return Compare(sorted[i], sorted[j]) < 0 })
	q := p.Query
	q.Range = &source.KeyRange{From: sorted[0], To: sorted[len(sorted)-1]}
	var groups map[string]*group
	var failed error
	read := func() ([][]any, error) {
		groups, failed = map[string]*group{}, nil // a retried read starts over
		byPos := make([]*group, len(sorted))
		at := func(pos int) *group {
			if byPos[pos] == nil {
				byPos[pos] = &group{}
				k, _ := KeyOf([]any{sorted[pos]}, []int{0})
				groups[k] = byPos[pos]
			}
			return byPos[pos]
		}
		// accept drops dangling and duplicate rows (the group's rows fill in on emit, so its
		// Keep check passes); the merger keeps at most Keep rows of the current parent.
		m := NewMerger(sorted, p.Keep, func(pos int, row []any) bool {
			return p.accept(at(pos), row)
		}, func(pos int, rows [][]any) {
			g := at(pos)
			g.rows = make([]source.RelatedRow, len(rows))
			for i, row := range rows {
				if !b.add(rowBytes(row)) {
					failed = errMemory
				}
				g.rows[i] = source.RelatedRow{Values: row}
			}
		})
		push := func(row []any) error {
			if failed != nil {
				return failed
			}
			m.Push(row[p.ChildIdx[0]], row)
			return nil
		}
		var err error
		if st, ok := r.Reader.(source.RelatedStreamer); ok {
			err = st.StreamRelated(ctx, q, nil, push)
		} else {
			var rows [][]any
			if rows, err = r.Reader.ReadRelated(ctx, q, nil); err == nil {
				for _, row := range rows {
					if err = push(row); err != nil {
						break
					}
				}
			}
		}
		m.Close()
		if err == nil {
			err = failed
		}
		return nil, err
	}
	var err error
	if r.Do != nil {
		_, err = r.Do(ctx, read)
	} else {
		_, err = read()
	}
	if err != nil {
		return nil, err
	}
	return groups, nil
}
