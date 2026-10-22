package transform

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/docschema"
	"omnidatabus/internal/mapping"
)

// computedFixture is embedFixture (customer, its address object and orders array) with extra
// design fields and computed mappings on them.
func computedFixture(t *testing.T, extra func(s *mapping.Spec)) (*Program, []mapping.Issue) {
	t.Helper()
	spec, customer, rel := embedFixture(mapping.NullInclude, 1000, "")
	extra(spec)
	spec.Normalize()
	return Compile(spec, customer, Options{Related: rel})
}

func tmpl(id, field, expr string) mapping.FieldMapping {
	return mapping.FieldMapping{ID: id, TargetFieldID: field, State: mapping.StateActive, Transforms: []mapping.Step{}, Computed: &mapping.Computed{Lang: mapping.ComputedTemplate, Expr: expr}}
}

func addField(s *mapping.Spec, parent string, f docschema.DocField) {
	var add func(n *docschema.DocField) bool
	add = func(n *docschema.DocField) bool {
		if n.ID == parent {
			n.Children = append(n.Children, f)
			return true
		}
		for i := range n.Children {
			if add(&n.Children[i]) {
				return true
			}
		}
		return n.Items != nil && add(n.Items)
	}
	add(&s.DocSchema.Root)
}

var computedRel = map[string][][]any{
	"emb_1": {{int32(3), "Lyon"}},
	"emb_2": {{int64(30), int32(7), "2026-03-01"}, {int64(20), int32(7), "2026-02-01"}},
}

func applyComputed(t *testing.T, p *Program) string {
	t.Helper()
	_, doc, _, rerr := p.ApplyRelated([]any{int32(7), int32(3)}, computedRel, nil)
	require.Nil(t, rerr)
	return string(doc)
}

// AC1: a template reads target values after their transforms, in an embedded object and at the
// root, and the field keeps its place in the design.
func TestComputed_AC1_TemplateReadsTargetValues(t *testing.T) {
	p, issues := computedFixture(t, func(s *mapping.Spec) {
		s.FieldMappings[1].Transforms = []mapping.Step{{Op: "upper"}} // address.city ← upper(city)
		addField(s, "f_addr", docschema.DocField{ID: "f_line", Name: "line", Type: docschema.String})
		s.DocSchema.Root.Children = append([]docschema.DocField{{ID: "f_ref", Name: "ref", Type: docschema.String}}, s.DocSchema.Root.Children...)
		s.FieldMappings = append(s.FieldMappings, tmpl("c1", "f_line", "${self.city}, France"), tmpl("c2", "f_ref", "CUST-${doc.id}"))
	})
	require.NotNil(t, p, "%v", issues)
	require.Equal(t, `{"ref":"CUST-7","id":7,"address":{"city":"LYON","line":"LYON, France"},"orders":[{"orderId":30,"date":"2026-03-01"},{"orderId":20,"date":"2026-02-01"}]}`, applyComputed(t, p))
}

// AC2: self, parent and index in an array's items.
func TestComputed_AC2_SelfParentIndexInArrays(t *testing.T) {
	p, issues := computedFixture(t, func(s *mapping.Spec) {
		addField(s, "f_item", docschema.DocField{ID: "f_label", Name: "label", Type: docschema.String})
		s.FieldMappings = append(s.FieldMappings, tmpl("c1", "f_label", "${index}: order ${self.orderId} of ${parent.id} (${parent.address.city})"))
	})
	require.NotNil(t, p, "%v", issues)
	require.JSONEq(t, `{"id":7,"address":{"city":"Lyon"},"orders":[
		{"orderId":30,"date":"2026-03-01","label":"0: order 30 of 7 (Lyon)"},
		{"orderId":20,"date":"2026-02-01","label":"1: order 20 of 7 (Lyon)"}]}`, applyComputed(t, p))
}

// AC3: a field that reads another computed field runs after it, including a container whose
// items hold computed fields; a cycle is a compile error naming the fields.
func TestComputed_AC3_TopologicalOrder(t *testing.T) {
	p, issues := computedFixture(t, func(s *mapping.Spec) {
		s.DocSchema.Root.Children = append(s.DocSchema.Root.Children,
			docschema.DocField{ID: "f_b", Name: "b", Type: docschema.String}, docschema.DocField{ID: "f_a", Name: "a", Type: docschema.String})
		s.FieldMappings = append(s.FieldMappings, tmpl("cb", "f_b", "${doc.a}!"), tmpl("ca", "f_a", "A${doc.id}"))
	})
	require.NotNil(t, p, "%v", issues)
	require.Contains(t, applyComputed(t, p), `"b":"A7!","a":"A7"`)
}

func TestComputed_AC3_ContainerDependsOnInnerComputed(t *testing.T) {
	p, issues := computedFixture(t, func(s *mapping.Spec) {
		s.DocSchema.Root.Children = append([]docschema.DocField{{ID: "f_sum", Name: "summary", Type: docschema.String}}, s.DocSchema.Root.Children...)
		addField(s, "f_item", docschema.DocField{ID: "f_tag", Name: "tag", Type: docschema.String})
		s.FieldMappings = append(s.FieldMappings, tmpl("cs", "f_sum", "${doc.orders}"), tmpl("ct", "f_tag", "#${self.orderId}"))
	})
	require.NotNil(t, p, "%v", issues)
	require.Contains(t, applyComputed(t, p), `"summary":"[{\"orderId\":30,\"date\":\"2026-03-01\",\"tag\":\"#30\"}`)
}

func TestCompile_AC3_ComputedCycle(t *testing.T) {
	p, issues := computedFixture(t, func(s *mapping.Spec) {
		s.DocSchema.Root.Children = append(s.DocSchema.Root.Children,
			docschema.DocField{ID: "f_a", Name: "a", Type: docschema.String}, docschema.DocField{ID: "f_b", Name: "b", Type: docschema.String})
		s.FieldMappings = append(s.FieldMappings, tmpl("ca", "f_a", "${doc.b}"), tmpl("cb", "f_b", "${doc.a}"))
	})
	require.Nil(t, p)
	require.Len(t, issues, 1)
	require.Equal(t, CodeComputedCycle, issues[0].Code)
	require.Contains(t, issues[0].Message, "a → b → a")
}

// OC-192: a field that reads itself, named like a column of the row, is told to read the column.
func TestCompile_OC192_ReadsItselfSuggestsRow(t *testing.T) {
	p, issues := computedFixture(t, func(s *mapping.Spec) {
		var keep []mapping.FieldMapping
		for _, m := range s.FieldMappings {
			if m.TargetFieldID != "f_id" {
				keep = append(keep, m)
			}
		}
		s.FieldMappings = append(keep, tmpl("c", "f_id", "paid_in_out::${doc.customer_id}"))
		s.DocSchema.Root.Children[0].Name = "customer_id" // the row has a customer_id column
	})
	require.Nil(t, p)
	require.Len(t, issues, 1)
	require.Equal(t, CodeComputedCycle, issues[0].Code)
	require.Equal(t, "customer_id reads itself. To read the source row's customer_id column, write ${row.customer_id}.", issues[0].Message)
}

// AC4: static checks with positions.
func TestCompile_AC4_ComputedIssues(t *testing.T) {
	for _, c := range []struct {
		name, field, expr, code string
		line, col               int
		lang                    string
		sources                 bool
	}{
		{"unknown field", "f_x", "${doc.fistName}", CodeComputedUnknownField, 1, 3, "", false},
		{"unknown on line 2", "f_x", "a\nb ${ doc.nope }", CodeComputedUnknownField, 2, 6, "", false},
		{"parent at the root", "f_x", "${parent.id}", CodeComputedOutOfScope, 1, 3, "", false},
		{"index outside arrays", "f_x", "${index}", CodeComputedOutOfScope, 1, 3, "", false},
		{"row in a relation", "f_label", "${row.order_id}", CodeComputedOutOfScope, 1, 3, "", false},
		{"row column missing", "f_x", "${row.nope}", CodeComputedOutOfScope, 1, 3, "", false},
		{"an operator", "f_x", "${doc.id + 1}", CodeComputedTemplateSyntax, 1, 3, "", false},
		{"a call", "f_x", "${doc.id.toString()}", CodeComputedTemplateSyntax, 1, 3, "", false},
		{"unclosed", "f_x", "x ${doc.id", CodeComputedTemplateSyntax, 1, 5, "", false},
		{"with sources", "f_x", "${doc.id}", CodeComputedWithSources, 0, 0, "", true},
		{"javascript", "f_x", "doc.id", CodeComputedLangUnsupported, 0, 0, mapping.ComputedJS, false},
	} {
		p, issues := computedFixture(t, func(s *mapping.Spec) {
			s.DocSchema.Root.Children = append(s.DocSchema.Root.Children, docschema.DocField{ID: "f_x", Name: "x", Type: docschema.String})
			addField(s, "f_item", docschema.DocField{ID: "f_label", Name: "label", Type: docschema.String})
			m := tmpl("c", c.field, c.expr)
			if c.lang != "" {
				m.Computed.Lang = c.lang
			}
			if c.sources {
				m.Sources = []mapping.SourceRef{{Column: "customer_id"}}
			}
			s.FieldMappings = append(s.FieldMappings, m)
		})
		require.Nil(t, p, c.name)
		require.Len(t, issues, 1, "%s: %v", c.name, issues)
		require.Equal(t, c.code, issues[0].Code, c.name)
		require.Equal(t, c.line, issues[0].Line, c.name)
		require.Equal(t, c.col, issues[0].Column, c.name)
		require.NotEmpty(t, issues[0].FieldID, c.name)
	}
}

// row reads the driving row's columns at the root, mapped or not.
func TestComputed_RowAtTheRoot(t *testing.T) {
	p, issues := computedFixture(t, func(s *mapping.Spec) {
		s.DocSchema.Root.Children = append(s.DocSchema.Root.Children, docschema.DocField{ID: "f_x", Name: "x", Type: docschema.String})
		s.FieldMappings = append(s.FieldMappings, tmpl("c", "f_x", "addr ${row.address_id}"))
	})
	require.NotNil(t, p, "%v", issues)
	require.Contains(t, applyComputed(t, p), `"x":"addr 3"`)
}

// AC5: missing values render empty; nullIfAnyMissing makes the field null (and omitted with
// null handling omit); a result over 1 MB follows onError.
func TestComputed_AC5_NullRendering(t *testing.T) {
	// address.zip is designed but unmapped (null): it renders as an empty string.
	p, issues := computedFixture(t, func(s *mapping.Spec) {
		addField(s, "f_addr", docschema.DocField{ID: "f_zip", Name: "zip", Type: docschema.String})
		s.DocSchema.Root.Children = append(s.DocSchema.Root.Children, docschema.DocField{ID: "f_x", Name: "x", Type: docschema.String})
		s.FieldMappings = append(s.FieldMappings, tmpl("c", "f_x", "${doc.id}-${doc.address.zip}"))
	})
	require.NotNil(t, p, "%v", issues)
	require.Contains(t, applyComputed(t, p), `"x":"7-"`)
}

func TestComputed_AC5_NullIfAnyMissingAndOmit(t *testing.T) {
	build := func(nullIfMissing bool, nullHandling string) string {
		p, issues := computedFixture(t, func(s *mapping.Spec) {
			s.NullHandling = nullHandling
			addField(s, "f_addr", docschema.DocField{ID: "f_zip", Name: "zip", Type: docschema.String})
			s.DocSchema.Root.Children = append(s.DocSchema.Root.Children, docschema.DocField{ID: "f_x", Name: "x", Type: docschema.String})
			m := tmpl("c", "f_x", "${doc.id}-${doc.address.zip}")
			m.Computed.NullIfAnyMissing = nullIfMissing
			s.FieldMappings = append(s.FieldMappings, m)
		})
		require.NotNil(t, p, "%v", issues)
		return applyComputed(t, p)
	}
	require.Contains(t, build(true, mapping.NullInclude), `"x":null`)
	require.NotContains(t, build(true, mapping.NullOmit), `"x"`)
}

func TestComputed_AC5_OnErrorPolicies(t *testing.T) {
	huge := strings.Repeat("x", maxComputedBytes+1)
	for policy, want := range map[string]string{mapping.OnErrorNull: `"x":null`, mapping.OnErrorOmit: ``, "": "fail"} {
		p, issues := computedFixture(t, func(s *mapping.Spec) {
			s.DocSchema.Root.Children = append(s.DocSchema.Root.Children, docschema.DocField{ID: "f_x", Name: "x", Type: docschema.String})
			m := tmpl("c", "f_x", huge)
			m.Computed.OnError = policy
			s.FieldMappings = append(s.FieldMappings, m)
		})
		require.NotNil(t, p, "%v", issues)
		_, doc, _, rerr := p.ApplyRelated([]any{int32(7), int32(3)}, computedRel, nil)
		switch want {
		case "fail":
			require.NotNil(t, rerr)
			require.Equal(t, CodeComputedTooLarge, rerr.Code)
			require.Equal(t, "x", rerr.FieldPath)
		case "":
			require.Nil(t, rerr)
			require.NotContains(t, string(doc), `"x"`)
		default:
			require.Nil(t, rerr)
			require.Contains(t, string(doc), want)
		}
	}
}

// The field's transform pipeline runs on the template's value; a placeholder keeps the design's
// order even when unmapped fields are omitted.
func TestComputed_TransformsAndOmitUnmapped(t *testing.T) {
	p, issues := computedFixture(t, func(s *mapping.Spec) {
		s.OmitUnmapped = true
		s.DocSchema.Root.Children = append([]docschema.DocField{{ID: "f_x", Name: "x", Type: docschema.String}}, s.DocSchema.Root.Children...)
		m := tmpl("c", "f_x", "id ${doc.id}")
		m.Transforms = []mapping.Step{{Op: "upper"}}
		s.FieldMappings = append(s.FieldMappings, m)
	})
	require.NotNil(t, p, "%v", issues)
	require.True(t, strings.HasPrefix(applyComputed(t, p), `{"x":"ID 7","id":7`))
}

// Strings with quotes and escapes survive the pass byte for byte.
func TestComputed_KeepsTheDocumentBytes(t *testing.T) {
	p, issues := computedFixture(t, func(s *mapping.Spec) {
		s.DocSchema.Root.Children = append(s.DocSchema.Root.Children, docschema.DocField{ID: "f_x", Name: "x", Type: docschema.String})
		s.FieldMappings = append(s.FieldMappings, tmpl("c", "f_x", `"${self.address.city}" \${literal}`))
	})
	require.NotNil(t, p, "%v", issues)
	_, doc, _, rerr := p.ApplyRelated([]any{int32(7), int32(3)}, map[string][][]any{"emb_1": {{int32(3), "Ly\"on\n☃"}}}, nil)
	require.Nil(t, rerr)
	require.Equal(t, `{"id":7,"address":{"city":"Ly\"on\n☃"},"orders":[],"x":"\"Ly\"on\n☃\" ${literal}"}`, strings.ReplaceAll(string(doc), `☃`, "☃"))
}
