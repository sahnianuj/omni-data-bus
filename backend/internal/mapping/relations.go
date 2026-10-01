package mapping

import (
	"fmt"
	"sort"
	"sync/atomic"
)

// Relation tree issue codes (OC-037).
const (
	CodeRelationCycle         = "RELATION_CYCLE"
	CodeRelationParentMissing = "RELATION_PARENT_MISSING"
	CodeRelationTooDeep       = "RELATION_TOO_DEEP"
)

// Relation depth: OC_MAX_RELATION_DEPTH, default 4, never more than 6 (architecture §6.1).
const (
	DefaultMaxRelationDepth = 4
	MaxRelationDepthCap     = 6
)

var maxRelationDepth atomic.Int32

func init() { maxRelationDepth.Store(DefaultMaxRelationDepth) }

// SetMaxRelationDepth sets the depth limit (OC_MAX_RELATION_DEPTH), clamped to 1..6.
func SetMaxRelationDepth(n int) {
	maxRelationDepth.Store(int32(min(max(n, 1), MaxRelationDepthCap))) //nolint:gosec // clamped to 1..6
}

// MaxRelationDepth is the configured depth limit.
func MaxRelationDepth() int { return int(maxRelationDepth.Load()) }

// Depths returns each relation's depth (1 = a child of the root) in topological order
// (parents before children, then by ID). It fails on a missing parent or a cycle.
func Depths(s *Spec) (order []string, depth map[string]int, issue *Issue) {
	byID := map[string]*Relation{}
	for i := range s.Relations {
		byID[s.Relations[i].ID] = &s.Relations[i]
	}
	depth = map[string]int{}
	state := map[string]int{} // 1 visiting, 2 done
	var visit func(id string, path []string) *Issue
	visit = func(id string, path []string) *Issue {
		switch state[id] {
		case 2:
			return nil
		case 1:
			return &Issue{
				Severity: SeverityError, Code: CodeRelationCycle, Path: "relations." + id,
				Message: fmt.Sprintf("Relations form a cycle: %v → %s.", path, id),
			}
		}
		state[id] = 1
		r := byID[id]
		d := 1
		if p := r.Parent(); p != "" {
			if byID[p] == nil {
				return &Issue{
					Severity: SeverityError, Code: CodeRelationParentMissing, Path: "relations." + id,
					Message: "Relation " + id + " has an unknown parent " + p + ".",
				}
			}
			if is := visit(p, append(path, id)); is != nil {
				return is
			}
			d = depth[p] + 1
		}
		depth[id] = d
		state[id] = 2
		order = append(order, id)
		return nil
	}
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if is := visit(id, nil); is != nil {
			return nil, nil, is
		}
	}
	// Stable topological order: by depth, then ID (independent of the spec's list order).
	sort.SliceStable(order, func(i, j int) bool {
		if depth[order[i]] != depth[order[j]] {
			return depth[order[i]] < depth[order[j]]
		}
		return order[i] < order[j]
	})
	return order, depth, nil
}

// ValidateRelationTree checks the v2 structure: parents exist, no cycles, and depth within the
// limit (OC-037). ValidateRelations checks each relation's own settings.
func ValidateRelationTree(s *Spec) []Issue {
	var out []Issue
	_, depth, is := Depths(s)
	if is != nil {
		return []Issue{*is}
	}
	limit := MaxRelationDepth()
	for _, r := range s.Relations {
		if depth[r.ID] > limit {
			out = append(out, Issue{
				Severity: SeverityError, Code: CodeRelationTooDeep, Path: "relations." + r.ID,
				Message: fmt.Sprintf("Relation %s is %d levels deep; the limit is %d.", r.ID, depth[r.ID], limit),
			})
		}
	}
	return out
}
