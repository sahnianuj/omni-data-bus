package transform

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/file"
	exprparser "github.com/expr-lang/expr/parser"
	"github.com/expr-lang/expr/vm"
)

// Expression limits: programs are capped in size at compile time and in memory per evaluation.
const (
	exprMaxNodes     = 500
	exprMemoryBudget = 100_000
)

// ExprHelpers is the allow-list of functions available to expressions besides the operators.
// Every expr built-in is disabled, so there is no way to reach I/O, the clock or reflection.
var ExprHelpers = []string{"upper", "lower", "trim", "len", "round", "coalesce", "now"}

// Env is the evaluation environment: value, row and the helpers. Fields typed any are
// checked dynamically. now is fixed to the job start, so every row sees the same time
// (OC-020 AC4).
type Env struct {
	Value    any                `expr:"value"`
	Row      map[string]any     `expr:"row"`
	Upper    func(any) string   `expr:"upper"`
	Lower    func(any) string   `expr:"lower"`
	Trim     func(any) string   `expr:"trim"`
	Len      func(any) int      `expr:"len"`
	Round    func(any, int) any `expr:"round"`
	Coalesce func(...any) any   `expr:"coalesce"`
	Now      func() string      `expr:"now"`
}

func exprEnv(now time.Time, value any, row map[string]any) Env {
	return Env{
		Value: value,
		Row:   row,
		Upper: func(s any) string { return strings.ToUpper(String(s)) },
		Lower: func(s any) string { return strings.ToLower(String(s)) },
		Trim:  func(s any) string { return strings.TrimSpace(String(s)) },
		Len: func(v any) int {
			switch x := v.(type) {
			case string:
				return len([]rune(x))
			case []any:
				return len(x)
			case map[string]any:
				return len(x)
			}
			return 0
		},
		Round: func(v any, digits int) any {
			f, ok := toFloat(v)
			if !ok {
				return v
			}
			p := math.Pow(10, float64(digits))
			return math.Round(f*p) / p
		},
		Coalesce: func(vs ...any) any {
			for _, v := range vs {
				if v != nil {
					return v
				}
			}
			return nil
		},
		Now: func() string { return ISO(now) },
	}
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case int64:
		return float64(x), true
	case int:
		return float64(x), true
	case float64:
		return x, true
	}
	return 0, false
}

// ExprError is a compile-time expression problem with its 1-based position.
type ExprError struct {
	Line, Column int
	Message      string
}

func (e *ExprError) Error() string {
	return fmt.Sprintf("%s (line %d, column %d)", e.Message, e.Line, e.Column)
}

func compileExpression(args map[string]any, env *compileEnv) (opFunc, error) {
	src, _ := argString(args, "expr")
	if strings.TrimSpace(src) == "" {
		return nil, errors.New("expr is required")
	}
	row := map[string]any{}
	for _, c := range env.columns {
		row[c] = nil
	}
	program, err := expr.Compile(src,
		expr.Env(exprEnv(env.opts.Now, nil, row)),
		expr.DisableAllBuiltins(),
		expr.AsAny(),
		expr.Optimize(true),
		expr.MaxNodes(exprMaxNodes),
	)
	if err != nil {
		var fe *file.Error
		if errors.As(err, &fe) {
			return nil, &ExprError{Line: fe.Line, Column: fe.Column + 1, Message: fe.Message}
		}
		return nil, err
	}
	if bad := unknownRowColumn(src, env.columns); bad != nil {
		return nil, bad
	}
	machine := &vm.VM{MemoryBudget: exprMemoryBudget}
	now := env.opts.Now
	return func(v any, ctx *stepCtx) (any, error) {
		out, err := machine.Run(program, exprEnv(now, v, ctx.row()))
		if err != nil {
			return nil, &stepErr{code: CodeExprError, msg: err.Error()}
		}
		return normalizeExprValue(out), nil
	}, nil
}

// normalizeExprValue maps expr results onto pipeline values (int → int64).
func normalizeExprValue(v any) any {
	switch x := v.(type) {
	case int:
		return int64(x)
	case int32:
		return int64(x)
	case float32:
		return float64(x)
	case []any:
		for i := range x {
			x[i] = normalizeExprValue(x[i])
		}
	}
	return v
}

// unknownRowColumn reports the first row.<name> (or row["name"]) that names no column of the
// table. expr sees row as a plain map, so a typo would otherwise evaluate to nil on every row.
func unknownRowColumn(src string, columns []string) *ExprError {
	tree, err := exprparser.Parse(src)
	if err != nil {
		return nil
	}
	v := &rowRefs{known: make(map[string]bool, len(columns))}
	for _, c := range columns {
		v.known[c] = true
	}
	ast.Walk(&tree.Node, v)
	if v.bad == nil {
		return nil
	}
	line, col := lineCol(src, v.at)
	msg := fmt.Sprintf("unknown column %q", v.bad.Value)
	if near := closestName(v.bad.Value, columns); near != "" {
		msg += fmt.Sprintf("; did you mean %q?", near)
	}
	return &ExprError{Line: line, Column: col, Message: msg}
}

type rowRefs struct {
	known map[string]bool
	bad   *ast.StringNode
	at    int
}

func (r *rowRefs) Visit(node *ast.Node) {
	m, ok := (*node).(*ast.MemberNode)
	if !ok {
		return
	}
	if id, ok := m.Node.(*ast.IdentifierNode); !ok || id.Value != "row" {
		return
	}
	prop, ok := m.Property.(*ast.StringNode)
	if !ok || r.known[prop.Value] {
		return
	}
	at := prop.Location().From
	if at == 0 {
		at = m.Location().From
	}
	if r.bad == nil || at < r.at {
		r.bad, r.at = prop, at
	}
}

// lineCol converts a rune offset into a 1-based line and column.
func lineCol(src string, offset int) (line, col int) {
	line, col = 1, 1
	for i, c := range []rune(src) {
		if i == offset {
			break
		}
		if c == '\n' {
			line, col = line+1, 1
		} else {
			col++
		}
	}
	return line, col
}

// closestName returns the column within edit distance 2 of name, if exactly one is nearest.
func closestName(name string, columns []string) string {
	best, bestD, tie := "", 3, false
	for _, c := range columns {
		d := editDistance(strings.ToLower(name), strings.ToLower(c))
		switch {
		case d < bestD:
			best, bestD, tie = c, d, false
		case d == bestD:
			tie = true
		}
	}
	if tie {
		return ""
	}
	return best
}

func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}
