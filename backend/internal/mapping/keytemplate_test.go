package mapping

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"omnidatabus/internal/race"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/schema"
)

type keyCase struct {
	Name          string   `json:"name"`
	Template      string   `json:"template"`
	Table         string   `json:"table"`
	Columns       []string `json:"columns"`
	Values        []any    `json:"values"`
	Expected      string   `json:"expected"`
	Error         string   `json:"error"`
	ParseErrorPos *int     `json:"parseErrorPos"`
}

func keyCases(t *testing.T) []keyCase {
	t.Helper()
	raw, err := os.ReadFile("testdata/key-templates.json")
	require.NoError(t, err)
	var f struct {
		Cases []keyCase `json:"cases"`
	}
	require.NoError(t, json.Unmarshal(raw, &f))
	return f.Cases
}

// JSON numbers decode as float64; the pipeline hands the renderer int64 for integer columns.
func rowOf(vals []any) []any {
	out := make([]any, len(vals))
	for i, v := range vals {
		if f, ok := v.(float64); ok && f == float64(int64(f)) {
			out[i] = int64(f)
		} else {
			out[i] = v
		}
	}
	return out
}

func TestParseKeyTemplate_AC1_TokensFiltersLiterals(t *testing.T) {
	for _, c := range keyCases(t) {
		if c.ParseErrorPos != nil || c.Error != "" {
			continue
		}
		kt, err := ParseKeyTemplate(c.Template)
		require.NoError(t, err, c.Name)
		require.NoError(t, kt.Bind(c.Table, c.Columns), c.Name)
		got, err := kt.Render(rowOf(c.Values))
		require.NoError(t, err, c.Name)
		require.Equal(t, c.Expected, got, c.Name)
	}
	kt, err := ParseKeyTemplate("a::{x}::{y}::{x}")
	require.NoError(t, err)
	require.Equal(t, []string{"x", "y"}, kt.Columns())
}

func TestRenderKey_AC1_Values(t *testing.T) {
	kt, err := ParseKeyTemplate("{i}|{f}|{w}|{s}|{b}")
	require.NoError(t, err)
	require.NoError(t, kt.Bind("t", []string{"i", "f", "w", "s", "b"}))
	got, err := kt.Render([]any{int64(-3), 2.5, 7.0, "x", false})
	require.NoError(t, err)
	require.Equal(t, "-3|2.5|7|x|false", got)

	u, err := ParseKeyTemplate("{$uuid}")
	require.NoError(t, err)
	require.NoError(t, u.Bind("t", nil))
	a, _ := u.Render(nil)
	b, _ := u.Render(nil)
	require.Len(t, a, 36)
	require.NotEqual(t, a, b)
}

func TestParseKeyTemplate_AC1_Errors(t *testing.T) {
	for _, c := range keyCases(t) {
		if c.ParseErrorPos == nil {
			continue
		}
		_, err := ParseKeyTemplate(c.Template)
		var te *TemplateError
		require.True(t, errors.As(err, &te), c.Name)
		require.Equal(t, *c.ParseErrorPos, te.Pos, c.Name)
		require.Contains(t, err.Error(), "position", c.Name)
	}
	for _, bad := range []string{"", "{}", "{$nope}"} {
		_, err := ParseKeyTemplate(bad)
		require.Error(t, err, bad)
	}
	kt, _ := ParseKeyTemplate("{missing}")
	require.Error(t, kt.Bind("t", []string{"id"}))
}

func FuzzParseKeyTemplate(f *testing.F) {
	for _, s := range []string{"order::{id}::{no|pad:8}", "{{x}}", "{a|lower|trim}", "{", "}", "{$uuid}"} {
		f.Add(s)
	}
	f.Fuzz(func(_ *testing.T, tpl string) {
		kt, err := ParseKeyTemplate(tpl)
		if err != nil {
			return
		}
		cols := kt.Columns()
		if kt.Bind("t", cols) != nil {
			return
		}
		row := make([]any, len(cols))
		for i := range row {
			row[i] = "v"
		}
		_, _ = kt.Render(row)
	})
}

func table(pk []string, uniques [][]string, cols ...string) *schema.Table {
	t := &schema.Table{Name: "customer", PrimaryKey: pk, UniqueConstraints: uniques}
	for i, c := range cols {
		t.Columns = append(t.Columns, schema.Column{Name: c, Ordinal: i + 1, UDTName: "int4"})
	}
	return t
}

func TestDefaultKeyTemplate_AC2_Table(t *testing.T) {
	tpl, warn := DefaultKeyTemplate(table([]string{"customer_id"}, nil, "customer_id"))
	require.Equal(t, "customer::{customer_id}", tpl)
	require.Empty(t, warn)
	ot := table([]string{"order_id", "line_no"}, nil, "order_id", "line_no")
	ot.Name = "order_item"
	tpl, _ = DefaultKeyTemplate(ot)
	require.Equal(t, "order_item::{order_id}::{line_no}", tpl)
	od := table([]string{"order_id"}, nil, "order_id")
	od.Name = "Order Details"
	tpl, _ = DefaultKeyTemplate(od)
	require.Equal(t, "order_details::{order_id}", tpl)
	tpl, warn = DefaultKeyTemplate(table(nil, nil, "viewed_at"))
	require.Equal(t, "{$uuid}", tpl)
	require.Equal(t, []string{CodeKeyNonIdempotent}, warn)
}

func TestKeyUniqueness_AC3_Table(t *testing.T) {
	tb := table([]string{"order_id", "line_no"}, [][]string{{"email"}}, "order_id", "line_no", "email", "last_name")
	check := func(tpl string) KeyAnalysis {
		kt, err := ParseKeyTemplate(tpl)
		require.NoError(t, err)
		return AnalyzeKey(kt, tb)
	}
	require.True(t, check("c::{email}").Unique, "unique constraint")
	require.Equal(t, []string{CodeKeyMayCollide}, check("c::{last_name}").Codes)
	require.Equal(t, []string{CodeKeyMayCollide}, check("c::{order_id}").Codes, "subset of a composite PK")
	require.True(t, check("c::{order_id}::{line_no}").Unique)
	require.Equal(t, []string{CodeKeyNonIdempotent}, check("{$uuid}").Codes)
	require.Equal(t, []string{"nope"}, check("{nope}").UnknownColumns)

	long := &schema.Table{Name: "t", PrimaryKey: []string{"a"}, Columns: []schema.Column{{Name: "a", UDTName: "varchar", MaxLength: ptr(100)}}}
	kt, _ := ParseKeyTemplate("prefix::{a}")
	a := AnalyzeKey(kt, long)
	require.Equal(t, 408, a.MaxBytes)
	require.Contains(t, a.Codes, CodeKeyMayBeTooLong)
	kt, _ = ParseKeyTemplate("{a|hash}::{$table}::{$uuid}")
	require.Equal(t, 40+2+1+2+36, AnalyzeKey(kt, long).MaxBytes)
	text := &schema.Table{Name: "t", Columns: []schema.Column{{Name: "a", UDTName: "text"}}}
	kt, _ = ParseKeyTemplate("{a}")
	require.Equal(t, 0, AnalyzeKey(kt, text).MaxBytes, "unbounded")
	kt, _ = ParseKeyTemplate("{a|pad:12}|{b}|{c}|{d}|{e}")
	mixed := &schema.Table{Name: "t", Columns: []schema.Column{{Name: "a", UDTName: "int2"}, {Name: "b", UDTName: "int8"}, {Name: "c", UDTName: "uuid"}, {Name: "d", UDTName: "timestamptz"}, {Name: "e", UDTName: "bool"}}}
	require.Equal(t, 12+20+36+32+5+4, AnalyzeKey(kt, mixed).MaxBytes)
}

func ptr[T any](v T) *T { return &v }

func TestRenderKey_AC4_TooLong(t *testing.T) {
	kt, _ := ParseKeyTemplate("k::{v}")
	require.NoError(t, kt.Bind("t", []string{"v"}))
	_, err := kt.Render([]any{strings.Repeat("x", 300)})
	var re *RowError
	require.True(t, errors.As(err, &re))
	require.Equal(t, CodeKeyTooLong, re.Code)
}

func TestRenderKey_AC4_NullComponent(t *testing.T) {
	kt, _ := ParseKeyTemplate("k::{v}")
	require.NoError(t, kt.Bind("t", []string{"v"}))
	_, err := kt.Render([]any{nil})
	var re *RowError
	require.True(t, errors.As(err, &re))
	require.Equal(t, CodeKeyNullComponent, re.Code)
	require.Contains(t, re.Error(), "v is null")
}

func BenchmarkRenderKey(b *testing.B) {
	kt, _ := ParseKeyTemplate("order::{customer_id}::{order_no}")
	_ = kt.Bind("orders", []string{"customer_id", "order_no"})
	row := []any{int64(42), int64(17)}
	buf := make([]byte, 0, 64)
	b.ReportAllocs()
	for b.Loop() {
		buf, _ = kt.AppendKey(buf[:0], row)
	}
}

// AC4: < 300 ns/op and ≤ 1 alloc for Render (the string itself).
func TestRenderKey_AC4_Budget(t *testing.T) {
	if race.Enabled || testing.CoverMode() != "" {
		t.Skip("timing budgets need uninstrumented code: run `make test-budgets`")
	}
	kt, _ := ParseKeyTemplate("order::{customer_id}::{order_no}")
	_ = kt.Bind("orders", []string{"customer_id", "order_no"})
	row := []any{int64(42), int64(17)}
	allocs := testing.AllocsPerRun(1000, func() { _, _ = kt.Render(row) })
	require.LessOrEqual(t, allocs, 1.0)
	res := testing.Benchmark(func(b *testing.B) {
		for b.Loop() {
			_, _ = kt.Render(row)
		}
	})
	require.Less(t, res.NsPerOp(), int64(300))
}
