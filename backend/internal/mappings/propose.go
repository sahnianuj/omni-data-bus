package mappings

import (
	"context"
	"net/http"
	"strings"
	"time"

	"omnidatabus/internal/automap"
	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/docschema"
	"omnidatabus/internal/httpapi"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/problem"
	"omnidatabus/internal/schema"
	"omnidatabus/internal/store"
)

// ProposeInput is POST …/mappings/{mid}/propose (OC-163).
type ProposeInput struct {
	// SpecOverride proposes for unsaved edits (an imported design not saved yet), like preview.
	SpecOverride *mapping.Spec `json:"specOverride,omitempty"`
	// DrivingTable fixes the driving table ("schema.table"); empty chooses it.
	DrivingTable string `json:"drivingTable,omitempty"`
	// Base keeps the mapping's bindings and mappings and proposes only what's missing (OC-170).
	Base bool `json:"base,omitempty"`
	// NodeID limits what's proposed to the fields under that node (OC-168).
	NodeID string `json:"nodeId,omitempty"`
}

// ProposeDesignInput is POST …/mappings/propose: a design with no mapping yet (New mapping
// from a design).
type ProposeDesignInput struct {
	DocSchema    docschema.Schema `json:"docSchema"`
	DrivingTable string           `json:"drivingTable,omitempty"`
}

// Propose proposes the whole mapping of the mapping's design against the project's connected
// source (OC-163). It changes nothing: the composer applies what the user keeps.
func (s *Service) Propose(ctx context.Context, ws store.WorkspaceID, projectID, id string, in ProposeInput) (automap.Proposal, error) {
	m, spec, p, _, err := s.load(ctx, ws, projectID, id)
	if err != nil {
		return automap.Proposal{}, err
	}
	if in.SpecOverride != nil {
		spec = *in.SpecOverride
		spec.Normalize()
		rootOf(m, &spec)
	}
	values, done := s.valueSampler(ctx, ws, p)
	defer done()
	return p.propose(&spec, in.DrivingTable, in.Base, in.NodeID, values)
}

// valueSampler reads a column's distinct values from the project's connected source, opening
// it on first use (a type column of variants, OC-171); done closes it.
func (s *Service) valueSampler(ctx context.Context, ws store.WorkspaceID, p projectCtx) (func(mapping.TableRef, string, int) []string, func()) {
	var sess source.Session
	opened := false
	values := func(t mapping.TableRef, col string, n int) []string {
		if !opened {
			opened = true
			if ss, _, err := s.openSource(ctx, ws, p.source.ID); err == nil {
				sess = ss
			}
		}
		tb := p.snap.Table(t.Schema, t.Name)
		if sess == nil || tb == nil {
			return nil
		}
		c, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		return source.DistinctValues(c, sess, t.Schema, tb, col, n)
	}
	return values, func() {
		if sess != nil {
			_ = sess.Close()
		}
	}
}

// ProposeDesign proposes a mapping for a design before the mapping exists: which table it's
// driven from, and everything else (OC-163 AC3).
func (s *Service) ProposeDesign(ctx context.Context, ws store.WorkspaceID, projectID string, in ProposeDesignInput) (automap.Proposal, error) {
	p, err := s.project(ctx, ws, projectID)
	if err != nil {
		return automap.Proposal{}, err
	}
	if len(in.DocSchema.Root.Children) == 0 {
		return automap.Proposal{}, problem.New(http.StatusUnprocessableEntity, problem.CodeValidation, "Empty design", "Import a design with at least one field.")
	}
	spec := mapping.Spec{SpecVersion: mapping.SpecVersion, Mode: mapping.ModeComposed, DocSchema: in.DocSchema}
	spec.Normalize()
	values, done := s.valueSampler(ctx, ws, p)
	defer done()
	return p.propose(&spec, in.DrivingTable, false, "", values)
}

func (p projectCtx) propose(spec *mapping.Spec, driving string, base bool, nodeID string, values func(mapping.TableRef, string, int) []string) (automap.Proposal, error) {
	var tables []mapping.TableRef
	for _, sc := range p.snap.Schemas {
		for _, t := range sc.Tables {
			if t.Readable {
				tables = append(tables, mapping.TableRef{Schema: sc.Name, Name: t.Name})
			}
		}
	}
	in := automap.ProposeInput{
		Spec: spec, Base: base, NodeID: nodeID, Tables: tables, Links: p.links(), MaxDepth: mapping.MaxRelationDepth(), Values: values,
		Table: func(t mapping.TableRef) *schema.Table { return p.snap.Table(t.Schema, t.Name) },
	}
	if driving != "" {
		sch, name, _ := strings.Cut(driving, ".")
		ref := mapping.TableRef{Schema: sch, Name: name}
		if p.snap.Table(ref.Schema, ref.Name) == nil {
			return automap.Proposal{}, tableNotFound(ref.Schema, ref.Name)
		}
		in.Driving = &ref
	}
	return automap.Propose(in), nil
}

func (h Handlers) propose(w http.ResponseWriter, r *http.Request) {
	var in ProposeInput
	if r.ContentLength != 0 {
		if err := decodeLarge(w, r, &in); err != nil {
			problem.Write(w, r, err)
			return
		}
	}
	res, err := h.Svc.Propose(r.Context(), principal(r).WorkspaceID, httpapi.URLParam(r, "id"), httpapi.URLParam(r, "mid"), in)
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.WriteJSON(w, http.StatusOK, res)
}

func (h Handlers) proposeDesign(w http.ResponseWriter, r *http.Request) {
	var in ProposeDesignInput
	if err := decodeLarge(w, r, &in); err != nil {
		problem.Write(w, r, err)
		return
	}
	res, err := h.Svc.ProposeDesign(r.Context(), principal(r).WorkspaceID, httpapi.URLParam(r, "id"), in)
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.WriteJSON(w, http.StatusOK, res)
}
