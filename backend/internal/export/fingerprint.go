package export

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"omnidatabus/internal/mapping"
	"omnidatabus/internal/plan"
	"omnidatabus/internal/schema"
)

// Fingerprint is the shape of the source tables a bundle reads, so a runner can tell whether the
// source changed since the export (OC-050). Only referenced columns count: adding an unrelated
// column to a table isn't drift.
type Fingerprint struct {
	SHA256 string                      `json:"sha256"`
	Tables map[string]TableFingerprint `json:"tables"`
}

// TableFingerprint is one table: the hash, and the columns it covers so a drift can be named.
type TableFingerprint struct {
	SHA256  string        `json:"sha256"`
	Columns []ColumnPrint `json:"columns"`
}

// ColumnPrint is what's compared per column.
type ColumnPrint struct {
	Name     string `json:"name"`
	UDTName  string `json:"udtName"`
	Nullable bool   `json:"nullable"`
}

// References are the columns each table's mappings read, keyed "schema.table".
type References map[string]map[string]bool

func (r References) add(t mapping.TableRef, cols ...string) {
	k := t.Schema + "." + t.Name
	if r[k] == nil {
		r[k] = map[string]bool{}
	}
	for _, c := range cols {
		if c != "" {
			r[k][c] = true
		}
	}
}

// Referenced lists the tables and columns a mapping's plan reads: the root table's projected
// columns and read key, each relation's columns, and junction tables' join and exposed columns.
func Referenced(refs References, pl *plan.ExecutionPlan, root *schema.Table, spec *mapping.Spec) {
	refs.add(pl.Root, pl.Columns...)
	refs.add(pl.Root, root.PrimaryKey...)
	refs.add(pl.Root, spec.GroupBy()...)
	for _, st := range pl.Relations {
		refs.add(st.Relation.Table, st.Columns...)
		if v := st.Relation.Via; v != nil {
			refs.add(v.Table, v.Columns...)
			for _, j := range v.ParentJoin {
				refs.add(v.Table, j.Via)
			}
			for _, j := range v.ChildJoin {
				refs.add(v.Table, j.Via)
			}
		}
	}
}

// ComputeFingerprint fingerprints the referenced columns as they are in snap. A referenced
// table or column missing from snap is left out (the plan wouldn't have compiled).
func ComputeFingerprint(snap *schema.Snapshot, refs References) Fingerprint {
	fp := Fingerprint{Tables: map[string]TableFingerprint{}}
	names := make([]string, 0, len(refs))
	for k := range refs {
		names = append(names, k)
	}
	sort.Strings(names)
	var all []string
	for _, k := range names {
		schemaName, table, _ := strings.Cut(k, ".")
		t := snap.Table(schemaName, table)
		if t == nil {
			continue
		}
		tf := TableFingerprint{Columns: []ColumnPrint{}}
		for _, c := range t.Columns {
			if refs[k][c.Name] {
				tf.Columns = append(tf.Columns, ColumnPrint{Name: c.Name, UDTName: c.UDTName, Nullable: c.Nullable})
			}
		}
		raw, _ := Canonical(tf.Columns)
		tf.SHA256 = sum(raw)
		fp.Tables[k] = tf
		all = append(all, k+"="+tf.SHA256)
	}
	fp.SHA256 = sum([]byte(strings.Join(all, "\n")))
	return fp
}

// Drift is how one table differs from its fingerprint.
type Drift struct {
	Table   string   `json:"table"`
	Missing bool     `json:"missing,omitempty"` // the table is gone
	Removed []string `json:"removed,omitempty"` // referenced columns that are gone
	// Retyped are columns whose type or nullability changed: "quantity: int4 → text".
	Retyped []string `json:"retyped,omitempty"`
	// Incompatible is true when the run can't continue even with --allow-drift.
	Incompatible bool `json:"incompatible,omitempty"`
}

// String is a one-line description for logs and the preflight report.
func (d Drift) String() string {
	if d.Missing {
		return d.Table + ": table missing"
	}
	var parts []string
	if len(d.Removed) > 0 {
		parts = append(parts, "removed "+strings.Join(d.Removed, ", "))
	}
	if len(d.Retyped) > 0 {
		parts = append(parts, "changed "+strings.Join(d.Retyped, ", "))
	}
	return d.Table + ": " + strings.Join(parts, "; ")
}

// CompareFingerprint compares the exported fingerprint with the live schema. A retyped column
// is compatible when values can still be coerced (OC-020 converts text, numbers, booleans and
// dates between each other, row by row); a removed column or table, or a change between an
// array or JSON value and a scalar, isn't.
func CompareFingerprint(want Fingerprint, live *schema.Snapshot) []Drift {
	names := make([]string, 0, len(want.Tables))
	for k := range want.Tables {
		names = append(names, k)
	}
	sort.Strings(names)
	var out []Drift
	for _, k := range names {
		tf := want.Tables[k]
		schemaName, table, _ := strings.Cut(k, ".")
		t := live.Table(schemaName, table)
		if t == nil {
			out = append(out, Drift{Table: k, Missing: true, Incompatible: true})
			continue
		}
		d := Drift{Table: k}
		for _, c := range tf.Columns {
			lc := t.Column(c.Name)
			switch {
			case lc == nil:
				d.Removed = append(d.Removed, c.Name)
				d.Incompatible = true
			case lc.UDTName != c.UDTName:
				d.Retyped = append(d.Retyped, fmt.Sprintf("%s: %s → %s", c.Name, c.UDTName, lc.UDTName))
				if family(lc.UDTName) != family(c.UDTName) {
					d.Incompatible = true
				}
			case lc.Nullable != c.Nullable:
				d.Retyped = append(d.Retyped, fmt.Sprintf("%s: %s → %s", c.Name, nullability(c.Nullable), nullability(lc.Nullable)))
			}
		}
		if len(d.Removed) > 0 || len(d.Retyped) > 0 {
			out = append(out, d)
		}
	}
	return out
}

func nullability(n bool) string {
	if n {
		return "nullable"
	}
	return "not null"
}

// family groups types by whether OC-020's coercions can convert between them.
func family(udt string) string {
	switch {
	case strings.HasPrefix(udt, "_"):
		return "array"
	case udt == "json" || udt == "jsonb":
		return "json"
	case udt == "bytea":
		return "binary"
	}
	return "scalar"
}

// DriftMessage summarizes drifts for an error or warning.
func DriftMessage(ds []Drift) string {
	parts := make([]string, len(ds))
	for i, d := range ds {
		parts[i] = d.String()
	}
	return strings.Join(parts, "; ")
}

// MarshalJSON keeps a nil table map as {} (the schema requires an object).
func (f Fingerprint) MarshalJSON() ([]byte, error) {
	type plain Fingerprint
	if f.Tables == nil {
		f.Tables = map[string]TableFingerprint{}
	}
	return json.Marshal(plain(f))
}
