package transform

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"omnidatabus/internal/docschema"
	"omnidatabus/internal/encode"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/schema"
)

// MaxDocBytes is Couchbase's document size limit.
const MaxDocBytes = 20 << 20

// Options configure compilation.
type Options struct {
	Encode encode.Options
	// Now is the job start; expressions' now() returns it for every row.
	Now time.Time
	// Related are the embedded tables by embed id (OC-021).
	Related map[string]*schema.Table

	embedded bool // compiling an embed's sub-program: no key or type field
}

// Warning is a non-fatal per-row finding.
type Warning struct {
	FieldPath string `json:"fieldPath"`
	Op        string `json:"op,omitempty"`
	Code      string `json:"code"`
	Message   string `json:"message"`
}

// RowError fails one row (dead-lettered by the pipeline, OC-026).
type RowError struct {
	FieldPath string `json:"fieldPath,omitempty"`
	Op        string `json:"op,omitempty"`
	Code      string `json:"code"`
	Message   string `json:"message"`
}

func (e *RowError) Error() string { return e.Code + ": " + e.Message }

// TraceStep is one value in a field's pipeline trace (OC-022 AC6).
type TraceStep struct {
	Step  string `json:"step"` // "source" or the op name
	Value any    `json:"value"`
}

type step struct {
	op  string
	fn  opFunc
	str func(string) string // set for pure string ops
}

// fieldProg fills one target field.
type fieldProg struct {
	path, name, fieldID string
	target              docschema.FieldType
	srcIdx              []int
	steps               []step
	omitNull            bool
	direct              bool // one source, no steps, exact type: written straight from the row
	strChain            bool // one source, every step a pure string op, string target
}

// node is the document skeleton in design order.
type node struct {
	name     string
	field    *fieldProg
	children []*node
	// null marks a designed field with no active mapping, written as null unless the spec
	// omits unmapped fields.
	null bool
	// embed fills the field from an embedded table (OC-021).
	embed *embedProg
}

// embedProg writes an embed: the related rows through the embed's own sub-program.
type embedProg struct {
	id, path string
	many     bool // one-to-many: an array
	limit    int
	omitNull bool // many-to-one without a row: omit rather than null
	omitNone bool // one-to-many without rows: omit rather than []
	sub      *Program
}

// Program turns rows into (key, document). It is not safe for concurrent use: the pipeline
// compiles one per worker.
type Program struct {
	cols     []schema.Column
	colNames []string
	key      *mapping.KeyTemplate
	keyCols  []int
	typeName string
	typeJSON []byte // pre-encoded "name":"value"
	root     []*node
	fields   []*fieldProg
	opts     Options
	needRow  bool
	ctx      stepCtx // reused for every transformed field
	rowCache map[string]any
	rowBuilt bool
	curRow   []any
	enc      []any // per-row encoded values, reused
	encoded  []bool
	keyBuf   []byte
	warnings []Warning
	embeds   []*embedProg
	related  map[string][][]any // the current row's embedded rows
}

// Compile resolves a spec against the source table. Issues (bad args, bad regex, unknown
// columns, type mismatches at the end of a pipeline, …) are returned with the field path and
// step index; the program is nil when any issue is an error.
func Compile(spec *mapping.Spec, t *schema.Table, opts Options) (*Program, []mapping.Issue) {
	if opts.Now.IsZero() {
		opts.Now = time.Now().UTC()
	}
	p := &Program{cols: t.Columns, opts: opts}
	for _, c := range t.Columns {
		p.colNames = append(p.colNames, c.Name)
	}
	idx := map[string]int{}
	for i, c := range t.Columns {
		idx[c.Name] = i
	}
	var issues []mapping.Issue
	errorf := func(path, fieldID, mapID, code, msg string, opIndex int, op string) {
		i := mapping.Issue{Severity: mapping.SeverityError, Code: code, Path: path, FieldID: fieldID, FieldMappingID: mapID, Message: msg, Op: op}
		if opIndex >= 0 {
			oi := opIndex
			i.OpIndex = &oi
		}
		issues = append(issues, i)
	}

	// Key.
	kt, err := mapping.ParseKeyTemplate(spec.Key.Template)
	switch {
	case opts.embedded:
	case err != nil:
		errorf("key", "", "", mapping.CodeKeyTemplateInvalid, "The key template is invalid: "+err.Error(), -1, "")
	default:
		if err := kt.Bind(t.Name, p.colNames); err != nil {
			errorf("key", "", "", mapping.CodeSourceColumnMissing, err.Error(), -1, "")
		}
		p.key = kt
		for _, c := range kt.Columns() {
			if i, ok := idx[c]; ok {
				p.keyCols = append(p.keyCols, i)
			}
		}
	}
	if spec.TypeField.Enabled && spec.TypeField.Name != "" && !opts.embedded {
		p.typeName = spec.TypeField.Name
		p.typeJSON = appendString(nil, spec.TypeField.Name)
		p.typeJSON = append(p.typeJSON, ':')
		p.typeJSON = appendString(p.typeJSON, spec.TypeField.Value)
	}

	// Embeds compile to sub-programs over their tables; their mappings belong to them.
	paths := spec.DocSchema.Paths()
	byEmbed := map[string]*embedProg{}
	for _, e := range spec.Embeds {
		path := paths[e.TargetFieldID]
		ep, eissues := compileEmbed(spec, e, path, opts)
		issues = append(issues, eissues...)
		if ep != nil {
			byEmbed[e.TargetFieldID] = ep
			p.embeds = append(p.embeds, ep)
		}
	}

	// Field programs for active mappings, by target field.
	byField := map[string]*fieldProg{}
	for _, m := range spec.FieldMappings {
		if !m.Active() || mapping.EmbedSources(m) {
			continue
		}
		path, ok := paths[m.TargetFieldID]
		f := spec.DocSchema.Find(m.TargetFieldID)
		if !ok || f == nil {
			errorf("fieldMappings."+m.ID, m.TargetFieldID, m.ID, mapping.CodeUnknownTargetField, "Unknown target field "+m.TargetFieldID+".", -1, "")
			continue
		}
		fp := &fieldProg{path: path, name: f.Name, fieldID: f.ID, target: f.Type}
		nh := m.NullHandling
		if nh == "" {
			nh = spec.NullHandling
		}
		fp.omitNull = nh == mapping.NullOmit
		inType := docschema.Any
		missing := false
		for _, s := range m.Sources {
			i, ok := idx[s.Column]
			if !ok {
				errorf(path, f.ID, m.ID, mapping.CodeSourceColumnMissing, "Column "+s.Column+" isn't in the source table.", -1, "")
				missing = true
				continue
			}
			fp.srcIdx = append(fp.srcIdx, i)
		}
		if missing {
			continue
		}
		if len(fp.srcIdx) == 1 {
			inType = docschema.FieldType(t.Columns[fp.srcIdx[0]].JSONType)
		}
		env := &compileEnv{sources: len(fp.srcIdx), columns: p.colNames, opts: opts}
		hasConcatOrExpr := false
		for si, st := range m.Transforms {
			spec, ok := ops[st.Op]
			if !ok {
				errorf(path, f.ID, m.ID, mapping.CodeTransformInvalid, "Unknown transform '"+st.Op+"'.", si, st.Op)
				continue
			}
			fn, err := spec.compile(st.Args, env)
			if err != nil {
				i := mapping.Issue{Severity: mapping.SeverityError, Code: mapping.CodeTransformInvalid, Path: path, FieldID: f.ID, FieldMappingID: m.ID, Op: st.Op, Message: st.Op + ": " + err.Error()}
				oi := si
				i.OpIndex = &oi
				var ee *ExprError
				if errors.As(err, &ee) {
					// The position travels in Line/Column, not in the message.
					i.Line, i.Column, i.Message = ee.Line, ee.Column, st.Op+": "+ee.Message
				}
				issues = append(issues, i)
				continue
			}
			if st.Op == "concat" || st.Op == "expression" || st.Op == "constant" {
				hasConcatOrExpr = true
			}
			if st.Op == "expression" {
				p.needRow = true
			}
			inType = spec.typeOut(inType, st.Args)
			stp := step{op: st.Op, fn: fn}
			if spec.str != nil {
				stp.str, _ = spec.str(st.Args) // args already validated by compile
			}
			fp.steps = append(fp.steps, stp)
		}
		switch {
		case len(fp.srcIdx) > 1 && !hasConcatOrExpr:
			errorf(path, f.ID, m.ID, mapping.CodeTransformInvalid, "Several sources need a concat step (or an expression) to combine them.", -1, "")
		case len(fp.srcIdx) == 0 && !hasConcatOrExpr:
			errorf(path, f.ID, m.ID, mapping.CodeTransformInvalid, "A mapping without sources needs a constant (or an expression).", -1, "")
		}
		if class, _ := mapping.Compat(string(inType), f.Type); class == mapping.ClassIncompatible {
			errorf(path, f.ID, m.ID, CodeTypeMismatch, fmt.Sprintf("The pipeline produces %s, but %s is %s.", inType, path, f.Type), len(m.Transforms)-1, "")
		}
		fp.direct = len(fp.srcIdx) == 1 && len(fp.steps) == 0 && directSafe(t.Columns[fp.srcIdx[0]], f.Type)
		fp.strChain = len(fp.srcIdx) == 1 && len(fp.steps) > 0 && (f.Type == docschema.String || f.Type == docschema.Any)
		for _, st := range fp.steps {
			fp.strChain = fp.strChain && st.str != nil
		}
		byField[f.ID] = fp
		p.fields = append(p.fields, fp)
	}
	p.root = buildNodes(spec.DocSchema.Root.Children, byField, byEmbed, !spec.OmitUnmapped)
	mapping.SortIssues(issues)
	for _, i := range issues {
		if i.Severity == mapping.SeverityError {
			return nil, issues
		}
	}
	p.enc = make([]any, len(t.Columns))
	p.encoded = make([]bool, len(t.Columns))
	return p, issues
}

// directSafe reports whether a column's encoded value already has the target's JSON type.
func directSafe(c schema.Column, target docschema.FieldType) bool {
	if target == docschema.Any {
		return true
	}
	class, _ := mapping.Compat(c.JSONType, target)
	return class == mapping.ClassExact && (c.JSONType != schema.TypeAny || target == docschema.Any)
}

// compileEmbed compiles one embed's sub-program against its table.
func compileEmbed(spec *mapping.Spec, e mapping.Embed, path string, opts Options) (*embedProg, []mapping.Issue) {
	issue := func(code, msg string) []mapping.Issue {
		return []mapping.Issue{{Severity: mapping.SeverityError, Code: code, Path: path, FieldID: e.TargetFieldID, Message: "Embed " + e.ID + ": " + msg}}
	}
	t := opts.Related[e.ID]
	if t == nil {
		return nil, issue(mapping.CodeEmbedTableMissing, "table "+e.Table.String()+" isn't known.")
	}
	if mapping.EmbedContainer(&spec.DocSchema, e) == nil {
		return nil, issue(mapping.CodeEmbedTargetKind, path+" doesn't have the shape of a "+e.Kind+" embed.")
	}
	scoped := mapping.Scoped(spec, e)
	sub := opts
	sub.embedded, sub.Related = true, nil
	prog, found := Compile(&scoped, t, sub)
	// The sub-program's paths are relative to the embed: make them absolute.
	prefix := path + "."
	if e.Kind == mapping.EmbedOneToMany {
		prefix = path + "[]."
	}
	for i := range found {
		found[i].Path = prefix + found[i].Path
	}
	if prog == nil {
		return nil, found
	}
	nh := spec.NullHandling == mapping.NullOmit
	return &embedProg{
		id: e.ID, path: path, many: e.Kind == mapping.EmbedOneToMany, limit: e.EffectiveLimit(),
		omitNull: nh, omitNone: e.Empty == mapping.EmptyOmit, sub: prog,
	}, found
}

// buildNodes lays out the document in design order. Leaf fields without an active mapping
// become null nodes when includeUnmapped (the spec's "Include unmapped fields"), else they're
// left out. Null handling only decides what a mapped field whose value is null becomes.
func buildNodes(fields []docschema.DocField, byField map[string]*fieldProg, byEmbed map[string]*embedProg, includeUnmapped bool) []*node {
	var out []*node
	for i := range fields {
		f := &fields[i]
		if fp, ok := byField[f.ID]; ok {
			out = append(out, &node{name: f.Name, field: fp})
			continue
		}
		if ep, ok := byEmbed[f.ID]; ok {
			out = append(out, &node{name: f.Name, embed: ep})
			continue
		}
		switch {
		case f.Type == docschema.Object && len(f.Children) > 0:
			if kids := buildNodes(f.Children, byField, byEmbed, includeUnmapped); len(kids) > 0 {
				out = append(out, &node{name: f.Name, children: kids})
			}
		case f.Type != docschema.Object && includeUnmapped:
			out = append(out, &node{name: f.Name, null: true})
		}
	}
	return out
}

// value returns the encoded value of column i for the current row (encoded once per row).
// Values that encode to themselves reuse the row's interface, which avoids re-boxing.
func (p *Program) value(row []any, i int) any {
	if !p.encoded[i] {
		if passThrough(row[i], p.cols[i], p.opts.Encode) {
			p.enc[i] = row[i]
		} else {
			p.enc[i] = encode.ValueWith(row[i], p.cols[i], p.opts.Encode)
		}
		p.encoded[i] = true
	}
	return p.enc[i]
}

// passThrough reports whether encoding v would return v unchanged.
func passThrough(v any, c schema.Column, opts encode.Options) bool {
	if c.JSONType == schema.TypeAny || c.IsArray {
		return false
	}
	switch x := v.(type) {
	case nil, bool:
		return true
	case string:
		return c.UDTName != "bpchar"
	case int64:
		if c.UDTName != "int8" {
			return true
		}
		switch opts.BigintAsString {
		case "never":
			return true
		case "always":
			return false
		}
		return x <= maxSafeInt && x >= -maxSafeInt
	}
	return false
}

const maxSafeInt = 1<<53 - 1

func (p *Program) resetRow(row []any) {
	for i := range p.encoded {
		p.encoded[i] = false
	}
	p.warnings = p.warnings[:0]
	p.curRow, p.rowBuilt = row, false
}

// rowMap is the column → value map expressions see as row, built at most once per row.
func (p *Program) rowMap() map[string]any {
	if !p.rowBuilt {
		if p.rowCache == nil {
			p.rowCache = make(map[string]any, len(p.cols))
		}
		for i, c := range p.cols {
			p.rowCache[c.Name] = p.value(p.curRow, i)
		}
		p.rowBuilt = true
	}
	return p.rowCache
}

// Apply builds the key and document for one row of driver values (one per table column).
// buf is reused for the document. Warnings are valid until the next call.
func (p *Program) Apply(row []any, buf []byte) (key string, doc []byte, warnings []Warning, rerr *RowError) {
	return p.apply(row, nil, buf, nil)
}

// ApplyRelated is Apply with the row's embedded rows (embed id → rows, OC-021). A missing
// embed has no rows.
func (p *Program) ApplyRelated(row []any, related map[string][][]any, buf []byte) (key string, doc []byte, warnings []Warning, rerr *RowError) {
	return p.apply(row, related, buf, nil)
}

// ApplyTrace is Apply that also records every field's value after the source read and after
// each step (the preview's trace view).
func (p *Program) ApplyTrace(row []any, buf []byte) (key string, doc []byte, warnings []Warning, trace map[string][]TraceStep, rerr *RowError) {
	return p.ApplyTraceRelated(row, nil, buf)
}

// ApplyTraceRelated is ApplyTrace with the row's embedded rows.
func (p *Program) ApplyTraceRelated(row []any, related map[string][][]any, buf []byte) (key string, doc []byte, warnings []Warning, trace map[string][]TraceStep, rerr *RowError) {
	trace = map[string][]TraceStep{}
	key, doc, warnings, rerr = p.apply(row, related, buf, trace)
	return key, doc, warnings, trace, rerr
}

// HasEmbeds reports whether documents need embedded rows (ApplyRelated).
func (p *Program) HasEmbeds() bool { return len(p.embeds) > 0 }

func (p *Program) apply(row []any, related map[string][][]any, buf []byte, trace map[string][]TraceStep) (string, []byte, []Warning, *RowError) {
	p.resetRow(row)
	p.related = related
	if len(row) != len(p.cols) {
		return "", nil, nil, &RowError{Code: CodeTypeMismatch, Message: fmt.Sprintf("row has %d values; the table has %d columns", len(row), len(p.cols))}
	}
	var key string
	if p.key != nil {
		for _, i := range p.keyCols {
			p.value(row, i)
		}
		kb, err := p.key.AppendKey(p.keyBuf[:0], p.enc)
		if err != nil {
			var ke *mapping.RowError
			if errors.As(err, &ke) {
				return "", nil, nil, &RowError{FieldPath: "key", Code: ke.Code, Message: ke.Message}
			}
			return "", nil, nil, &RowError{FieldPath: "key", Code: mapping.CodeKeyTemplateInvalid, Message: err.Error()}
		}
		p.keyBuf = kb
		key = string(kb)
	}
	doc := buf[:0]
	doc = append(doc, '{')
	first := true
	if p.typeJSON != nil {
		doc = append(doc, p.typeJSON...)
		first = false
	}
	doc, _, rerr := p.writeNodes(doc, p.root, row, first, trace)
	if rerr != nil {
		return "", nil, nil, rerr
	}
	doc = append(doc, '}')
	if len(doc) > MaxDocBytes {
		return "", nil, nil, &RowError{Code: CodeDocTooLarge, Message: fmt.Sprintf("the document is %d bytes; Couchbase allows %d", len(doc), MaxDocBytes)}
	}
	return key, doc, p.warnings, nil
}

// writeNodes appends the members of an object; wrote reports whether any member was written.
func (p *Program) writeNodes(dst []byte, nodes []*node, row []any, first bool, trace map[string][]TraceStep) ([]byte, bool, *RowError) {
	wrote := false
	for _, n := range nodes {
		mark := len(dst)
		if !first {
			dst = append(dst, ',')
		}
		dst = appendString(dst, n.name)
		dst = append(dst, ':')
		switch {
		case n.null:
			dst = append(dst, "null"...)
		case n.embed != nil:
			var ok bool
			var rerr *RowError
			dst, ok, rerr = p.writeEmbed(dst, n.embed)
			if rerr != nil {
				return dst, false, rerr
			}
			if !ok {
				dst = dst[:mark]
				continue
			}
		case n.field == nil:
			dst = append(dst, '{')
			var nonEmpty bool
			var rerr *RowError
			dst, nonEmpty, rerr = p.writeNodes(dst, n.children, row, true, trace)
			if rerr != nil {
				return dst, false, rerr
			}
			if !nonEmpty {
				dst = dst[:mark] // every child was omitted: omit the object too
				continue
			}
			dst = append(dst, '}')
		default:
			var ok bool
			var rerr *RowError
			dst, ok, rerr = p.writeField(dst, n.field, row, trace)
			if rerr != nil {
				return dst, false, rerr
			}
			if !ok {
				dst = dst[:mark]
				continue
			}
		}
		first, wrote = false, true
	}
	return dst, wrote, nil
}

// CodeEmbedTruncated warns that a one-to-many embed had more rows than its limit.
const CodeEmbedTruncated = "EMBED_TRUNCATED"

// writeEmbed appends an embed's value: the related row as an object (many-to-one; null or
// omitted without one), or the related rows as an array of objects, at most limit of them
// (one-to-many; [] or omitted without any). ok is false when the field is omitted.
func (p *Program) writeEmbed(dst []byte, e *embedProg) ([]byte, bool, *RowError) {
	rows := p.related[e.id]
	if !e.many {
		if len(rows) == 0 {
			if e.omitNull {
				return dst, false, nil
			}
			return append(dst, "null"...), true, nil
		}
		dst, rerr := e.sub.writeObject(dst, rows[0], e.path+".")
		p.addWarnings(e.sub.warnings, e.path+".")
		return dst, true, rerr
	}
	if len(rows) == 0 && e.omitNone {
		return dst, false, nil
	}
	if len(rows) > e.limit {
		p.warnings = append(p.warnings, Warning{
			FieldPath: e.path, Code: CodeEmbedTruncated,
			Message: fmt.Sprintf("more than %d related rows; kept the first %d", e.limit, e.limit),
		})
		rows = rows[:e.limit]
	}
	dst = append(dst, '[')
	for i, r := range rows {
		if i > 0 {
			dst = append(dst, ',')
		}
		var rerr *RowError
		prefix := e.path + "[" + strconv.Itoa(i) + "]."
		dst, rerr = e.sub.writeObject(dst, r, prefix)
		p.addWarnings(e.sub.warnings, prefix)
		if rerr != nil {
			return dst, false, rerr
		}
	}
	return append(dst, ']'), true, nil
}

func (p *Program) addWarnings(ws []Warning, prefix string) {
	for _, w := range ws {
		w.FieldPath = prefix + w.FieldPath
		p.warnings = append(p.warnings, w)
	}
}

// writeObject appends one embedded row as an object; errors carry the full field path.
func (p *Program) writeObject(dst []byte, row []any, prefix string) ([]byte, *RowError) {
	p.resetRow(row)
	if len(row) != len(p.cols) {
		return dst, &RowError{FieldPath: strings.TrimSuffix(prefix, "."), Code: CodeTypeMismatch, Message: fmt.Sprintf("related row has %d values; the table has %d columns", len(row), len(p.cols))}
	}
	dst = append(dst, '{')
	dst, _, rerr := p.writeNodes(dst, p.root, row, true, nil)
	if rerr != nil {
		rerr.FieldPath = prefix + rerr.FieldPath
		return dst, rerr
	}
	return append(dst, '}'), nil
}

// writeField appends one field's value; ok is false when a null is omitted.
func (p *Program) writeField(dst []byte, f *fieldProg, row []any, trace map[string][]TraceStep) ([]byte, bool, *RowError) {
	if f.direct && trace == nil {
		raw := row[f.srcIdx[0]]
		if raw == nil {
			if f.omitNull {
				return dst, false, nil
			}
			return append(dst, "null"...), true, nil
		}
		return p.appendValue(dst, p.value(row, f.srcIdx[0]), f), true, nil
	}
	if f.strChain && trace == nil {
		if s, ok := p.value(row, f.srcIdx[0]).(string); ok {
			for _, st := range f.steps {
				s = st.str(s)
			}
			return appendString(dst, s), true, nil
		}
	}
	var v any
	var sources []any
	if len(f.srcIdx) == 1 {
		v = p.value(row, f.srcIdx[0])
	} else if len(f.srcIdx) > 1 {
		sources = make([]any, len(f.srcIdx))
		for i, si := range f.srcIdx {
			sources[i] = p.value(row, si)
		}
		v = sources[0]
	}
	if trace != nil {
		trace[f.path] = append(trace[f.path], TraceStep{Step: "source", Value: v})
	}
	ctx := &p.ctx
	ctx.sources, ctx.warnings, ctx.path = sources, &p.warnings, f.path
	if ctx.row == nil {
		ctx.row = p.rowMap
	}
	for _, s := range f.steps {
		ctx.op = s.op
		out, err := s.fn(v, ctx)
		if err != nil {
			code := CodeParseFailed
			var se *stepErr
			if errors.As(err, &se) {
				code = se.code
			}
			return dst, false, &RowError{FieldPath: f.path, Op: s.op, Code: code, Message: err.Error()}
		}
		v = out
		if trace != nil {
			trace[f.path] = append(trace[f.path], TraceStep{Step: s.op, Value: v})
		}
	}
	v, err := coerce(v, f.target)
	if err != nil {
		return dst, false, &RowError{FieldPath: f.path, Code: CodeTypeMismatch, Message: err.Error()}
	}
	if v == nil {
		if f.omitNull {
			return dst, false, nil
		}
		return append(dst, "null"...), true, nil
	}
	return p.appendValue(dst, v, f), true, nil
}

// coerce checks the final value against the target type, converting losslessly where safe.
func coerce(v any, target docschema.FieldType) (any, error) {
	if v == nil || target == docschema.Any {
		return v, nil
	}
	mismatch := func() (any, error) {
		return nil, fmt.Errorf("%s value %s doesn't fit a %s field (add a conversion step)", kindOf(v), clip(String(v)), target)
	}
	switch target {
	case docschema.String:
		switch v.(type) {
		case string:
			return v, nil
		case int64, float64, bool, json.Number:
			return String(v), nil
		}
	case docschema.Integer:
		switch x := v.(type) {
		case int64:
			return x, nil
		case float64:
			if x == math.Trunc(x) {
				return int64(x), nil
			}
		case json.Number:
			if _, err := x.Int64(); err == nil {
				return x, nil
			}
		case string:
			if _, err := strconv.ParseInt(x, 10, 64); err == nil {
				return x, nil // a bigint kept as a string (bigintAsString) stays a string
			}
		}
	case docschema.Number:
		switch x := v.(type) {
		case int64, float64, json.Number:
			return x, nil
		case string:
			if _, err := strconv.ParseFloat(x, 64); err == nil {
				return x, nil // high-precision numeric kept exact as a string
			}
		}
	case docschema.Boolean:
		if _, ok := v.(bool); ok {
			return v, nil
		}
	case docschema.Datetime:
		switch v.(type) {
		case string, int64:
			return v, nil
		}
	case docschema.Object:
		if _, ok := v.(map[string]any); ok {
			return v, nil
		}
	case docschema.Array:
		if _, ok := v.([]any); ok {
			return v, nil
		}
	}
	return mismatch()
}

func kindOf(v any) string {
	switch v.(type) {
	case string:
		return "text"
	case int64, float64, json.Number:
		return "numeric"
	case bool:
		return "boolean"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return fmt.Sprintf("%T", v)
}

func clip(s string) string {
	if len(s) > 40 {
		return strconv.Quote(s[:40]) + "…"
	}
	return strconv.Quote(s)
}

// appendValue writes an encoded value as JSON.
func (p *Program) appendValue(dst []byte, v any, f *fieldProg) []byte {
	if x, ok := v.(float64); ok && (math.IsNaN(x) || math.IsInf(x, 0)) {
		p.warnings = append(p.warnings, Warning{FieldPath: f.path, Code: CodeNonFiniteNumber, Message: "NaN/Infinity isn't valid JSON; wrote null"})
		return append(dst, "null"...)
	}
	return appendJSON(dst, v)
}

// appendJSON serializes pipeline values without reflection for the common types.
func appendJSON(dst []byte, v any) []byte {
	switch x := v.(type) {
	case nil:
		return append(dst, "null"...)
	case string:
		return appendString(dst, x)
	case int64:
		return strconv.AppendInt(dst, x, 10)
	case int:
		return strconv.AppendInt(dst, int64(x), 10)
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return append(dst, "null"...)
		}
		return strconv.AppendFloat(dst, x, 'g', -1, 64)
	case bool:
		return strconv.AppendBool(dst, x)
	case json.Number:
		return append(dst, x...)
	case []any:
		dst = append(dst, '[')
		for i, e := range x {
			if i > 0 {
				dst = append(dst, ',')
			}
			dst = appendJSON(dst, e)
		}
		return append(dst, ']')
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys) // deterministic output for json/jsonb objects
		dst = append(dst, '{')
		for i, k := range keys {
			if i > 0 {
				dst = append(dst, ',')
			}
			dst = appendString(dst, k)
			dst = append(dst, ':')
			dst = appendJSON(dst, x[k])
		}
		return append(dst, '}')
	}
	b, err := json.Marshal(v)
	if err != nil {
		return appendString(dst, fmt.Sprint(v))
	}
	return append(dst, b...)
}

const hexDigits = "0123456789abcdef"

// appendString writes s as a JSON string.
func appendString(dst []byte, s string) []byte {
	dst = append(dst, '"')
	start := 0
	for i := 0; i < len(s); {
		c := s[i]
		if c >= 0x20 && c != '"' && c != '\\' && c < utf8.RuneSelf {
			i++
			continue
		}
		if c >= utf8.RuneSelf {
			r, size := utf8.DecodeRuneInString(s[i:])
			if r == utf8.RuneError && size == 1 {
				dst = append(dst, s[start:i]...)
				dst = append(dst, `�`...)
				i += size
				start = i
				continue
			}
			if r == ' ' || r == ' ' {
				dst = append(dst, s[start:i]...)
				dst = append(dst, `\u202`...)
				dst = append(dst, hexDigits[r&0xF])
				i += size
				start = i
				continue
			}
			i += size
			continue
		}
		dst = append(dst, s[start:i]...)
		switch c {
		case '"', '\\':
			dst = append(dst, '\\', c)
		case '\n':
			dst = append(dst, '\\', 'n')
		case '\r':
			dst = append(dst, '\\', 'r')
		case '\t':
			dst = append(dst, '\\', 't')
		default:
			dst = append(dst, '\\', 'u', '0', '0', hexDigits[c>>4], hexDigits[c&0xF])
		}
		i++
		start = i
	}
	dst = append(dst, s[start:]...)
	return append(dst, '"')
}
