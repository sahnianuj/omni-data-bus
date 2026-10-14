package mapping

import (
	"strings"

	"omnidatabus/internal/docschema"
	"omnidatabus/internal/schema"
)

// Grouping issue codes (OC-063).
const (
	// CodeGroupByColumnMissing: a group-by column isn't in the driving table.
	CodeGroupByColumnMissing = "GROUP_BY_COLUMN_MISSING"
	// CodeGroupRowsWithoutGroup: a node is bound to the group's rows, but the driving table
	// isn't grouped.
	CodeGroupRowsWithoutGroup = "GROUP_ROWS_WITHOUT_GROUP"
	// CodeGroupRowsInvalid: more than one group-rows relation, or one not directly under the root.
	CodeGroupRowsInvalid = "GROUP_ROWS_INVALID"
	// CodeRootFieldFirstRow: a root field reads a column that isn't grouped on, so it takes the
	// group's first row (by primary key). A warning: right for values every row shares.
	CodeRootFieldFirstRow = "ROOT_FIELD_FIRST_ROW"
	// CodeArrayRepeatsRoot: a root array is bound to the driving table itself on matching
	// columns, so each document repeats rows of its own table; combining rows is what's meant.
	CodeArrayRepeatsRoot = "ARRAY_REPEATS_ROOT"
	// CodeKeyNotGrouped: the key uses a column that isn't grouped on, so a group's documents
	// would get another key per row.
	CodeKeyNotGrouped = "KEY_NOT_GROUPED"
	// CodeItemReadsParentRow: a field in an array of rows of the parent's own table (the
	// group's rows) reads the parent row's column, so every item repeats it (OC-065).
	CodeItemReadsParentRow = "ITEM_READS_PARENT_ROW"
)

// validateItemsReadOwnRow (OC-065): an array bound to rows of its parent scope's own table —
// the group's rows, or a self relation — must read each row; a field reading the parent's
// column repeats the parent's (first) row in every item.
func validateItemsReadOwnRow(s *Spec) []Issue {
	var out []Issue
	scopes := FieldScopes(s)
	paths := s.DocSchema.Paths()
	tableOf := func(relID string) TableRef {
		if relID == "" {
			if s.Root != nil {
				return s.Root.Table
			}
			return TableRef{}
		}
		if r := s.RelationByID(relID); r != nil {
			return r.Table
		}
		return TableRef{}
	}
	for _, r := range s.Relations {
		if r.LookupOnly || r.TargetFieldID == "" || r.Via != nil || (!r.Group && r.Table != tableOf(r.Parent())) {
			continue
		}
		for _, m := range s.FieldMappings {
			if !m.Active() || scopes[m.TargetFieldID] != r.ID {
				continue
			}
			for _, src := range m.Sources {
				if src.Relation == r.Parent() && src.From == "" {
					out = append(out, Issue{
						Severity: SeverityError, Code: CodeItemReadsParentRow, Path: paths[m.TargetFieldID], FieldID: m.TargetFieldID, FieldMappingID: m.ID, RelationID: r.ID,
						Message: paths[m.TargetFieldID] + " reads " + src.Column + " of the parent row, so every item repeats the same value; read it from each row instead.",
						Fix:     &Fix{Kind: "repoint-source", Label: "Read it from each row", RelationID: r.ID},
					})
					break
				}
			}
		}
	}
	return out
}

// Grouped reports whether the mapping makes one document per group of driving-table rows.
func (s *Spec) Grouped() bool { return s.Composed() && s.Root != nil && len(s.Root.GroupBy) > 0 }

// GroupBy is the group-by columns ("" when not grouped).
func (s *Spec) GroupBy() []string {
	if !s.Grouped() {
		return nil
	}
	return s.Root.GroupBy
}

// GroupRelation is the relation holding a group's rows, or nil.
func (s *Spec) GroupRelation() *Relation {
	for i := range s.Relations {
		if s.Relations[i].Group {
			return &s.Relations[i]
		}
	}
	return nil
}

// SyncGroup makes every group relation a one-to-many from the driving table to itself on the
// group-by columns, directly under the root, so the resolver, scopes and auto-map treat it as
// any relation.
func SyncGroup(s *Spec) {
	if s.Root == nil || s.Root.Table.Name == "" {
		return
	}
	for i := range s.Relations {
		r := &s.Relations[i]
		if !r.Group {
			continue
		}
		r.Table, r.Kind, r.Via, r.ParentID = s.Root.Table, EmbedOneToMany, nil, nil
		r.Join = make([]JoinPair, 0, len(s.Root.GroupBy))
		for _, c := range s.Root.GroupBy {
			r.Join = append(r.Join, JoinPair{Parent: c, Child: c})
		}
	}
}

// GroupedTable is the driving table as a grouped read sees it: the group-by columns are its
// key (key-uniqueness checks use them).
func GroupedTable(s *Spec, t *schema.Table) *schema.Table {
	if !s.Grouped() || t == nil {
		return t
	}
	g := *t
	g.PrimaryKey = append([]string(nil), s.Root.GroupBy...)
	g.UniqueConstraints = nil
	return &g
}

func validateGroup(s *Spec, t *schema.Table) []Issue {
	out := validateItemsReadOwnRow(s)
	groups := 0
	for _, r := range s.Relations {
		if !r.Group {
			continue
		}
		groups++
		switch {
		case !s.Grouped():
			out = append(out, Issue{
				Severity: SeverityError, Code: CodeGroupRowsWithoutGroup, Path: "relations." + r.ID, RelationID: r.ID, FieldID: r.TargetFieldID,
				Message: "This node holds the group's rows, but the driving table isn't grouped: choose “One document per” columns, or bind it to a table.",
			})
		case groups > 1:
			out = append(out, Issue{
				Severity: SeverityError, Code: CodeGroupRowsInvalid, Path: "relations." + r.ID, RelationID: r.ID, FieldID: r.TargetFieldID,
				Message: "Only one array can hold the group's rows.",
			})
		}
	}
	if !s.Grouped() {
		return out
	}
	grouped := map[string]bool{}
	for _, c := range s.Root.GroupBy {
		grouped[c] = true
		if t != nil && t.Column(c) == nil {
			out = append(out, Issue{
				Severity: SeverityError, Code: CodeGroupByColumnMissing, Path: "root.groupBy",
				Message: "Column " + c + " isn't in " + s.Root.Table.Name + "; documents can't be grouped by it.",
			})
		}
	}
	// Root fields reading other columns take the first row of each group.
	scopes := FieldScopes(s)
	paths := s.DocSchema.Paths()
	for _, m := range s.FieldMappings {
		if !m.Active() || scopes[m.TargetFieldID] != "" {
			continue
		}
		var other []string
		for _, src := range m.Sources {
			if src.Relation == "" && !grouped[src.Column] {
				other = append(other, src.Column)
			}
		}
		if len(other) > 0 {
			out = append(out, Issue{
				Severity: SeverityWarning, Code: CodeRootFieldFirstRow, Path: paths[m.TargetFieldID], FieldID: m.TargetFieldID, FieldMappingID: m.ID,
				Message: paths[m.TargetFieldID] + " reads " + strings.Join(other, ", ") + ", which isn't grouped on: it takes each group's first row. Right for a value every row shares; otherwise move the field into the group's rows or use an aggregate.",
			})
		}
	}
	if kt, err := ParseKeyTemplate(s.Key.Template); err == nil {
		var other []string
		for _, c := range kt.Columns() {
			if !grouped[c] {
				other = append(other, c)
			}
		}
		if len(other) > 0 {
			out = append(out, Issue{
				Severity: SeverityError, Code: CodeKeyNotGrouped, Path: "key",
				Message: "The key uses " + strings.Join(other, ", ") + ", which isn't grouped on; use only " + strings.Join(s.Root.GroupBy, ", ") + " (e.g. order::{" + s.Root.GroupBy[0] + "}).",
			})
		}
	}
	return out
}

// combineInstead returns the columns to group by when relation e looks like an attempt to
// collect a group's rows: an array at the root, bound to the driving table itself with every
// join pair on the same column (items ← Order Details on line_no = line_no). Grouping by the
// primary key's leading columns ("Order Details" (order_id, line_no) → order_id) makes one
// document per order with its lines in the array (OC-066). nil when it doesn't apply.
func combineInstead(s *Spec, e Relation, root *schema.Table) []string {
	if !s.Composed() || s.Grouped() || e.Group || e.Parent() != "" || s.Root == nil || e.Table != s.Root.Table || root == nil || len(e.Join) == 0 {
		return nil
	}
	if f := s.DocSchema.Find(e.TargetFieldID); f == nil || f.Type != docschema.Array {
		return nil
	}
	pk := map[string]bool{}
	for _, c := range root.PrimaryKey {
		pk[c] = true
	}
	join := make([]string, 0, len(e.Join))
	inPK := 0
	for _, p := range e.Join {
		if p.Parent != p.Child {
			return nil // a hierarchy (employees.manager_id = employees.id), not a group
		}
		join = append(join, p.Parent)
		if pk[p.Parent] {
			inPK++
		}
	}
	switch {
	case inPK == len(join) && len(join) < len(root.PrimaryKey):
		return join
	case len(root.PrimaryKey) > 1:
		return append([]string(nil), root.PrimaryKey[:len(root.PrimaryKey)-1]...)
	case inPK < len(root.PrimaryKey):
		return join
	}
	return nil
}

// repeatsRootIssue reports ARRAY_REPEATS_ROOT with the combine-rows fix for that array. It's
// an error when the binding can't work as it is (an array bound many-to-one), else a warning.
func repeatsRootIssue(e Relation, path string, cols []string, broken bool) Issue {
	severity := SeverityWarning
	if broken {
		severity = SeverityError
	}
	by := strings.Join(cols, ", ")
	return Issue{
		Severity: severity, Code: CodeArrayRepeatsRoot, Path: path, FieldID: e.TargetFieldID, RelationID: e.ID,
		Message: path + " is bound to " + e.Table.Name + ", the table each document comes from, so every document is one row. To make one document per " + by + " with its rows in " + path + ", combine rows.",
		Fix:     &Fix{Kind: "combine-rows", Label: "Combine rows by " + by, Columns: cols, RelationID: e.ID},
	}
}
