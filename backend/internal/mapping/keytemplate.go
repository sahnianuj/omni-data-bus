package mapping

import (
	"crypto/sha1" //nolint:gosec // a key-shortening digest, not a security primitive
	"encoding/hex"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"omnidatabus/internal/naming"
	"omnidatabus/internal/schema"
)

// MaxKeyBytes is Couchbase's document key limit.
const MaxKeyBytes = 250

// Key issue codes (OC-016).
const (
	CodeKeyTemplateInvalid = "KEY_TEMPLATE_INVALID"
	CodeKeyTemplateMissing = "KEY_TEMPLATE_MISSING"
	CodeKeyMayCollide      = "KEY_MAY_COLLIDE"
	CodeKeyNonIdempotent   = "NON_IDEMPOTENT_KEY"
	CodeKeyTooLong         = "KEY_TOO_LONG"
	CodeKeyNullComponent   = "KEY_NULL_COMPONENT"
	CodeKeyMayBeTooLong    = "KEY_MAY_BE_TOO_LONG"
)

// TemplateError is a parse error at a byte position of the template.
type TemplateError struct {
	Pos int
	Msg string
}

func (e *TemplateError) Error() string { return fmt.Sprintf("%s (at position %d)", e.Msg, e.Pos) }

// RowError is a per-row key failure (dead-lettered by the pipeline, OC-026).
type RowError struct {
	Code    string
	Message string
}

func (e *RowError) Error() string { return e.Code + ": " + e.Message }

type filter struct {
	name string
	pad  int
}

type segment struct {
	literal string
	column  string // "" for literal / special
	special string // "$table" | "$uuid"
	filters []filter
}

// KeyTemplate is a parsed key template: literal text, {column} tokens with optional
// |lower|upper|trim|pad:N|hash filters, and {$table} / {$uuid}. Braces are escaped as {{ and }}.
type KeyTemplate struct {
	Source   string
	segments []segment
	cols     []int // column index per segment (-1 when not a column), set by Bind
	table    string
}

// ParseKeyTemplate parses a template; errors carry the byte position.
func ParseKeyTemplate(tpl string) (*KeyTemplate, error) {
	kt := &KeyTemplate{Source: tpl}
	var lit strings.Builder
	flush := func() {
		if lit.Len() > 0 {
			kt.segments = append(kt.segments, segment{literal: lit.String()})
			lit.Reset()
		}
	}
	for i := 0; i < len(tpl); i++ {
		c := tpl[i]
		switch {
		case c == '{' && i+1 < len(tpl) && tpl[i+1] == '{':
			lit.WriteByte('{')
			i++
		case c == '}' && i+1 < len(tpl) && tpl[i+1] == '}':
			lit.WriteByte('}')
			i++
		case c == '}':
			return nil, &TemplateError{Pos: i, Msg: "unexpected '}' (write }} for a literal brace)"}
		case c == '{':
			end := strings.IndexByte(tpl[i+1:], '}')
			if end < 0 {
				return nil, &TemplateError{Pos: i, Msg: "unclosed '{'"}
			}
			body := tpl[i+1 : i+1+end]
			seg, err := parseToken(body, i+1)
			if err != nil {
				return nil, err
			}
			flush()
			kt.segments = append(kt.segments, seg)
			i += end + 1
		default:
			lit.WriteByte(c)
		}
	}
	flush()
	if len(kt.segments) == 0 {
		return nil, &TemplateError{Pos: 0, Msg: "the key template is empty"}
	}
	return kt, nil
}

func parseToken(body string, pos int) (segment, error) {
	parts := strings.Split(body, "|")
	name := strings.TrimSpace(parts[0])
	if name == "" {
		return segment{}, &TemplateError{Pos: pos, Msg: "empty token"}
	}
	seg := segment{column: name}
	if strings.HasPrefix(name, "$") {
		if name != "$table" && name != "$uuid" {
			return segment{}, &TemplateError{Pos: pos, Msg: "unknown special token " + name + " (use {$table} or {$uuid})"}
		}
		seg = segment{special: name}
	}
	off := pos + len(parts[0]) + 1
	for _, p := range parts[1:] {
		f := strings.TrimSpace(p)
		switch {
		case f == "lower", f == "upper", f == "trim", f == "hash":
			seg.filters = append(seg.filters, filter{name: f})
		case strings.HasPrefix(f, "pad:"):
			n, err := strconv.Atoi(strings.TrimPrefix(f, "pad:"))
			if err != nil || n < 1 || n > 64 {
				return segment{}, &TemplateError{Pos: off, Msg: "pad needs a width between 1 and 64, e.g. pad:8"}
			}
			seg.filters = append(seg.filters, filter{name: "pad", pad: n})
		default:
			return segment{}, &TemplateError{Pos: off, Msg: "unknown filter '" + f + "' (use lower, upper, trim, pad:N or hash)"}
		}
		off += len(p) + 1
	}
	return seg, nil
}

// Columns lists the referenced columns, in template order without duplicates.
func (kt *KeyTemplate) Columns() []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range kt.segments {
		if s.column != "" && s.special == "" && !seen[s.column] {
			seen[s.column] = true
			out = append(out, s.column)
		}
	}
	return out
}

// UsesUUID reports whether the template contains {$uuid}.
func (kt *KeyTemplate) UsesUUID() bool {
	for _, s := range kt.segments {
		if s.special == "$uuid" {
			return true
		}
	}
	return false
}

// Bind resolves column tokens to row indexes. Unknown columns are an error.
func (kt *KeyTemplate) Bind(table string, columns []string) error {
	idx := make(map[string]int, len(columns))
	for i, c := range columns {
		idx[c] = i
	}
	kt.table = table
	kt.cols = make([]int, len(kt.segments))
	for i, s := range kt.segments {
		kt.cols[i] = -1
		if s.column != "" && s.special == "" {
			ci, ok := idx[s.column]
			if !ok {
				return fmt.Errorf("the key template uses unknown column %q", s.column)
			}
			kt.cols[i] = ci
		}
	}
	return nil
}

// AppendKey renders the key for one row of encoded values (encode.Value output) onto dst.
// It doesn't allocate for integer, string and boolean components.
func (kt *KeyTemplate) AppendKey(dst []byte, row []any) ([]byte, error) {
	start := len(dst)
	for i, s := range kt.segments {
		switch {
		case s.literal != "":
			dst = append(dst, s.literal...)
		case s.special == "$table":
			dst = append(dst, kt.table...)
		case s.special == "$uuid":
			dst = append(dst, uuid.Must(uuid.NewV7()).String()...)
		default:
			ci := kt.cols[i]
			if ci < 0 || ci >= len(row) || row[ci] == nil {
				return dst[:start], &RowError{Code: CodeKeyNullComponent, Message: "key column " + s.column + " is null"}
			}
			if len(s.filters) == 0 {
				dst = appendScalar(dst, row[ci])
				continue
			}
			v := string(appendScalar(nil, row[ci]))
			for _, f := range s.filters {
				v = applyFilter(f, v)
			}
			dst = append(dst, v...)
		}
	}
	if len(dst)-start > MaxKeyBytes {
		return dst[:start], &RowError{Code: CodeKeyTooLong, Message: fmt.Sprintf("the key is %d bytes; Couchbase allows %d", len(dst)-start, MaxKeyBytes)}
	}
	return dst, nil
}

// Render renders the key for one row.
func (kt *KeyTemplate) Render(row []any) (string, error) {
	var buf [MaxKeyBytes + 64]byte
	b, err := kt.AppendKey(buf[:0], row)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func appendScalar(dst []byte, v any) []byte {
	switch x := v.(type) {
	case string:
		return append(dst, x...)
	case int64:
		return strconv.AppendInt(dst, x, 10)
	case int:
		return strconv.AppendInt(dst, int64(x), 10)
	case int32:
		return strconv.AppendInt(dst, int64(x), 10)
	case float64:
		if x == math.Trunc(x) && math.Abs(x) < 1e15 {
			return strconv.AppendInt(dst, int64(x), 10)
		}
		return strconv.AppendFloat(dst, x, 'f', -1, 64)
	case bool:
		return strconv.AppendBool(dst, x)
	case fmt.Stringer:
		return append(dst, x.String()...)
	}
	return fmt.Append(dst, v)
}

func applyFilter(f filter, v string) string {
	switch f.name {
	case "lower":
		return strings.ToLower(v)
	case "upper":
		return strings.ToUpper(v)
	case "trim":
		return strings.TrimSpace(v)
	case "pad":
		if len(v) < f.pad {
			return strings.Repeat("0", f.pad-len(v)) + v
		}
		return v
	case "hash":
		sum := sha1.Sum([]byte(v)) //nolint:gosec // see import
		return hex.EncodeToString(sum[:])
	}
	return v
}

// DefaultKeyTemplate is <table>::{pk1}[::{pk2}…], or {$uuid} (not idempotent) without a PK.
func DefaultKeyTemplate(t *schema.Table) (string, []string) {
	if len(t.PrimaryKey) == 0 {
		return "{$uuid}", []string{CodeKeyNonIdempotent}
	}
	var b strings.Builder
	b.WriteString(naming.Snake(t.Name))
	for _, pk := range t.PrimaryKey {
		b.WriteString("::{")
		b.WriteString(pk)
		b.WriteString("}")
	}
	return b.String(), nil
}

// KeyAnalysis is the designer's badge: unique, may collide, or not idempotent.
type KeyAnalysis struct {
	Unique         bool     `json:"unique"`
	Codes          []string `json:"codes"`
	MaxBytes       int      `json:"maxBytes,omitempty"` // worst-case estimate; 0 = unknown
	UnknownColumns []string `json:"unknownColumns,omitempty"`
}

// AnalyzeKey checks a template against the table: every column must exist; referenced
// columns must cover the PK or a unique constraint (else KEY_MAY_COLLIDE); {$uuid} is
// NON_IDEMPOTENT_KEY; and a worst-case length over 250 bytes is KEY_MAY_BE_TOO_LONG.
func AnalyzeKey(kt *KeyTemplate, t *schema.Table) KeyAnalysis {
	a := KeyAnalysis{Codes: []string{}}
	used := map[string]bool{}
	for _, c := range kt.Columns() {
		if t.Column(c) == nil {
			a.UnknownColumns = append(a.UnknownColumns, c)
		}
		used[c] = true
	}
	covers := func(cols []string) bool {
		if len(cols) == 0 {
			return false
		}
		for _, c := range cols {
			if !used[c] {
				return false
			}
		}
		return true
	}
	switch {
	case kt.UsesUUID():
		a.Codes = append(a.Codes, CodeKeyNonIdempotent)
	case covers(t.PrimaryKey):
		a.Unique = true
	default:
		for _, u := range t.UniqueConstraints {
			if covers(u) {
				a.Unique = true
			}
		}
		if !a.Unique {
			a.Codes = append(a.Codes, CodeKeyMayCollide)
		}
	}
	a.MaxBytes = estimate(kt, t)
	if a.MaxBytes > MaxKeyBytes {
		a.Codes = append(a.Codes, CodeKeyMayBeTooLong)
	}
	return a
}

// estimate is the worst-case key length, or 0 when a component is unbounded (text).
func estimate(kt *KeyTemplate, t *schema.Table) int {
	n := 0
	for _, s := range kt.segments {
		switch {
		case s.literal != "":
			n += len(s.literal)
		case s.special == "$table":
			n += len(t.Name)
		case s.special == "$uuid":
			n += 36
		default:
			w := columnWidth(t.Column(s.column))
			for _, f := range s.filters {
				switch f.name {
				case "hash":
					w = 40
				case "pad":
					if w > 0 && w < f.pad {
						w = f.pad
					}
				}
			}
			if w == 0 {
				return 0
			}
			n += w
		}
	}
	return n
}

func columnWidth(c *schema.Column) int {
	if c == nil {
		return 0
	}
	switch c.UDTName {
	case "int2":
		return 6
	case "int4":
		return 11
	case "int8":
		return 20
	case "uuid":
		return 36
	case "date":
		return 10
	case "timestamp", "timestamptz":
		return 32
	case "bool":
		return 5
	}
	if c.MaxLength != nil {
		return *c.MaxLength * 4 // UTF-8 worst case
	}
	return 0
}
