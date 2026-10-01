package automap

import (
	"net/http"

	"omnidatabus/internal/docschema"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/problem"
	"omnidatabus/internal/schema"
)

// RunScope auto-maps one bound scope of a composed document (OC-049): nodeID is a bound
// node (or its array's item), or the root (docschema.RootID or ""). Candidates are the columns
// of the relation bound there (and its junction's payload columns), or the root table's for
// the root; targets are the unmapped fields that belong to that scope, never those inside a
// deeper bound node. Fields that are already mapped are never changed.
func RunScope(spec *mapping.Spec, nodeID string, root *schema.Table, rel mapping.RelatedTables) (Result, error) {
	if nodeID == "" || nodeID == docschema.RootID {
		return Run(spec, Columns(root), Options{Mode: ModeFill}), nil
	}
	r := boundAt(spec, nodeID)
	if r == nil {
		return Result{}, problem.New(http.StatusUnprocessableEntity, problem.CodeValidation, "Not a bound node",
			"Auto-map runs for the root or for an object or array bound to a table; "+nodeID+" isn't bound.")
	}
	var child, junction *schema.Table
	if rel != nil {
		child = rel(r.Table.Schema, r.Table.Name)
		if r.Via != nil {
			junction = rel(r.Via.Table.Schema, r.Via.Table.Name)
		}
	}
	if child == nil {
		return Result{}, problem.New(http.StatusUnprocessableEntity, problem.CodeValidation, "Table not found",
			r.Table.String()+" isn't in the latest schema snapshot.")
	}
	cols := EmbedColumns(child, *r, false)
	if junction != nil {
		for _, c := range r.Via.Columns {
			if jc := junction.Column(c); jc != nil {
				cols = append(cols, SourceCol{Name: c, JSONType: jc.JSONType, Ordinal: len(cols) + 1, Table: junction.Name, EmbedPath: r.ID + ":via"})
			}
		}
	}
	res := RunEmbed(spec, *r, cols, Options{Mode: ModeFill})
	// RunEmbed sees the relation's whole sub-tree; keep only what it proposed for this scope.
	scopes := mapping.FieldScopes(spec)
	before := map[string]bool{}
	for _, m := range spec.FieldMappings {
		before[m.ID] = true
	}
	kept := res.FieldMappings[:0]
	for _, m := range res.FieldMappings {
		if before[m.ID] || scopes[m.TargetFieldID] == r.ID {
			kept = append(kept, m)
		}
	}
	res.FieldMappings = kept
	return res, nil
}

// RunAll runs RunScope for the root and then every bound scope, parents before
// children (the composer's Auto-map all).
func RunAll(spec *mapping.Spec, root *schema.Table, rel mapping.RelatedTables) (Result, error) {
	next := *spec
	res, err := RunScope(&next, docschema.RootID, root, rel)
	if err != nil {
		return res, err
	}
	next.FieldMappings = res.FieldMappings
	explain := res.Explain
	order, _, is := mapping.Depths(&next)
	if is != nil {
		return res, nil
	}
	for _, id := range order {
		r := next.RelationByID(id)
		if r.LookupOnly || r.TargetFieldID == "" || next.DocSchema.Find(r.TargetFieldID) == nil {
			continue
		}
		part, err := RunScope(&next, r.TargetFieldID, root, rel)
		if err != nil {
			continue // a relation whose table is gone is reported by validation
		}
		next.FieldMappings = part.FieldMappings
		for k, v := range part.Explain {
			if explain == nil {
				explain = map[string]Explanation{}
			}
			explain[k] = v
		}
	}
	res.FieldMappings, res.Explain = next.FieldMappings, explain
	return res, nil
}

// boundAt is the relation bound at a node, or at the array whose item the node is.
func boundAt(spec *mapping.Spec, nodeID string) *mapping.Relation {
	for i := range spec.Relations {
		r := &spec.Relations[i]
		if r.LookupOnly || r.TargetFieldID == "" {
			continue
		}
		if r.TargetFieldID == nodeID {
			return r
		}
		if f := spec.DocSchema.Find(r.TargetFieldID); f != nil && f.Items != nil && f.Items.ID == nodeID {
			return r
		}
	}
	return nil
}
