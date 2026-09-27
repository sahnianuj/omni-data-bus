// Package discovery runs schema discovery on a project's source repository, stores the
// snapshots and reports drift between them (OC-010).
package discovery

import (
	"sort"

	"omnidatabus/internal/schema"
)

// Diff is what changed between two snapshots. Tables are "schema.table".
type Diff struct {
	AddedTables   []string      `json:"addedTables"`
	RemovedTables []string      `json:"removedTables"`
	ChangedTables []TableChange `json:"changedTables"`
}

// TableChange lists a table's column changes.
type TableChange struct {
	Table          string       `json:"table"`
	AddedColumns   []string     `json:"addedColumns"`
	RemovedColumns []string     `json:"removedColumns"`
	TypeChanged    []TypeChange `json:"typeChanged"`
}

// TypeChange is a column whose declared type changed, e.g. integer → bigint.
type TypeChange struct {
	Column string `json:"column"`
	From   string `json:"from"`
	To     string `json:"to"`
}

// Empty reports whether nothing changed.
func (d Diff) Empty() bool {
	return len(d.AddedTables) == 0 && len(d.RemovedTables) == 0 && len(d.ChangedTables) == 0
}

// TablesChanged counts added, removed and changed tables (the "N tables changed" toast).
func (d Diff) TablesChanged() int {
	return len(d.AddedTables) + len(d.RemovedTables) + len(d.ChangedTables)
}

// Compare computes the drift from prev to next. Output lists are sorted and never nil.
func Compare(prev, next *schema.Snapshot) Diff {
	d := Diff{AddedTables: []string{}, RemovedTables: []string{}, ChangedTables: []TableChange{}}
	before, after := tables(prev), tables(next)
	for name, nt := range after {
		pt, ok := before[name]
		if !ok {
			d.AddedTables = append(d.AddedTables, name)
			continue
		}
		if ch, changed := compareTable(name, pt, nt); changed {
			d.ChangedTables = append(d.ChangedTables, ch)
		}
	}
	for name := range before {
		if _, ok := after[name]; !ok {
			d.RemovedTables = append(d.RemovedTables, name)
		}
	}
	sort.Strings(d.AddedTables)
	sort.Strings(d.RemovedTables)
	sort.Slice(d.ChangedTables, func(i, j int) bool { return d.ChangedTables[i].Table < d.ChangedTables[j].Table })
	return d
}

func tables(s *schema.Snapshot) map[string]*schema.Table {
	out := map[string]*schema.Table{}
	if s == nil {
		return out
	}
	for i := range s.Schemas {
		for j := range s.Schemas[i].Tables {
			t := &s.Schemas[i].Tables[j]
			out[s.Schemas[i].Name+"."+t.Name] = t
		}
	}
	return out
}

func compareTable(name string, prev, next *schema.Table) (TableChange, bool) {
	ch := TableChange{Table: name, AddedColumns: []string{}, RemovedColumns: []string{}, TypeChanged: []TypeChange{}}
	before := map[string]schema.Column{}
	for _, c := range prev.Columns {
		before[c.Name] = c
	}
	after := map[string]bool{}
	for _, c := range next.Columns {
		after[c.Name] = true
		old, ok := before[c.Name]
		switch {
		case !ok:
			ch.AddedColumns = append(ch.AddedColumns, c.Name)
		case old.DataType != c.DataType:
			ch.TypeChanged = append(ch.TypeChanged, TypeChange{Column: c.Name, From: old.DataType, To: c.DataType})
		}
	}
	for _, c := range prev.Columns {
		if !after[c.Name] {
			ch.RemovedColumns = append(ch.RemovedColumns, c.Name)
		}
	}
	sort.Strings(ch.AddedColumns)
	sort.Strings(ch.RemovedColumns)
	sort.Slice(ch.TypeChanged, func(i, j int) bool { return ch.TypeChanged[i].Column < ch.TypeChanged[j].Column })
	changed := len(ch.AddedColumns)+len(ch.RemovedColumns)+len(ch.TypeChanged) > 0
	return ch, changed
}
