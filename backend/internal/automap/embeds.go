package automap

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"omnidatabus/internal/docschema"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/naming"
	"omnidatabus/internal/problem"
	"omnidatabus/internal/schema"
)

// EmbedColumns are an embedded table's mappable columns: every column except the join columns
// (the relation itself), unless includeJoin. A many-to-one embed always keeps them: they are the
// embedded row's primary key, which replaces the parent's foreign key (see AddEmbed).
func EmbedColumns(child *schema.Table, e mapping.Relation, includeJoin bool) []SourceCol {
	var out []SourceCol
	includeJoin = includeJoin || e.Kind != mapping.EmbedOneToMany
	for _, c := range Columns(child) {
		if includeJoin || !joinColumn(e, c.Name) {
			c.EmbedPath = e.ID
			out = append(out, c)
		}
	}
	return out
}

// RunEmbed auto-maps one relation's fields from its table's columns (and, for many-to-many, the
// junction's payload columns), scoped to the relation's sub-tree (OC-021 AC2, OC-043 AC7); the
// spec's other mappings are unchanged.
func RunEmbed(spec *mapping.Spec, e mapping.Relation, cols []SourceCol, opts Options) Result {
	scoped := mapping.Scoped(spec, e)
	res := Run(&scoped, cols, opts)
	payload := map[string]bool{}
	if e.Via != nil {
		for _, c := range e.Via.Columns {
			payload[c] = true
		}
	}
	tableCol := map[string]bool{}
	for _, c := range cols {
		if c.EmbedPath == e.ID {
			tableCol[c.Name] = true
		}
	}
	scopes := mapping.FieldScopes(spec)
	out := make([]mapping.FieldMapping, 0, len(spec.FieldMappings)+len(res.FieldMappings))
	for _, m := range spec.FieldMappings {
		if !mapping.UsesRelation(m, e.ID) && scopes[m.TargetFieldID] != e.ID {
			out = append(out, m)
		}
	}
	explain := map[string]Explanation{}
	for _, m := range res.FieldMappings {
		old := m.ID
		if m.Origin != mapping.OriginManual && len(m.Sources) == 1 {
			m.ID = mappingID(e.ID+"\x00"+m.Sources[0].Column, m.TargetFieldID)
		}
		for i := range m.Sources {
			if m.Sources[i].Relation != "" {
				continue // an ancestor's or a lookup's column, kept as is
			}
			m.Sources[i].Relation = e.ID
			if payload[m.Sources[i].Column] && !tableCol[m.Sources[i].Column] {
				m.Sources[i].From = mapping.FromVia // a junction payload column (OC-044)
			}
		}
		if x, ok := res.Explain[old]; ok {
			explain[m.ID] = x
		}
		out = append(out, m)
	}
	res.FieldMappings, res.Explain = out, explain
	return res
}

// AggregateInput adds an aggregate over the new relation's rows, written into a new field next
// to the relation's field (OC-045).
type AggregateInput struct {
	Fn         string `json:"fn"`
	Column     string `json:"column,omitempty"`
	From       string `json:"from,omitempty"`
	TargetName string `json:"targetName"`
}

// EmbedInput adds a relation (the Embed dialog, OC-021; extended by OC-043 – OC-045).
type EmbedInput struct {
	Kind string `json:"kind" validate:"required,oneof=many-to-one one-to-many many-to-many"`
	// ParentID nests the relation under an existing one (OC-043): its field goes inside that
	// relation's object or items.
	ParentID string           `json:"parentId,omitempty"`
	Table    mapping.TableRef `json:"table"`
	// Join pairs parent and child columns (not for many-to-many, which uses Via).
	Join []mapping.JoinPair `json:"join"`
	Via  *mapping.Via       `json:"via,omitempty"`
	// LookupOnly adds a many-to-one relation without an object: Generate then adds its columns
	// as flat fields next to the relation (customerName from customers.name).
	LookupOnly bool                  `json:"lookupOnly,omitempty"`
	TargetName string                `json:"targetName"`
	OrderBy    []mapping.OrderColumn `json:"orderBy"`
	Limit      int                   `json:"limit"`
	Empty      string                `json:"empty"`
	// Array options (OC-044 – OC-046).
	Shape        string           `json:"shape,omitempty"`
	ValueColumn  string           `json:"valueColumn,omitempty"`
	KeyColumn    string           `json:"keyColumn,omitempty"`
	Filter       []mapping.Filter `json:"filter,omitempty"`
	Overflow     string           `json:"overflow,omitempty"`
	Strategy     string           `json:"strategy,omitempty"`
	Distinct     bool             `json:"distinct,omitempty"`
	KeepDangling bool             `json:"keepDangling,omitempty"`
	// NoField adds the relation for its aggregates only (no array field).
	NoField    bool             `json:"noField,omitempty"`
	Aggregates []AggregateInput `json:"aggregates,omitempty"`
	// Generate fills the relation's fields from the table's columns and auto-maps them.
	Generate bool `json:"generate"`
	// IncludeJoinColumns also generates fields for a one-to-many embed's join columns (left out by
	// default). A many-to-one embed always includes them.
	IncludeJoinColumns bool `json:"includeJoinColumns"`
	// Then are further hops, each nested under the one before (a path picked in the Related
	// tree, e.g. order_items → products; OC-043 AC7).
	Then []EmbedInput `json:"then,omitempty"`
}

func bad(format string, args ...any) error {
	return problem.New(http.StatusUnprocessableEntity, problem.CodeValidation, "Can't embed", fmt.Sprintf(format, args...))
}

// AddEmbed adds a depth-1 relation of child to the spec (the 1.0 entry point, OC-021 AC2): see
// AddRelation.
func AddEmbed(spec mapping.Spec, child *schema.Table, in EmbedInput, gen docschema.GenerateOptions) (mapping.Spec, mapping.Relation, Result, error) {
	next, added, res, err := AddRelation(spec, func(string, string) *schema.Table { return child }, in, gen)
	if err != nil {
		return spec, mapping.Relation{}, Result{}, err
	}
	return next, added[0], res, nil
}

// AddRelation adds a relation (and the hops in Then, each nested under the previous) with its
// field: an object (many-to-one), an array of objects, values or a keyed object (one-to-many,
// many-to-many), or no field (lookup-only, aggregate-only). The field goes at the root, or inside
// the parent relation's object or items. With Generate the fields come from the table's columns
// (and a junction's payload columns) and are auto-mapped inside that sub-tree only.
//
// A many-to-one relation moves the key into the object: the embedded row's primary key (the
// join's child columns) is generated and mapped there, and the parent scope's mappings of the
// foreign key (the join's parent columns) are removed, and so is a key field left with nothing
// to hold (OC-082): the key now lives in the object.
func AddRelation(spec mapping.Spec, rel mapping.RelatedTables, in EmbedInput, gen docschema.GenerateOptions) (mapping.Spec, []mapping.Relation, Result, error) {
	orig := spec
	spec = cloneSpec(spec)
	var added []mapping.Relation
	res := Result{FieldMappings: spec.FieldMappings, Explain: map[string]Explanation{}}
	hop := in
	for {
		e, r, err := addOne(&spec, rel, hop, gen)
		if err != nil {
			return orig, nil, Result{}, err
		}
		added = append(added, e)
		if r != nil {
			for k, v := range res.Explain {
				r.Explain[k] = v
			}
			res = *r
		}
		if len(hop.Then) == 0 {
			break
		}
		next := hop.Then[0]
		next.Then = hop.Then[1:]
		next.ParentID = e.ID
		hop = next
	}
	spec.Normalize()
	res.FieldMappings = spec.FieldMappings
	return spec, added, res, nil
}

func cloneSpec(s mapping.Spec) mapping.Spec {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err) // plain data
	}
	var out mapping.Spec
	if err := json.Unmarshal(b, &out); err != nil {
		panic(err)
	}
	return out
}

func addOne(spec *mapping.Spec, rel mapping.RelatedTables, in EmbedInput, gen docschema.GenerateOptions) (mapping.Relation, *Result, error) {
	switch in.Kind {
	case mapping.EmbedManyToOne, mapping.EmbedOneToMany, mapping.RelationManyToMany:
	default:
		return mapping.Relation{}, nil, bad("kind must be many-to-one, one-to-many or many-to-many")
	}
	child := rel(in.Table.Schema, in.Table.Name)
	if child == nil {
		return mapping.Relation{}, nil, bad("table %s isn't in the latest schema snapshot", in.Table)
	}
	var junction *schema.Table
	if in.Kind == mapping.RelationManyToMany {
		if in.Via == nil || len(in.Via.ParentJoin) == 0 || len(in.Via.ChildJoin) == 0 {
			return mapping.Relation{}, nil, bad("a many-to-many relation needs its junction table and join columns")
		}
		if junction = rel(in.Via.Table.Schema, in.Via.Table.Name); junction == nil {
			return mapping.Relation{}, nil, bad("junction %s isn't in the latest schema snapshot", in.Via.Table)
		}
		for _, j := range in.Via.ChildJoin {
			if child.Column(j.Child) == nil || junction.Column(j.Via) == nil {
				return mapping.Relation{}, nil, bad("join column %s → %s is missing", j.Via, j.Child)
			}
		}
	} else {
		if len(in.Join) == 0 {
			return mapping.Relation{}, nil, bad("set the join columns")
		}
		for _, j := range in.Join {
			if child.Column(j.Child) == nil {
				return mapping.Relation{}, nil, bad("column %s isn't in %s", j.Child, in.Table)
			}
		}
	}
	if in.LookupOnly && in.Kind != mapping.EmbedManyToOne {
		return mapping.Relation{}, nil, bad("only a many-to-one relation can be values only (lookup)")
	}
	container := &spec.DocSchema.Root
	var parent *mapping.Relation
	if in.ParentID != "" {
		if parent = spec.RelationByID(in.ParentID); parent == nil {
			return mapping.Relation{}, nil, bad("relation %s doesn't exist", in.ParentID)
		}
		if container = mapping.RelationContainer(&spec.DocSchema, *parent); container == nil {
			return mapping.Relation{}, nil, bad("relation %s has no object to nest into", in.ParentID)
		}
	}
	e := mapping.Relation{
		ID: mapping.EmbedID(), Kind: in.Kind, Table: in.Table, Join: in.Join, Via: in.Via, LookupOnly: in.LookupOnly,
		OrderBy: in.OrderBy, Limit: in.Limit, Empty: in.Empty, Shape: in.Shape, ValueColumn: in.ValueColumn, KeyColumn: in.KeyColumn,
		Filter: in.Filter, Overflow: in.Overflow, Strategy: in.Strategy, Distinct: in.Distinct, KeepDangling: in.KeepDangling,
	}
	if e.Kind == mapping.RelationManyToMany {
		e.Join = nil
	}
	if in.ParentID != "" {
		p := in.ParentID
		e.ParentID = &p
	}
	name := in.TargetName
	if name == "" {
		name = docschema.Rename(child.Name, gen.Naming)
	}
	spec.Relations = append(spec.Relations, e)

	switch {
	case in.LookupOnly:
		var fm []mapping.FieldMapping
		if in.Generate {
			prefix := naming.SingularSnake(child.Name)
			for _, c := range child.Columns {
				if joinColumn(e, c.Name) {
					continue
				}
				t, format := docschema.FieldTypeOf(c)
				f := docschema.DocField{ID: docschema.NewID(), Name: docschema.UniqueName(container, docschema.Rename(prefix+"_"+c.Name, gen.Naming)), Type: t, Format: format}
				container.Children = append(container.Children, f)
				fm = append(fm, mapping.FieldMapping{
					ID: mapping.NewMappingID(), TargetFieldID: f.ID, Sources: []mapping.SourceRef{{Relation: e.ID, Column: c.Name}},
					Origin: mapping.OriginAuto, State: mapping.StateActive, Confidence: 1, Reason: "lookup column",
				})
			}
		}
		spec.FieldMappings = append(spec.FieldMappings, fm...)
		addAggregates(spec, container, e, child, junction, in.Aggregates, gen)
		return e, nil, nil
	case in.NoField && e.Many():
		addAggregates(spec, container, e, child, junction, in.Aggregates, gen)
		return e, nil, nil
	}

	var target docschema.DocField
	if e.Many() && e.EffectiveShape() == mapping.ShapeScalars {
		vc := child.Column(in.ValueColumn)
		if vc == nil {
			return mapping.Relation{}, nil, bad("choose the value column of %s", in.Table)
		}
		t, format := docschema.FieldTypeOf(*vc)
		target = docschema.DocField{ID: docschema.NewID(), Name: name, Type: docschema.Array, Items: &docschema.DocField{ID: docschema.NewID(), Name: "[]", Type: t, Format: format}}
	} else {
		fields := []docschema.DocField{}
		if in.Generate {
			fields = generateFields(e, child, junction, in.IncludeJoinColumns, gen)
		}
		obj := docschema.DocField{ID: docschema.NewID(), Name: name, Type: docschema.Object, Children: fields}
		target = obj
		if e.Many() && e.EffectiveShape() != mapping.ShapeKeyed {
			obj.Name = "[]"
			target = docschema.DocField{ID: docschema.NewID(), Name: name, Type: docschema.Array, Items: &obj}
		}
	}
	target.Name = docschema.UniqueName(container, target.Name)
	spec.Relations[len(spec.Relations)-1].TargetFieldID = target.ID
	e.TargetFieldID = target.ID
	container.Children = append(container.Children, target)
	var unmapped []string
	if e.Kind == mapping.EmbedManyToOne {
		spec.FieldMappings, unmapped = unmapForeignKey(spec, e)
	}
	addAggregates(spec, container, e, child, junction, in.Aggregates, gen)
	// Last: removing fields moves siblings, and container points into the tree.
	dropKeyFields(spec, unmapped)
	if !in.Generate {
		return e, nil, nil
	}
	cols := EmbedColumns(child, e, in.IncludeJoinColumns)
	if junction != nil {
		for _, c := range in.Via.Columns {
			if jc := junction.Column(c); jc != nil {
				cols = append(cols, SourceCol{Name: c, JSONType: jc.JSONType, Ordinal: len(cols) + 1, Table: junction.Name, EmbedPath: e.ID + ":via"})
			}
		}
	}
	res := RunEmbed(spec, e, cols, Options{})
	spec.FieldMappings = res.FieldMappings
	return e, &res, nil
}

// generateFields are the item fields of a relation: its table's columns (without a
// one-to-many's join columns unless asked), then a many-to-many junction's payload columns.
func generateFields(e mapping.Relation, child, junction *schema.Table, includeJoin bool, gen docschema.GenerateOptions) []docschema.DocField {
	src := *child
	src.Columns = nil
	for _, c := range child.Columns {
		if includeJoin || e.Kind != mapping.EmbedOneToMany || !joinColumn(e, c.Name) {
			src.Columns = append(src.Columns, c)
		}
	}
	fields := docschema.FromTable(&src, gen).Root.Children
	if junction == nil || e.Via == nil {
		return fields
	}
	holder := docschema.DocField{Children: fields}
	for _, c := range e.Via.Columns {
		jc := junction.Column(c)
		if jc == nil {
			continue
		}
		one := schema.Table{Name: junction.Name, Columns: []schema.Column{*jc}}
		f := docschema.FromTable(&one, gen).Root.Children[0]
		f.Name = docschema.UniqueName(&holder, f.Name)
		holder.Children = append(holder.Children, f)
	}
	return holder.Children
}

// addAggregates adds each aggregate's field next to the relation's field (OC-045).
func addAggregates(spec *mapping.Spec, container *docschema.DocField, e mapping.Relation, child, junction *schema.Table, in []AggregateInput, gen docschema.GenerateOptions) {
	for n, a := range in {
		t := docschema.Number
		switch a.Fn {
		case "count":
			t = docschema.Integer
		case "min", "max", "first", "last":
			col := child.Column(a.Column)
			if a.From == mapping.FromVia && junction != nil {
				col = junction.Column(a.Column)
			}
			if col != nil {
				t, _ = docschema.FieldTypeOf(*col)
			}
		}
		name := a.TargetName
		if name == "" {
			name = docschema.Rename(naming.SingularSnake(e.Table.Name)+"_"+strings.TrimSpace(a.Fn+"_"+a.Column), gen.Naming)
		}
		f := docschema.DocField{ID: docschema.NewID(), Name: docschema.UniqueName(container, name), Type: t}
		container.Children = append(container.Children, f)
		spec.Aggregates = append(spec.Aggregates, mapping.Aggregate{
			ID: fmt.Sprintf("agg_%s_%d", strings.TrimPrefix(e.ID, "emb_"), n+1), RelationID: e.ID, Fn: a.Fn, Column: a.Column, From: a.From, TargetFieldID: f.ID,
		})
	}
}

// unmapForeignKey drops the mappings in the relation's parent scope that copy a join's parent
// column (the foreign key) as is, and returns their target fields; expressions that combine it
// with other columns are kept.
func unmapForeignKey(spec *mapping.Spec, e mapping.Relation) ([]mapping.FieldMapping, []string) {
	fk := map[string]bool{}
	for _, j := range e.Join {
		fk[j.Parent] = true
	}
	scopes := mapping.FieldScopes(spec)
	out := make([]mapping.FieldMapping, 0, len(spec.FieldMappings))
	var targets []string
	for _, m := range spec.FieldMappings {
		if len(m.Sources) == 1 && m.Sources[0].Relation == e.Parent() && fk[m.Sources[0].Column] && scopes[m.TargetFieldID] == e.Parent() {
			targets = append(targets, m.TargetFieldID)
			continue
		}
		out = append(out, m)
	}
	return out, targets
}

// dropKeyFields removes the foreign-key fields an embed unmapped (OC-082), unless something else
// still fills them: another mapping or an aggregate. Only scalar fields go.
func dropKeyFields(spec *mapping.Spec, ids []string) {
	used := map[string]bool{}
	for _, m := range spec.FieldMappings {
		used[m.TargetFieldID] = true
	}
	for _, a := range spec.Aggregates {
		used[a.TargetFieldID] = true
	}
	drop := map[string]bool{}
	for _, id := range ids {
		if f := spec.DocSchema.Find(id); f != nil && !used[id] && f.Type != docschema.Object && f.Type != docschema.Array {
			drop[id] = true
		}
	}
	if len(drop) > 0 {
		dropFields(&spec.DocSchema.Root, drop)
	}
}

func dropFields(f *docschema.DocField, ids map[string]bool) {
	kept := f.Children[:0]
	for _, c := range f.Children {
		if ids[c.ID] {
			continue
		}
		dropFields(&c, ids)
		kept = append(kept, c)
	}
	f.Children = kept
	if f.Items != nil {
		dropFields(f.Items, ids)
	}
}

func joinColumn(e mapping.Relation, column string) bool {
	for _, j := range e.Join {
		if j.Child == column {
			return true
		}
	}
	return false // a many-to-many target keeps its key (it has no Join)
}
