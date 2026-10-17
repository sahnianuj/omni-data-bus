package related

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"omnidatabus/internal/connectors/source"
)

// CodeRelationMemoryExceeded fails a row whose related rows alone exceed the memory cap (OC-043).
const CodeRelationMemoryExceeded = "RELATION_MEMORY_EXCEEDED"

// DefaultMemoryMB is OC_RELATION_MEMORY_MB's default.
const DefaultMemoryMB = 256

var memoryLimit atomic.Int64

func init() { memoryLimit.Store(DefaultMemoryMB << 20) }

// SetMemoryLimitMB sets the cap on a batch's related rows in memory (OC_RELATION_MEMORY_MB).
func SetMemoryLimitMB(mb int) { memoryLimit.Store(int64(max(mb, 1)) << 20) }

// MemoryLimitBytes is the configured cap.
func MemoryLimitBytes() int64 { return memoryLimit.Load() }

var errMemory = errors.New("related rows exceed the memory cap")

// Resolver looks up a batch's related rows.
type Resolver struct {
	// Plans are topologically ordered (parents before children), as Plans returns them.
	Plans       []Plan
	Reader      source.RelatedReader
	Concurrency int
	// Do runs one lookup (the pipeline wraps it with retries); nil calls it directly.
	Do func(ctx context.Context, read func() ([][]any, error)) ([][]any, error)
	// MemoryBytes caps the related rows held for one batch; 0 = MemoryLimitBytes(). A batch
	// over the cap is split in half and retried, down to one parent row.
	MemoryBytes int64
	// NoShare reads every relation on its own (tests compare it with shared reads, OC-144).
	NoShare bool
}

// Resolve returns each root row's related rows (index = row index) and the rows that failed
// (RELATION_MEMORY_EXCEEDED). Relations are resolved depth by depth: every relation at depth d
// runs one query with the distinct join keys of all rows produced at depth d-1 for its parent,
// so the number of queries per batch is the number of relations, whatever the depth or the
// number of rows (OC-043 AC2). Relations of one depth run concurrently, at most Concurrency
// at once; rows with a null join key have none.
func (r *Resolver) Resolve(ctx context.Context, rows [][]any) ([]source.Related, map[int]source.RowFailure, error) {
	out := make([]source.Related, len(rows))
	if len(rows) == 0 || len(r.Plans) == 0 {
		return out, nil, nil
	}
	var fails map[int]source.RowFailure
	var span func(rows [][]any, off int) error
	span = func(rows [][]any, off int) error {
		res, err := r.resolveAll(ctx, rows)
		switch {
		case errors.Is(err, errMemory) && len(rows) == 1:
			if fails == nil {
				fails = map[int]source.RowFailure{}
			}
			fails[off] = source.RowFailure{Code: CodeRelationMemoryExceeded, Message: fmt.Sprintf(
				"this row's related rows need more than %d MB (OC_RELATION_MEMORY_MB); lower the relation limits", r.limit()>>20)}
			return nil
		case errors.Is(err, errMemory):
			h := len(rows) / 2
			if err := span(rows[:h], off); err != nil {
				return err
			}
			return span(rows[h:], off+h)
		case err != nil:
			return err
		}
		copy(out[off:], res)
		return nil
	}
	if err := span(rows, 0); err != nil {
		return nil, nil, err
	}
	return out, fails, nil
}

func (r *Resolver) limit() int64 {
	if r.MemoryBytes > 0 {
		return r.MemoryBytes
	}
	return MemoryLimitBytes()
}

// parentRow is a row that relations hang under: a root row, or a related row.
type parentRow struct {
	values []any
	sub    *source.Related
}

type lookupResult struct {
	per      [][]source.RelatedRow
	dangling []int
}

// budget counts the bytes of related rows held for one batch.
type budget struct {
	used, limit atomic.Int64
}

func (b *budget) add(n int64) bool { return b.used.Add(n) <= b.limit.Load() }

func (r *Resolver) resolveAll(ctx context.Context, rows [][]any) ([]source.Related, error) {
	out := make([]source.Related, len(rows))
	level := map[string][]parentRow{"": make([]parentRow, len(rows))}
	for i := range rows {
		level[""][i] = parentRow{values: rows[i], sub: &out[i]}
	}
	b := &budget{}
	b.limit.Store(r.limit())
	for start := 0; start < len(r.Plans); {
		end := start
		for end < len(r.Plans) && r.Plans[end].Depth == r.Plans[start].Depth {
			end++
		}
		plans := r.Plans[start:end]
		results := make([]lookupResult, len(plans))
		errs := make([]error, len(plans))
		sem := make(chan struct{}, max(r.Concurrency, 1))
		var wg sync.WaitGroup
		// Relations that read the same rows share one query (OC-144); the rest read alone.
		for _, unit := range r.units(plans) {
			wg.Add(1)
			go func() {
				defer wg.Done()
				select {
				case sem <- struct{}{}:
				case <-ctx.Done():
					for _, n := range unit {
						errs[n] = ctx.Err()
					}
					return
				}
				defer func() { <-sem }()
				if len(unit) == 1 {
					n := unit[0]
					results[n], errs[n] = r.lookup(ctx, plans[n], level[plans[n].Parent], b)
					return
				}
				res, err := r.lookupShared(ctx, plans, unit, level, b)
				for i, n := range unit {
					results[n], errs[n] = res[i], err
				}
			}()
		}
		wg.Wait()
		for n, err := range errs {
			if err != nil {
				if errors.Is(err, errMemory) {
					return nil, err
				}
				p := plans[n]
				return nil, fmt.Errorf("embed %s (%s): %w", p.ID, p.Query.Table.Schema+"."+p.Query.Table.Table, err)
			}
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// Attach single-threaded: sibling relations share their parents' maps.
		for n, p := range plans {
			res := results[n]
			seen := map[*source.RelatedRow]bool{}
			for i, parent := range level[p.Parent] {
				if len(res.per[i]) == 0 && res.dangling[i] == 0 {
					continue
				}
				if *parent.sub == nil {
					*parent.sub = source.Related{}
				}
				set := &source.RelatedSet{Rows: res.per[i], Dangling: res.dangling[i]}
				(*parent.sub)[p.ID] = set
				for j := range set.Rows {
					// Parents with the same key share their rows (600 products under 4,000
					// items): each row is a parent of the next depth once.
					row := &set.Rows[j]
					if !seen[row] {
						seen[row] = true
						level[p.ID] = append(level[p.ID], parentRow{values: row.Values, sub: &row.Sub})
					}
				}
			}
		}
		start = end
	}
	return out, nil
}

// group collects one parent key's rows.
type group struct {
	rows     []source.RelatedRow
	dangling int
	seen     map[string]bool // distinct target rows
}

// accept applies the per-row rules: dangling many-to-many rows are dropped (and counted)
// unless kept, duplicates of a distinct relation are dropped, and at most Keep rows are kept.
func (p *Plan) accept(g *group, row []any) bool {
	if len(p.DanglingIdx) > 0 && allNull(row, p.DanglingIdx) {
		if !p.KeepDangling {
			g.dangling++
			return false
		}
	} else if len(p.DistinctIdx) > 0 {
		k, _ := KeyOf(row, p.DistinctIdx)
		if g.seen == nil {
			g.seen = map[string]bool{}
		}
		if g.seen[k] {
			return false
		}
		g.seen[k] = true
	}
	return p.Keep <= 0 || len(g.rows) < p.Keep
}

func allNull(row []any, idx []int) bool {
	for _, i := range idx {
		if i < len(row) && row[i] != nil {
			return false
		}
	}
	return true
}

// rowBytes estimates a row's memory.
func rowBytes(row []any) int64 {
	n := int64(24 + 16*len(row))
	for _, v := range row {
		switch x := v.(type) {
		case string:
			n += int64(len(x))
		case []byte:
			n += int64(len(x))
		case map[string]any, []any:
			n += 64
		case time.Time:
			n += 24
		}
	}
	return n
}

// shareKey identifies the many-to-one relations whose reads can be one query: the same table,
// join columns and order, keeping one row per key, with no filter, junction or key range.
// "" = the relation reads alone.
func shareKey(p Plan) string {
	q := p.Query
	if p.Many || p.Keep != 1 || q.Via != nil || len(q.Filter) > 0 || q.Range != nil || p.Strategy == StrategySortedMerge ||
		len(p.DanglingIdx) > 0 || len(p.DistinctIdx) > 0 {
		return ""
	}
	k := q.Table.Schema + "." + q.Table.Table + "|" + strings.Join(q.Join, ",") + "|" + strconv.Itoa(q.PerKeyLimit)
	for _, o := range q.OrderBy {
		k += "|" + o.Column + ":" + strconv.FormatBool(o.Desc)
	}
	return k
}

// units groups one depth's plans: relations with the same shareKey together, the rest alone.
func (r *Resolver) units(plans []Plan) [][]int {
	var out [][]int
	shared := map[string]int{}
	for n, p := range plans {
		k := ""
		if !r.NoShare {
			k = shareKey(p)
		}
		if i, ok := shared[k]; ok && k != "" {
			out[i] = append(out[i], n)
			continue
		}
		if k != "" {
			shared[k] = len(out)
		}
		out = append(out, []int{n})
	}
	return out
}

// keysOf are a plan's parents' join keys: per parent ("" = none) and each distinct one.
func keysOf(p Plan, parents []parentRow, seen map[string]bool, keys [][]any) ([]string, [][]any) {
	rowKeys := make([]string, len(parents))
	for i, parent := range parents {
		k, ok := KeyOf(parent.values, p.ParentIdx)
		if !ok {
			continue
		}
		rowKeys[i] = k
		if !seen[k] {
			seen[k] = true
			key := make([]any, len(p.ParentIdx))
			for n, j := range p.ParentIdx {
				key[n] = parent.values[j]
			}
			keys = append(keys, key)
		}
	}
	return rowKeys, keys
}

// lookupShared reads the rows of several relations with one query: the union of their keys
// and columns. Each relation gets its own parents' rows, as if it had read them alone.
func (r *Resolver) lookupShared(ctx context.Context, plans []Plan, unit []int, level map[string][]parentRow, b *budget) ([]lookupResult, error) {
	merged := plans[unit[0]]
	merged.Query.Table.Project = append([]bool(nil), merged.Query.Table.Project...)
	seen := map[string]bool{}
	var keys [][]any
	rowKeys := make([][]string, len(unit))
	for i, n := range unit {
		p := plans[n]
		for c, on := range p.Query.Table.Project {
			if on && c < len(merged.Query.Table.Project) {
				merged.Query.Table.Project[c] = true
			}
		}
		rowKeys[i], keys = keysOf(p, level[p.Parent], seen, keys)
	}
	out := make([]lookupResult, len(unit))
	var groups map[string]*group
	if len(keys) > 0 {
		var err error
		if groups, err = r.batched(ctx, merged, keys, b); err != nil {
			return out, err
		}
	}
	for i := range unit {
		res := lookupResult{per: make([][]source.RelatedRow, len(rowKeys[i])), dangling: make([]int, len(rowKeys[i]))}
		for j, k := range rowKeys[i] {
			if g := groups[k]; k != "" && g != nil {
				res.per[j] = g.rows
			}
		}
		out[i] = res
	}
	return out, nil
}

func (r *Resolver) lookup(ctx context.Context, p Plan, parents []parentRow, b *budget) (lookupResult, error) {
	res := lookupResult{per: make([][]source.RelatedRow, len(parents)), dangling: make([]int, len(parents))}
	rowKeys, keys := keysOf(p, parents, map[string]bool{}, nil)
	if len(keys) == 0 {
		return res, nil
	}
	var groups map[string]*group
	var err error
	if p.Strategy == StrategySortedMerge && len(p.ParentIdx) == 1 {
		groups, err = r.merge(ctx, p, keys, b)
	} else {
		groups, err = r.batched(ctx, p, keys, b)
	}
	if err != nil {
		return res, err
	}
	for i, k := range rowKeys {
		if g := groups[k]; k != "" && g != nil {
			res.per[i], res.dangling[i] = g.rows, g.dangling
		}
	}
	return res, nil
}

// batched reads every key's rows with one = ANY / unnest query (OC-021).
func (r *Resolver) batched(ctx context.Context, p Plan, keys [][]any, b *budget) (map[string]*group, error) {
	read := func() ([][]any, error) { return r.Reader.ReadRelated(ctx, p.Query, keys) }
	var children [][]any
	var err error
	if r.Do != nil {
		children, err = r.Do(ctx, read)
	} else {
		children, err = read()
	}
	if err != nil {
		return nil, err
	}
	groups := map[string]*group{}
	for _, c := range children {
		k, ok := KeyOf(c, p.ChildIdx)
		if !ok {
			continue
		}
		g := groups[k]
		if g == nil {
			g = &group{}
			groups[k] = g
		}
		if !p.accept(g, c) {
			continue
		}
		if !b.add(rowBytes(c)) {
			return nil, errMemory
		}
		g.rows = append(g.rows, source.RelatedRow{Values: c})
	}
	return groups, nil
}
