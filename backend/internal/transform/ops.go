// Package transform compiles a mapping spec into a Program that turns one source row into a
// document key and JSON body (OC-020). Preview (OC-022) and the migration pipeline run the
// same compiled program, so a preview is exactly what a migration writes.
package transform

import (
	"bytes"
	"crypto/sha1" //nolint:gosec // offered as a pseudonymization digest, not for security
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"omnidatabus/internal/docschema"
)

// Row error and warning codes (OC-020).
const (
	CodeParseFailed     = "PARSE_FAILED"
	CodeTypeMismatch    = "TYPE_MISMATCH"
	CodeExprError       = "EXPR_ERROR"
	CodeDocTooLarge     = "DOC_TOO_LARGE"
	CodeNonFiniteNumber = "NON_FINITE_NUMBER"
)

// opFunc transforms one value. src holds the field's source values (for concat and
// constant-free multi-source ops); ctx collects warnings.
type opFunc func(v any, ctx *stepCtx) (any, error)

type stepCtx struct {
	sources  []any
	row      func() map[string]any // lazily built column → value map (expressions)
	scoped   func() map[string]any // the scoped expression environment (OC-047)
	warnings *[]Warning
	path     string
	op       string
}

func (c *stepCtx) warn(code, msg string) {
	*c.warnings = append(*c.warnings, Warning{FieldPath: c.path, Op: c.op, Code: code, Message: msg})
}

// stepErr is a row error raised by an op.
type stepErr struct{ code, msg string }

func (e *stepErr) Error() string { return e.msg }

// opSpec compiles one op from its args; typeOut is the static output type given the input
// type (for compile-time checks), "" meaning unchanged.
type opSpec struct {
	compile func(args map[string]any, env *compileEnv) (opFunc, error)
	typeOut func(in docschema.FieldType, args map[string]any) docschema.FieldType
	// str, when set, is the same op on plain strings: chains of such ops run without boxing.
	str func(args map[string]any) (func(string) string, error)
}

func strOnly(f func(string) string) func(map[string]any) (func(string) string, error) {
	return func(map[string]any) (func(string) string, error) { return f, nil }
}

type compileEnv struct {
	sources int
	columns []string
	opts    Options
	escope  *exprScope // names scoped expressions can read (OC-047)
}

var same = func(in docschema.FieldType, _ map[string]any) docschema.FieldType { return in }

func fixed(t docschema.FieldType) func(docschema.FieldType, map[string]any) docschema.FieldType {
	return func(docschema.FieldType, map[string]any) docschema.FieldType { return t }
}

// ops is the transform library (OC-020 table).
var ops = map[string]opSpec{}

func init() {
	ops = map[string]opSpec{
		"toString":       {compile: noArgs(toString), typeOut: fixed(docschema.String)},
		"parseInteger":   {compile: parser(parseInteger), typeOut: fixed(docschema.Integer)},
		"parseNumber":    {compile: parser(parseNumber), typeOut: fixed(docschema.Number)},
		"parseBoolean":   {compile: parser(parseBoolean), typeOut: fixed(docschema.Boolean)},
		"toDatetime":     {compile: compileToDatetime, typeOut: fixed(docschema.Datetime)},
		"formatDatetime": {compile: compileFormatDatetime, typeOut: formatDatetimeType},
		"now":            {compile: compileNow, typeOut: formatDatetimeType},
		"trim":           {compile: noArgs(stringOp(strings.TrimSpace)), typeOut: same, str: strOnly(strings.TrimSpace)},
		"lower":          {compile: noArgs(stringOp(strings.ToLower)), typeOut: same, str: strOnly(strings.ToLower)},
		"upper":          {compile: noArgs(stringOp(strings.ToUpper)), typeOut: same, str: strOnly(strings.ToUpper)},
		"substring":      {compile: compileSubstring, typeOut: same},
		"replace":        {compile: compileReplace, typeOut: same, str: replaceStr},
		"concat":         {compile: compileConcat, typeOut: fixed(docschema.String)},
		"split":          {compile: compileSplit, typeOut: fixed(docschema.Array)},
		"default":        {compile: compileDefault, typeOut: same},
		"nullIf":         {compile: compileNullIf, typeOut: same},
		"constant":       {compile: compileConstant, typeOut: constantType},
		"lookup":         {compile: compileLookup, typeOut: fixed(docschema.Any)},
		"round":          {compile: compileRound, typeOut: same},
		"jsonParse":      {compile: noArgs(jsonParse), typeOut: fixed(docschema.Any)},
		"mask":           {compile: compileMask, typeOut: fixed(docschema.String)},
		"hash":           {compile: compileHash, typeOut: fixed(docschema.String)},
		"expression":     {compile: compileExpression, typeOut: fixed(docschema.Any)},
		"array":          {compile: compileArray, typeOut: fixed(docschema.Array)},
	}
}

// Ops lists the supported op names (for the UI palette and tests).
func Ops() []string {
	out := make([]string, 0, len(ops))
	for k := range ops {
		out = append(out, k)
	}
	return out
}

func noArgs(f opFunc) func(map[string]any, *compileEnv) (opFunc, error) {
	return func(map[string]any, *compileEnv) (opFunc, error) { return f, nil }
}

// --- argument helpers ---

func argString(args map[string]any, k string) (string, bool) {
	v, ok := args[k]
	if !ok || v == nil {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

func argInt(args map[string]any, k string, def int) (int, error) {
	v, ok := args[k]
	if !ok || v == nil {
		return def, nil
	}
	switch x := v.(type) {
	case float64:
		if x != math.Trunc(x) {
			return 0, fmt.Errorf("%s must be a whole number", k)
		}
		return int(x), nil
	case int:
		return x, nil
	case int64:
		return int(x), nil
	case json.Number:
		n, err := x.Int64()
		return int(n), err
	}
	return 0, fmt.Errorf("%s must be a number", k)
}

func argBool(args map[string]any, k string, def bool) bool {
	if v, ok := args[k].(bool); ok {
		return v
	}
	return def
}

// --- value helpers ---

// String renders a pipeline value as text (the toString op).
func String(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	case json.Number:
		return x.String()
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

func toString(v any, _ *stepCtx) (any, error) {
	if v == nil {
		return nil, nil
	}
	return String(v), nil
}

func stringOp(f func(string) string) opFunc {
	return func(v any, _ *stepCtx) (any, error) {
		if s, ok := v.(string); ok {
			return f(s), nil
		}
		return v, nil // non-strings pass through
	}
}

type parseFn func(v any) (any, bool)

// parser wraps a parse function with strict / non-strict failure handling.
func parser(p parseFn) func(map[string]any, *compileEnv) (opFunc, error) {
	return func(args map[string]any, _ *compileEnv) (opFunc, error) {
		strict := argBool(args, "strict", false)
		return func(v any, ctx *stepCtx) (any, error) {
			if v == nil {
				return nil, nil
			}
			out, ok := p(v)
			if ok {
				return out, nil
			}
			msg := fmt.Sprintf("can't parse %q", String(v))
			if strict {
				return nil, &stepErr{code: CodeParseFailed, msg: msg}
			}
			ctx.warn(CodeParseFailed, msg+"; wrote null")
			return nil, nil
		}, nil
	}
}

func parseInteger(v any) (any, bool) {
	switch x := v.(type) {
	case int64:
		return x, true
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return nil, false
		}
		return int64(math.Trunc(x)), true
	case json.Number:
		if n, err := x.Int64(); err == nil {
			return n, true
		}
		if f, err := x.Float64(); err == nil {
			return int64(math.Trunc(f)), true
		}
	case bool:
		if x {
			return int64(1), true
		}
		return int64(0), true
	case string:
		s := strings.TrimSpace(x)
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return n, true
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil && f == math.Trunc(f) && !math.IsInf(f, 0) {
			return int64(f), true
		}
	}
	return nil, false
}

func parseNumber(v any) (any, bool) {
	switch x := v.(type) {
	case int64, float64:
		return x, true
	case json.Number:
		return x, true
	case bool:
		if x {
			return int64(1), true
		}
		return int64(0), true
	case string:
		s := strings.TrimSpace(x)
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return n, true
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil && !math.IsInf(f, 0) && !math.IsNaN(f) {
			return f, true
		}
	}
	return nil, false
}

var boolWords = map[string]bool{
	"true": true, "t": true, "yes": true, "y": true, "1": true, "on": true,
	"false": false, "f": false, "no": false, "n": false, "0": false, "off": false,
}

func parseBoolean(v any) (any, bool) {
	switch x := v.(type) {
	case bool:
		return x, true
	case int64:
		if x == 0 || x == 1 {
			return x == 1, true
		}
	case string:
		b, ok := boolWords[strings.ToLower(strings.TrimSpace(x))]
		return b, ok
	}
	return nil, false
}

func compileSubstring(args map[string]any, _ *compileEnv) (opFunc, error) {
	start, err := argInt(args, "start", 0)
	if err != nil {
		return nil, err
	}
	length, err := argInt(args, "length", -1)
	if err != nil {
		return nil, err
	}
	if start < 0 {
		return nil, fmt.Errorf("start must be 0 or more")
	}
	return func(v any, _ *stepCtx) (any, error) {
		s, ok := v.(string)
		if !ok {
			return v, nil
		}
		if utf8.RuneCountInString(s) <= start {
			return "", nil
		}
		r := []rune(s)[start:]
		if length >= 0 && length < len(r) {
			r = r[:length]
		}
		return string(r), nil
	}, nil
}

func compileReplace(args map[string]any, _ *compileEnv) (opFunc, error) {
	f, err := replaceStr(args)
	if err != nil {
		return nil, err
	}
	return stringOp(f), nil
}

func replaceStr(args map[string]any) (func(string) string, error) {
	pattern, ok := argString(args, "pattern")
	if !ok || pattern == "" {
		return nil, fmt.Errorf("pattern is required")
	}
	repl, _ := argString(args, "replacement")
	if argBool(args, "regex", false) {
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid regular expression: %v", err)
		}
		return func(s string) string { return re.ReplaceAllString(s, repl) }, nil
	}
	return func(s string) string { return strings.ReplaceAll(s, pattern, repl) }, nil
}

func compileConcat(args map[string]any, env *compileEnv) (opFunc, error) {
	if env.sources < 2 {
		return nil, fmt.Errorf("concat needs at least two sources")
	}
	sep, ok := argString(args, "separator")
	if !ok {
		sep = " "
	}
	skipNulls := argBool(args, "skipNulls", true)
	return func(_ any, ctx *stepCtx) (any, error) {
		var b strings.Builder
		n := 0
		for _, s := range ctx.sources {
			if s == nil && skipNulls {
				continue
			}
			if n > 0 {
				b.WriteString(sep)
			}
			b.WriteString(String(s))
			n++
		}
		if n == 0 {
			return nil, nil
		}
		return b.String(), nil
	}, nil
}

func compileSplit(args map[string]any, _ *compileEnv) (opFunc, error) {
	sep, ok := argString(args, "separator")
	if !ok || sep == "" {
		return nil, fmt.Errorf("separator is required")
	}
	trim := argBool(args, "trim", true)
	return func(v any, _ *stepCtx) (any, error) {
		s, ok := v.(string)
		if !ok {
			return v, nil
		}
		parts := strings.Split(s, sep)
		out := make([]any, 0, len(parts))
		for _, p := range parts {
			if trim {
				p = strings.TrimSpace(p)
			}
			out = append(out, p)
		}
		return out, nil
	}, nil
}

func compileDefault(args map[string]any, _ *compileEnv) (opFunc, error) {
	def, ok := args["value"]
	if !ok {
		return nil, fmt.Errorf("value is required")
	}
	def = normalizeArg(def)
	return func(v any, _ *stepCtx) (any, error) {
		if v == nil {
			return def, nil
		}
		return v, nil
	}, nil
}

func compileNullIf(args map[string]any, _ *compileEnv) (opFunc, error) {
	target, ok := args["value"]
	if !ok {
		return nil, fmt.Errorf("value is required")
	}
	t := String(normalizeArg(target))
	return func(v any, _ *stepCtx) (any, error) {
		if v != nil && String(v) == t {
			return nil, nil
		}
		return v, nil
	}, nil
}

func compileConstant(args map[string]any, _ *compileEnv) (opFunc, error) {
	c, ok := args["value"]
	if !ok {
		return nil, fmt.Errorf("value is required")
	}
	c = normalizeArg(c)
	return func(any, *stepCtx) (any, error) { return c, nil }, nil
}

func constantType(_ docschema.FieldType, args map[string]any) docschema.FieldType {
	switch normalizeArg(args["value"]).(type) {
	case string:
		return docschema.String
	case int64:
		return docschema.Integer
	case float64:
		return docschema.Number
	case bool:
		return docschema.Boolean
	case []any:
		return docschema.Array
	case map[string]any:
		return docschema.Object
	}
	return docschema.Any
}

// normalizeArg turns JSON-decoded args (float64 numbers) into pipeline values (int64 when
// integral).
func normalizeArg(v any) any {
	switch x := v.(type) {
	case float64:
		if x == math.Trunc(x) && math.Abs(x) < 1<<53 {
			return int64(x)
		}
	case json.Number:
		if n, err := x.Int64(); err == nil {
			return n
		}
		if f, err := x.Float64(); err == nil {
			return f
		}
	}
	return v
}

func compileLookup(args map[string]any, _ *compileEnv) (opFunc, error) {
	raw, ok := args["map"].(map[string]any)
	if !ok || len(raw) == 0 {
		return nil, fmt.Errorf("map is required (e.g. {\"A\": \"Active\"})")
	}
	table := make(map[string]any, len(raw))
	for k, v := range raw {
		table[k] = normalizeArg(v)
	}
	fallback, _ := argString(args, "fallback")
	if fallback == "" {
		fallback = "keep"
	}
	if fallback != "keep" && fallback != "null" && fallback != "value" {
		return nil, fmt.Errorf("fallback must be keep, null or value")
	}
	fbValue := normalizeArg(args["value"])
	return func(v any, _ *stepCtx) (any, error) {
		if v == nil {
			return nil, nil
		}
		if out, ok := table[String(v)]; ok {
			return out, nil
		}
		switch fallback {
		case "null":
			return nil, nil
		case "value":
			return fbValue, nil
		}
		return v, nil
	}, nil
}

func compileRound(args map[string]any, _ *compileEnv) (opFunc, error) {
	digits, err := argInt(args, "digits", 0)
	if err != nil {
		return nil, err
	}
	if digits < 0 || digits > 15 {
		return nil, fmt.Errorf("digits must be between 0 and 15")
	}
	pow := math.Pow(10, float64(digits))
	return func(v any, _ *stepCtx) (any, error) {
		switch x := v.(type) {
		case float64:
			r := math.Round(x*pow) / pow
			if digits == 0 {
				return int64(r), nil
			}
			return r, nil
		case json.Number:
			if f, err := x.Float64(); err == nil {
				return math.Round(f*pow) / pow, nil
			}
		}
		return v, nil
	}, nil
}

func jsonParse(v any, _ *stepCtx) (any, error) {
	s, ok := v.(string)
	if !ok {
		return v, nil
	}
	dec := json.NewDecoder(bytes.NewReader([]byte(s)))
	dec.UseNumber()
	var out any
	if err := dec.Decode(&out); err != nil {
		return nil, &stepErr{code: CodeParseFailed, msg: "not valid JSON: " + err.Error()}
	}
	return out, nil
}

func compileMask(args map[string]any, _ *compileEnv) (opFunc, error) {
	keep, err := argInt(args, "keepLast", 4)
	if err != nil {
		return nil, err
	}
	char, ok := argString(args, "char")
	if !ok || char == "" {
		char = "*"
	}
	return func(v any, _ *stepCtx) (any, error) {
		if v == nil {
			return nil, nil
		}
		r := []rune(String(v))
		n := len(r) - keep
		if n <= 0 {
			return string(r), nil
		}
		return strings.Repeat(char, n) + string(r[n:]), nil
	}, nil
}

func compileHash(args map[string]any, _ *compileEnv) (opFunc, error) {
	algo, _ := argString(args, "algo")
	switch algo {
	case "", "sha256":
		return func(v any, _ *stepCtx) (any, error) {
			if v == nil {
				return nil, nil
			}
			sum := sha256.Sum256([]byte(String(v)))
			return hex.EncodeToString(sum[:]), nil
		}, nil
	case "sha1":
		return func(v any, _ *stepCtx) (any, error) {
			if v == nil {
				return nil, nil
			}
			sum := sha1.Sum([]byte(String(v))) //nolint:gosec // see import
			return hex.EncodeToString(sum[:]), nil
		}, nil
	}
	return nil, fmt.Errorf("algo must be sha256 or sha1")
}
