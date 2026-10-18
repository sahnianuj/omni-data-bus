package transform

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"omnidatabus/internal/docschema"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/schema"
)

// Computed fields (OC-136): a target field filled from the document's other target fields by a
// template such as `${self.firstName} ${self.lastName}`. Templates are compiled once with the
// root program: their paths are resolved against the document design, checked for scope, and
// ordered so a field that reads another computed field runs after it. After the document is
// assembled, a pass evaluates them over an order-preserving copy of it. Mappings without
// computed fields never take that pass.

// Computed field issue and row-error codes.
const (
	CodeComputedUnknownField    = "COMPUTED_UNKNOWN_FIELD"
	CodeComputedCycle           = "COMPUTED_CYCLE"
	CodeComputedOutOfScope      = "COMPUTED_OUT_OF_SCOPE"
	CodeComputedTemplateSyntax  = "COMPUTED_TEMPLATE_SYNTAX"
	CodeComputedWithSources     = "COMPUTED_WITH_SOURCES"
	CodeComputedLangUnsupported = "COMPUTED_LANG_UNSUPPORTED"
	CodeComputedTooLarge        = "COMPUTED_RESULT_TOO_LARGE"
)

// maxComputedBytes is the largest computed value.
const maxComputedBytes = 1 << 20

// seg is one step of a path: a member (name) or an array position (index ≥ 0).
type seg struct {
	name  string
	index int
}

// tmplRef is one ${…} substitution: its root name, path and where it starts in the text.
type tmplRef struct {
	root         string
	segs         []seg
	line, column int
}

type tmplPart struct {
	lit string
	ref *tmplRef
}

var (
	refRoot = regexp.MustCompile(`^(doc|self|parent|index|row)`)
	refSeg  = regexp.MustCompile(`^(?:\.([A-Za-z_$][A-Za-z0-9_$]*)|\[(\d+)\]|\["((?:[^"\\]|\\.)*)"\])`)
)

// position is the 1-based line and column of offset in s.
func position(s string, offset int) (int, int) {
	line, col := 1, 1
	for i, r := range s {
		if i >= offset {
			break
		}
		if r == '\n' {
			line, col = line+1, 1
		} else {
			col++
		}
	}
	return line, col
}

// parseTemplate splits a template into literal text and ${path} substitutions. Only member
// paths rooted at doc, self, parent, row or index are allowed; `\${` is a literal "${".
func parseTemplate(expr string) ([]tmplPart, *ExprError) {
	var parts []tmplPart
	var lit strings.Builder
	for i := 0; i < len(expr); {
		if strings.HasPrefix(expr[i:], `\${`) {
			lit.WriteString("${")
			i += 3
			continue
		}
		if !strings.HasPrefix(expr[i:], "${") {
			lit.WriteByte(expr[i])
			i++
			continue
		}
		end := strings.IndexByte(expr[i+2:], '}')
		start := i + 2
		line, col := position(expr, start)
		if end < 0 {
			return nil, &ExprError{Line: line, Column: col, Code: CodeComputedTemplateSyntax, Message: "${ isn't closed with }"}
		}
		body := expr[start : start+end]
		ref, err := parseRef(strings.TrimSpace(body))
		if err != "" {
			return nil, &ExprError{
				Line: line, Column: col, Code: CodeComputedTemplateSyntax,
				Message: fmt.Sprintf("${%s}: %s. A template only reads values, e.g. ${doc.firstName}; use JavaScript (a later release) for calls and operators", strings.TrimSpace(body), err),
			}
		}
		ref.line, ref.column = position(expr, start+len(body)-len(strings.TrimLeft(body, " \t\n")))
		if lit.Len() > 0 {
			parts = append(parts, tmplPart{lit: lit.String()})
			lit.Reset()
		}
		parts = append(parts, tmplPart{ref: ref})
		i = start + end + 1
	}
	if lit.Len() > 0 {
		parts = append(parts, tmplPart{lit: lit.String()})
	}
	return parts, nil
}

func parseRef(s string) (*tmplRef, string) {
	m := refRoot.FindString(s)
	if m == "" {
		return nil, "it must start with doc, self, parent, row or index"
	}
	ref := &tmplRef{root: m}
	rest := s[len(m):]
	for rest != "" {
		g := refSeg.FindStringSubmatch(rest)
		if g == nil {
			return nil, "only .name, [n] and [\"name\"] can follow it"
		}
		switch {
		case g[1] != "":
			ref.segs = append(ref.segs, seg{name: g[1], index: -1})
		case g[2] != "":
			n, _ := strconv.Atoi(g[2])
			ref.segs = append(ref.segs, seg{index: n})
		default:
			name, err := strconv.Unquote(`"` + g[3] + `"`)
			if err != nil {
				return nil, "the quoted name isn't valid"
			}
			ref.segs = append(ref.segs, seg{name: name, index: -1})
		}
		rest = rest[len(g[0]):]
	}
	if ref.root == "index" && len(ref.segs) > 0 {
		return nil, "index is a number: nothing can follow it"
	}
	return ref, ""
}

// holderStep is one step from the root to a computed field's object: a member, or an array
// whose every item is visited.
type holderStep struct {
	name  string
	array bool
}

// cpart is a resolved template part.
type cpart struct {
	lit  string
	ref  bool
	base string // doc | self | parent | index | row
	segs []seg
}

// compProg evaluates one computed field.
type compProg struct {
	fieldID, path, name string
	holder              []holderStep
	parts               []cpart
	deps                []string
	nullIfMissing       bool
	onError             string
	omitNull            bool
	steps               []step
	target              docschema.FieldType
}

// docTree indexes the design: every field's parent (an array's items' parent is the array).
type docTree struct {
	parent map[string]*docschema.DocField
	byID   map[string]*docschema.DocField
	paths  map[string]string
	root   *docschema.DocField
}

func newDocTree(s *docschema.Schema) *docTree {
	t := &docTree{parent: map[string]*docschema.DocField{}, byID: map[string]*docschema.DocField{}, paths: s.Paths(), root: &s.Root}
	s.Walk(func(f *docschema.DocField, _ string, parent *docschema.DocField) {
		t.byID[f.ID] = f
		t.parent[f.ID] = parent
	})
	return t
}

// containers are the objects from the root down to the one holding the field (array items
// count as objects; arrays themselves are steps), with the steps to reach the holder.
func (t *docTree) containers(fieldID string) ([]*docschema.DocField, []holderStep) {
	var chain []*docschema.DocField
	for p := t.parent[fieldID]; p != nil; p = t.parent[p.ID] {
		chain = append([]*docschema.DocField{p}, chain...)
		if p.ID == t.root.ID {
			break
		}
	}
	if len(chain) == 0 || chain[0].ID != t.root.ID {
		chain = append([]*docschema.DocField{t.root}, chain...)
	}
	var objs []*docschema.DocField
	var steps []holderStep
	for i, f := range chain {
		switch {
		case i == 0:
			objs = append(objs, f)
		case f.Type == docschema.Array:
			steps = append(steps, holderStep{name: f.Name, array: true})
		case t.parent[f.ID] != nil && t.parent[f.ID].Type == docschema.Array:
			objs = append(objs, f) // the array's items
		default:
			steps = append(steps, holderStep{name: f.Name})
			objs = append(objs, f)
		}
	}
	return objs, steps
}

// resolve follows segs from a design field; nil when a step doesn't exist.
func resolve(from *docschema.DocField, segs []seg) *docschema.DocField {
	cur := from
	for _, s := range segs {
		switch {
		case s.index >= 0:
			if cur.Type != docschema.Array || cur.Items == nil {
				return nil
			}
			cur = cur.Items
		default:
			var next *docschema.DocField
			for i := range cur.Children {
				if cur.Children[i].Name == s.name {
					next = &cur.Children[i]
				}
			}
			if next == nil {
				return nil
			}
			cur = next
		}
	}
	return cur
}

// computedIDs are the fields with an active computed mapping.
func computedIDs(spec *mapping.Spec) map[string]bool {
	out := map[string]bool{}
	for _, m := range spec.FieldMappings {
		if m.Active() && m.Computed != nil {
			out[m.TargetFieldID] = true
		}
	}
	return out
}

// compileComputed compiles every computed field of the spec, in evaluation order.
func compileComputed(spec *mapping.Spec, t *schema.Table, opts Options) ([]*compProg, []mapping.Issue) {
	var issues []mapping.Issue
	tree := newDocTree(&spec.DocSchema)
	scopes := mapping.FieldScopes(spec)
	isComputed := computedIDs(spec)
	issue := func(path, fieldID, mapID, code, msg string, line, col int) {
		issues = append(issues, mapping.Issue{Severity: mapping.SeverityError, Code: code, Path: path, FieldID: fieldID, FieldMappingID: mapID, Message: msg, Line: line, Column: col})
	}
	cols := map[string]bool{}
	for _, c := range t.Columns {
		cols[c.Name] = true
	}
	byID := map[string]*compProg{}
	var order []string
	for _, m := range spec.FieldMappings {
		if !m.Active() || m.Computed == nil {
			continue
		}
		f := tree.byID[m.TargetFieldID]
		path := tree.paths[m.TargetFieldID]
		if f == nil {
			issue("fieldMappings."+m.ID, m.TargetFieldID, m.ID, mapping.CodeUnknownTargetField, "Unknown target field "+m.TargetFieldID+".", 0, 0)
			continue
		}
		c := m.Computed
		switch c.Lang {
		case mapping.ComputedTemplate:
		case mapping.ComputedJS:
			issue(path, f.ID, m.ID, CodeComputedLangUnsupported, "JavaScript computed fields come in a later release (OC-137): use a template.", 0, 0)
			continue
		default:
			issue(path, f.ID, m.ID, CodeComputedLangUnsupported, fmt.Sprintf("Unknown computed language %q: use template.", c.Lang), 0, 0)
			continue
		}
		bad := len(m.Sources) > 0
		for _, st := range m.Transforms {
			bad = bad || st.Op == "constant" || st.Op == "expression" || st.Op == "now"
		}
		if bad {
			issue(path, f.ID, m.ID, CodeComputedWithSources, "A computed field takes its value from its template: remove its source columns and constant or expression steps.", 0, 0)
			continue
		}
		parts, perr := parseTemplate(c.Expr)
		if perr != nil {
			issue(path, f.ID, m.ID, perr.Code, perr.Message, perr.Line, perr.Column)
			continue
		}
		objs, steps := tree.containers(f.ID)
		self := objs[len(objs)-1]
		var parent *docschema.DocField
		if len(objs) > 1 {
			parent = objs[len(objs)-2]
		}
		inArray := false
		for _, s := range steps {
			inArray = inArray || s.array
		}
		cp := &compProg{fieldID: f.ID, path: path, name: f.Name, holder: steps, nullIfMissing: c.NullIfAnyMissing, onError: c.OnError, target: f.Type}
		nh := m.NullHandling
		if nh == "" {
			nh = spec.NullHandling
		}
		cp.omitNull = nh == mapping.NullOmit
		ok := true
		deps := map[string]bool{}
		for _, part := range parts {
			if part.ref == nil {
				cp.parts = append(cp.parts, cpart{lit: part.lit})
				continue
			}
			r := part.ref
			text := r.root
			for _, s := range r.segs {
				if s.index >= 0 {
					text += "[" + strconv.Itoa(s.index) + "]"
				} else {
					text += "." + s.name
				}
			}
			outOfScope := func(msg string) {
				issue(path, f.ID, m.ID, CodeComputedOutOfScope, text+": "+msg, r.line, r.column)
				ok = false
			}
			var base *docschema.DocField
			switch r.root {
			case "doc":
				base = tree.root
			case "self":
				base = self
			case "parent":
				if parent == nil {
					outOfScope("the document root has no parent")
					continue
				}
				base = parent
			case "index":
				if !inArray {
					outOfScope("the field isn't inside an array")
				}
				cp.parts = append(cp.parts, cpart{ref: true, base: "index"})
				continue
			case "row":
				switch {
				case scopes[f.ID] != "":
					outOfScope("row is the source row of fields at the document root; inside a relation, read the fields with self or parent")
				case len(r.segs) != 1 || r.segs[0].index >= 0:
					outOfScope("name one column, e.g. row.created_by")
				case !cols[r.segs[0].name]:
					outOfScope("column " + r.segs[0].name + " isn't in " + t.Name)
				}
				cp.parts = append(cp.parts, cpart{ref: true, base: "row", segs: r.segs})
				continue
			}
			target := resolve(base, r.segs)
			if target == nil {
				issue(path, f.ID, m.ID, CodeComputedUnknownField, text+" isn't a field of the document design.", r.line, r.column)
				ok = false
				continue
			}
			if target.ID == f.ID {
				issue(path, f.ID, m.ID, CodeComputedCycle, path+" reads itself.", r.line, r.column)
				ok = false
				continue
			}
			// A field it reads, or anything inside a container it reads, that is computed.
			var mark func(d *docschema.DocField)
			mark = func(d *docschema.DocField) {
				if isComputed[d.ID] && d.ID != f.ID {
					deps[d.ID] = true
				}
				for i := range d.Children {
					mark(&d.Children[i])
				}
				if d.Items != nil {
					mark(d.Items)
				}
			}
			mark(target)
			cp.parts = append(cp.parts, cpart{ref: true, base: r.root, segs: r.segs})
		}
		env := &compileEnv{sources: 1, opts: opts}
		inType := docschema.String
		for si, st := range m.Transforms {
			o, found := ops[st.Op]
			if !found {
				issues = append(issues, mapping.Issue{Severity: mapping.SeverityError, Code: mapping.CodeTransformInvalid, Path: path, FieldID: f.ID, FieldMappingID: m.ID, Op: st.Op, Message: "Unknown transform '" + st.Op + "'.", OpIndex: &si})
				ok = false
				continue
			}
			fn, err := o.compile(st.Args, env)
			if err != nil {
				issues = append(issues, mapping.Issue{Severity: mapping.SeverityError, Code: mapping.CodeTransformInvalid, Path: path, FieldID: f.ID, FieldMappingID: m.ID, Op: st.Op, Message: st.Op + ": " + err.Error(), OpIndex: &si})
				ok = false
				continue
			}
			inType = o.typeOut(inType, st.Args)
			cp.steps = append(cp.steps, step{op: st.Op, fn: fn})
		}
		if class, _ := mapping.Compat(string(inType), f.Type); class == mapping.ClassIncompatible {
			issue(path, f.ID, m.ID, CodeTypeMismatch, fmt.Sprintf("The template produces %s, but %s is %s.", inType, path, f.Type), 0, 0)
			ok = false
		}
		if !ok {
			continue
		}
		for d := range deps {
			cp.deps = append(cp.deps, d)
		}
		byID[f.ID] = cp
		order = append(order, f.ID)
	}

	// Dependency order: a field runs after every computed field it reads.
	var out []*compProg
	state := map[string]int{} // 0 new, 1 visiting, 2 done
	var stack []string
	var visit func(id string) bool
	visit = func(id string) bool {
		switch state[id] {
		case 1:
			i := 0
			for stack[i] != id {
				i++
			}
			names := make([]string, 0, len(stack)-i+1)
			for _, s := range append(stack[i:], id) {
				names = append(names, tree.paths[s])
			}
			cp := byID[id]
			issue(cp.path, cp.fieldID, "", CodeComputedCycle, "Computed fields read each other: "+strings.Join(names, " → ")+".", 0, 0)
			return false
		case 2:
			return true
		}
		state[id] = 1
		stack = append(stack, id)
		for _, d := range byID[id].deps {
			if _, known := byID[d]; known && !visit(d) {
				return false
			}
		}
		stack = stack[:len(stack)-1]
		state[id] = 2
		out = append(out, byID[id])
		return true
	}
	for _, id := range order {
		if state[id] == 0 {
			stack = stack[:0]
			if !visit(id) {
				return nil, issues
			}
		}
	}
	return out, issues
}

// jv is a JSON value that keeps its members' order, for the computed pass. Keys and scalars
// are slices of the document being read; nodes and member lists come from the program's
// reusable arenas (jvArena), so a document costs no allocations once the arenas have grown.
type jv struct {
	kind byte // 'o' object, 'a' array, 's' string, 'r' other scalar (raw)
	keys [][]byte
	vals []*jv
	raw  []byte // the encoded value as read (containers too: copied as is unless dirty)
	// dirty marks a container whose members changed (or are inside one that did).
	dirty bool
}

func (v *jv) get(name string) *jv {
	if v == nil || v.kind != 'o' {
		return nil
	}
	for i, k := range v.keys {
		if string(k) == name {
			return v.vals[i]
		}
	}
	return nil
}

func (v *jv) set(name string, val *jv) {
	v.dirty = true
	for i, k := range v.keys {
		if string(k) == name {
			v.vals[i] = val
			return
		}
	}
	// A new member: own slices, so the arena's neighbours aren't overwritten.
	v.keys = append(v.keys[:len(v.keys):len(v.keys)], []byte(name))
	v.vals = append(v.vals[:len(v.vals):len(v.vals)], val)
}

func (v *jv) del(name string) {
	v.dirty = true
	for i, k := range v.keys {
		if string(k) == name {
			v.keys = append(v.keys[:i:i], v.keys[i+1:]...)
			v.vals = append(v.vals[:i:i], v.vals[i+1:]...)
			return
		}
	}
}

// jvArena hands out nodes and member lists, reused from one document to the next. Growing
// starts a new block: values handed out earlier keep pointing into the old one.
type jvArena struct {
	nodes []jv
	vals  []*jv
	keys  [][]byte
	// The members of the objects and arrays being parsed, copied into vals/keys when each ends.
	stackVals []*jv
	stackKeys [][]byte
}

func (a *jvArena) reset() {
	a.nodes, a.vals, a.keys = a.nodes[:0], a.vals[:0], a.keys[:0]
	a.stackVals, a.stackKeys = a.stackVals[:0], a.stackKeys[:0]
}

func (a *jvArena) node(kind byte, raw []byte) *jv {
	if len(a.nodes) == cap(a.nodes) {
		a.nodes = make([]jv, 0, max(256, 2*cap(a.nodes)))
	}
	a.nodes = append(a.nodes, jv{kind: kind, raw: raw})
	return &a.nodes[len(a.nodes)-1]
}

// members moves the stacks' entries from start (and keyStart, for an object's keys) into the
// arena and returns them.
func (a *jvArena) members(start, keyStart int, withKeys bool) ([][]byte, []*jv) {
	n := len(a.stackVals) - start
	if len(a.vals)+n > cap(a.vals) {
		a.vals = make([]*jv, 0, max(512, 2*cap(a.vals), n))
	}
	vals := a.vals[len(a.vals) : len(a.vals)+n : len(a.vals)+n]
	copy(vals, a.stackVals[start:])
	a.vals = a.vals[:len(a.vals)+n]
	var keys [][]byte
	if withKeys {
		if len(a.keys)+n > cap(a.keys) {
			a.keys = make([][]byte, 0, max(512, 2*cap(a.keys), n))
		}
		keys = a.keys[len(a.keys) : len(a.keys)+n : len(a.keys)+n]
		copy(keys, a.stackKeys[keyStart:])
		a.keys = a.keys[:len(a.keys)+n]
		a.stackKeys = a.stackKeys[:keyStart]
	}
	a.stackVals = a.stackVals[:start]
	return keys, vals
}

var errBadJSON = errors.New("the document isn't valid JSON")

func skipSpace(b []byte, i int) int {
	for i < len(b) && (b[i] == ' ' || b[i] == '\n' || b[i] == '\t' || b[i] == '\r') {
		i++
	}
	return i
}

// scanString returns the index after the string starting at b[i] (a quote).
func scanString(b []byte, i int) (int, error) {
	for j := i + 1; j < len(b); j++ {
		switch b[j] {
		case '\\':
			j++
		case '"':
			return j + 1, nil
		}
	}
	return i, errBadJSON
}

// parseJV parses the JSON value at b[i:]; it returns the value and the index after it.
func (a *jvArena) parseJV(b []byte, i int) (*jv, int, error) {
	i = skipSpace(b, i)
	if i >= len(b) {
		return nil, i, errBadJSON
	}
	switch b[i] {
	case '{':
		start, keyStart, at := len(a.stackVals), len(a.stackKeys), i
		i = skipSpace(b, i+1)
		if i < len(b) && b[i] == '}' {
			return a.node('o', b[at:i+1]), i + 1, nil
		}
		for {
			if i >= len(b) || b[i] != '"' {
				return nil, i, errBadJSON
			}
			end, err := scanString(b, i)
			if err != nil {
				return nil, i, err
			}
			key := b[i+1 : end-1]
			if bytesContains(key, '\\') {
				u, err := strconv.Unquote(string(b[i:end]))
				if err != nil {
					return nil, i, errBadJSON
				}
				key = []byte(u)
			}
			i = skipSpace(b, end)
			if i >= len(b) || b[i] != ':' {
				return nil, i, errBadJSON
			}
			val, next, err := a.parseJV(b, i+1)
			if err != nil {
				return nil, i, err
			}
			a.stackKeys = append(a.stackKeys, key)
			a.stackVals = append(a.stackVals, val)
			i = skipSpace(b, next)
			if i < len(b) && b[i] == ',' {
				i = skipSpace(b, i+1)
				continue
			}
			if i < len(b) && b[i] == '}' {
				v := a.node('o', b[at:i+1])
				v.keys, v.vals = a.members(start, keyStart, true)
				return v, i + 1, nil
			}
			return nil, i, errBadJSON
		}
	case '[':
		start, at := len(a.stackVals), i
		i = skipSpace(b, i+1)
		if i < len(b) && b[i] == ']' {
			return a.node('a', b[at:i+1]), i + 1, nil
		}
		for {
			val, next, err := a.parseJV(b, i)
			if err != nil {
				return nil, i, err
			}
			a.stackVals = append(a.stackVals, val)
			i = skipSpace(b, next)
			if i < len(b) && b[i] == ',' {
				i++
				continue
			}
			if i < len(b) && b[i] == ']' {
				v := a.node('a', b[at:i+1])
				_, v.vals = a.members(start, 0, false)
				return v, i + 1, nil
			}
			return nil, i, errBadJSON
		}
	case '"':
		end, err := scanString(b, i)
		if err != nil {
			return nil, i, err
		}
		return a.node('s', b[i:end]), end, nil
	default:
		j := i
		for j < len(b) && b[j] != ',' && b[j] != '}' && b[j] != ']' && b[j] != ' ' && b[j] != '\n' && b[j] != '\t' && b[j] != '\r' {
			j++
		}
		return a.node('r', b[i:j]), j, nil
	}
}

func bytesContains(b []byte, c byte) bool {
	for _, x := range b {
		if x == c {
			return true
		}
	}
	return false
}

func appendJV(dst []byte, v *jv) []byte {
	if !v.dirty && v.raw != nil {
		return append(dst, v.raw...)
	}
	switch v.kind {
	case 'o':
		dst = append(dst, '{')
		for i, k := range v.keys {
			if i > 0 {
				dst = append(dst, ',')
			}
			dst = appendString(dst, string(k))
			dst = append(dst, ':')
			dst = appendJV(dst, v.vals[i])
		}
		return append(dst, '}')
	case 'a':
		dst = append(dst, '[')
		for i, e := range v.vals {
			if i > 0 {
				dst = append(dst, ',')
			}
			dst = appendJV(dst, e)
		}
		return append(dst, ']')
	}
	return append(dst, v.raw...)
}

// text renders a value inside a template; missing reports null or absent.
func (v *jv) text() (string, bool) {
	switch {
	case v == nil:
		return "", true
	case v.kind == 's':
		inner := v.raw[1 : len(v.raw)-1]
		if !bytesContains(inner, '\\') {
			return string(inner), false
		}
		u, err := strconv.Unquote(string(v.raw))
		if err != nil {
			return string(inner), false
		}
		return u, false
	case v.kind == 'r':
		if string(v.raw) == "null" {
			return "", true
		}
		return string(v.raw), false
	}
	return string(appendJV(nil, v)), false
}

// follow walks segs from a value.
func follow(v *jv, segs []seg) *jv {
	for _, s := range segs {
		if v == nil {
			return nil
		}
		if s.index >= 0 {
			if v.kind != 'a' || s.index >= len(v.vals) {
				return nil
			}
			v = v.vals[s.index]
			continue
		}
		v = v.get(s.name)
	}
	return v
}

// instances calls fn for every object that holds the field: the root, an embedded object, or
// each item of the arrays on the way. Containers on the way to a changed holder are marked
// dirty, so they are encoded again; the rest of the document is copied as read.
func instances(cur, parent *jv, steps []holderStep, index int, fn func(holder, parent *jv, index int) *RowError) *RowError {
	if len(steps) == 0 {
		return fn(cur, parent, index)
	}
	child := cur.get(steps[0].name)
	if child == nil {
		return nil
	}
	if !steps[0].array {
		if child.kind != 'o' {
			return nil
		}
		err := instances(child, cur, steps[1:], index, fn)
		cur.dirty = cur.dirty || child.dirty
		return err
	}
	if child.kind != 'a' {
		return nil
	}
	for i, item := range child.vals {
		if item.kind != 'o' {
			continue
		}
		err := instances(item, cur, steps[1:], i, fn)
		child.dirty = child.dirty || item.dirty
		if err != nil {
			return err
		}
	}
	cur.dirty = cur.dirty || child.dirty
	return nil
}

// applyComputed fills the computed fields of an assembled document.
func (p *Program) applyComputed(doc []byte) ([]byte, *RowError) {
	p.arena.reset()
	root, _, err := p.arena.parseJV(doc, 0)
	if err != nil {
		return nil, &RowError{Code: CodeTypeMismatch, Message: err.Error()}
	}
	for _, c := range p.computed {
		rerr := instances(root, nil, c.holder, -1, func(holder, parent *jv, index int) *RowError {
			return p.evalComputed(c, root, holder, parent, index)
		})
		if rerr != nil {
			return nil, rerr
		}
	}
	// Another buffer: the parsed values still point into doc. The two are swapped each row.
	out := appendJV(p.compBuf[:0], root)
	p.compBuf = doc[:0]
	return out, nil
}

func (p *Program) evalComputed(c *compProg, root, holder, parent *jv, index int) *RowError {
	var b strings.Builder
	missing := false
	for _, part := range c.parts {
		if !part.ref {
			b.WriteString(part.lit)
			continue
		}
		var s string
		var miss bool
		switch part.base {
		case "index":
			s = strconv.Itoa(index)
		case "row":
			v := p.rowMap()[part.segs[0].name]
			switch x := v.(type) {
			case nil:
				miss = true
			case string:
				s = x
			default:
				s = string(appendJSON(nil, v))
			}
		default:
			base := root
			switch part.base {
			case "self":
				base = holder
			case "parent":
				base = parent
			}
			s, miss = follow(base, part.segs).text()
		}
		missing = missing || miss
		b.WriteString(s)
	}
	var v any = b.String()
	if c.nullIfMissing && missing {
		v = nil
	}
	if len(b.String()) > maxComputedBytes {
		return p.computedError(c, holder, CodeComputedTooLarge, fmt.Sprintf("the value is %d bytes; at most %d", b.Len(), maxComputedBytes))
	}
	if v != nil {
		ctx := &p.ctx
		ctx.sources, ctx.warnings, ctx.path = nil, &p.warnings, c.path
		for _, s := range c.steps {
			ctx.op = s.op
			out, err := s.fn(v, ctx)
			if err != nil {
				code := CodeParseFailed
				var se *stepErr
				if errors.As(err, &se) {
					code = se.code
				}
				return p.computedError(c, holder, code, s.op+": "+err.Error())
			}
			v = out
		}
	}
	v, cerr := coerce(v, c.target)
	if cerr != nil {
		return p.computedError(c, holder, CodeTypeMismatch, cerr.Error())
	}
	if v == nil && c.omitNull {
		holder.del(c.name)
		return nil
	}
	val, _, perr := p.arena.parseJV(appendJSON(nil, v), 0)
	if perr != nil {
		return p.computedError(c, holder, CodeTypeMismatch, perr.Error())
	}
	holder.set(c.name, val)
	return nil
}

// computedError follows the field's onError: fail the row (default), write null, or omit it.
func (p *Program) computedError(c *compProg, holder *jv, code, msg string) *RowError {
	switch c.onError {
	case mapping.OnErrorNull:
		holder.set(c.name, &jv{kind: 'r', raw: []byte("null")})
		return nil
	case mapping.OnErrorOmit:
		holder.del(c.name)
		return nil
	}
	return &RowError{FieldPath: c.path, Code: code, Message: msg}
}
