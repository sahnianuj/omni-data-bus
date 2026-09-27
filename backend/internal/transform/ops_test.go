package transform

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

type opCase struct {
	in      any
	sources []any
	want    any
	errCode string
	warn    bool
}

// runOp compiles one op and runs it over cases.
func runOp(t *testing.T, op string, args map[string]any, sources int, cases []opCase) {
	t.Helper()
	fn, err := ops[op].compile(args, &compileEnv{sources: sources})
	require.NoError(t, err, op)
	for _, c := range cases {
		var warnings []Warning
		out, err := fn(c.in, &stepCtx{sources: c.sources, warnings: &warnings, path: "f", op: op, row: func() map[string]any { return nil }})
		if c.errCode != "" {
			var se *stepErr
			require.ErrorAs(t, err, &se, "%s(%v)", op, c.in)
			require.Equal(t, c.errCode, se.code)
			continue
		}
		require.NoError(t, err, "%s(%v)", op, c.in)
		require.Equal(t, c.want, out, "%s(%v)", op, c.in)
		require.Equal(t, c.warn, len(warnings) > 0, "%s(%v) warnings", op, c.in)
	}
}

func TestOps_AC2_ToString(t *testing.T) {
	runOp(t, "toString", nil, 1, []opCase{
		{in: nil, want: nil},
		{in: int64(42), want: "42"},
		{in: 2.5, want: "2.5"},
		{in: true, want: "true"},
		{in: json.Number("1e3"), want: "1e3"},
		{in: []any{int64(1), "a"}, want: `[1,"a"]`},
		{in: "ü", want: "ü"},
	})
}

func TestOps_AC2_ParseInteger(t *testing.T) {
	runOp(t, "parseInteger", nil, 1, []opCase{
		{in: " 42 ", want: int64(42)},
		{in: "7.0", want: int64(7)},
		{in: 9.9, want: int64(9)},
		{in: true, want: int64(1)},
		{in: json.Number("12"), want: int64(12)},
		{in: "x", want: nil, warn: true},
		{in: nil, want: nil},
	})
	runOp(t, "parseInteger", map[string]any{"strict": true}, 1, []opCase{{in: "x", errCode: CodeParseFailed}, {in: "3", want: int64(3)}})
}

func TestOps_AC2_ParseNumber(t *testing.T) {
	runOp(t, "parseNumber", nil, 1, []opCase{
		{in: "1.5", want: 1.5},
		{in: "3", want: int64(3)},
		{in: int64(2), want: int64(2)},
		{in: "NaN", want: nil, warn: true},
		{in: false, want: int64(0)},
	})
}

func TestOps_AC2_ParseBoolean(t *testing.T) {
	runOp(t, "parseBoolean", nil, 1, []opCase{
		{in: "yes", want: true},
		{in: "Y", want: true},
		{in: "false", want: false},
		{in: "off", want: false},
		{in: int64(1), want: true},
		{in: int64(0), want: false},
		{in: int64(7), want: nil, warn: true},
		{in: "maybe", want: nil, warn: true},
	})
}

func TestOps_AC2_ToDatetime(t *testing.T) {
	runOp(t, "toDatetime", nil, 1, []opCase{
		{in: "2024-01-02T03:04:05+05:30", want: "2024-01-01T21:34:05Z"},
		{in: "2024-01-02", want: "2024-01-02T00:00:00Z"},
		{in: int64(1700000000), want: "2023-11-14T22:13:20Z"},
		{in: int64(1700000000123), want: "2023-11-14T22:13:20.123Z"},
		{in: "soon", want: nil, warn: true},
	})
	runOp(t, "toDatetime", map[string]any{"inputFormat": "dd/MM/yyyy HH:mm", "timezone": "Asia/Kolkata"}, 1, []opCase{{in: "02/01/2024 10:30", want: "2024-01-02T05:00:00Z"}})
	runOp(t, "toDatetime", map[string]any{"strict": true}, 1, []opCase{{in: "soon", errCode: CodeParseFailed}})
}

func TestOps_AC2_FormatDatetime(t *testing.T) {
	ts := "2024-01-02T03:04:05.5Z"
	runOp(t, "formatDatetime", map[string]any{"format": "epoch_ms"}, 1, []opCase{{in: ts, want: int64(1704164645500)}, {in: nil, want: nil}})
	runOp(t, "formatDatetime", map[string]any{"format": "epoch_s"}, 1, []opCase{{in: ts, want: int64(1704164645)}})
	runOp(t, "formatDatetime", map[string]any{"format": "date", "timezone": "America/Los_Angeles"}, 1, []opCase{{in: ts, want: "2024-01-01"}})
	runOp(t, "formatDatetime", map[string]any{"format": "iso8601", "timezone": "Asia/Kolkata"}, 1, []opCase{{in: ts, want: "2024-01-02T08:34:05.5+05:30"}})
	runOp(t, "formatDatetime", map[string]any{"format": "custom", "pattern": "yyyy-MM-dd'T'HH:mm"}, 1, []opCase{{in: ts, want: "2024-01-02T03:04"}, {in: "nope", want: nil, warn: true}})
}

func TestFormatDatetime_AC2_FriendlyPatternConversion(t *testing.T) {
	cases := map[string]string{
		"yyyy-MM-dd'T'HH:mm":         "2006-01-02T15:04",
		"dd/MM/yy hh:mm a":           "02/01/06 03:04 PM",
		"EEE, d MMM yyyy HH:mm:ss Z": "Mon, 2 Jan 2006 15:04:05 -0700",
		"yyyy-MM-dd HH:mm:ss.SSSXXX": "2006-01-02 15:04:05.000Z07:00",
		"'at' HH 'o''clock'":         "at 15 o'clock",
		"MMMM d":                     "January 2",
	}
	for in, want := range cases {
		got, err := GoLayout(in)
		require.NoError(t, err, in)
		require.Equal(t, want, got, in)
	}
	_, err := GoLayout("yyyy-QQ")
	require.Error(t, err)
	_, err = GoLayout("'open")
	require.Error(t, err)
}

func TestOps_AC2_Trim(t *testing.T) {
	runOp(t, "trim", nil, 1, []opCase{{in: "  a  ", want: "a"}, {in: int64(1), want: int64(1)}, {in: nil, want: nil}})
}

func TestOps_AC2_Lower(t *testing.T) {
	runOp(t, "lower", nil, 1, []opCase{{in: "ÄBC", want: "äbc"}, {in: true, want: true}})
}

func TestOps_AC2_Upper(t *testing.T) {
	runOp(t, "upper", nil, 1, []opCase{{in: "straße", want: "STRAßE"}}) // Unicode simple case mapping
}

func TestOps_AC2_Substring(t *testing.T) {
	runOp(t, "substring", map[string]any{"start": 1.0, "length": 3.0}, 1, []opCase{{in: "héllo", want: "éll"}, {in: "a", want: ""}, {in: int64(5), want: int64(5)}})
	runOp(t, "substring", map[string]any{"start": 2.0}, 1, []opCase{{in: "abcdef", want: "cdef"}})
}

func TestOps_AC2_Replace(t *testing.T) {
	runOp(t, "replace", map[string]any{"pattern": "-", "replacement": ""}, 1, []opCase{{in: "555-0101", want: "5550101"}})
	runOp(t, "replace", map[string]any{"pattern": `\s+`, "replacement": " ", "regex": true}, 1, []opCase{{in: "a  \t b", want: "a b"}})
}

func TestOps_AC2_Concat(t *testing.T) {
	runOp(t, "concat", map[string]any{"separator": " "}, 2, []opCase{
		{sources: []any{"Ada", "Lovelace"}, want: "Ada Lovelace"},
		{sources: []any{"Ada", nil}, want: "Ada"},
		{sources: []any{nil, nil}, want: nil},
		{sources: []any{int64(1), true}, want: "1 true"},
	})
	runOp(t, "concat", map[string]any{"separator": "-", "skipNulls": false}, 2, []opCase{{sources: []any{"a", nil}, want: "a-"}})
}

func TestOps_AC2_Split(t *testing.T) {
	runOp(t, "split", map[string]any{"separator": ","}, 1, []opCase{{in: "a, b,c", want: []any{"a", "b", "c"}}, {in: nil, want: nil}})
	runOp(t, "split", map[string]any{"separator": ",", "trim": false}, 1, []opCase{{in: "a, b", want: []any{"a", " b"}}})
}

func TestOps_AC2_Default(t *testing.T) {
	runOp(t, "default", map[string]any{"value": "n/a"}, 1, []opCase{{in: nil, want: "n/a"}, {in: "x", want: "x"}})
	runOp(t, "default", map[string]any{"value": 0.0}, 1, []opCase{{in: nil, want: int64(0)}})
}

func TestOps_AC2_NullIf(t *testing.T) {
	runOp(t, "nullIf", map[string]any{"value": ""}, 1, []opCase{{in: "", want: nil}, {in: "x", want: "x"}})
	runOp(t, "nullIf", map[string]any{"value": -1.0}, 1, []opCase{{in: int64(-1), want: nil}, {in: int64(1), want: int64(1)}})
}

func TestOps_AC2_Constant(t *testing.T) {
	runOp(t, "constant", map[string]any{"value": "v1"}, 0, []opCase{{in: nil, want: "v1"}, {in: "ignored", want: "v1"}})
}

func TestOps_AC2_Lookup(t *testing.T) {
	m := map[string]any{"A": "Active", "I": "Inactive", "1": true}
	runOp(t, "lookup", map[string]any{"map": m}, 1, []opCase{{in: "A", want: "Active"}, {in: int64(1), want: true}, {in: "Z", want: "Z"}, {in: nil, want: nil}})
	runOp(t, "lookup", map[string]any{"map": m, "fallback": "null"}, 1, []opCase{{in: "Z", want: nil}})
	runOp(t, "lookup", map[string]any{"map": m, "fallback": "value", "value": "Unknown"}, 1, []opCase{{in: "Z", want: "Unknown"}})
}

func TestOps_AC2_Round(t *testing.T) {
	runOp(t, "round", map[string]any{"digits": 2.0}, 1, []opCase{{in: 1.005, want: 1.0}, {in: 2.345, want: 2.35}, {in: int64(3), want: int64(3)}, {in: json.Number("1.239"), want: 1.24}})
	runOp(t, "round", nil, 1, []opCase{{in: 2.5, want: int64(3)}})
}

func TestOps_AC2_JSONParse(t *testing.T) {
	runOp(t, "jsonParse", nil, 1, []opCase{{in: `{"a":12345678901234567890}`, want: map[string]any{"a": json.Number("12345678901234567890")}}, {in: int64(1), want: int64(1)}, {in: "{", errCode: CodeParseFailed}})
}

func TestOps_AC2_Mask(t *testing.T) {
	runOp(t, "mask", nil, 1, []opCase{{in: "4111111111111111", want: "************1111"}, {in: "abc", want: "abc"}, {in: nil, want: nil}})
	runOp(t, "mask", map[string]any{"keepLast": 2.0, "char": "#"}, 1, []opCase{{in: "héllo", want: "###lo"}})
}

func TestOps_AC2_Hash(t *testing.T) {
	runOp(t, "hash", nil, 1, []opCase{{in: "ann", want: sha256Hex("ann")}, {in: nil, want: nil}})
	runOp(t, "hash", map[string]any{"algo": "sha1"}, 1, []opCase{{in: "ann", want: sha1Hex("ann")}})
}

func TestOps_ArgErrors(t *testing.T) {
	bad := []struct {
		op      string
		args    map[string]any
		sources int
	}{
		{"replace", map[string]any{}, 1},
		{"replace", map[string]any{"pattern": "(", "regex": true}, 1},
		{"concat", map[string]any{}, 1},
		{"split", map[string]any{}, 1},
		{"default", map[string]any{}, 1},
		{"nullIf", map[string]any{}, 1},
		{"constant", map[string]any{}, 0},
		{"lookup", map[string]any{}, 1},
		{"lookup", map[string]any{"map": map[string]any{"a": "b"}, "fallback": "explode"}, 1},
		{"round", map[string]any{"digits": 99.0}, 1},
		{"round", map[string]any{"digits": 1.5}, 1},
		{"substring", map[string]any{"start": -1.0}, 1},
		{"substring", map[string]any{"start": "x"}, 1},
		{"hash", map[string]any{"algo": "md5"}, 1},
		{"formatDatetime", map[string]any{"format": "custom"}, 1},
		{"formatDatetime", map[string]any{"format": "weird"}, 1},
		{"formatDatetime", map[string]any{"timezone": "Mars/Base"}, 1},
		{"toDatetime", map[string]any{"inputFormat": "yyyy-QQ"}, 1},
		{"expression", map[string]any{}, 1},
	}
	for _, b := range bad {
		_, err := ops[b.op].compile(b.args, &compileEnv{sources: b.sources})
		require.Error(t, err, "%s %v", b.op, b.args)
	}
	require.Len(t, Ops(), 22)
}
