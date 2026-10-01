package mapping

import (
	"strings"

	"omnidatabus/internal/docschema"
)

// Stats is the mapping score (OC-017 step 5). Stored as the automap snapshot ("10 of 23 fields
// automatically mapped") and recomputed live after every edit.
type Stats struct {
	TotalSourceColumns     int     `json:"totalSourceColumns"` // excluding ignored columns
	AutoMapped             int     `json:"autoMapped"`
	Suggested              int     `json:"suggested"`
	ManuallyMapped         int     `json:"manuallyMapped"`
	UnmappedSource         int     `json:"unmappedSource"`
	Ignored                int     `json:"ignored"`
	TotalTargetLeaves      int     `json:"totalTargetLeaves"`
	MappedTargetLeaves     int     `json:"mappedTargetLeaves"`
	RequiredTargetUnmapped int     `json:"requiredTargetUnmapped"`
	AutoMapRatio           float64 `json:"autoMapRatio"`
	CoverageRatio          float64 `json:"coverageRatio"`
	AverageConfidence      float64 `json:"averageConfidence"`
	// Embedded counts the embedded tables' columns separately (OC-021 AC2).
	Embedded *EmbeddedStats `json:"embedded,omitempty"`
	// ByNode is the score per bound scope, root first (OC-049): "root 8/10 · items 5/6".
	ByNode []NodeScore `json:"byNode,omitempty"`
}

// NodeScore is how many of a bound scope's fields are mapped.
type NodeScore struct {
	NodeID     string `json:"nodeId"` // the bound node; docschema.RootID for the root
	RelationID string `json:"relationId,omitempty"`
	Path       string `json:"path"` // dotted field names (items.product), "root" for the root
	Mapped     int    `json:"mapped"`
	Total      int    `json:"total"`
}

// EmbeddedStats is the "embedded" group of the score: columns of embedded tables.
type EmbeddedStats struct {
	Tables         int `json:"tables"`
	TotalColumns   int `json:"totalColumns"`
	AutoMapped     int `json:"autoMapped"`
	ManuallyMapped int `json:"manuallyMapped"`
	Suggested      int `json:"suggested"`
	UnmappedSource int `json:"unmappedSource"`
}

// ComputeStats counts source columns by how they're mapped. A column used by any active
// manual mapping (including accepted suggestions) counts as manual; one used only by active
// auto mappings counts as auto; one in a pending suggestion counts as suggested.
func ComputeStats(s *Spec, columns []string) Stats { return ComputeStatsWith(s, columns, nil) }

// ComputeStatsWith also counts the embedded tables' columns (embed id → column names) in the
// separate embedded group; embedded sources never count as the table's own columns.
func ComputeStatsWith(s *Spec, columns []string, embedded map[string][]string) Stats {
	var st Stats
	auto, manual, suggested := map[SourceRef]bool{}, map[SourceRef]bool{}, map[SourceRef]bool{}
	var confSum float64
	var confN int
	mappedTargets := map[string]bool{}
	for _, m := range s.FieldMappings {
		switch m.State {
		case StateActive:
			mappedTargets[m.TargetFieldID] = true
			for _, src := range m.Sources {
				if m.Origin == OriginAuto {
					auto[src] = true
				} else {
					manual[src] = true
				}
			}
			if m.Origin == OriginAuto {
				confSum += m.Confidence
				confN++
			}
		case StatePending:
			for _, src := range m.Sources {
				suggested[src] = true
			}
		}
	}
	carried := embeddedForeignKeys(s)
	for _, c := range columns {
		if s.Ignored(c) {
			st.Ignored++
			continue
		}
		ref := SourceRef{Column: c}
		if carried[c] && !manual[ref] && !auto[ref] && !suggested[ref] {
			continue // an unmapped foreign key whose row is embedded: the object carries its key
		}
		st.TotalSourceColumns++
		switch {
		case manual[ref]:
			st.ManuallyMapped++
		case auto[ref]:
			st.AutoMapped++
		case suggested[ref]:
			st.Suggested++
		default:
			st.UnmappedSource++
		}
	}
	if len(s.Relations) > 0 && embedded != nil {
		e := &EmbeddedStats{Tables: len(s.Relations)}
		for _, emb := range s.Relations {
			joins := map[string]bool{}
			for _, j := range emb.Join {
				joins[j.Child] = true
			}
			for _, c := range embedded[emb.ID] {
				ref := SourceRef{Column: c, Relation: emb.ID}
				if joins[c] && !manual[ref] && !auto[ref] && !suggested[ref] {
					continue // an unmapped join column is the relation itself, not data to map
				}
				e.TotalColumns++
				switch {
				case manual[ref]:
					e.ManuallyMapped++
				case auto[ref]:
					e.AutoMapped++
				case suggested[ref]:
					e.Suggested++
				default:
					e.UnmappedSource++
				}
			}
		}
		st.Embedded = e
	}
	for _, l := range s.DocSchema.Leaves() {
		st.TotalTargetLeaves++
		if mappedTargets[l.FieldID] {
			st.MappedTargetLeaves++
		} else if l.Required {
			st.RequiredTargetUnmapped++
		}
	}
	if st.TotalSourceColumns > 0 {
		st.AutoMapRatio = float64(st.AutoMapped) / float64(st.TotalSourceColumns)
		st.CoverageRatio = float64(st.AutoMapped+st.ManuallyMapped) / float64(st.TotalSourceColumns)
	}
	if confN > 0 {
		st.AverageConfidence = confSum / float64(confN)
	}
	if len(s.Relations) > 0 {
		st.ByNode = ByNode(s)
	}
	return st
}

// embeddedForeignKeys are the parent's join columns of its many-to-one embeds.
func embeddedForeignKeys(s *Spec) map[string]bool {
	out := map[string]bool{}
	for _, e := range s.Relations {
		if e.Kind == EmbedManyToOne {
			for _, j := range e.Join {
				out[j.Parent] = true
			}
		}
	}
	return out
}

// ByNode scores each bound scope (the root, then every relation that fills a node, parents
// before children): its mappable fields — single values, and unbound arrays of values — that
// belong to it, and how many of them have an active mapping or an aggregate (OC-049 AC5).
func ByNode(s *Spec) []NodeScore {
	scopes := FieldScopes(s)
	mapped := map[string]bool{}
	for _, m := range s.FieldMappings {
		if m.Active() {
			mapped[m.TargetFieldID] = true
		}
	}
	for _, a := range s.Aggregates {
		mapped[a.TargetFieldID] = true
	}
	bound := map[string]bool{}
	for _, r := range s.Relations {
		if !r.LookupOnly && r.TargetFieldID != "" {
			bound[r.TargetFieldID] = true
		}
	}
	out := []NodeScore{{NodeID: docschema.RootID, Path: "root"}}
	at := map[string]int{"": 0}
	order, _, is := Depths(s)
	if is != nil {
		order = nil
	}
	paths := s.DocSchema.Paths()
	for _, id := range order {
		r := s.RelationByID(id)
		if r.LookupOnly || r.TargetFieldID == "" || paths[r.TargetFieldID] == "" {
			continue
		}
		at[id] = len(out)
		out = append(out, NodeScore{NodeID: r.TargetFieldID, RelationID: id, Path: strings.ReplaceAll(paths[r.TargetFieldID], "[]", "")})
	}
	s.DocSchema.Walk(func(f *docschema.DocField, _ string, parent *docschema.DocField) {
		if parent != nil && parent.Type == docschema.Array {
			return // an array's item: its fields are walked, the item itself isn't a field
		}
		leaf := f.Type.Scalar() || (f.Type == docschema.Array && !bound[f.ID] && (f.Items == nil || f.Items.Type.Scalar()))
		if !leaf {
			return
		}
		i, ok := at[scopes[f.ID]]
		if !ok {
			return
		}
		out[i].Total++
		if mapped[f.ID] {
			out[i].Mapped++
		}
	})
	return out
}
