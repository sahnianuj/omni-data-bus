package plan

import (
	"fmt"

	"omnidatabus/internal/mapping"
	"omnidatabus/internal/related"
	"omnidatabus/internal/schema"
)

// Auto strategy thresholds (OC-046): sorted-merge pays off for high fan-out relations.
const (
	SortedMergeRowsPerParent = 200
	SortedMergeChildRows     = 50_000_000
)

// chooseStrategy resolves a relation's strategy (OC-046): batched, or sorted-merge for a
// one-to-many or many-to-many relation directly under the root when auto finds more than 200
// estimated rows per parent or more than 50M child rows, and the relation can be merged: it
// joins on the root's single-column primary key (the order the root is read in), both key
// types sort the same in Go and PostgreSQL, and the child's join column leads an index.
// An explicit strategy is kept when it can run; note says why the strategy was chosen.
func chooseStrategy(e mapping.Relation, depth int, root *schema.Table, rel mapping.RelatedTables) (strategy, note string) {
	want := e.Strategy
	if want == "" {
		want = mapping.StrategyAuto
	}
	if want == mapping.StrategyBatched || !e.Many() {
		return StrategyBatched, ""
	}
	fan, parentCol, childCol := mergeSides(e, rel)
	if depth > 1 {
		if want == mapping.StrategySortedMerge || highFanout(root, fan) {
			return StrategyBatched, fmt.Sprintf("%s is nested, so it's read with batched lookups (sorted-merge reads only relations directly under the root).", e.Table)
		}
		return StrategyBatched, ""
	}
	why := mergeBlocker(root, fan, parentCol, childCol)
	if want == mapping.StrategySortedMerge {
		if why != "" {
			return StrategyBatched, "Sorted-merge isn't possible (" + why + "); reading with batched lookups."
		}
		return StrategySortedMerge, mergeNote(e, root, parentCol)
	}
	if !highFanout(root, fan) {
		return StrategyBatched, ""
	}
	if why != "" {
		return StrategyBatched, fmt.Sprintf("%s has many rows per %s row, but sorted-merge isn't possible (%s); reading with batched lookups.", e.Table, root.Name, why)
	}
	return StrategySortedMerge, mergeNote(e, root, parentCol)
}

func mergeNote(e mapping.Relation, root *schema.Table, parentCol string) string {
	return fmt.Sprintf("Reads %s alongside %s, sorted by %s.", e.Table.Name, root.Name, parentCol)
}

// mergeSides are what a merge compares: the table whose rows fan out per parent (the junction
// for many-to-many) and the join columns.
func mergeSides(e mapping.Relation, rel mapping.RelatedTables) (fan *schema.Table, parentCol, childCol string) {
	if rel == nil {
		return nil, "", ""
	}
	fan = rel(e.Table.Schema, e.Table.Name)
	switch {
	case e.Kind == mapping.RelationManyToMany && e.Via != nil:
		fan = rel(e.Via.Table.Schema, e.Via.Table.Name)
		if len(e.Via.ParentJoin) == 1 {
			parentCol, childCol = e.Via.ParentJoin[0].Parent, e.Via.ParentJoin[0].Via
		}
	case len(e.Join) == 1:
		parentCol, childCol = e.Join[0].Parent, e.Join[0].Child
	}
	return fan, parentCol, childCol
}

func highFanout(root, fan *schema.Table) bool {
	if root == nil || fan == nil || fan.RowEstimate == nil {
		return false
	}
	if *fan.RowEstimate > SortedMergeChildRows {
		return true
	}
	return root.RowEstimate != nil && *root.RowEstimate > 0 && float64(*fan.RowEstimate)/float64(*root.RowEstimate) > SortedMergeRowsPerParent
}

// mergeBlocker says why a relation can't be sorted-merged ("" when it can).
func mergeBlocker(root, fan *schema.Table, parentCol, childCol string) string {
	switch {
	case root == nil || fan == nil:
		return "the tables aren't in the snapshot"
	case parentCol == "":
		return "it joins on several columns"
	case len(root.PrimaryKey) != 1 || root.PrimaryKey[0] != parentCol:
		return fmt.Sprintf("it doesn't join on %s's primary key", root.Name)
	}
	pc, cc := root.Column(parentCol), fan.Column(childCol)
	switch {
	case pc == nil || cc == nil:
		return "a join column is missing"
	case !related.MergeableType(pc.UDTName) || !related.MergeableType(cc.UDTName):
		return fmt.Sprintf("%s keys can't be merged in order", pc.UDTName)
	case !Indexed(fan, childCol):
		return fmt.Sprintf("%s.%s has no index", fan.Name, childCol)
	}
	return ""
}

// Indexed reports whether column leads one of the table's indexes (or its primary key).
func Indexed(t *schema.Table, column string) bool { return mapping.LeadingIndex(t, []string{column}) }
