package transform

import (
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/docschema"
	"omnidatabus/internal/encode"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/schema"
)

// Row warning and error codes of relations (OC-021, OC-044 – OC-046).
const (
	// CodeEmbedTruncated warns that an array relation had more rows than its limit.
	CodeEmbedTruncated        = "EMBED_TRUNCATED"
	CodeRelationDangling      = "RELATION_DANGLING"
	CodeRelationDuplicateKey  = "RELATION_DUPLICATE_KEY"
	CodeRelationNullKey       = "RELATION_NULL_KEY"
	CodeRelationLimitExceeded = "RELATION_LIMIT_EXCEEDED"
	CodeDocSizeTruncated      = "DOC_SIZE_TRUNCATED"
	// CodeAdapterDiscardsRows warns that an object bound with the first adapter had several
	// related rows (OC-047/049).
	CodeAdapterDiscardsRows = "ADAPTER_DISCARDS_ROWS"
)

// Page is an extra document holding the items of a split array past the parent's (OC-046):
// key {parentKey}::{field}::{page}, body {parent, page, items}.
type Page struct {
	Key string
	Doc []byte
}

// Pages are the current document's split pages, valid until the next Apply.
func (p *Program) Pages() []Page { return p.pages }

// embedProg writes a relation's field from its rows through the relation's sub-program.
type embedProg struct {
	id, path, name, table string
	// many writes an array (or keyed object); first writes the first of several rows as an
	// object (the composer's first adapter, OC-047).
	many, first bool
	shape       string
	limit       int
	// overflow is the explicit policy ("" = 1.0: truncate at the limit, fail the size guard).
	overflow string
	omitNull bool // many-to-one without a row: omit rather than null
	omitNone bool // an array without rows: omit rather than []
	keyIdx   int  // keyed shape: the key column in the row
	keyCol   schema.Column
	sub      *Program
}

// compileEmbed compiles one relation's sub-program against its row table: the fields of its
// container (or the value field of a scalars array) with the mappings in its scope, which may
// read ancestors and lookups (OC-043), and its own nested relations.
func compileEmbed(spec *mapping.Spec, e mapping.Relation, path string, opts Options) (*embedProg, []mapping.Issue) {
	issue := func(code, msg string) []mapping.Issue {
		return []mapping.Issue{{Severity: mapping.SeverityError, Code: code, Path: path, FieldID: e.TargetFieldID, Message: "Embed " + e.ID + ": " + msg}}
	}
	t := opts.Related[e.ID]
	if t == nil {
		return nil, issue(mapping.CodeEmbedTableMissing, "table "+e.Table.String()+" isn't known.")
	}
	sc := opts.scope
	sub := mapping.Spec{
		NullHandling: sc.spec.NullHandling, OmitUnmapped: sc.spec.OmitUnmapped, FieldMappings: sc.spec.FieldMappings,
		Relations: sc.spec.Relations, Aggregates: sc.spec.Aggregates, DocSchema: docschema.Schema{Root: docschema.NewRoot()},
	}
	adapter := mapping.Adapter(&spec.DocSchema, e)
	ep := &embedProg{
		id: e.ID, path: path, table: e.Table.String(), shape: e.EffectiveShape(), limit: e.EffectiveLimit(),
		many:     (e.Many() && adapter != docschema.AdapterFirst) || adapter == docschema.AdapterWrap,
		first:    e.Many() && adapter == docschema.AdapterFirst,
		overflow: e.Overflow, omitNull: sc.spec.NullHandling == mapping.NullOmit, omitNone: e.Empty == mapping.EmptyOmit, keyIdx: -1,
	}
	if f := spec.DocSchema.Find(e.TargetFieldID); f != nil {
		ep.name = f.Name
	}
	prefix := path + "."
	if vf := mapping.RelationValueField(&spec.DocSchema, e); vf != nil {
		sub.DocSchema.Root.Children = []docschema.DocField{*vf}
		mapped := false
		for _, m := range sc.spec.FieldMappings {
			mapped = mapped || (m.Active() && m.TargetFieldID == vf.ID)
		}
		if !mapped {
			sub.FieldMappings = append(append([]mapping.FieldMapping(nil), sub.FieldMappings...), mapping.FieldMapping{
				ID: "value:" + e.ID, TargetFieldID: vf.ID, State: mapping.StateActive,
				Sources: []mapping.SourceRef{{Relation: e.ID, Column: e.ValueColumn}}, Transforms: []mapping.Step{},
			})
		}
		prefix = path
	} else {
		c := mapping.RelationContainer(&spec.DocSchema, e)
		if c == nil {
			return nil, issue(mapping.CodeEmbedTargetKind, path+" doesn't have the shape of a "+e.Kind+" relation.")
		}
		sub.DocSchema.Root.Children = c.Children
		if ep.many && ep.shape == mapping.ShapeObjects {
			prefix = path + "[]."
		}
	}
	if ep.shape == mapping.ShapeKeyed && ep.many {
		if ep.keyIdx = columnIndex(t, e.KeyColumn); ep.keyIdx < 0 {
			return nil, issue(mapping.CodeRelationOptionInvalid, "key column "+e.KeyColumn+" isn't in "+e.Table.String()+".")
		}
		ep.keyCol = t.Columns[ep.keyIdx]
	}
	subOpts := opts
	subOpts.embedded = true
	subOpts.scope = &compileScope{spec: sc.spec, scopes: sc.scopes, id: e.ID, up: sc, table: t}
	prog, found := Compile(&sub, t, subOpts)
	// The sub-program's paths are relative to the relation: make them absolute.
	for i := range found {
		found[i].Path = prefix + found[i].Path
	}
	if prog == nil {
		return nil, found
	}
	ep.sub = prog
	return ep, found
}

// writeEmbed appends a relation's value: the related row as an object (many-to-one; null or
// omitted without one), or the related rows as an array of objects, an array of values or a
// keyed object, at most limit of them (one-to-many and many-to-many; [] or omitted without
// any). Past the limit the overflow policy truncates (with EMBED_TRUNCATED), fails the row, or
// writes the rest to page documents. ok is false when the field is omitted.
func (p *Program) writeEmbed(dst []byte, e *embedProg) ([]byte, bool, *RowError) {
	set := p.related[e.id]
	if set != nil && set.Dangling > 0 {
		p.warnings = append(p.warnings, Warning{
			FieldPath: e.path, Code: CodeRelationDangling,
			Message: fmt.Sprintf("%d junction row(s) point to missing rows of %s; dropped", set.Dangling, e.table),
		})
	}
	var rows []source.RelatedRow
	if set != nil {
		rows = set.Rows
	}
	if !e.many {
		if len(rows) == 0 {
			if e.omitNull {
				return dst, false, nil
			}
			return append(dst, "null"...), true, nil
		}
		if e.first && len(rows) > 1 {
			n := strconv.Itoa(len(rows))
			if len(rows) > e.limit {
				n = "more than " + strconv.Itoa(e.limit)
			}
			p.warnings = append(p.warnings, Warning{
				FieldPath: e.path, Code: CodeAdapterDiscardsRows,
				Message: "only the first of " + n + " rows of " + e.table + " is used",
			})
		}
		dst, rerr := e.sub.writeObject(dst, rows[0], e.path+".")
		p.addWarnings(e.sub.warnings, e.path+".")
		return dst, true, rerr
	}
	if len(rows) == 0 && e.omitNone {
		return dst, false, nil
	}
	keep, pageSize := len(rows), 0
	if len(rows) > e.limit {
		switch e.overflow {
		case mapping.OverflowFail:
			return dst, false, &RowError{
				FieldPath: e.path, Code: CodeRelationLimitExceeded,
				Message: fmt.Sprintf("%d related rows; the limit is %d", len(rows), e.limit),
			}
		case mapping.OverflowSplit:
			keep, pageSize = e.limit, e.limit
		default:
			p.warnings = append(p.warnings, Warning{
				FieldPath: e.path, Code: CodeEmbedTruncated,
				Message: fmt.Sprintf("more than %d related rows; kept the first %d", e.limit, e.limit),
			})
			keep = e.limit
		}
	}
	root := p.parent == nil
	if root {
		if c, ok := p.guard.caps[e.id]; ok && c < keep {
			keep = c
			if e.overflow == mapping.OverflowSplit {
				pageSize = max(c, 1)
			}
		}
	}
	start := len(dst)
	var rerr *RowError
	switch e.shape {
	case mapping.ShapeScalars:
		dst, rerr = p.writeScalars(dst, e, rows[:keep])
	case mapping.ShapeKeyed:
		dst, rerr = p.writeKeyed(dst, e, rows[:keep])
	default:
		dst, rerr = p.writeObjects(dst, e, rows[:keep], 0)
	}
	if rerr != nil {
		return dst, false, rerr
	}
	if root {
		p.guard.track(e, keep, len(dst)-start)
	}
	if pageSize > 0 && keep < len(rows) {
		if dst, rerr = p.writePages(dst, e, rows, keep, pageSize); rerr != nil {
			return dst, false, rerr
		}
	}
	return dst, true, nil
}

// writeObjects appends rows as an array of objects; item paths count from first.
func (p *Program) writeObjects(dst []byte, e *embedProg, rows []source.RelatedRow, first int) ([]byte, *RowError) {
	dst = append(dst, '[')
	for i, r := range rows {
		if i > 0 {
			dst = append(dst, ',')
		}
		var rerr *RowError
		prefix := e.path + "[" + strconv.Itoa(first+i) + "]."
		dst, rerr = e.sub.writeObject(dst, r, prefix)
		p.addWarnings(e.sub.warnings, prefix)
		if rerr != nil {
			return dst, rerr
		}
	}
	return append(dst, ']'), nil
}

// writeScalars appends rows as an array of one value each (OC-045).
func (p *Program) writeScalars(dst []byte, e *embedProg, rows []source.RelatedRow) ([]byte, *RowError) {
	dst = append(dst, '[')
	for i, r := range rows {
		if i > 0 {
			dst = append(dst, ',')
		}
		var rerr *RowError
		prefix := e.path + "[" + strconv.Itoa(i) + "]"
		dst, rerr = e.sub.writeValue(dst, r, prefix)
		p.addWarnings(e.sub.warnings, prefix)
		if rerr != nil {
			return dst, rerr
		}
	}
	return append(dst, ']'), nil
}

// writeKeyed appends rows as an object keyed by the key column's text (OC-045): a duplicate
// key keeps the last row (in its first position) with RELATION_DUPLICATE_KEY; a null key skips
// the row with RELATION_NULL_KEY.
func (p *Program) writeKeyed(dst []byte, e *embedProg, rows []source.RelatedRow) ([]byte, *RowError) {
	type entry struct {
		key string
		doc []byte
	}
	var entries []entry
	at := map[string]int{}
	for _, r := range rows {
		var raw any
		if e.keyIdx < len(r.Values) {
			raw = r.Values[e.keyIdx]
		}
		if raw == nil {
			p.warnings = append(p.warnings, Warning{FieldPath: e.path, Code: CodeRelationNullKey, Message: "a related row has no " + e.keyCol.Name + "; left out"})
			continue
		}
		k := String(encode.ValueWith(raw, e.keyCol, p.opts.Encode))
		prefix := e.path + "." + k + "."
		doc, rerr := e.sub.writeObject(nil, r, prefix)
		p.addWarnings(e.sub.warnings, prefix)
		if rerr != nil {
			return dst, rerr
		}
		if i, dup := at[k]; dup {
			entries[i].doc = doc
			p.warnings = append(p.warnings, Warning{FieldPath: e.path, Code: CodeRelationDuplicateKey, Message: fmt.Sprintf("several related rows have %s %q; kept the last", e.keyCol.Name, k)})
			continue
		}
		at[k] = len(entries)
		entries = append(entries, entry{key: k, doc: doc})
	}
	dst = append(dst, '{')
	for i, en := range entries {
		if i > 0 {
			dst = append(dst, ',')
		}
		dst = appendString(dst, en.key)
		dst = append(dst, ':')
		dst = append(dst, en.doc...)
	}
	return append(dst, '}'), nil
}

// writePages writes rows[keep:] to page documents of pageSize items, numbered from 2 (the
// parent is page 1), and appends the parent's "<field>Overflow" member (OC-046 split).
func (p *Program) writePages(dst []byte, e *embedProg, rows []source.RelatedRow, keep, pageSize int) ([]byte, *RowError) {
	var keys []string
	for n, from := 2, keep; from < len(rows); n, from = n+1, from+pageSize {
		to := min(from+pageSize, len(rows))
		key := p.curKey + "::" + e.name + "::" + strconv.Itoa(n)
		if len(key) > mapping.MaxKeyBytes {
			return dst, &RowError{FieldPath: e.path, Code: mapping.CodeKeyTooLong, Message: fmt.Sprintf("page key %s… is %d bytes; keys may have %d", key[:32], len(key), mapping.MaxKeyBytes)}
		}
		doc := make([]byte, 0, 256)
		doc = append(doc, `{"parent":`...)
		doc = appendString(doc, p.curKey)
		doc = append(doc, `,"page":`...)
		doc = strconv.AppendInt(doc, int64(n), 10)
		doc = append(doc, `,"items":`...)
		var rerr *RowError
		if doc, rerr = p.writeObjects(doc, e, rows[from:to], from); rerr != nil {
			return dst, rerr
		}
		doc = append(doc, '}')
		p.pages = append(p.pages, Page{Key: key, Doc: doc})
		keys = append(keys, key)
	}
	dst = append(dst, ',')
	dst = appendString(dst, e.name+"Overflow")
	dst = append(dst, `:{"count":`...)
	dst = strconv.AppendInt(dst, int64(len(rows)), 10)
	dst = append(dst, `,"pages":[`...)
	for i, k := range keys {
		if i > 0 {
			dst = append(dst, ',')
		}
		dst = appendString(dst, k)
	}
	return append(dst, "]}"...), nil
}

// writeObject appends one related row as an object, with its own nested relations' rows;
// errors carry the full field path.
func (p *Program) writeObject(dst []byte, row source.RelatedRow, prefix string) ([]byte, *RowError) {
	p.resetRow(row.Values)
	p.related = row.Sub
	if len(row.Values) != len(p.cols) {
		return dst, &RowError{FieldPath: strings.TrimSuffix(prefix, "."), Code: CodeTypeMismatch, Message: fmt.Sprintf("related row has %d values; the table has %d columns", len(row.Values), len(p.cols))}
	}
	dst = append(dst, '{')
	dst, _, rerr := p.writeNodes(dst, p.root, row.Values, true, nil)
	if rerr != nil {
		rerr.FieldPath = prefix + rerr.FieldPath
		return dst, rerr
	}
	return append(dst, '}'), nil
}

// writeValue appends one related row's value (a scalars array's item, OC-045).
func (p *Program) writeValue(dst []byte, row source.RelatedRow, path string) ([]byte, *RowError) {
	p.resetRow(row.Values)
	p.related = row.Sub
	if len(row.Values) != len(p.cols) || len(p.root) == 0 {
		return append(dst, "null"...), nil
	}
	n := p.root[0]
	if n.field == nil {
		return append(dst, "null"...), nil
	}
	dst, ok, rerr := p.writeField(dst, n.field, row.Values, nil)
	if rerr != nil {
		rerr.FieldPath = path
		return dst, rerr
	}
	if !ok {
		dst = append(dst, "null"...) // an array item can't be omitted
	}
	return dst, nil
}

// guardState tracks the root's arrays while a document is written, and the caps the size
// guard (OC-046) puts on them when the document is too big.
type guardState struct {
	caps    map[string]int // relation → items kept
	written map[string]guardArray
	cut     []guardCut
}

type guardArray struct {
	e     *embedProg
	items int
	bytes int
}

type guardCut struct {
	path     string
	from, to int
}

func (g *guardState) reset() {
	clear(g.caps)
	clear(g.written)
	g.cut = g.cut[:0]
}

func (g *guardState) track(e *embedProg, items, bytes int) {
	if g.written == nil {
		g.written = map[string]guardArray{}
	}
	g.written[e.id] = guardArray{e: e, items: items, bytes: bytes}
}

// shrink halves the largest array whose relation's overflow policy is truncate or split;
// false when the document can't be made smaller (DOC_TOO_LARGE, the 1.0 behavior and the fail
// policy's).
func (g *guardState) shrink() bool {
	var big guardArray
	for _, a := range g.written {
		if a.items > 0 && a.bytes > big.bytes {
			big = a
		}
	}
	if big.e == nil || (big.e.overflow != mapping.OverflowTruncate && big.e.overflow != mapping.OverflowSplit) {
		return false
	}
	if g.caps == nil {
		g.caps = map[string]int{}
	}
	to := big.items / 2
	g.caps[big.e.id] = to
	if n := len(g.cut); n > 0 && g.cut[n-1].path == big.e.path {
		g.cut[n-1].to = to
	} else {
		g.cut = append(g.cut, guardCut{path: big.e.path, from: big.items, to: to})
	}
	return true
}

func (g *guardState) warnings() []Warning {
	var out []Warning
	for _, c := range g.cut {
		out = append(out, Warning{
			FieldPath: c.path, Code: CodeDocSizeTruncated,
			Message: fmt.Sprintf("the document was too large; kept %d of %d items in %s", c.to, c.from, c.path),
		})
	}
	return out
}

// aggProg computes an aggregate over a relation's rows into a scalar field (OC-045).
type aggProg struct {
	rel, fn, path string
	idx           int
	col           schema.Column
	target        docschema.FieldType
	omitNull      bool
}

func compileAggregate(spec *mapping.Spec, a mapping.Aggregate, r mapping.Relation, path string, opts Options) (*aggProg, []mapping.Issue) {
	bad := func(msg string) []mapping.Issue {
		return []mapping.Issue{{Severity: mapping.SeverityError, Code: mapping.CodeRelationAggregateInvalid, Path: path, FieldID: a.TargetFieldID, Message: "Aggregate " + a.ID + ": " + msg}}
	}
	f := spec.DocSchema.Find(a.TargetFieldID)
	ap := &aggProg{rel: r.ID, fn: a.Fn, path: path, idx: -1, target: f.Type, omitNull: spec.NullHandling == mapping.NullOmit}
	if !mapping.AggregateFn(a.Fn) {
		return nil, bad("unknown function " + a.Fn + ".")
	}
	if a.Fn == "count" {
		return ap, nil
	}
	t := opts.Related[r.ID]
	if t == nil {
		return nil, bad("table " + r.Table.String() + " isn't known.")
	}
	name := a.Column
	if a.From == mapping.FromVia {
		name = mapping.ViaColumn(a.Column)
	}
	if ap.idx = columnIndex(t, name); ap.idx < 0 {
		return nil, bad("column " + a.Column + " isn't in " + r.Table.String() + ".")
	}
	ap.col = t.Columns[ap.idx]
	return ap, nil
}

// writeAggregate appends an aggregate over the relation's rows after its filter and before its
// limit: count, exact decimal sum and avg, min, max, and the first or last row's value in the
// relation's order. An empty relation gives count 0 and null otherwise.
func (p *Program) writeAggregate(dst []byte, a *aggProg) ([]byte, bool, *RowError) {
	set := p.related[a.rel]
	var rows []source.RelatedRow
	if set != nil {
		rows = set.Rows
	}
	var v any
	switch a.fn {
	case "count":
		v = int64(len(rows))
	case "first", "last":
		if len(rows) > 0 {
			r := rows[0]
			if a.fn == "last" {
				r = rows[len(rows)-1]
			}
			v = p.aggValue(a, r)
		}
	case "sum", "avg":
		sum, n, scale := new(big.Rat), 0, 0
		for _, r := range rows {
			s := numberText(p.aggValue(a, r))
			if s == "" {
				continue
			}
			x, ok := new(big.Rat).SetString(s)
			if !ok {
				return dst, false, &RowError{FieldPath: a.path, Code: CodeTypeMismatch, Message: "can't add " + clip(s)}
			}
			sum.Add(sum, x)
			n++
			if i := strings.IndexByte(s, '.'); i >= 0 && !strings.ContainsAny(s, "eE") {
				scale = max(scale, len(s)-i-1)
			}
		}
		switch {
		case n == 0:
		case a.fn == "sum":
			v = ratNumber(sum, scale)
		default:
			v = ratNumber(sum.Quo(sum, new(big.Rat).SetInt64(int64(n))), scale+6)
		}
	case "min", "max":
		for _, r := range rows {
			x := p.aggValue(a, r)
			if x == nil {
				continue
			}
			if v == nil || (a.fn == "min") == lessValue(x, v) {
				v = x
			}
		}
	}
	v, err := coerce(v, a.target)
	if err != nil {
		return dst, false, &RowError{FieldPath: a.path, Code: CodeTypeMismatch, Message: err.Error()}
	}
	if v == nil {
		if a.omitNull {
			return dst, false, nil
		}
		return append(dst, "null"...), true, nil
	}
	return appendJSON(dst, v), true, nil
}

func (p *Program) aggValue(a *aggProg, r source.RelatedRow) any {
	if a.idx < 0 || a.idx >= len(r.Values) || r.Values[a.idx] == nil {
		return nil
	}
	return encode.ValueWith(r.Values[a.idx], a.col, p.opts.Encode)
}

// numberText is an encoded numeric value's exact text ("" when it isn't a number).
func numberText(v any) string {
	switch x := v.(type) {
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	case json.Number:
		return string(x)
	case string:
		if _, ok := new(big.Rat).SetString(x); ok {
			return x // a high-precision numeric kept as text
		}
	}
	return ""
}

// ratNumber renders an exact decimal with at most scale decimals, trailing zeros trimmed.
func ratNumber(r *big.Rat, scale int) json.Number {
	if r.IsInt() {
		return json.Number(r.Num().String())
	}
	s := r.FloatString(scale)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	return json.Number(s)
}

// lessValue orders two encoded values: numbers exactly, anything else as text (ISO dates
// sort correctly as text).
func lessValue(a, b any) bool {
	if x, y := numberText(a), numberText(b); x != "" && y != "" {
		rx, _ := new(big.Rat).SetString(x)
		ry, _ := new(big.Rat).SetString(y)
		return rx.Cmp(ry) < 0
	}
	return String(a) < String(b)
}
