package mapping

import (
	"fmt"
	"sort"

	"omnidatabus/internal/docschema"
	"omnidatabus/internal/schema"
)

// Issue severities.
const (
	SeverityError   = "error"
	SeverityWarning = "warning"
	SeverityInfo    = "info"
)

// Issue codes (OC-015, OC-016, OC-019, OC-022).
const (
	CodeTypeFieldCollision    = "TYPE_FIELD_COLLISION"
	CodeTypeFieldInvalid      = "TYPE_FIELD_INVALID"
	CodeUnknownTargetField    = "UNKNOWN_TARGET_FIELD"
	CodeSourceColumnMissing   = "SOURCE_COLUMN_MISSING"
	CodeDuplicateTarget       = "DUPLICATE_TARGET"
	CodeRequiredFieldUnmapped = "REQUIRED_FIELD_UNMAPPED"
	CodeSuggestionsPending    = "SUGGESTIONS_PENDING"
	CodeLossyConversion       = "LOSSY_CONVERSION"
	CodeTypeIncompatible      = "TYPE_INCOMPATIBLE"
	CodeTargetKeyspaceMissing = "TARGET_KEYSPACE_MISSING"
	CodeUnmappedSourceColumns = "UNMAPPED_SOURCE_COLUMNS"
	CodeInvalidMapping        = "INVALID_MAPPING"
	CodeTransformInvalid      = "TRANSFORM_INVALID"
)

// Fix is a one-click remedy the UI can apply (OC-022 AC2).
type Fix struct {
	Kind  string `json:"kind"`  // mark-optional | accept-suggestions | add-transform | enable-create
	Label string `json:"label"` // e.g. "Mark optional"
	Step  *Step  `json:"step,omitempty"`
}

// Issue is one validation finding. Path is a target field path (or "key", "typeField",
// "target", "source.<column>").
type Issue struct {
	Severity       string `json:"severity"`
	Code           string `json:"code"`
	Path           string `json:"path"`
	Message        string `json:"message"`
	FieldID        string `json:"fieldId,omitempty"`
	FieldMappingID string `json:"fieldMappingId,omitempty"`
	Fix            *Fix   `json:"fix,omitempty"`
	// Transform issues (OC-020): which step, and for expressions the 1-based position.
	Op      string `json:"op,omitempty"`
	OpIndex *int   `json:"opIndex,omitempty"`
	Line    int    `json:"line,omitempty"`
	Column  int    `json:"column,omitempty"`
}

// Context is what Validate checks the spec against.
type Context struct {
	Table *schema.Table // the source table in the latest snapshot
	// TargetExists is nil when the keyspace wasn't checked (saves don't contact the cluster).
	TargetExists *bool
	Target       Target
	// Related resolves embedded tables (OC-021); nil = none are known.
	Related RelatedTables
}

// sourceColumn resolves a mapping source: a column of the table, or of an embedded table.
func (c Context) sourceColumn(s *Spec, t *schema.Table, src SourceRef) *schema.Column {
	if src.Embed == "" {
		return t.Column(src.Column)
	}
	e := s.EmbedByID(src.Embed)
	if e == nil || c.Related == nil {
		return nil
	}
	if ct := c.Related(e.Table.Schema, e.Table.Name); ct != nil {
		return ct.Column(src.Column)
	}
	return nil
}

var severityRank = map[string]int{SeverityError: 0, SeverityWarning: 1, SeverityInfo: 2}

// Validate aggregates the structural rules: document design, key template and uniqueness,
// type field, references (target fields and source columns, i.e. drift), duplicate targets,
// required fields, pending suggestions, conversions, keyspace and unmapped columns.
// Transform compile issues are added by the service (internal/transform). Issues are sorted
// by severity, then path, then code.
func Validate(s *Spec, ctx Context) []Issue {
	issues := []Issue{}
	add := func(i Issue) { issues = append(issues, i) }

	for _, di := range docschema.Validate(&s.DocSchema) {
		add(Issue{Severity: SeverityError, Code: di.Code, Path: di.Path, FieldID: di.FieldID, Message: di.Message})
	}
	paths := s.DocSchema.Paths()
	leaves := map[string]docschema.Leaf{}
	for _, l := range s.DocSchema.Leaves() {
		leaves[l.FieldID] = l
	}
	t := ctx.Table
	if t == nil {
		t = &schema.Table{}
	}

	// Key template (OC-016).
	switch kt, err := ParseKeyTemplate(s.Key.Template); {
	case s.Key.Template == "":
		add(Issue{Severity: SeverityError, Code: CodeKeyTemplateMissing, Path: "key", Message: "Set a document key template, e.g. customer::{customer_id}."})
	case err != nil:
		add(Issue{Severity: SeverityError, Code: CodeKeyTemplateInvalid, Path: "key", Message: "The key template is invalid: " + err.Error()})
	default:
		a := AnalyzeKey(kt, t)
		for _, c := range a.UnknownColumns {
			add(Issue{Severity: SeverityError, Code: CodeSourceColumnMissing, Path: "key", Message: "The key uses column " + c + ", which isn't in the source table."})
		}
		for _, code := range a.Codes {
			msg := map[string]string{
				CodeKeyMayCollide:    "The key's columns don't cover the primary key or a unique constraint, so documents may overwrite each other.",
				CodeKeyNonIdempotent: "{$uuid} keys are new on every run: re-running creates duplicates. Choose unique columns.",
				CodeKeyMayBeTooLong:  fmt.Sprintf("Keys may reach %d bytes; Couchbase allows %d (rows over the limit fail). Consider |hash.", a.MaxBytes, MaxKeyBytes),
			}[code]
			add(Issue{Severity: SeverityWarning, Code: code, Path: "key", Message: msg})
		}
	}

	// Type field (OC-016 AC5).
	if s.TypeField.Enabled {
		if s.TypeField.Name == "" {
			add(Issue{Severity: SeverityError, Code: CodeTypeFieldInvalid, Path: "typeField", Message: "Give the type field a name."})
		}
		for _, c := range s.DocSchema.Root.Children {
			if c.Name == s.TypeField.Name {
				add(Issue{
					Severity: SeverityError, Code: CodeTypeFieldCollision, Path: "typeField", FieldID: c.ID,
					Message: "The type field '" + c.Name + "' collides with a designed field; rename one of them.",
				})
			}
		}
	}

	// Field mappings: references, duplicates, conversions.
	activeTargets := map[string]string{}
	pending := 0
	ids := map[string]bool{}
	for _, m := range s.FieldMappings {
		path, known := paths[m.TargetFieldID]
		if m.ID == "" || ids[m.ID] {
			add(Issue{Severity: SeverityError, Code: CodeInvalidMapping, Path: path, FieldMappingID: m.ID, Message: "Every field mapping needs a unique id."})
		}
		ids[m.ID] = true
		if !known {
			add(Issue{
				Severity: SeverityError, Code: CodeUnknownTargetField, Path: "fieldMappings." + m.ID, FieldMappingID: m.ID,
				Message: "The mapping targets field " + m.TargetFieldID + ", which isn't in the design.",
			})
			continue
		}
		if m.State != StateActive && m.State != StatePending && m.State != StateRejected {
			add(Issue{Severity: SeverityError, Code: CodeInvalidMapping, Path: path, FieldMappingID: m.ID, Message: "Unknown mapping state '" + m.State + "'."})
		}
		if m.State == StateRejected {
			continue
		}
		missing := false
		for _, src := range m.Sources {
			if ctx.sourceColumn(s, t, src) == nil {
				missing = true
				table := "the source table"
				if src.Embed != "" {
					table = "embedded table " + src.Embed
				}
				add(Issue{
					Severity: SeverityError, Code: CodeSourceColumnMissing, Path: path, FieldID: m.TargetFieldID, FieldMappingID: m.ID,
					Message: "Column " + src.Column + " is no longer in " + table + " (it was dropped or renamed). Re-map this field.",
				})
			}
		}
		if m.State == StatePending {
			pending++
			continue
		}
		if prev, dup := activeTargets[m.TargetFieldID]; dup {
			add(Issue{
				Severity: SeverityError, Code: CodeDuplicateTarget, Path: path, FieldID: m.TargetFieldID, FieldMappingID: m.ID,
				Message: "Field " + path + " has two mappings (" + prev + ", " + m.ID + "); keep one.",
			})
		}
		activeTargets[m.TargetFieldID] = m.ID
		if missing || len(m.Sources) != 1 || len(m.Transforms) > 0 {
			continue // multi-source and transformed mappings are type-checked by the compiler
		}
		col := ctx.sourceColumn(s, t, m.Sources[0])
		leaf, isLeaf := leaves[m.TargetFieldID]
		if !isLeaf {
			continue
		}
		class, fix := Compat(col.JSONType, leaf.Type)
		switch class {
		case ClassIncompatible:
			add(Issue{
				Severity: SeverityError, Code: CodeTypeIncompatible, Path: path, FieldID: m.TargetFieldID, FieldMappingID: m.ID,
				Message: fmt.Sprintf("%s (%s) can't be written to %s (%s).", col.Name, col.JSONType, path, leaf.Type),
			})
		case ClassLossy:
			i := Issue{
				Severity: SeverityWarning, Code: CodeLossyConversion, Path: path, FieldID: m.TargetFieldID, FieldMappingID: m.ID,
				Message: fmt.Sprintf("%s (%s) → %s (%s) may lose data or fail to parse; add an explicit conversion.", col.Name, col.JSONType, path, leaf.Type),
			}
			if fix != nil {
				i.Fix = &Fix{Kind: "add-transform", Label: "Add " + fix.Op, Step: fix}
			}
			add(i)
		}
	}
	for _, l := range s.DocSchema.Leaves() {
		if l.Required && activeTargets[l.FieldID] == "" {
			add(Issue{
				Severity: SeverityError, Code: CodeRequiredFieldUnmapped, Path: l.Path, FieldID: l.FieldID,
				Message: "Required field " + l.Path + " has no mapping. Map a source, set a constant, or mark it optional.",
				Fix:     &Fix{Kind: "mark-optional", Label: "Mark optional"},
			})
		}
	}
	if pending > 0 {
		add(Issue{
			Severity: SeverityWarning, Code: CodeSuggestionsPending, Path: "fieldMappings",
			Message: fmt.Sprintf("%d suggestion(s) are waiting for review; they aren't migrated until accepted.", pending),
			Fix:     &Fix{Kind: "accept-suggestions", Label: "Accept all suggestions"},
		})
	}
	for _, i := range ValidateEmbeds(s, t, ctx.Related) {
		add(i)
	}
	if ctx.TargetExists != nil && !*ctx.TargetExists && !ctx.Target.CreateIfMissing {
		add(Issue{
			Severity: SeverityError, Code: CodeTargetKeyspaceMissing, Path: "target",
			Message: fmt.Sprintf("Collection %s.%s.%s doesn't exist. Create it, or let the migration create it.", ctx.Target.Bucket, ctx.Target.Scope, ctx.Target.Collection),
			Fix:     &Fix{Kind: "enable-create", Label: "Create when migration starts"},
		})
	}
	if st := ComputeStatsWith(s, columnNames(t), nil); st.UnmappedSource > 0 {
		add(Issue{
			Severity: SeverityInfo, Code: CodeUnmappedSourceColumns, Path: "source",
			Message: fmt.Sprintf("%d source column(s) aren't mapped and won't be migrated.", st.UnmappedSource),
		})
	}
	SortIssues(issues)
	return issues
}

// SortIssues orders by severity, then path, then code (deterministic output, OC-022 AC1).
func SortIssues(issues []Issue) {
	sort.SliceStable(issues, func(i, j int) bool {
		a, b := issues[i], issues[j]
		if severityRank[a.Severity] != severityRank[b.Severity] {
			return severityRank[a.Severity] < severityRank[b.Severity]
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Code < b.Code
	})
}

func columnNames(t *schema.Table) []string {
	out := make([]string, len(t.Columns))
	for i, c := range t.Columns {
		out[i] = c.Name
	}
	return out
}

// Ready reports whether issues contain no errors.
func Ready(issues []Issue) bool {
	for _, i := range issues {
		if i.Severity == SeverityError {
			return false
		}
	}
	return true
}

// Mapping statuses (OC-035 AC3).
const (
	StatusDraft = "draft"
	StatusReady = "ready"
	StatusStale = "stale"
)

// Status derives a mapping's status: stale when a mapped source column disappeared from the
// latest snapshot, draft on any other error, ready otherwise (warnings allowed).
func Status(issues []Issue) string {
	for _, i := range issues {
		if i.Code == CodeSourceColumnMissing {
			return StatusStale
		}
	}
	if !Ready(issues) {
		return StatusDraft
	}
	return StatusReady
}

// CheckReferences rejects a spec (HTTP 422, OC-019 AC9) whose mappings reference unknown
// target fields, or source columns that are neither in the table nor already referenced by
// the stored spec (existing references survive source drift so they can be fixed). Embedded
// columns are checked against their embed's table.
func CheckReferences(s *Spec, t *schema.Table, previous *Spec, related RelatedTables) []Issue {
	var issues []Issue
	paths := s.DocSchema.Paths()
	known := map[SourceRef]bool{}
	if previous != nil {
		for _, m := range previous.FieldMappings {
			for _, src := range m.Sources {
				known[src] = true
			}
		}
	}
	ctx := Context{Related: related}
	for _, m := range s.FieldMappings {
		if _, ok := paths[m.TargetFieldID]; !ok {
			issues = append(issues, Issue{
				Severity: SeverityError, Code: CodeUnknownTargetField, Path: "fieldMappings." + m.ID, FieldMappingID: m.ID,
				Message: "Mapping " + m.ID + " targets unknown field " + m.TargetFieldID + ".",
			})
		}
		for _, src := range m.Sources {
			if src.Embed != "" && s.EmbedByID(src.Embed) == nil {
				issues = append(issues, Issue{
					Severity: SeverityError, Code: CodeEmbedInvalid, Path: "fieldMappings." + m.ID, FieldMappingID: m.ID,
					Message: "Mapping " + m.ID + " reads from unknown embed " + src.Embed + ".",
				})
				continue
			}
			if ctx.sourceColumn(s, t, src) == nil && !known[src] {
				issues = append(issues, Issue{
					Severity: SeverityError, Code: CodeSourceColumnMissing, Path: "fieldMappings." + m.ID, FieldMappingID: m.ID,
					Message: "Mapping " + m.ID + " uses unknown column " + src.Column + ".",
				})
			}
		}
	}
	return issues
}
