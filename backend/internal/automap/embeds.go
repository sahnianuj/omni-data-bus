package automap

import (
	"fmt"
	"net/http"

	"omnidatabus/internal/docschema"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/problem"
	"omnidatabus/internal/schema"
)

// EmbedColumns are an embedded table's mappable columns: every column except the join columns
// (the relation itself), unless includeJoin. A many-to-one embed always keeps them: they are the
// embedded row's primary key, which replaces the parent's foreign key (see AddEmbed).
func EmbedColumns(child *schema.Table, e mapping.Embed, includeJoin bool) []SourceCol {
	var out []SourceCol
	includeJoin = includeJoin || e.Kind == mapping.EmbedManyToOne
	for _, c := range Columns(child) {
		if includeJoin || !joinColumn(e, c.Name) {
			c.EmbedPath = e.ID
			out = append(out, c)
		}
	}
	return out
}

// RunEmbed auto-maps one embed's fields from its table's columns, scoped to the embed's
// sub-tree (OC-021 AC2); the spec's other mappings are unchanged.
func RunEmbed(spec *mapping.Spec, e mapping.Embed, cols []SourceCol, opts Options) Result {
	scoped := mapping.Scoped(spec, e)
	res := Run(&scoped, cols, opts)
	out := make([]mapping.FieldMapping, 0, len(spec.FieldMappings)+len(res.FieldMappings))
	for _, m := range spec.FieldMappings {
		if !mapping.UsesEmbed(m, e.ID) {
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
			m.Sources[i].Embed = e.ID
		}
		if x, ok := res.Explain[old]; ok {
			explain[m.ID] = x
		}
		out = append(out, m)
	}
	res.FieldMappings, res.Explain = out, explain
	return res
}

// EmbedInput adds an embed (the Embed dialog, OC-021).
type EmbedInput struct {
	Kind       string                `json:"kind" validate:"required,oneof=many-to-one one-to-many"`
	Table      mapping.TableRef      `json:"table"`
	Join       []mapping.JoinPair    `json:"join" validate:"required,min=1"`
	TargetName string                `json:"targetName"`
	OrderBy    []mapping.OrderColumn `json:"orderBy"`
	Limit      int                   `json:"limit"`
	Empty      string                `json:"empty"`
	// Generate fills the embed's fields from the table's columns and auto-maps them.
	Generate bool `json:"generate"`
	// IncludeJoinColumns also generates fields for a one-to-many embed's join columns (left out by
	// default). A many-to-one embed always includes them.
	IncludeJoinColumns bool `json:"includeJoinColumns"`
}

// AddEmbed adds an embed to the spec: a new object (many-to-one) or array-of-object
// (one-to-many) field at the root named TargetName, optionally with its fields generated from
// the table and auto-mapped inside that sub-tree only (OC-021 AC2).
//
// A many-to-one embed moves the key into the object: the embedded row's primary key (the join's
// child columns) is generated and mapped there, and the root's mappings of the foreign key (the
// join's parent columns) are removed; the root field stays, unmapped.
func AddEmbed(spec mapping.Spec, child *schema.Table, in EmbedInput, gen docschema.GenerateOptions) (mapping.Spec, mapping.Embed, Result, error) {
	bad := func(msg string) (mapping.Spec, mapping.Embed, Result, error) {
		return spec, mapping.Embed{}, Result{}, problem.New(http.StatusUnprocessableEntity, problem.CodeValidation, "Can't embed", msg)
	}
	if in.Kind != mapping.EmbedManyToOne && in.Kind != mapping.EmbedOneToMany {
		return bad("kind must be many-to-one or one-to-many")
	}
	if len(in.Join) == 0 {
		return bad("set the join columns")
	}
	for _, j := range in.Join {
		if child.Column(j.Child) == nil {
			return bad(fmt.Sprintf("column %s isn't in %s", j.Child, in.Table))
		}
	}
	name := in.TargetName
	if name == "" {
		name = docschema.Rename(child.Name, gen.Naming)
	}
	name = docschema.UniqueName(&spec.DocSchema.Root, name)
	e := mapping.Embed{
		ID: mapping.EmbedID(), Kind: in.Kind, Table: in.Table, Join: in.Join, OrderBy: in.OrderBy, Limit: in.Limit, Empty: in.Empty,
	}
	fields := []docschema.DocField{}
	if in.Generate {
		src := *child
		src.Columns = nil
		for _, c := range child.Columns {
			if in.IncludeJoinColumns || e.Kind == mapping.EmbedManyToOne || !joinColumn(e, c.Name) {
				src.Columns = append(src.Columns, c)
			}
		}
		fields = docschema.FromTable(&src, gen).Root.Children
	}
	obj := docschema.DocField{ID: docschema.NewID(), Name: name, Type: docschema.Object, Children: fields}
	target := obj
	if in.Kind == mapping.EmbedOneToMany {
		obj.Name = "[]"
		target = docschema.DocField{ID: docschema.NewID(), Name: name, Type: docschema.Array, Items: &obj}
	}
	e.TargetFieldID = target.ID
	spec.DocSchema.Root.Children = append(append([]docschema.DocField(nil), spec.DocSchema.Root.Children...), target)
	spec.Embeds = append(append([]mapping.Embed(nil), spec.Embeds...), e)
	if e.Kind == mapping.EmbedManyToOne {
		spec.FieldMappings = unmapForeignKey(spec.FieldMappings, e)
	}
	res := Result{FieldMappings: spec.FieldMappings, Explain: map[string]Explanation{}}
	if in.Generate {
		res = RunEmbed(&spec, e, EmbedColumns(child, e, in.IncludeJoinColumns), Options{})
		spec.FieldMappings = res.FieldMappings
	}
	spec.Normalize()
	return spec, e, res, nil
}

// unmapForeignKey drops the root mappings that copy a join's parent column (the foreign key) as is;
// expressions that combine it with other columns are kept.
func unmapForeignKey(ms []mapping.FieldMapping, e mapping.Embed) []mapping.FieldMapping {
	fk := map[string]bool{}
	for _, j := range e.Join {
		fk[j.Parent] = true
	}
	out := make([]mapping.FieldMapping, 0, len(ms))
	for _, m := range ms {
		if len(m.Sources) == 1 && m.Sources[0].Embed == "" && fk[m.Sources[0].Column] {
			continue
		}
		out = append(out, m)
	}
	return out
}

func joinColumn(e mapping.Embed, column string) bool {
	for _, j := range e.Join {
		if j.Child == column {
			return true
		}
	}
	return false
}
