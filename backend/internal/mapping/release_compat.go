package mapping

// MinWorker20 is the release a job needs when RequiresV2 reports reasons (OC-057).
const MinWorker20 = "2.0.0"

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
