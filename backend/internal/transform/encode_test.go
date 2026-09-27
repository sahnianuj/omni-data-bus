package transform

import (
	"encoding/json"
	"flag"
	"math"
	"net"
	"net/netip"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"omnidatabus/internal/docschema"
	"omnidatabus/internal/encode"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/schema"
)

var update = flag.Bool("update", false, "rewrite golden files")

// allTypes is one column per §6.4 type, including edge cases.
func allTypes() (*schema.Table, []any) {
	p38, p12 := 38, 12
	mac, _ := net.ParseMAC("08:00:2b:01:02:03")
	cols := []struct {
		c schema.Column
		v any
	}{
		{schema.Column{Name: "id", UDTName: "int4", JSONType: schema.TypeInteger}, int32(1)},
		{schema.Column{Name: "small", UDTName: "int2", JSONType: schema.TypeInteger}, int16(-7)},
		{schema.Column{Name: "big_max", UDTName: "int8", JSONType: schema.TypeInteger}, int64(math.MaxInt64)},
		{schema.Column{Name: "big_safe", UDTName: "int8", JSONType: schema.TypeInteger}, int64(1 << 40)},
		{schema.Column{Name: "price", UDTName: "numeric", JSONType: schema.TypeNumber, NumericPrecision: &p12}, mustNumeric("1234.50")},
		{schema.Column{Name: "precise", UDTName: "numeric", JSONType: schema.TypeNumber, NumericPrecision: &p38}, mustNumeric("12345678901234567890.0000000001")},
		{schema.Column{Name: "nan", UDTName: "float8", JSONType: schema.TypeNumber}, math.NaN()},
		{schema.Column{Name: "real", UDTName: "float4", JSONType: schema.TypeNumber}, float32(0.1)},
		{schema.Column{Name: "flag", UDTName: "bool", JSONType: schema.TypeBoolean}, true},
		{schema.Column{Name: "code", UDTName: "bpchar", JSONType: schema.TypeString}, "AB  "},
		{schema.Column{Name: "ref", UDTName: "uuid", JSONType: schema.TypeString}, [16]byte{0x0f, 0x8f, 0xad, 0x5b, 0xd9, 0xcb, 0x46, 0x9f, 0xa1, 0x65, 0x70, 0x86, 0x77, 0x28, 0x95, 0x0e}},
		{schema.Column{Name: "born", UDTName: "date", JSONType: schema.TypeDatetime}, time.Date(1990, 2, 3, 0, 0, 0, 0, time.UTC)},
		{schema.Column{Name: "local_ts", UDTName: "timestamp", JSONType: schema.TypeDatetime}, time.Date(2024, 6, 1, 12, 0, 0, 500000000, time.UTC)},
		{schema.Column{Name: "at", UDTName: "timestamptz", JSONType: schema.TypeDatetime}, time.Date(2024, 1, 2, 3, 4, 5, 120000000, time.FixedZone("IST", 19800))},
		{schema.Column{Name: "opens", UDTName: "time", JSONType: schema.TypeString}, pgtype.Time{Microseconds: 9 * 3600 * 1_000_000, Valid: true}},
		{schema.Column{Name: "warranty", UDTName: "interval", JSONType: schema.TypeString}, pgtype.Interval{Months: 14, Days: 3, Microseconds: 3_600_000_000, Valid: true}},
		{schema.Column{Name: "doc", UDTName: "jsonb", JSONType: schema.TypeAny}, `{"big":12345678901234567890,"list":[1,"a",null],"nested":{"b":true}}`},
		{schema.Column{Name: "empty_blob", UDTName: "bytea", JSONType: schema.TypeString}, []byte{}},
		{schema.Column{Name: "blob", UDTName: "bytea", JSONType: schema.TypeString}, []byte{0x89, 0x50, 0x4e, 0x47}},
		{schema.Column{Name: "status", UDTName: "order_status", JSONType: schema.TypeString}, "paid"},
		{schema.Column{Name: "sizes", UDTName: "_int4", JSONType: schema.TypeArray, ElementType: schema.TypeInteger, IsArray: true}, []any{int32(1), nil, int32(3)}},
		{schema.Column{Name: "ip", UDTName: "inet", JSONType: schema.TypeString}, netip.MustParsePrefix("10.0.0.5/32")},
		{schema.Column{Name: "net", UDTName: "cidr", JSONType: schema.TypeString}, netip.MustParsePrefix("10.0.0.0/16")},
		{schema.Column{Name: "mac", UDTName: "macaddr", JSONType: schema.TypeString}, mac},
		{schema.Column{Name: "money", UDTName: "money", JSONType: schema.TypeNumber}, mustNumeric("5.00")},
		{schema.Column{Name: "vector", UDTName: "tsvector", JSONType: schema.TypeString}, "'a':1"},
		{schema.Column{Name: "nothing", UDTName: "text", JSONType: schema.TypeString}, nil},
	}
	t := &schema.Table{Name: "all_types", PrimaryKey: []string{"id"}}
	var row []any
	for i := range cols {
		c := cols[i].c
		c.Ordinal = i + 1
		t.Columns = append(t.Columns, c)
		row = append(row, cols[i].v)
	}
	return t, row
}

// identitySpec maps every column 1:1 into a generated design.
func identitySpec(t *schema.Table) *mapping.Spec {
	s := &mapping.Spec{DocSchema: docschema.FromTable(t, docschema.GenerateOptions{Naming: docschema.NamingPreserve}), Key: mapping.KeySpec{Template: "all::{id}"}}
	for i, f := range s.DocSchema.Root.Children {
		s.FieldMappings = append(s.FieldMappings, mapping.FieldMapping{ID: f.ID, TargetFieldID: f.ID, Sources: []mapping.SourceRef{{Column: t.Columns[i].Name}}, State: mapping.StateActive, Origin: mapping.OriginAuto})
	}
	s.Normalize()
	return s
}

func encodeDoc(t *testing.T, opts encode.Options) []byte {
	t.Helper()
	tb, row := allTypes()
	p, issues := Compile(identitySpec(tb), tb, Options{Encode: opts})
	require.Empty(t, issues)
	_, doc, _, rerr := p.Apply(row, nil)
	require.Nil(t, rerr)
	require.True(t, json.Valid(doc), string(doc))
	return doc
}

func TestEncoders_AC1_Golden(t *testing.T) {
	doc := encodeDoc(t, encode.Options{})
	var pretty map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(doc, &pretty))
	out, _ := json.MarshalIndent(pretty, "", "  ")
	golden := "testdata/encoders.golden.json"
	if *update {
		require.NoError(t, os.MkdirAll("testdata", 0o755))
		require.NoError(t, os.WriteFile(golden, append(out, '\n'), 0o644)) //nolint:gosec // a checked-in fixture
	}
	want, err := os.ReadFile(golden)
	require.NoError(t, err, "run with -update to create the golden file")
	require.JSONEq(t, string(want), string(doc))
}

func field(t *testing.T, doc []byte, name string) string {
	t.Helper()
	var m map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(doc, &m))
	return string(m[name])
}

func TestEncoders_AC1_BigintAsStringModes(t *testing.T) {
	require.Equal(t, `"9223372036854775807"`, field(t, encodeDoc(t, encode.Options{}), "big_max"), "auto: beyond 2^53 → string")
	require.Equal(t, `1099511627776`, field(t, encodeDoc(t, encode.Options{}), "big_safe"))
	require.Equal(t, `"1099511627776"`, field(t, encodeDoc(t, encode.Options{BigintAsString: "always"}), "big_safe"))
	require.Equal(t, `9223372036854775807`, field(t, encodeDoc(t, encode.Options{BigintAsString: "never"}), "big_max"))
}

func TestEncoders_AC1_NumericModes(t *testing.T) {
	require.Equal(t, `"12345678901234567890.0000000001"`, field(t, encodeDoc(t, encode.Options{}), "precise"))
	require.Equal(t, `12345678901234567890.0000000001`, field(t, encodeDoc(t, encode.Options{NumericMode: "number"}), "precise"))
	require.Equal(t, `"1234.50"`, field(t, encodeDoc(t, encode.Options{NumericMode: "string"}), "price"))
}

func TestEncoders_AC1_JSONBPreservesPrecision(t *testing.T) {
	doc := field(t, encodeDoc(t, encode.Options{}), "doc")
	require.Contains(t, doc, `"big":12345678901234567890`)
}
