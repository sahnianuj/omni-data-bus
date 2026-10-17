package transform

import (
	"fmt"
	"testing"
	"time"

	"omnidatabus/internal/docschema"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/race"
	"omnidatabus/internal/schema"
)

// bench25 is a 25-field mapping: 20 direct fields and 5 with one transform step each.
func bench25() (*Program, []any) {
	t, s, row := bench25Parts()
	p, issues := Compile(s, t, Options{})
	if len(issues) > 0 {
		panic(fmt.Sprint(issues))
	}
	return p, row
}

func benchSpec25() *mapping.Spec {
	_, s, _ := bench25Parts()
	return s
}

func bench25Parts() (*schema.Table, *mapping.Spec, []any) {
	t := &schema.Table{Name: "events", PrimaryKey: []string{"c00"}}
	s := &mapping.Spec{Key: mapping.KeySpec{Template: "event::{c00}"}, TypeField: mapping.TypeField{Enabled: true, Name: "type", Value: "event"}}
	s.DocSchema.Root = docschema.NewRoot()
	row := make([]any, 25)
	steps := []mapping.Step{{Op: "trim"}, {Op: "upper"}, {Op: "lower"}, {Op: "nullIf", Args: map[string]any{"value": ""}}, {Op: "default", Args: map[string]any{"value": "n/a"}}}
	for i := range 25 {
		name := fmt.Sprintf("c%02d", i)
		var c schema.Column
		switch i % 3 {
		case 0:
			c = schema.Column{Name: name, UDTName: "int8", JSONType: schema.TypeInteger}
			row[i] = int64(1000 + i)
		case 1:
			c = schema.Column{Name: name, UDTName: "text", JSONType: schema.TypeString}
			row[i] = fmt.Sprintf("  value %d  ", i)
		default:
			c = schema.Column{Name: name, UDTName: "bool", JSONType: schema.TypeBoolean}
			row[i] = i%2 == 0
		}
		t.Columns = append(t.Columns, c)
		ft, _ := docschema.FieldTypeOf(c)
		f := docschema.DocField{ID: "f" + name, Name: "field" + name, Type: ft}
		s.DocSchema.Root.Children = append(s.DocSchema.Root.Children, f)
		m := mapping.FieldMapping{ID: "m" + name, TargetFieldID: f.ID, Sources: []mapping.SourceRef{{Column: name}}, State: mapping.StateActive}
		if i%3 == 1 && i/3 < 5 {
			m.Transforms = []mapping.Step{steps[i/3]}
		}
		s.FieldMappings = append(s.FieldMappings, m)
	}
	s.Normalize()
	return t, s, row
}

// AC7: ≥ 300k rows/s on one core, ≤ 3 allocations per row beyond the output buffer.
func BenchmarkApply_AC7_25Fields(b *testing.B) {
	p, row := bench25()
	buf := make([]byte, 0, 4096)
	b.ReportAllocs()
	for b.Loop() {
		_, buf, _, _ = p.Apply(row, buf)
	}
	b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "rows/s")
}

func TestApply_AC7_Budget(t *testing.T) {
	if race.Enabled || testing.CoverMode() != "" {
		t.Skip("timing budgets need uninstrumented code: run `make test-budgets`")
	}
	p, row := bench25()
	buf := make([]byte, 0, 4096)
	allocs := testing.AllocsPerRun(1000, func() { _, buf, _, _ = p.Apply(row, buf) })
	res := testing.Benchmark(func(b *testing.B) {
		for b.Loop() {
			_, buf, _, _ = p.Apply(row, buf)
		}
	})
	rate := float64(res.N) / res.T.Seconds()
	t.Logf("%.0f rows/s, %.1f allocs/row", rate, allocs)
	if rate < 300_000 {
		t.Fatalf("%.0f rows/s; the budget is 300k (%s/row)", rate, time.Duration(res.NsPerOp()))
	}
	if allocs > 3 {
		t.Fatalf("%.1f allocations per row; the budget is 3", allocs)
	}
}

// bench25Templates is bench25 plus 3 template computed fields (OC-136 AC7).
func bench25Templates() (*Program, []any) {
	p, row := bench25()
	s := benchSpec25()
	for i, expr := range []string{"${doc.fieldc01}-${doc.fieldc00}", "EVT-${doc.fieldc03}", "${doc.fieldc04} / ${doc.fieldc07}"} {
		id := fmt.Sprintf("fcomp%d", i)
		s.DocSchema.Root.Children = append(s.DocSchema.Root.Children, docschema.DocField{ID: id, Name: fmt.Sprintf("computed%d", i), Type: docschema.String})
		s.FieldMappings = append(s.FieldMappings, mapping.FieldMapping{ID: "mc" + id, TargetFieldID: id, State: mapping.StateActive, Computed: &mapping.Computed{Lang: mapping.ComputedTemplate, Expr: expr}})
	}
	s.Normalize()
	cp, issues := Compile(s, &schema.Table{Name: "events", PrimaryKey: []string{"c00"}, Columns: p.cols}, Options{})
	if len(issues) > 0 {
		panic(fmt.Sprint(issues))
	}
	return cp, row
}

// AC7 (OC-136): 3 template fields keep ≥ 250k rows/s on one core.
func BenchmarkApply_AC7_ThreeTemplates(b *testing.B) {
	p, row := bench25Templates()
	buf := make([]byte, 0, 4096)
	b.ReportAllocs()
	for b.Loop() {
		_, buf, _, _ = p.Apply(row, buf)
	}
	b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "rows/s")
}

// TestApply_AC7_TemplatesBudget (OC-136 AC7): the 25-field mapping plus 3 template fields keeps
// ≥ 250k rows/s on one core.
func TestApply_AC7_TemplatesBudget(t *testing.T) {
	if race.Enabled || testing.CoverMode() != "" {
		t.Skip("timing budgets need uninstrumented code: run `make test-budgets`")
	}
	p, row := bench25Templates()
	buf := make([]byte, 0, 4096)
	res := testing.Benchmark(func(b *testing.B) {
		for b.Loop() {
			_, buf, _, _ = p.Apply(row, buf)
		}
	})
	rate := float64(res.N) / res.T.Seconds()
	t.Logf("%.0f rows/s with 3 templates", rate)
	if rate < 250_000 {
		t.Fatalf("%.0f rows/s; the budget is 250k (%s/row)", rate, time.Duration(res.NsPerOp()))
	}
}
