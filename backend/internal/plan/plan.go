// Package plan compiles a mapping spec (v2, OC-037) into the one execution plan that the
// migration pipeline, the mapping preview and the headless runner (OC-051) all run, so a
// document is built the same way wherever it's built.
package plan

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"omnidatabus/internal/mapping"
	"omnidatabus/internal/related"
	"omnidatabus/internal/schema"
	"omnidatabus/internal/transform"
)

// Relation strategies: batched lookups (OC-021) or sorted-merge (OC-046).
const (
	StrategyBatched     = related.StrategyBatched
	StrategySortedMerge = related.StrategySortedMerge
)

// ExecutionPlan is how one mapping's documents are built.
type ExecutionPlan struct {
	Root mapping.TableRef
	// Columns are the root table's columns the documents need (projection pushdown): mapped
	// columns, key template columns and the relations' parent join columns, in table order.
	Columns []string
	// Relations are topologically ordered: parents before children.
	Relations []RelationStep
	// Program is compiled once to check the spec; workers call NewProgram (a Program isn't
	// safe for concurrent use). Nil when the spec doesn't compile (see the issues).
	Program *transform.Program
	Key     *mapping.KeyTemplate
	// Hash identifies the plan (sha256 of its canonical form): recorded on jobs and bundles so
	// runs can be compared (OC-056).
	Hash string

	table   *schema.Table
	spec    *mapping.Spec
	opts    transform.Options
	lookups []related.Plan
	all     bool // an expression may read any root column
}

// RelationStep is one relation's lookup.
type RelationStep struct {
	Relation mapping.Relation
	Depth    int
	// Columns of the related table that are read: mapped columns, join and order columns.
	Columns  []string
	Strategy string
	// StrategyNote explains the strategy (the Embed dialog and the job log show it).
	StrategyNote string `json:"-"`
}

// Error is a plan that can't be built: the relation tree is invalid (RELATION_*), or a
// related table isn't in the latest snapshot (Code "SOURCE_TABLE_MISSING").
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Message }

// CodeRelatedTableMissing is Error.Code for a related table that's gone from the source.
const CodeRelatedTableMissing = "SOURCE_TABLE_MISSING"

// Input is what a plan is compiled from.
type Input struct {
	Spec *mapping.Spec
	// Root names the mapped table and Table is it in the latest snapshot.
	Root  mapping.TableRef
	Table *schema.Table
	// Related resolves related tables from the same snapshot.
	Related mapping.RelatedTables
	// Options are the transform options (the job's start time for now()).
	Options transform.Options
}

// Compile builds the plan. Issues are the transform compiler's findings; Program is nil when
// one of them is an error. The error is an *Error when the plan can't be built at all.
func Compile(in Input) (*ExecutionPlan, []mapping.Issue, error) {
	s := *in.Spec
	s.Normalize()
	root, rel, opts := in.Table, in.Related, in.Options
	order, depth, is := mapping.Depths(&s)
	if is != nil {
		return nil, nil, &Error{Code: is.Code, Message: is.Message}
	}
	lookups, err := related.Plans(&s, root, rel)
	if err != nil {
		return nil, nil, &Error{Code: CodeRelatedTableMissing, Message: err.Error()}
	}
	opts.Related = related.Tables(&s, rel)
	p := &ExecutionPlan{Root: in.Root, table: root, spec: &s, opts: opts, lookups: lookups}
	if kt, err := mapping.ParseKeyTemplate(s.Key.Template); err == nil {
		p.Key = kt
	}
	p.Columns, p.all = rootColumns(&s, root, p.Key)
	byID := map[string]related.Plan{}
	notes := map[string]string{}
	for i := range lookups {
		st, note := chooseStrategy(*s.RelationByID(lookups[i].ID), lookups[i].Depth, root, rel)
		lookups[i].Strategy = st
		byID[lookups[i].ID] = lookups[i]
		notes[lookups[i].ID] = note
	}
	for _, id := range order {
		r := s.RelationByID(id)
		step := RelationStep{Relation: *r, Depth: depth[id], Strategy: StrategyBatched, StrategyNote: notes[id]}
		if l, ok := byID[id]; ok {
			step.Strategy = l.Strategy
			for i, c := range l.Query.Table.Columns {
				if l.Query.Table.Project[i] {
					step.Columns = append(step.Columns, c.Name)
				}
			}
		}
		p.Relations = append(p.Relations, step)
	}
	prog, issues := transform.Compile(&s, root, opts)
	p.Program = prog
	p.Hash = p.hash()
	return p, issues, nil
}

// rootColumns are the root columns documents need; all is true when an expression may read
// row.<any column>, so every column is read.
func rootColumns(s *mapping.Spec, t *schema.Table, key *mapping.KeyTemplate) ([]string, bool) {
	need := map[string]bool{}
	all := false
	scopes := mapping.FieldScopes(s)
	for _, fm := range s.FieldMappings {
		if fm.State != mapping.StateActive {
			continue
		}
		// Fields inside relations may read root columns too (OC-043 AC4).
		for _, src := range fm.Sources {
			if src.Relation == "" {
				need[src.Column] = true
			}
		}
		if scopes[fm.TargetFieldID] == "" {
			for _, st := range fm.Transforms {
				all = all || st.Op == "expression"
			}
		}
	}
	// Expressions anywhere may read the root row as $root (or $parent at depth 1, OC-047).
	for _, src := range mapping.Expressions(s) {
		names := mapping.ExprIdentifiers(src)
		all = all || names["$root"] || names["$parent"]
	}
	if key != nil {
		for _, c := range key.Columns() {
			need[c] = true
		}
	}
	for _, c := range related.ParentColumns(s) {
		need[c] = true
	}
	var out []string
	for _, c := range t.Columns {
		if all || need[c.Name] {
			out = append(out, c.Name)
		}
	}
	return out, all
}

// Project is the root table's projection for a read: the plan's columns plus extra (the
// read's keyset columns), aligned with the table's columns.
func (p *ExecutionPlan) Project(extra ...string) []bool {
	need := map[string]bool{}
	for _, c := range p.Columns {
		need[c] = true
	}
	for _, c := range extra {
		need[c] = true
	}
	out := make([]bool, len(p.table.Columns))
	for i, c := range p.table.Columns {
		out[i] = p.all || need[c.Name]
	}
	return out
}

// Lookups are the batched related-row lookups the relation resolver runs (OC-021).
func (p *ExecutionPlan) Lookups() []related.Plan { return p.lookups }

// HasRelations reports whether documents need related rows.
func (p *ExecutionPlan) HasRelations() bool { return len(p.lookups) > 0 }

// Spec is the (normalized) spec the plan was compiled from.
func (p *ExecutionPlan) Spec() *mapping.Spec { return p.spec }

// SetMaxDocBytes sets the size guard's limit for programs compiled after it (the target's,
// when it allows less than Couchbase's 20 MiB; OC-046).
func (p *ExecutionPlan) SetMaxDocBytes(n int64) {
	if n > 0 {
		p.opts.MaxDocBytes = int(n)
	}
}

// NewProgram compiles a fresh program for one transform worker.
func (p *ExecutionPlan) NewProgram() (*transform.Program, error) {
	prog, issues := transform.Compile(p.spec, p.table, p.opts)
	if prog == nil {
		return nil, errors.New(ErrorMessages(issues))
	}
	return prog, nil
}

// ErrorMessages joins the error-severity issues ("; ").
func ErrorMessages(issues []mapping.Issue) string {
	var msgs []string
	for _, i := range issues {
		if i.Severity == mapping.SeverityError {
			msgs = append(msgs, i.Message)
		}
	}
	return strings.Join(msgs, "; ")
}

// canonical is what Hash covers: everything that decides the documents, nothing that doesn't
// (auto-map confidence, reasons, rejected suggestions).
type canonical struct {
	Version   int              `json:"v"`
	Root      mapping.TableRef `json:"root"`
	Columns   []string         `json:"columns"`
	Relations []canonicalStep  `json:"relations"`
	Spec      canonicalSpec    `json:"spec"`
}

type canonicalStep struct {
	Relation mapping.Relation `json:"relation"`
	Depth    int              `json:"depth"`
	Columns  []string         `json:"columns"`
	Strategy string           `json:"strategy"`
}

type canonicalMapping struct {
	ID            string              `json:"id"`
	TargetFieldID string              `json:"targetFieldId"`
	Sources       []mapping.SourceRef `json:"sources"`
	Transforms    []mapping.Step      `json:"transforms"`
	NullHandling  string              `json:"nullHandling,omitempty"`
}

type canonicalSpec struct {
	DocSchema     json.RawMessage     `json:"docSchema"`
	Key           mapping.KeySpec     `json:"key"`
	TypeField     mapping.TypeField   `json:"typeField"`
	NullHandling  string              `json:"nullHandling"`
	OmitUnmapped  bool                `json:"omitUnmapped"`
	ExpirySec     int64               `json:"expirySec"`
	FieldMappings []canonicalMapping  `json:"fieldMappings"`
	Aggregates    []mapping.Aggregate `json:"aggregates"`
}

// hash is sha256 of the canonical JSON: encoding/json sorts map keys (transform args), and
// every list is in a defined order, so equal plans hash equal.
func (p *ExecutionPlan) hash() string {
	s := p.spec
	doc, _ := json.Marshal(mapping.CanonicalField(s.DocSchema.Root))
	c := canonical{Version: 1, Root: p.Root, Columns: p.Columns, Spec: canonicalSpec{
		DocSchema: doc, Key: s.Key, TypeField: s.TypeField, NullHandling: s.NullHandling,
		OmitUnmapped: s.OmitUnmapped, ExpirySec: s.ExpirySec, Aggregates: s.Aggregates,
	}}
	for _, r := range p.Relations {
		c.Relations = append(c.Relations, canonicalStep{Relation: r.Relation, Depth: r.Depth, Columns: r.Columns, Strategy: r.Strategy})
	}
	for _, m := range s.FieldMappings {
		if m.Active() {
			c.Spec.FieldMappings = append(c.Spec.FieldMappings, canonicalMapping{
				ID: m.ID, TargetFieldID: m.TargetFieldID, Sources: m.Sources, Transforms: m.Transforms, NullHandling: m.NullHandling,
			})
		}
	}
	b, err := json.Marshal(c)
	if err != nil {
		panic(fmt.Sprintf("plan: canonical form: %v", err)) // only plain data: can't fail
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
