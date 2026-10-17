package mapping

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"omnidatabus/internal/docschema"
)

// SheetRow is one design field of the mapping sheet (OC-061): what fills it, for the customer
// to review the mapping outside the tool.
type SheetRow struct {
	Path      string
	Type      string
	Required  bool
	Example   string
	Source    string
	Transform string
	Constant  string
	Status    string // bound | grouping | mapped | constant | computed | aggregate | suggested | unmapped
	Issues    string
}

// Sheet statuses.
const (
	SheetBound     = "bound"
	SheetGrouping  = "grouping"
	SheetMapped    = "mapped"
	SheetConstant  = "constant"
	SheetComputed  = "computed" // a template over the document's fields (OC-136)
	SheetAggregate = "aggregate"
	SheetSuggested = "suggested"
	SheetUnmapped  = "unmapped"
)

var sheetHeader = []string{"path", "type", "required", "example", "source", "transform", "constant", "status", "issues"}

// Sheet lists every design field in tree order (array items are shown through their array)
// with its source, transforms and status, and the codes of the issues on it.
func Sheet(s *Spec, driving TableRef, issues []Issue) []SheetRow {
	active, pending := map[string]*FieldMapping{}, map[string]*FieldMapping{}
	for i := range s.FieldMappings {
		m := &s.FieldMappings[i]
		switch m.State {
		case StateActive:
			active[m.TargetFieldID] = m
		case StatePending:
			pending[m.TargetFieldID] = m
		}
	}
	bound := map[string]*Relation{}
	for i := range s.Relations {
		if r := &s.Relations[i]; !r.LookupOnly && r.TargetFieldID != "" {
			bound[r.TargetFieldID] = r
		}
	}
	aggs := map[string]*Aggregate{}
	for i := range s.Aggregates {
		aggs[s.Aggregates[i].TargetFieldID] = &s.Aggregates[i]
	}
	codes := map[string][]string{}
	paths := s.DocSchema.Paths()
	for _, is := range issues {
		p := is.NodePath
		if is.FieldID != "" {
			p = paths[is.FieldID]
		}
		if p == "" {
			p = is.Path
		}
		codes[p] = append(codes[p], is.Code)
	}
	tableOf := func(ref SourceRef) string {
		if ref.Relation == "" {
			return driving.Name
		}
		r := s.RelationByID(ref.Relation)
		switch {
		case r == nil:
			return ref.Relation
		case ref.From == "via" && r.Via != nil:
			return r.Via.Table.Name
		}
		return r.Table.Name
	}
	var rows []SheetRow
	s.DocSchema.Walk(func(f *docschema.DocField, path string, _ *docschema.DocField) {
		if f.Name == "[]" && strings.HasSuffix(path, "[]") {
			return
		}
		row := SheetRow{Path: path, Type: string(f.Type), Required: f.Required}
		if f.Type == docschema.Array && f.Items != nil {
			row.Type = "array<" + string(f.Items.Type) + ">"
		}
		if f.Format != "" {
			row.Type += " (" + f.Format + ")"
		}
		if len(f.Example) > 0 {
			var v any
			if json.Unmarshal(f.Example, &v) == nil {
				row.Example = fmt.Sprint(v)
			}
		}
		switch r, m := bound[f.ID], active[f.ID]; {
		case r != nil:
			row.Status, row.Source = SheetBound, r.Table.Name+" ("+r.Kind+")"
			if r.Via != nil {
				row.Source += " via " + r.Via.Table.Name
			}
			if a := adapterOf(f); a != "" {
				row.Source += ", " + a
			}
			if r.ValueColumn != "" {
				row.Source += ", values " + r.ValueColumn
			}
		case aggs[f.ID] != nil:
			a := aggs[f.ID]
			row.Status = SheetAggregate
			target := ""
			if r := s.RelationByID(a.RelationID); r != nil {
				target = r.Table.Name
			}
			if a.Column != "" {
				target += "." + a.Column
			}
			row.Source = a.Fn + "(" + target + ")"
		case m != nil && m.Computed != nil:
			row.Status = SheetComputed
			row.Source = m.Computed.Lang + ": " + m.Computed.Expr
			var steps []string
			for _, st := range m.Transforms {
				steps = append(steps, st.Op)
			}
			row.Transform = strings.Join(steps, " → ")
		case m != nil:
			row.Status = SheetMapped
			var srcs, steps []string
			for _, ref := range m.Sources {
				srcs = append(srcs, tableOf(ref)+"."+ref.Column)
			}
			for _, st := range m.Transforms {
				switch st.Op {
				case "constant":
					b, _ := json.Marshal(st.Args["value"])
					row.Constant = string(b)
				case "expression":
					steps = append(steps, "expression: "+fmt.Sprint(st.Args["expr"]))
				default:
					steps = append(steps, st.Op)
				}
			}
			row.Source, row.Transform = strings.Join(srcs, " + "), strings.Join(steps, " → ")
			if len(m.Sources) == 0 && row.Constant != "" {
				row.Status = SheetConstant
			}
		case pending[f.ID] != nil:
			row.Status = SheetSuggested
			for _, ref := range pending[f.ID].Sources {
				row.Source = tableOf(ref) + "." + ref.Column
			}
		case f.Type == docschema.Object:
			row.Status = SheetGrouping
		default:
			row.Status = SheetUnmapped
		}
		if c := codes[path]; len(c) > 0 {
			sort.Strings(c)
			row.Issues = strings.Join(c, ", ")
		}
		rows = append(rows, row)
	})
	return rows
}

func (r SheetRow) cells() []string {
	req := ""
	if r.Required {
		req = "yes"
	}
	return []string{r.Path, r.Type, req, r.Example, r.Source, r.Transform, r.Constant, r.Status, r.Issues}
}

// WriteSheetCSV writes the sheet as CSV with a header row.
func WriteSheetCSV(w io.Writer, rows []SheetRow) error {
	cw := csv.NewWriter(w)
	if err := cw.Write(sheetHeader); err != nil {
		return err
	}
	for _, r := range rows {
		if err := cw.Write(r.cells()); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// WriteSheetMarkdown writes the sheet as a Markdown table under a title.
func WriteSheetMarkdown(w io.Writer, title string, rows []SheetRow) error {
	esc := func(s string) string { return strings.ReplaceAll(strings.ReplaceAll(s, "|", `\|`), "\n", " ") }
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n| %s |\n|%s\n", esc(title), strings.Join(sheetHeader, " | "), strings.Repeat("---|", len(sheetHeader)))
	for _, r := range rows {
		cells := r.cells()
		for i := range cells {
			cells[i] = esc(cells[i])
			if i == 0 || i == 4 {
				if cells[i] != "" {
					cells[i] = "`" + cells[i] + "`"
				}
			}
		}
		fmt.Fprintf(&b, "| %s |\n", strings.Join(cells, " | "))
	}
	_, err := io.WriteString(w, b.String())
	return err
}
