package postgres

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// sqlText matches string literals that are (part of) a SQL statement.
var sqlText = regexp.MustCompile(`(?i)\b(select|from|where|order by|join|declare|fetch|insert|update|delete|set transaction)\b`)

// identFields are struct fields holding user-controlled identifiers.
var identFields = map[string]bool{"Schema": true, "Table": true, "Name": true, "Column": true, "Columns": true, "Key": true, "UDTName": true, "SourceTable": true, "SourceSchema": true}

// safeCalls quote or format their argument; anything under them is safe.
var safeCalls = map[string]bool{"Sanitize": true, "quoteIdent": true, "tableIdent": true, "Itoa": true, "FormatFloat": true, "ReplaceAll": true}

// sqlFindings reports unsafe SQL construction in one file.
func sqlFindings(fset *token.FileSet, f *ast.File) []string {
	var out []string
	report := func(n ast.Node, msg string) { out = append(out, fset.Position(n.Pos()).String()+": "+msg) }
	isSQL := func(e ast.Expr) bool {
		lit, ok := e.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return false
		}
		s, _ := strconv.Unquote(lit.Value)
		return sqlText.MatchString(s)
	}
	// rawIdent reports a user-identifier field read that isn't wrapped in a safe call.
	rawIdent := func(e ast.Expr) ast.Node {
		var hit ast.Node
		ast.Inspect(e, func(n ast.Node) bool {
			if hit != nil {
				return false
			}
			switch x := n.(type) {
			case *ast.CallExpr:
				name := ""
				switch fn := x.Fun.(type) {
				case *ast.Ident:
					name = fn.Name
				case *ast.SelectorExpr:
					name = fn.Sel.Name
				}
				return !safeCalls[name] // don't look inside safe calls
			case *ast.SelectorExpr:
				if identFields[x.Sel.Name] {
					hit = x
				}
			}
			return true
		})
		return hit
	}
	// operands flattens a + chain.
	var operands func(e ast.Expr) []ast.Expr
	operands = func(e ast.Expr) []ast.Expr {
		if b, ok := e.(*ast.BinaryExpr); ok && b.Op == token.ADD {
			return append(operands(b.X), operands(b.Y)...)
		}
		return []ast.Expr{e}
	}
	seen := map[ast.Node]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CallExpr:
			if sel, ok := x.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Sprintf" && len(x.Args) > 0 && isSQL(x.Args[0]) {
				for _, arg := range x.Args[1:] {
					if hit := rawIdent(arg); hit != nil {
						report(hit, "identifier formatted into SQL with fmt.Sprintf without pgx.Identifier.Sanitize/quoteIdent")
					}
				}
			}
		case *ast.BinaryExpr:
			if x.Op != token.ADD || seen[x] {
				return true
			}
			ops := operands(x)
			ast.Inspect(x, func(m ast.Node) bool { seen[m] = true; return true })
			sql := false
			for _, o := range ops {
				sql = sql || isSQL(o)
			}
			if !sql {
				return true
			}
			for _, o := range ops {
				if hit := rawIdent(o); hit != nil {
					report(hit, "identifier concatenated into SQL without pgx.Identifier.Sanitize/quoteIdent")
				}
			}
		}
		return true
	})
	return out
}

func TestNoUnsafeSQLConstruction_AC6(t *testing.T) {
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	fset := token.NewFileSet()
	checked := 0
	var findings []string
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		require.NoError(t, err)
		findings = append(findings, sqlFindings(fset, f)...)
		checked++
	}
	require.Greater(t, checked, 5)
	require.Empty(t, findings)
}

// The lint itself catches both unsafe patterns (and accepts the safe one).
func TestNoUnsafeSQLConstruction_AC6_DetectsUnsafe(t *testing.T) {
	src, err := os.ReadFile("testdata/sqllint/unsafe.go.txt")
	require.NoError(t, err)
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "unsafe.go", src, 0)
	require.NoError(t, err)
	findings := sqlFindings(fset, f)
	require.Len(t, findings, 3, "%v", findings) // t.Schema and t.Table, then the Sprintf
	require.Contains(t, strings.Join(findings, "\n"), "fmt.Sprintf")
	require.Contains(t, strings.Join(findings, "\n"), "unsafe.go:9", "the Sprintf of t.Name")
	for _, fnd := range findings {
		require.NotContains(t, fnd, "unsafe.go:11", "the quoted form is safe")
	}
}
