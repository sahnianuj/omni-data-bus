package mappings

import (
	"context"

	"omnidatabus/internal/automap"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/relations"
	"omnidatabus/internal/schema"
	"omnidatabus/internal/store"
)

// SuggestInput is POST …/suggest-bindings (OC-060).
type SuggestInput struct {
	// NodeID limits the result to one node ("" = every unbound object and array).
	NodeID string `json:"nodeId,omitempty"`
	// Root also ranks the driving table from the root's fields.
	Root bool `json:"root,omitempty"`
	// SpecOverride suggests for unsaved edits, like preview.
	SpecOverride *mapping.Spec `json:"specOverride,omitempty"`
}

// SuggestBindings proposes the driving table and a binding for each unbound object and array
// of a composed design (OC-060). It changes nothing: the composer applies what the user
// accepts with its own bind operation, then auto-maps.
func (s *Service) SuggestBindings(ctx context.Context, ws store.WorkspaceID, projectID, id string, in SuggestInput) (automap.Suggestions, error) {
	m, spec, p, _, err := s.load(ctx, ws, projectID, id)
	if err != nil {
		return automap.Suggestions{}, err
	}
	if in.SpecOverride != nil {
		spec = *in.SpecOverride
		spec.Normalize()
		rootOf(m, &spec)
	}
	var tables []mapping.TableRef
	for _, sc := range p.snap.Schemas {
		for _, t := range sc.Tables {
			if t.Readable {
				tables = append(tables, mapping.TableRef{Schema: sc.Name, Name: t.Name})
			}
		}
	}
	return automap.SuggestBindings(automap.SuggestInput{
		Spec: &spec, Driving: driving(m, &spec), Root: in.Root, NodeID: in.NodeID, Tables: tables,
		Table: func(t mapping.TableRef) *schema.Table { return p.snap.Table(t.Schema, t.Name) },
		Links: p.links(),
	}), nil
}

// links are the relations a node can bind to from a table: foreign-key and virtual hops, and
// many-to-many through junctions (OC-042/044).
func (p projectCtx) links() automap.Links {
	g := relations.Build(p.snap, p.virtual)
	return func(from mapping.TableRef) []automap.Link {
		var out []automap.Link
		for _, h := range g.Hops(from) {
			out = append(out, automap.Link{Kind: h.Kind, Table: h.To, Join: h.Join, Virtual: h.Virtual})
		}
		for _, j := range relations.Junctions(p.snap, from) {
			out = append(out, automap.Link{Kind: mapping.RelationManyToMany, Table: j.Table, Via: &mapping.Via{Table: j.Via, ParentJoin: j.ParentJoin, ChildJoin: j.ChildJoin, Columns: j.PayloadColumns}})
		}
		return out
	}
}
