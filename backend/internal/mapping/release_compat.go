package mapping

import "omnidatabus/internal/docschema"

// MinWorker20 is the release a job needs when RequiresV2 reports reasons (OC-057).
const MinWorker20 = "2.0.0"

// MinWorker25 is the release a job needs when RequiresV25 reports reasons (OC-171).
const MinWorker25 = "2.5.0"

// RequiresV25 lists why a 2.0 worker can't run this spec: it would write variant items with
// none of their fields (OC-171), and doesn't know the now step (OC-179). Nil means a 2.0
// worker runs it the same way.
func RequiresV25(s *Spec) []string {
	for _, m := range s.FieldMappings {
		if m.Active() && hasStep(m.Transforms, "now") {
			return []string{"current date and time"}
		}
	}
	for _, r := range s.Relations {
		if len(r.Variants) > 0 {
			return []string{"variant shapes"}
		}
	}
	found := false
	s.DocSchema.Walk(func(f *docschema.DocField, _ string, _ *docschema.DocField) {
		found = found || f.Variant != nil
	})
	if found {
		return []string{"variant shapes"}
	}
	return nil
}

// RequiresV2 lists why a 1.0 worker can't run this spec during a rolling upgrade (OC-057):
// jobs freeze their specs in the v2 form, where relations replace 1.0's embeds, so any relation
// (an embed included) needs a 2.0 worker, as do composed documents, grouping, aggregates and
// computed fields (OC-136, OC-140: a 1.0 worker would write the documents without them).
// Nil means a 1.0 worker runs it the same way.
func RequiresV2(s *Spec) []string {
	var why []string
	if s.Mode == ModeComposed {
		why = append(why, "composed document")
	}
	if len(s.Relations) > 0 {
		why = append(why, "relations")
	}
	if len(s.GroupBy()) > 0 {
		why = append(why, "grouped rows")
	}
	if len(s.Aggregates) > 0 {
		why = append(why, "aggregates")
	}
	for _, m := range s.FieldMappings {
		if m.Computed != nil && m.Active() {
			why = append(why, "computed fields")
			break
		}
	}
	return why
}

func hasStep(steps []Step, op string) bool {
	for _, st := range steps {
		if st.Op == op {
			return true
		}
	}
	return false
}
