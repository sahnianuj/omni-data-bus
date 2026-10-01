// Package preview holds what the mapping preview adds to the shared execution plan for nested
// and composed documents (OC-049): relation reads capped per parent so a preview stays under
// 2 s, per-node statistics of each previewed document, and the worst case's size estimate. The
// documents themselves are built by the same program a migration runs (internal/plan).
package preview

import (
	"encoding/json"
	"strings"

	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/related"
)

// Cap is the most rows per parent a preview shows of any relation ("showing 50 of 1,240").
const Cap = 50

// NodeStats describes one relation node of a previewed document.
type NodeStats struct {
	// Rows are the related rows in the document (summed over parents for nested nodes).
	Rows int `json:"rows"`
	// Total is every related row, when counted (arrays directly under the root).
	Total *int64 `json:"total,omitempty"`
	// Truncated: some parent had more than Cap rows; the preview shows Cap of them.
	Truncated bool `json:"truncated"`
	// Dangling many-to-many junction rows whose target row is missing (dropped).
	Dangling int `json:"dangling"`
	// AdapterDiscarded are rows the first adapter left out (OC-047).
	AdapterDiscarded int `json:"adapterDiscarded"`
}

// Capped returns the lookups reading at most Cap+1 rows per parent: one more than shown, so a
// cut is visible. Relations whose aggregates or split pages need every row (Keep 0) and small
// limits are left alone.
func Capped(plans []related.Plan) []related.Plan {
	out := make([]related.Plan, len(plans))
	for i, p := range plans {
		if p.Many && p.Keep > Cap+1 {
			p.Keep = Cap + 1
			if p.Query.PerKeyLimit == 0 || p.Query.PerKeyLimit > Cap+1 {
				p.Query.PerKeyLimit = Cap + 1
			}
		}
		out[i] = p
	}
	return out
}

// NodePaths maps each relation that fills a node to its dotted path (items, items.product).
func NodePaths(s *mapping.Spec) map[string]string {
	paths := s.DocSchema.Paths()
	out := map[string]string{}
	for _, r := range s.Relations {
		if r.LookupOnly || r.TargetFieldID == "" || paths[r.TargetFieldID] == "" {
			continue
		}
		out[r.ID] = strings.ReplaceAll(paths[r.TargetFieldID], "[]", "")
	}
	return out
}

// Stats computes a document's per-node statistics from its related rows as read (at most
// Cap+1 per parent), before Trim. totals are counted rows of depth-1 relations for this root row.
func Stats(s *mapping.Spec, rel source.Related, totals map[string]int64) map[string]NodeStats {
	paths := NodePaths(s)
	first := map[string]bool{}
	for _, r := range s.Relations {
		first[r.ID] = r.Many() && mapping.Adapter(&s.DocSchema, r) == "first"
	}
	out := map[string]NodeStats{}
	for id, p := range paths {
		out[p] = NodeStats{}
		if n, ok := totals[id]; ok {
			n := n
			st := out[p]
			st.Total = &n
			out[p] = st
		}
	}
	var walk func(rel source.Related)
	walk = func(rel source.Related) {
		for id, set := range rel {
			if set == nil {
				continue
			}
			p, ok := paths[id]
			if ok {
				st := out[p]
				shown := min(len(set.Rows), Cap)
				st.Rows += shown
				st.Truncated = st.Truncated || len(set.Rows) > Cap
				st.Dangling += set.Dangling
				if first[id] && len(set.Rows) > 1 {
					n := len(set.Rows) - 1
					if st.Total != nil && *st.Total > 0 {
						n = int(*st.Total) - 1
					}
					st.AdapterDiscarded += n
					st.Rows -= shown - 1
				}
				out[p] = st
			}
			for i, r := range set.Rows {
				if i >= Cap {
					break
				}
				walk(r.Sub)
			}
		}
	}
	walk(rel)
	return out
}

// Trim cuts every relation's rows to Cap per parent, at every level (a copy; rel is unchanged).
func Trim(rel source.Related) source.Related {
	if rel == nil {
		return nil
	}
	out := make(source.Related, len(rel))
	for id, set := range rel {
		if set == nil {
			out[id] = nil
			continue
		}
		rows := set.Rows
		if len(rows) > Cap {
			rows = rows[:Cap]
		}
		cp := &source.RelatedSet{Rows: make([]source.RelatedRow, len(rows)), Dangling: set.Dangling}
		for i, r := range rows {
			cp.Rows[i] = source.RelatedRow{Values: r.Values, Sub: Trim(r.Sub)}
		}
		out[id] = cp
	}
	return out
}

// EstimateBytes estimates the full document's size from a preview holding shown of total items
// of the array at path (field names from the root): the document plus the missing items at
// the shown items' average size.
func EstimateBytes(doc []byte, path []string, shown int, total int64) int64 {
	full := int64(len(doc))
	if shown <= 0 || total <= int64(shown) {
		return full
	}
	var v any
	if json.Unmarshal(doc, &v) != nil {
		return full
	}
	for _, name := range path {
		m, ok := v.(map[string]any)
		if !ok {
			return full
		}
		v = m[name]
	}
	arr, ok := v.([]any)
	if !ok || len(arr) == 0 {
		return full
	}
	b, _ := json.Marshal(arr)
	per := float64(len(b)-2) / float64(len(arr)) // without the brackets
	return full + int64(per*float64(total-int64(len(arr))))
}
