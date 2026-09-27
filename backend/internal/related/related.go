// Package related resolves embeds (OC-021): for a batch of parent rows it looks up each
// embedded table's rows with one query per embed (never one per row) and groups them by
// parent. The pipeline and the mapping preview both use it.
package related

import (
	"context"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/schema"
)

// DefaultConcurrency bounds the embed lookups of one batch that run at once.
const DefaultConcurrency = 4

// Plan is how one embed's rows are read.
type Plan struct {
	ID   string
	Many bool // one-to-many
	// ParentIdx are the join columns in the parent row; ChildIdx the same in the child row.
	ParentIdx []int
	ChildIdx  []int
	Query     source.RelatedQuery
	// Keep is the most rows kept per parent: limit+1 for one-to-many (so the transform can
	// tell the array was truncated), 1 for many-to-one.
	Keep int
}

// Tables resolves each embed's table by embed id (for transform.Options.Related).
func Tables(s *mapping.Spec, rel mapping.RelatedTables) map[string]*schema.Table {
	out := map[string]*schema.Table{}
	if rel == nil {
		return out
	}
	for _, e := range s.Embeds {
		if t := rel(e.Table.Schema, e.Table.Name); t != nil {
			out[e.ID] = t
		}
	}
	return out
}

// ParentColumns are the columns of the mapped table that embeds join on (they must be read).
func ParentColumns(s *mapping.Spec) []string {
	var out []string
	for _, e := range s.Embeds {
		for _, j := range e.Join {
			out = append(out, j.Parent)
		}
	}
	return out
}

// Plans builds one plan per embed. The embedded table's projection is the columns its mappings
// use plus the join and order columns (every column when an expression may read row.<any>).
func Plans(s *mapping.Spec, parent *schema.Table, rel mapping.RelatedTables) ([]Plan, error) {
	var out []Plan
	for _, e := range s.Embeds {
		var child *schema.Table
		if rel != nil {
			child = rel(e.Table.Schema, e.Table.Name)
		}
		if child == nil {
			return nil, fmt.Errorf("embedded table %s isn't in the latest schema snapshot", e.Table)
		}
		need := map[string]bool{}
		all := false
		for _, m := range s.FieldMappings {
			if !m.Active() || !mapping.UsesEmbed(m, e.ID) {
				continue
			}
			for _, src := range m.Sources {
				need[src.Column] = true
			}
			for _, st := range m.Transforms {
				all = all || st.Op == "expression"
			}
		}
		p := Plan{ID: e.ID, Many: e.Kind == mapping.EmbedOneToMany, Keep: 1}
		q := source.RelatedQuery{Table: source.TableRead{Schema: e.Table.Schema, Table: e.Table.Name, Columns: child.Columns}}
		for _, j := range e.Join {
			pi, ci := index(parent, j.Parent), index(child, j.Child)
			if pi < 0 || ci < 0 {
				return nil, fmt.Errorf("embed %s: join column %s → %s is missing", e.ID, j.Parent, j.Child)
			}
			p.ParentIdx, p.ChildIdx = append(p.ParentIdx, pi), append(p.ChildIdx, ci)
			q.Join = append(q.Join, j.Child)
			need[j.Child] = true
		}
		if p.Many {
			for _, o := range e.OrderBy {
				q.OrderBy = append(q.OrderBy, source.OrderColumn{Column: o.Column, Desc: o.Desc})
				need[o.Column] = true
			}
			p.Keep = e.EffectiveLimit() + 1
			if e.EffectiveLimit() <= mapping.SQLLimitThreshold {
				q.PerKeyLimit = p.Keep // small limits are applied in SQL to transfer less
			}
		}
		q.Table.Project = make([]bool, len(child.Columns))
		for i, c := range child.Columns {
			q.Table.Project[i] = all || need[c.Name]
		}
		p.Query = q
		out = append(out, p)
	}
	return out, nil
}

func index(t *schema.Table, col string) int {
	for i, c := range t.Columns {
		if c.Name == col {
			return i
		}
	}
	return -1
}

// Resolver looks up a batch's embedded rows.
type Resolver struct {
	Plans       []Plan
	Reader      source.RelatedReader
	Concurrency int
	// Do runs one lookup (the pipeline wraps it with retries); nil calls it directly.
	Do func(ctx context.Context, read func() ([][]any, error)) ([][]any, error)
}

// Resolve returns, per embed id, each parent row's related rows (in the query's order). The
// embeds run concurrently, at most Concurrency at once; rows with a null join key have none.
func (r *Resolver) Resolve(ctx context.Context, rows [][]any) (map[string][][][]any, error) {
	out := make(map[string][][][]any, len(r.Plans))
	if len(rows) == 0 || len(r.Plans) == 0 {
		return out, nil
	}
	sem := make(chan struct{}, max(r.Concurrency, 1))
	var mu sync.Mutex
	var firstErr error
	var wg sync.WaitGroup
	for _, p := range r.Plans {
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()
			per, err := r.resolve(ctx, p, rows)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("embed %s (%s): %w", p.ID, p.Query.Table.Schema+"."+p.Query.Table.Table, err)
				}
				return
			}
			out[p.ID] = per
		}()
	}
	wg.Wait()
	if firstErr == nil && ctx.Err() != nil {
		firstErr = ctx.Err()
	}
	return out, firstErr
}

func (r *Resolver) resolve(ctx context.Context, p Plan, rows [][]any) ([][][]any, error) {
	rowKeys := make([]string, len(rows))
	seen := map[string]bool{}
	var keys [][]any
	for i, row := range rows {
		k, ok := KeyOf(row, p.ParentIdx)
		if !ok {
			continue
		}
		rowKeys[i] = k
		if !seen[k] {
			seen[k] = true
			key := make([]any, len(p.ParentIdx))
			for n, j := range p.ParentIdx {
				key[n] = row[j]
			}
			keys = append(keys, key)
		}
	}
	per := make([][][]any, len(rows))
	if len(keys) == 0 {
		return per, nil
	}
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
	groups := map[string][][]any{}
	for _, c := range children {
		k, ok := KeyOf(c, p.ChildIdx)
		if !ok || len(groups[k]) >= p.Keep {
			continue
		}
		groups[k] = append(groups[k], c)
	}
	for i, k := range rowKeys {
		if k != "" {
			per[i] = groups[k]
		}
	}
	return per, nil
}

// KeyOf renders the join columns of a row as a comparable string, normalizing driver types
// (an int4 parent column matches an int8 child column); ok is false when any part is null.
func KeyOf(row []any, idx []int) (string, bool) {
	var b strings.Builder
	for n, i := range idx {
		if i >= len(row) || row[i] == nil {
			return "", false
		}
		if n > 0 {
			b.WriteByte(0)
		}
		switch x := row[i].(type) {
		case int16:
			b.WriteString("i" + strconv.FormatInt(int64(x), 10))
		case int32:
			b.WriteString("i" + strconv.FormatInt(int64(x), 10))
		case int64:
			b.WriteString("i" + strconv.FormatInt(x, 10))
		case int:
			b.WriteString("i" + strconv.Itoa(x))
		case string:
			b.WriteString("s" + x)
		case [16]byte:
			b.WriteString("u" + hex.EncodeToString(x[:]))
		case []byte:
			b.WriteString("b" + hex.EncodeToString(x))
		case time.Time:
			b.WriteString("t" + strconv.FormatInt(x.UnixNano(), 10))
		default:
			fmt.Fprintf(&b, "v%v", x)
		}
	}
	return b.String(), true
}

// Row picks one parent row's related rows (nil when there are none).
func Row(all map[string][][][]any, i int) map[string][][]any {
	if len(all) == 0 {
		return nil
	}
	out := make(map[string][][]any, len(all))
	for id, per := range all {
		if i < len(per) {
			out[id] = per[i]
		}
	}
	return out
}
