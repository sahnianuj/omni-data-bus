package mapping

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"omnidatabus/internal/docschema"
	"omnidatabus/internal/schema"
)

// Relation issue codes of 2.0 (OC-043 – OC-046).
const (
	CodeRelationFieldOutsideParent = "RELATION_FIELD_OUTSIDE_PARENT"
	CodeRelationLookupKind         = "RELATION_LOOKUP_KIND"
	// CodeLookupNotUnique: a lookup's join doesn't name one row of its table (OC-165).
	CodeLookupNotUnique = "LOOKUP_NOT_UNIQUE"
	// CodeVariantInvalid: a relation's variants don't fit its items or table (OC-171);
	// CodeVariantArcUnconfirmed: no CHECK constraint says exactly one of the variants'
	// columns is set, so a row may match none or several (the first wins).
	CodeVariantInvalid           = "VARIANT_INVALID"
	CodeVariantArcUnconfirmed    = "VARIANT_ARC_UNCONFIRMED"
	CodeRelationViaInvalid       = "RELATION_VIA_INVALID"
	CodeRelationFilterValue      = "RELATION_FILTER_VALUE"
	CodeRelationAggregateInvalid = "RELATION_AGGREGATE_INVALID"
	CodeRelationOptionInvalid    = "RELATION_OPTION_INVALID"
)

// Filter operators (OC-045).
var filterOps = map[string]bool{
	"eq": true, "ne": true, "lt": true, "lte": true, "gt": true, "gte": true,
	"in": true, "notIn": true, "isNull": true, "notNull": true, "like": true,
}

// RelationTables resolves the tables a spec's relations read: each relation's table and, for
// many-to-many, its junction. A table missing from the snapshot is nil.
type RelationTables struct {
	Table    map[string]*schema.Table
	Junction map[string]*schema.Table
}

// ResolveTables looks up every relation's table (and junction) with rel.
func ResolveTables(s *Spec, rel RelatedTables) RelationTables {
	out := RelationTables{Table: map[string]*schema.Table{}, Junction: map[string]*schema.Table{}}
	if rel == nil {
		return out
	}
	for _, e := range s.Relations {
		out.Table[e.ID] = rel(e.Table.Schema, e.Table.Name)
		if e.Via != nil {
			out.Junction[e.ID] = rel(e.Via.Table.Schema, e.Via.Table.Name)
		}
	}
	return out
}

// ValidateRelations checks each relation (OC-021 AC7, extended by OC-043 – OC-046): its table
// and join columns exist with compatible types (through the junction for many-to-many), its
// target field has the right shape and lies inside its parent's field, lookup-only relations
// are many-to-one, filters, shapes, aggregates and overflow settings are valid, and every
// mapping reads only relations in its field's scope. Size and cardinality estimates are warnings.
func ValidateRelations(s *Spec, root *schema.Table, related RelatedTables) []Issue {
	out := ValidateRelationTree(s)
	_, depth, _ := Depths(s)
	paths := s.DocSchema.Paths()
	scopes := FieldScopes(s)
	tables := ResolveTables(s, related)
	aggregated := map[string]bool{}
	for _, a := range s.Aggregates {
		aggregated[a.RelationID] = true
	}
	targets := map[string]string{}
	ids := map[string]bool{}
	composed := s.Composed()
	for _, e := range s.Relations {
		path := paths[e.TargetFieldID]
		if path == "" {
			path = "relations." + e.ID
		}
		bad := func(code, msg string) {
			out = append(out, Issue{Severity: SeverityError, Code: code, Path: path, FieldID: e.TargetFieldID, Message: "Embed " + e.ID + ": " + msg})
		}
		if e.ID == "" || ids[e.ID] {
			bad(CodeEmbedInvalid, "every relation needs a unique id.")
		}
		ids[e.ID] = true
		if e.Kind != EmbedManyToOne && e.Kind != EmbedOneToMany && e.Kind != RelationManyToMany {
			bad(CodeEmbedInvalid, "kind must be many-to-one, one-to-many or many-to-many.")
			continue
		}
		validateRelationOptions(e, depth[e.ID], bad)
		switch {
		case e.LookupOnly:
			if e.Kind != EmbedManyToOne {
				bad(CodeRelationLookupKind, "only a many-to-one relation can be lookup-only (it gives at most one row per parent).")
			} else if t := tables.Table[e.ID]; t != nil && declaresKeys(t) && !t.UniqueKey(childColumns(e.Join)) {
				// A one-to-one table looked up by its key is fine (OC-165); a column several rows
				// share isn't: which row would the field get?
				bad(CodeLookupNotUnique, "a lookup reads one row, but "+strings.Join(childColumns(e.Join), ", ")+" isn't a key of "+e.Table.Name+". Embed it as an object (first row) instead.")
			}
		case e.TargetFieldID == "":
			if !e.Many() || !aggregated[e.ID] {
				bad(CodeEmbedInvalid, "choose the field it fills.")
			}
		default:
			if prev, dup := targets[e.TargetFieldID]; dup {
				bad(CodeEmbedDuplicate, "field "+path+" is already filled by relation "+prev+".")
			}
			targets[e.TargetFieldID] = e.ID
			if s.DocSchema.Find(e.TargetFieldID) == nil {
				bad(CodeUnknownTargetField, "the target field isn't in the design.")
				break
			}
			mismatched := RelationContainer(&s.DocSchema, e) == nil && RelationValueField(&s.DocSchema, e) == nil
			if cols := combineInstead(s, e, root); cols != nil {
				out = append(out, repeatsRootIssue(e, path, cols, mismatched))
			} else if mismatched {
				if composed || Adapter(&s.DocSchema, e) != "" {
					out = append(out, Issue{Severity: SeverityError, Code: CodeBindingKindMismatch, Path: path, FieldID: e.TargetFieldID, Message: bindingMismatch(s, e, path)})
				} else {
					bad(CodeEmbedTargetKind, path+" must be "+wantShape(e)+" for this relation.")
				}
			}
			if scopes[e.TargetFieldID] != e.Parent() {
				where := "the document root"
				if p := e.Parent(); p != "" {
					where = "the field of relation " + p
				}
				bad(CodeRelationFieldOutsideParent, path+" must be inside "+where+".")
			}
		}
		child := tables.Table[e.ID]
		if child == nil {
			bad(CodeEmbedTableMissing, "table "+e.Table.String()+" isn't in the latest schema snapshot.")
			continue
		}
		out = append(out, validateVariants(s, e, child, path, targets)...)
		parent := root
		if p := e.Parent(); p != "" {
			parent = tables.Table[p]
		}
		if parent == nil {
			continue // the parent's own issue is reported
		}
		// A parent join column that's gone means the binding broke (the driving table or the
		// schema changed, OC-047): the composer flags it rather than dropping the relation.
		parentMissing := func(col, where string) {
			if composed {
				out = append(out, Issue{
					Severity: SeverityError, Code: CodeBindingBroken, Path: path, FieldID: e.TargetFieldID,
					Message: "Binding " + e.ID + " (" + e.Table.Name + ") is broken: join column " + col + " isn't in " + where + ". Re-bind it, or change the join.",
				})
				return
			}
			if e.Kind == RelationManyToMany {
				bad(CodeRelationViaInvalid, "parent join column "+col+" isn't in "+where+".")
				return
			}
			bad(CodeEmbedJoinInvalid, "join column "+col+" isn't in "+where+".")
		}
		if e.Kind == RelationManyToMany {
			validateVia(e, parent, child, tables.Junction[e.ID], bad, parentMissing)
		} else {
			if e.Via != nil {
				bad(CodeRelationViaInvalid, "only a many-to-many relation goes through a junction table.")
			}
			if len(e.Join) == 0 {
				bad(CodeEmbedJoinInvalid, "set the join columns.")
			}
			for _, j := range e.Join {
				pc, cc := parent.Column(j.Parent), child.Column(j.Child)
				switch {
				case pc == nil:
					parentMissing(j.Parent, parent.Name)
				case cc == nil:
					bad(CodeEmbedJoinInvalid, "join column "+j.Child+" isn't in "+e.Table.String()+".")
				case !joinCompatible(*pc, *cc):
					bad(CodeEmbedJoinInvalid, fmt.Sprintf("%s (%s) and %s (%s) have incompatible types.", j.Parent, pc.UDTName, j.Child, cc.UDTName))
				}
			}
		}
		rowTable := RowTable(e, child, tables.Junction[e.ID])
		for _, o := range e.OrderBy {
			if relationColumn(e, rowTable, o.Column, o.From) == nil {
				bad(CodeEmbedJoinInvalid, "order column "+o.Column+" isn't in "+sourceName(e, o.From)+".")
			}
		}
		switch e.EffectiveShape() {
		case ShapeScalars:
			if e.ValueColumn == "" && !mapsField(s, RelationValueField(&s.DocSchema, e)) {
				bad(CodeRelationOptionInvalid, "a scalars array needs a value column.")
			} else if e.ValueColumn != "" && child.Column(e.ValueColumn) == nil {
				bad(CodeRelationOptionInvalid, "value column "+e.ValueColumn+" isn't in "+e.Table.String()+".")
			}
		case ShapeKeyed:
			if e.KeyColumn == "" || child.Column(e.KeyColumn) == nil {
				bad(CodeRelationOptionInvalid, "a keyed object needs a key column of "+e.Table.String()+".")
			}
		}
		for i, f := range e.Filter {
			col := relationColumn(e, rowTable, f.Column, f.From)
			if col == nil {
				bad(CodeRelationFilterValue, fmt.Sprintf("filter %d: column %s isn't in %s.", i+1, f.Column, sourceName(e, f.From)))
				continue
			}
			if _, err := FilterArgs(*col, f); err != nil {
				bad(CodeRelationFilterValue, fmt.Sprintf("filter %d on %s: %s", i+1, f.Column, err.Error()))
			}
		}
	}
	out = append(out, validateAggregates(s, tables, targets, paths, scopes)...)
	out = append(out, relationEstimates(s, root, tables, paths)...)
	out = append(out, validateScopes(s, scopes, paths)...)
	out = append(out, relationIndexAdvice(s, tables, paths)...)
	if composed {
		out = append(out, validateComposed(s)...)
	}
	return out
}

func wantShape(e Relation) string {
	switch {
	case e.Kind == EmbedManyToOne:
		return "an object"
	case e.EffectiveShape() == ShapeScalars:
		return "an array of values"
	case e.EffectiveShape() == ShapeKeyed:
		return "an object"
	}
	return "an array of objects"
}

func sourceName(e Relation, from string) string {
	if from == FromVia && e.Via != nil {
		return "junction " + e.Via.Table.String()
	}
	return e.Table.String()
}

// relationColumn resolves a column of a relation's rows: the table's, or the junction's when
// from is "via" (many-to-many only).
func relationColumn(e Relation, rowTable *schema.Table, column, from string) *schema.Column {
	if rowTable == nil {
		return nil
	}
	if from == FromVia {
		if e.Kind != RelationManyToMany {
			return nil
		}
		return rowTable.Column(ViaColumn(column))
	}
	return rowTable.Column(column)
}

func mapsField(s *Spec, f *docschema.DocField) bool {
	if f == nil {
		return false
	}
	for _, m := range s.FieldMappings {
		if m.Active() && m.TargetFieldID == f.ID {
			return true
		}
	}
	return false
}

func validateRelationOptions(e Relation, depth int, bad func(code, msg string)) {
	if !e.Many() {
		switch {
		case e.Shape != "" && e.Shape != ShapeObjects:
			bad(CodeRelationOptionInvalid, "a many-to-one relation has no array shape.")
		case len(e.Filter) > 0:
			bad(CodeRelationOptionInvalid, "filters apply to one-to-many and many-to-many relations.")
		case e.Strategy == StrategySortedMerge:
			bad(CodeRelationOptionInvalid, "sorted-merge applies to one-to-many and many-to-many relations.")
		}
		return
	}
	if e.Limit < 0 || e.Limit > MaxEmbedLimit {
		bad(CodeEmbedInvalid, fmt.Sprintf("limit must be between 1 and %d.", MaxEmbedLimit))
	}
	if e.Empty != "" && e.Empty != EmptyArray && e.Empty != EmptyOmit {
		bad(CodeEmbedInvalid, "empty must be array or omit.")
	}
	switch e.EffectiveShape() {
	case ShapeObjects, ShapeScalars, ShapeKeyed:
	default:
		bad(CodeRelationOptionInvalid, "shape must be objects, scalars or keyed.")
	}
	switch e.EffectiveOverflow() {
	case OverflowTruncate, OverflowFail:
	case OverflowSplit:
		if depth > 1 || e.EffectiveShape() != ShapeObjects {
			bad(CodeRelationOptionInvalid, "only an array of objects directly under the root can be split into pages.")
		}
	default:
		bad(CodeRelationOptionInvalid, "overflow must be truncate, fail or split.")
	}
	switch e.Strategy {
	case "", StrategyAuto, StrategyBatched:
	case StrategySortedMerge:
		if depth > 1 {
			bad(CodeRelationOptionInvalid, "sorted-merge reads only relations directly under the root; nested ones are always batched.")
		}
	default:
		bad(CodeRelationOptionInvalid, "strategy must be auto, batched or sorted-merge.")
	}
	if e.Distinct && e.Kind != RelationManyToMany {
		bad(CodeRelationOptionInvalid, "distinct applies to many-to-many relations.")
	}
	if e.KeepDangling && e.Kind != RelationManyToMany {
		bad(CodeRelationOptionInvalid, "keepDangling applies to many-to-many relations.")
	}
	for _, f := range e.Filter {
		if !filterOps[f.Op] {
			bad(CodeRelationFilterValue, "unknown filter operator '"+f.Op+"'.")
		}
		if f.From == FromVia && e.Kind != RelationManyToMany {
			bad(CodeRelationFilterValue, "only a many-to-many relation can filter on junction columns.")
		}
	}
}

// validateVia checks a many-to-many relation's junction (OC-044 AC5).
func validateVia(e Relation, parent, child, junction *schema.Table, bad func(code, msg string), parentMissing func(col, where string)) {
	if len(e.Join) > 0 {
		bad(CodeRelationViaInvalid, "a many-to-many relation joins through via, not join.")
	}
	if e.Via == nil {
		bad(CodeRelationViaInvalid, "set the junction table (via).")
		return
	}
	if junction == nil {
		bad(CodeEmbedTableMissing, "junction "+e.Via.Table.String()+" isn't in the latest schema snapshot.")
		return
	}
	jname := e.Via.Table.String()
	if len(e.Via.ParentJoin) == 0 || len(e.Via.ChildJoin) == 0 {
		bad(CodeRelationViaInvalid, "set the junction's parent and child join columns.")
	}
	joinCols := map[string]bool{}
	for _, j := range e.Via.ParentJoin {
		joinCols[j.Via] = true
		pc, vc := parent.Column(j.Parent), junction.Column(j.Via)
		switch {
		case pc == nil:
			parentMissing(j.Parent, parent.Name)
		case vc == nil:
			bad(CodeRelationViaInvalid, "join column "+j.Via+" isn't in junction "+jname+".")
		case !joinCompatible(*pc, *vc):
			bad(CodeRelationViaInvalid, fmt.Sprintf("%s (%s) and %s.%s (%s) have incompatible types.", j.Parent, pc.UDTName, jname, j.Via, vc.UDTName))
		}
	}
	for _, j := range e.Via.ChildJoin {
		joinCols[j.Via] = true
		vc, cc := junction.Column(j.Via), child.Column(j.Child)
		switch {
		case vc == nil:
			bad(CodeRelationViaInvalid, "join column "+j.Via+" isn't in junction "+jname+".")
		case cc == nil:
			bad(CodeRelationViaInvalid, "join column "+j.Child+" isn't in "+e.Table.String()+".")
		case !joinCompatible(*vc, *cc):
			bad(CodeRelationViaInvalid, fmt.Sprintf("%s.%s (%s) and %s (%s) have incompatible types.", jname, j.Via, vc.UDTName, j.Child, cc.UDTName))
		}
	}
	for _, c := range e.Via.Columns {
		switch {
		case junction.Column(c) == nil:
			bad(CodeRelationViaInvalid, "junction column "+c+" isn't in "+jname+".")
		case joinCols[c]:
			bad(CodeRelationViaInvalid, "junction column "+c+" is a join column; it can't also be a payload column.")
		}
	}
}

// validateVariants checks a relation's variants (OC-171): each names a variant node of its
// items and a column of its rows; values are distinct; an arc not confirmed by a CHECK is a
// warning.
func validateVariants(s *Spec, e Relation, t *schema.Table, path string, targets map[string]string) []Issue {
	if len(e.Variants) == 0 && e.OnNoMatch == "" {
		return nil
	}
	var out []Issue
	bad := func(sev, code, msg string) {
		out = append(out, Issue{Severity: sev, Code: code, Path: path, FieldID: e.TargetFieldID, Message: "Embed " + e.ID + ": " + msg})
	}
	if e.OnNoMatch != "" && e.OnNoMatch != VariantNoMatchSkip && e.OnNoMatch != VariantNoMatchFail {
		bad(SeverityError, CodeVariantInvalid, "onNoMatch must be skip or fail.")
	}
	c := RelationContainer(&s.DocSchema, e)
	if c == nil || !e.Many() {
		bad(SeverityError, CodeVariantInvalid, "only an array of objects has variants.")
		return out
	}
	mine := map[string]bool{}
	for i := range c.Children {
		if c.Children[i].Variant != nil {
			mine[c.Children[i].ID] = true
		}
	}
	seen := map[string]bool{}
	var arc []string
	for _, v := range e.Variants {
		switch {
		case !mine[v.FieldID]:
			bad(SeverityError, CodeVariantInvalid, "variant "+v.FieldID+" isn't one of "+path+"'s variants.")
		case t.Column(v.When) == nil:
			bad(SeverityError, CodeVariantInvalid, "column "+v.When+" isn't in "+e.Table.String()+".")
		case targets[v.FieldID] != "":
			bad(SeverityError, CodeVariantInvalid, "variant "+v.FieldID+" is filled by relation "+targets[v.FieldID]+" too.")
		}
		k := v.When + "=" + v.Value
		if seen[k] {
			bad(SeverityError, CodeVariantInvalid, "two variants match the same rows ("+k+").")
		}
		seen[k] = true
		if v.Value == "" {
			arc = append(arc, v.When)
		}
	}
	if len(arc) > 1 && !coveredByArc(t, arc) {
		bad(SeverityWarning, CodeVariantArcUnconfirmed, "nothing in "+e.Table.Name+" says exactly one of "+strings.Join(arc, ", ")+" is set: a row with several takes the first variant, one with none is skipped.")
	}
	return out
}

func coveredByArc(t *schema.Table, cols []string) bool {
	for _, a := range t.ExclusiveArcs {
		all := true
		for _, c := range cols {
			all = all && slices.Contains(a, c)
		}
		if all {
			return true
		}
	}
	return false
}

// declaresKeys: the table has a primary or unique key, so a join on other columns isn't one.
func declaresKeys(t *schema.Table) bool { return len(t.PrimaryKey) > 0 || len(t.UniqueConstraints) > 0 }

func childColumns(j []JoinPair) []string {
	out := make([]string, len(j))
	for i, p := range j {
		out[i] = p.Child
	}
	return out
}

// validateAggregates checks OC-045 aggregates: a known array relation and function, a numeric
// column for sum and avg, and a free scalar target field in the relation's parent scope.
func validateAggregates(s *Spec, tables RelationTables, targets map[string]string, paths, scopes map[string]string) []Issue {
	var out []Issue
	ids := map[string]bool{}
	mapped := map[string]bool{}
	for _, m := range s.FieldMappings {
		if m.Active() {
			mapped[m.TargetFieldID] = true
		}
	}
	for _, a := range s.Aggregates {
		path := paths[a.TargetFieldID]
		if path == "" {
			path = "aggregates." + a.ID
		}
		bad := func(msg string) {
			out = append(out, Issue{Severity: SeverityError, Code: CodeRelationAggregateInvalid, Path: path, FieldID: a.TargetFieldID, Message: "Aggregate " + a.ID + ": " + msg})
		}
		if a.ID == "" || ids[a.ID] {
			bad("every aggregate needs a unique id.")
		}
		ids[a.ID] = true
		r := s.RelationByID(a.RelationID)
		if r == nil || !r.Many() {
			bad("it must summarize a one-to-many or many-to-many relation.")
			continue
		}
		if !AggregateFn(a.Fn) {
			bad("function must be count, sum, min, max, avg, first or last.")
		}
		f := s.DocSchema.Find(a.TargetFieldID)
		switch {
		case f == nil:
			bad("the target field isn't in the design.")
		case !f.Type.Scalar():
			bad(path + " must be a single value.")
		case mapped[a.TargetFieldID] || targets[a.TargetFieldID] != "":
			bad(path + " is already filled by a mapping or relation.")
		case scopes[a.TargetFieldID] != r.Parent():
			bad(path + " must be where the relation's parent rows are (next to its array).")
		}
		if a.Fn == "count" {
			continue
		}
		rowTable := RowTable(*r, tables.Table[r.ID], tables.Junction[r.ID])
		col := relationColumn(*r, rowTable, a.Column, a.From)
		switch {
		case a.Column == "":
			bad(a.Fn + " needs a column.")
		case rowTable != nil && col == nil:
			bad("column " + a.Column + " isn't in " + r.Table.String() + ".")
		case col != nil && (a.Fn == "sum" || a.Fn == "avg") && col.JSONType != schema.TypeInteger && col.JSONType != schema.TypeNumber:
			bad(a.Fn + " needs a numeric column; " + a.Column + " is " + col.UDTName + ".")
		}
	}
	return out
}

// validateScopes checks that each mapping reads only relations in its field's scope
// (architecture §6.1): the root, relations on the path to the field, and lookup-only relations
// attached to that path. Junction columns come from a many-to-many relation's payload.
func validateScopes(s *Spec, scopes, paths map[string]string) []Issue {
	var out []Issue
	for _, m := range s.FieldMappings {
		if m.State == StateRejected {
			continue
		}
		path := paths[m.TargetFieldID]
		scope := scopes[m.TargetFieldID]
		issue := func(msg string) {
			out = append(out, Issue{Severity: SeverityError, Code: CodeSourceOutOfScope, Path: path, FieldID: m.TargetFieldID, FieldMappingID: m.ID, Message: msg})
		}
		for _, src := range m.Sources {
			if src.Relation != "" && s.RelationByID(src.Relation) == nil {
				continue // reported as an unknown relation
			}
			if !InScope(s, scope, src.Relation) {
				issue(OutOfScopeMessage(s, paths, path, scope, src.Relation, src.Column))
				continue
			}
			if src.From == FromVia {
				r := s.RelationByID(src.Relation)
				switch {
				case r == nil || r.Kind != RelationManyToMany || r.Via == nil:
					issue("Only a many-to-many relation's junction columns can be read with from: via.")
				case !contains(r.Via.Columns, src.Column):
					issue("Junction column " + src.Column + " isn't one of relation " + r.ID + "'s payload columns.")
				}
			}
		}
	}
	return out
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// relationEstimates warns about relations that may produce huge arrays or documents (OC-021
// AC6; OC-043 multiplies across levels): more child rows per parent than the limit, or an
// estimated document over 10 MB, counting nested arrays as items × items per item.
func relationEstimates(s *Spec, root *schema.Table, tables RelationTables, paths map[string]string) []Issue {
	var out []Issue
	if root == nil {
		return nil
	}
	items := map[string]float64{} // estimated rows of a relation per root row
	docBytes := avgRowBytes(root)
	var biggest Relation
	var biggestBytes float64
	order, _, is := Depths(s)
	if is != nil {
		return nil
	}
	for _, id := range order {
		e := s.RelationByID(id)
		child := tables.Table[id]
		if child == nil {
			continue
		}
		parent, parentItems := root, 1.0
		if p := e.Parent(); p != "" {
			parent, parentItems = tables.Table[p], items[p]
		}
		if parent == nil {
			continue
		}
		per := 1.0
		if e.Many() {
			fan := child
			if e.Kind == RelationManyToMany && tables.Junction[id] != nil {
				fan = tables.Junction[id]
			}
			per = rowsPerParent(parent, fan)
			path := paths[e.TargetFieldID]
			if path == "" {
				path = "relations." + e.ID
			}
			if limit := e.EffectiveLimit(); per > float64(limit) {
				out = append(out, Issue{
					Severity: SeverityWarning, Code: CodeEmbedCardinality, Path: path, FieldID: e.TargetFieldID,
					Message: fmt.Sprintf("%s has about %.0f rows per %s row, more than the limit of %d: %s.", e.Table, per, parent.Name, limit, overflowEffect(*e)),
				})
			}
			per = math.Min(per, float64(e.EffectiveLimit()))
		}
		items[id] = parentItems * per
		if e.LookupOnly || e.TargetFieldID == "" {
			continue
		}
		b := items[id] * avgRowBytes(child)
		docBytes += b
		if b > biggestBytes {
			biggest, biggestBytes = *e, b
		}
	}
	if docBytes > EmbedDocWarnBytes && biggestBytes > 0 {
		path := paths[biggest.TargetFieldID]
		code := CodeEmbedDocSize
		if s.Composed() {
			code = CodeDocSizeRisk
		}
		out = append(out, Issue{
			Severity: SeverityWarning, Code: code, Path: path, FieldID: biggest.TargetFieldID,
			Message: fmt.Sprintf("Documents may reach about %.1f MB, mostly from %s (Couchbase allows 20 MB); lower its limit.", docBytes/(1<<20), biggest.Table),
		})
	}
	return out
}

func overflowEffect(e Relation) string {
	switch e.EffectiveOverflow() {
	case OverflowFail:
		return "those rows will fail"
	case OverflowSplit:
		return "arrays will be split into pages"
	}
	return "arrays will be truncated"
}

// FilterArgs checks a filter's value against its column's type (OC-045 AC3) and returns the
// values as PostgreSQL input text, sent as bind parameters: one for a comparison, several for
// in and notIn, none for isNull and notNull.
func FilterArgs(col schema.Column, f Filter) ([]string, error) {
	switch f.Op {
	case "isNull", "notNull":
		return nil, nil
	case "in", "notIn":
		list, ok := f.Value.([]any)
		if !ok {
			if ss, isStrings := f.Value.([]string); isStrings {
				for _, x := range ss {
					list = append(list, x)
				}
				ok = true
			}
		}
		if !ok || len(list) == 0 {
			return nil, fmt.Errorf("%s needs a list of values", f.Op)
		}
		out := make([]string, len(list))
		for i, v := range list {
			t, err := filterText(col, v)
			if err != nil {
				return nil, err
			}
			out[i] = t
		}
		return out, nil
	case "like":
		s, ok := f.Value.(string)
		if !ok || col.JSONType != schema.TypeString {
			return nil, fmt.Errorf("like needs a text column and a text pattern")
		}
		return []string{s}, nil
	}
	t, err := filterText(col, f.Value)
	if err != nil {
		return nil, err
	}
	return []string{t}, nil
}

// filterText coerces one filter value to the column's type (as OC-020 coercion would).
func filterText(col schema.Column, v any) (string, error) {
	fail := func() (string, error) {
		return "", fmt.Errorf("%v can't be used as a %s value", v, col.UDTName)
	}
	if v == nil {
		return "", fmt.Errorf("use isNull or notNull to compare with null")
	}
	switch col.JSONType {
	case schema.TypeInteger:
		switch x := v.(type) {
		case float64:
			if x != math.Trunc(x) {
				return fail()
			}
			return strconv.FormatInt(int64(x), 10), nil
		case int:
			return strconv.Itoa(x), nil
		case int64:
			return strconv.FormatInt(x, 10), nil
		case string:
			if _, err := strconv.ParseInt(strings.TrimSpace(x), 10, 64); err == nil {
				return strings.TrimSpace(x), nil
			}
		}
		return fail()
	case schema.TypeNumber:
		switch x := v.(type) {
		case float64:
			return strconv.FormatFloat(x, 'f', -1, 64), nil
		case int:
			return strconv.Itoa(x), nil
		case int64:
			return strconv.FormatInt(x, 10), nil
		case string:
			if _, err := strconv.ParseFloat(strings.TrimSpace(x), 64); err == nil {
				return strings.TrimSpace(x), nil
			}
		}
		return fail()
	case schema.TypeBoolean:
		switch x := v.(type) {
		case bool:
			return strconv.FormatBool(x), nil
		case string:
			if b, err := strconv.ParseBool(x); err == nil {
				return strconv.FormatBool(b), nil
			}
		}
		return fail()
	case schema.TypeDatetime:
		x, ok := v.(string)
		if !ok {
			return fail()
		}
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02"} {
			if _, err := time.Parse(layout, x); err == nil {
				return x, nil
			}
		}
		return fail()
	case schema.TypeString:
		switch x := v.(type) {
		case string:
			return x, nil
		case float64:
			return strconv.FormatFloat(x, 'f', -1, 64), nil
		case bool:
			return strconv.FormatBool(x), nil
		}
		return fail()
	}
	switch x := v.(type) {
	case string:
		return x, nil
	case float64, bool:
		return fmt.Sprint(x), nil
	}
	return fail()
}
