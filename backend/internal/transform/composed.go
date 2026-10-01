package transform

import (
	"fmt"
	"sort"

	"github.com/expr-lang/expr/ast"
	exprparser "github.com/expr-lang/expr/parser"

	"omnidatabus/internal/encode"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/schema"
)

// Names an expression can use besides value, row and the helpers (OC-047): $root is the
// document's root row and $parent the enclosing relation's row (the root row at the root).
const (
	exprRoot   = "$root"
	exprParent = "$parent"
)

// exprScope is what a program's expressions can read by name (OC-047): the row's columns as
// bare names, $root, $parent, and every relation in scope — by id, by the name of the field it
// fills and by its table's name — as an object of its columns. Names of relations outside the
// scope are remembered to report SOURCE_OUT_OF_SCOPE rather than "unknown name".
type exprScope struct {
	current  []string
	root     *schema.Table
	parent   *schema.Table
	parUp    int
	aliases  map[string]exprAlias
	outside  map[string]string // name → the relation's table name
	where    string            // "under items" or "at the root", for messages
	reserved map[string]bool
}

// exprAlias is an in-scope relation's row: an ancestor program's row (up levels up) or a
// lookup-only relation's row under it.
type exprAlias struct {
	up     int
	lookup string
	table  *schema.Table
}

// newExprScope lists the names available to expressions in a (sub-)program's scope.
func newExprScope(sc *compileScope, related map[string]*schema.Table, columns []string) *exprScope {
	es := &exprScope{current: columns, aliases: map[string]exprAlias{}, outside: map[string]string{}, reserved: map[string]bool{"value": true, "row": true}}
	for _, h := range ExprHelpers {
		es.reserved[h] = true
	}
	for _, c := range columns {
		es.reserved[c] = true // a bare column wins over a relation's name
	}
	spec := sc.spec
	paths := spec.DocSchema.Paths()
	es.where = "at the root"
	if r := spec.RelationByID(sc.id); r != nil && paths[r.TargetFieldID] != "" {
		es.where = "under " + paths[r.TargetFieldID]
	}
	inScope := map[string]bool{}
	add := func(r *mapping.Relation, a exprAlias) {
		inScope[r.ID] = true
		names := []string{r.ID}
		if f := spec.DocSchema.Find(r.TargetFieldID); f != nil && !r.LookupOnly {
			names = append(names, f.Name)
		}
		names = append(names, r.Table.Name)
		for _, n := range names {
			if _, taken := es.aliases[n]; !taken && !es.reserved[n] && n != exprRoot && n != exprParent {
				es.aliases[n] = a
			}
		}
	}
	up := 0
	for s := sc; s != nil; s, up = s.up, up+1 {
		if s.up == nil {
			es.root = s.table
		}
		if up == 1 {
			es.parent, es.parUp = s.table, 1
		}
		if r := spec.RelationByID(s.id); r != nil {
			add(r, exprAlias{up: up, table: s.table})
		}
		for i := range spec.Relations {
			r := &spec.Relations[i]
			if r.LookupOnly && r.Parent() == s.id && related[r.ID] != nil {
				add(r, exprAlias{up: up, lookup: r.ID, table: related[r.ID]})
			}
		}
	}
	if es.parent == nil {
		es.parent, es.parUp = es.root, 0 // at the root, $parent is the root row
	}
	for i := range spec.Relations {
		r := &spec.Relations[i]
		if inScope[r.ID] {
			continue
		}
		names := []string{r.ID, r.Table.Name}
		if f := spec.DocSchema.Find(r.TargetFieldID); f != nil && !r.LookupOnly {
			names = append(names, f.Name)
		}
		for _, n := range names {
			if _, ok := es.aliases[n]; !ok && !es.reserved[n] {
				es.outside[n] = r.Table.Name
			}
		}
	}
	return es
}

// freeNames are the identifiers an expression reads that the plain environment (value, row,
// helpers) doesn't have, with their first rune offset.
func freeNames(src string) map[string]int {
	tree, err := exprparser.Parse(src)
	if err != nil {
		return nil
	}
	v := &identRefs{found: map[string]int{}}
	ast.Walk(&tree.Node, v)
	return v.found
}

type identRefs struct{ found map[string]int }

func (r *identRefs) Visit(node *ast.Node) {
	id, ok := (*node).(*ast.IdentifierNode)
	if !ok {
		return
	}
	switch id.Value {
	case "value", "row":
		return
	}
	for _, h := range ExprHelpers {
		if id.Value == h {
			return
		}
	}
	at := id.Location().From
	if prev, seen := r.found[id.Value]; !seen || at < prev {
		r.found[id.Value] = at
	}
}

// check reports the first name that's a relation outside the scope (SOURCE_OUT_OF_SCOPE), or a
// column that isn't in the table it's read from ($root.x, items.x).
func (es *exprScope) check(src string, names map[string]int) *ExprError {
	var first *ExprError
	firstAt := -1
	report := func(at int, code, msg string) {
		if first == nil || at < firstAt {
			line, col := lineCol(src, at)
			first, firstAt = &ExprError{Line: line, Column: col, Message: msg, Code: code}, at
		}
	}
	for n, at := range names {
		if table, out := es.outside[n]; out {
			report(at, mapping.CodeSourceOutOfScope, fmt.Sprintf("%s isn't bound on the path to this field; bind %s %s or add a lookup", n, table, es.where))
		}
	}
	tree, err := exprparser.Parse(src)
	if err == nil {
		v := &memberRefs{es: es, report: report}
		ast.Walk(&tree.Node, v)
	}
	return first
}

type memberRefs struct {
	es     *exprScope
	report func(at int, code, msg string)
}

func (m *memberRefs) Visit(node *ast.Node) {
	mn, ok := (*node).(*ast.MemberNode)
	if !ok {
		return
	}
	id, ok := mn.Node.(*ast.IdentifierNode)
	if !ok {
		return
	}
	prop, ok := mn.Property.(*ast.StringNode)
	if !ok {
		return
	}
	var t *schema.Table
	switch id.Value {
	case exprRoot:
		t = m.es.root
	case exprParent:
		t = m.es.parent
	default:
		a, ok := m.es.aliases[id.Value]
		if !ok {
			return
		}
		t = a.table
	}
	if t == nil || columnIndex(t, prop.Value) >= 0 {
		return
	}
	at := prop.Location().From
	if at == 0 {
		at = mn.Location().From
	}
	msg := fmt.Sprintf("unknown column %q of %s", prop.Value, id.Value)
	if near := closestName(prop.Value, tableColumns(t)); near != "" {
		msg += fmt.Sprintf("; did you mean %q?", near)
	}
	m.report(at, "", msg)
}

func tableColumns(t *schema.Table) []string {
	out := make([]string, len(t.Columns))
	for i, c := range t.Columns {
		out[i] = c.Name
	}
	return out
}

// template is the environment an expression is compiled against: every name with a value of
// the right kind (the values are filled per row by Program.scopedEnv).
func (es *exprScope) template(helpers map[string]any) map[string]any {
	env := make(map[string]any, len(helpers)+len(es.current)+len(es.aliases)+4)
	for k, v := range helpers {
		env[k] = v
	}
	env["value"] = nil
	env["row"] = map[string]any{}
	for _, c := range es.current {
		if _, taken := env[c]; !taken {
			env[c] = nil
		}
	}
	env[exprRoot] = map[string]any{}
	env[exprParent] = map[string]any{}
	for n := range es.aliases {
		env[n] = map[string]any{}
	}
	return env
}

// helperMap is the helpers as environment entries.
func helperMap(e Env) map[string]any {
	return map[string]any{
		"upper": e.Upper, "lower": e.Lower, "trim": e.Trim, "len": e.Len, "round": e.Round,
		"coalesce": e.Coalesce, "now": e.Now, "concat": e.Concat,
	}
}

// ancestor is the program up levels up (the root program at most).
func (p *Program) ancestor(up int) *Program {
	q := p
	for i := 0; i < up && q.parent != nil; i++ {
		q = q.parent
	}
	return q
}

// scopedEnv is the current row's environment for scoped expressions, built once per row; value
// is set per evaluation.
func (p *Program) scopedEnv() map[string]any {
	if p.envBuilt {
		return p.envCache
	}
	es := p.escope
	if p.envCache == nil {
		p.envCache = make(map[string]any, len(p.helpers)+len(p.cols)+len(es.aliases)+4)
		for k, v := range p.helpers {
			p.envCache[k] = v
		}
	}
	env := p.envCache
	row := p.rowMap()
	env["row"] = row
	for _, c := range es.current {
		if _, helper := p.helpers[c]; !helper {
			env[c] = row[c]
		}
	}
	root := p.ancestor(1 << 30)
	env[exprRoot] = root.rowMap()
	env[exprParent] = p.ancestor(es.parUp).rowMap()
	for n, a := range es.aliases {
		q := p.ancestor(a.up)
		if a.lookup == "" {
			env[n] = q.rowMap()
			continue
		}
		env[n] = lookupRow(q, a, p.opts.Encode)
	}
	p.envBuilt = true
	return env
}

// lookupRow is a lookup-only relation's row under q as a column → value map (empty without one).
func lookupRow(q *Program, a exprAlias, opts encode.Options) map[string]any {
	out := map[string]any{}
	set := q.related[a.lookup]
	if set.Len() == 0 {
		return out
	}
	vals := set.Rows[0].Values
	for i, c := range a.table.Columns {
		if i < len(vals) {
			out[c.Name] = encode.ValueWith(vals[i], c, opts)
		}
	}
	return out
}

// ExpressionNames are the in-scope names a composer expression can use at a field (the
// inspector's autocompletion, OC-048): $root, $parent and the relations' names, sorted.
func ExpressionNames(spec *mapping.Spec, fieldID string, related map[string]*schema.Table, table *schema.Table) []string {
	scopes := mapping.FieldScopes(spec)
	scope, ok := scopes[fieldID]
	if !ok {
		return nil
	}
	root := &compileScope{spec: spec, scopes: scopes, table: table}
	sc := root
	chain := mapping.ScopeChain(spec, scope)
	for i := len(chain) - 1; i >= 0; i-- {
		sc = &compileScope{spec: spec, scopes: scopes, id: chain[i], up: sc, table: related[chain[i]]}
	}
	if sc.table == nil {
		sc.table = &schema.Table{}
	}
	es := newExprScope(sc, related, tableColumns(sc.table))
	out := []string{exprRoot, exprParent}
	for n := range es.aliases {
		out = append(out, n)
	}
	sort.Strings(out[2:])
	return out
}

// compileArray builds an array from the field's sources (the composer's literal arrays, e.g.
// phones: [home_phone, mobile_phone], OC-047); dropNulls leaves null elements out.
func compileArray(args map[string]any, env *compileEnv) (opFunc, error) {
	if env.sources < 1 {
		return nil, fmt.Errorf("array needs at least one source")
	}
	drop := argBool(args, "dropNulls", false)
	return func(v any, ctx *stepCtx) (any, error) {
		src := ctx.sources
		if src == nil {
			src = []any{v}
		}
		out := make([]any, 0, len(src))
		for _, x := range src {
			if x == nil && drop {
				continue
			}
			out = append(out, x)
		}
		return out, nil
	}, nil
}
