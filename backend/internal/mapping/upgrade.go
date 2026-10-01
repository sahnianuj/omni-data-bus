package mapping

import "encoding/json"

// UnmarshalJSON decodes a spec of any version and upgrades a 1.0 spec (UpgradeV1): reading
// never needs to know which version was stored, and saving writes v2 (OC-037).
func (s *Spec) UnmarshalJSON(b []byte) error {
	type plain Spec // without this method
	var aux struct {
		plain
		// Embeds is 1.0's flat list (relations at depth 1).
		Embeds []Relation `json:"embeds"`
	}
	if err := json.Unmarshal(b, &aux); err != nil {
		return err
	}
	*s = Spec(aux.plain)
	if s.Version() < SpecVersion && len(s.Relations) == 0 {
		s.Relations = aux.Embeds
	}
	*s = UpgradeV1(*s)
	return nil
}

// UnmarshalJSON accepts 1.0's {embed: …} as {relation: …}; old clients and bundles send it.
func (r *SourceRef) UnmarshalJSON(b []byte) error {
	type plain SourceRef
	var aux struct {
		plain
		Embed string `json:"embed"`
	}
	if err := json.Unmarshal(b, &aux); err != nil {
		return err
	}
	*r = SourceRef(aux.plain)
	if r.Relation == "" {
		r.Relation = aux.Embed
	}
	return nil
}

// Version is the spec's version: 1 when specVersion is absent (1.0).
func (s *Spec) Version() int {
	if s.SpecVersion == 0 {
		return 1
	}
	return s.SpecVersion
}

// VersionOf reads the version of an encoded spec without upgrading it.
func VersionOf(raw []byte) int {
	var v struct {
		SpecVersion int `json:"specVersion"`
	}
	if json.Unmarshal(raw, &v) != nil || v.SpecVersion == 0 {
		return 1
	}
	return v.SpecVersion
}

// UpgradeV1 turns a 1.0 spec into v2 losslessly: each embed becomes a relation of the root
// table (parentId null) with the same ID, kind, join, order, limit and empty handling, and
// sources {embed} become {relation} (both done while decoding). The mapping's 1.0 target
// becomes its location in the store (OC-036). It is idempotent, so it's safe on any spec.
func UpgradeV1(s Spec) Spec {
	if s.Version() < SpecVersion {
		for i := range s.Relations {
			s.Relations[i].ParentID = nil // 1.0 embeds hang off the root
		}
		s.SpecVersion = SpecVersion
	}
	s.Normalize()
	return s
}
