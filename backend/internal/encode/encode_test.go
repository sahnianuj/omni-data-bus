package encode

import (
	"encoding/json"
	"math"
	"math/big"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"omnidatabus/internal/schema"
)

func c(udt, jsonType string) schema.Column { return schema.Column{UDTName: udt, JSONType: jsonType} }

func precise(p int) schema.Column {
	return schema.Column{UDTName: "numeric", JSONType: schema.TypeNumber, NumericPrecision: &p}
}

func num(s string) pgtype.Numeric {
	var n pgtype.Numeric
	if err := n.Scan(s); err != nil {
		panic(err)
	}
	return n
}

// One case per row of architecture §6.4.
func TestValue_TypeMatrix(t *testing.T) {
	ts := time.Date(2024, 6, 1, 12, 30, 45, 123456000, time.FixedZone("IST", 5*3600+1800))
	mac, _ := net.ParseMAC("08:00:2b:01:02:03")
	cases := []struct {
		name string
		v    any
		col  schema.Column
		want any
	}{
		{"null", nil, c("int4", schema.TypeInteger), nil},
		{"smallint", int16(7), c("int2", schema.TypeInteger), int64(7)},
		{"integer", int32(-7), c("int4", schema.TypeInteger), int64(-7)},
		{"bigint", int64(1) << 50, c("int8", schema.TypeInteger), int64(1) << 50},
		{"bigint beyond 2^53 → exact string", int64(1) << 60, c("int8", schema.TypeInteger), "1152921504606846976"},
		{"numeric(12,2)", num("1234.50"), precise(12), 1234.5},
		{"numeric(38,10) is always a string", num("1.0000000001"), precise(38), "1.0000000001"},
		{"unconstrained numeric, small value", num("1234.50"), c("numeric", schema.TypeNumber), 1234.5},
		{"unconstrained numeric, wide value", num("12345678901234567890.0000000001"), c("numeric", schema.TypeNumber), "12345678901234567890.0000000001"},
		{"numeric NaN", pgtype.Numeric{NaN: true, Valid: true}, c("numeric", schema.TypeNumber), nil},
		{"real", float32(1.5), c("float4", schema.TypeNumber), 1.5},
		{"real keeps its decimal value", float32(0.1), c("float4", schema.TypeNumber), 0.1},
		{"real NaN", float32(math.NaN()), c("float4", schema.TypeNumber), nil},
		{"double NaN", math.NaN(), c("float8", schema.TypeNumber), nil},
		{"double Inf", math.Inf(1), c("float8", schema.TypeNumber), nil},
		{"boolean", true, c("bool", schema.TypeBoolean), true},
		{"text", "hi", c("text", schema.TypeString), "hi"},
		{"char(4) right-trimmed", "AB  ", c("bpchar", schema.TypeString), "AB"},
		{"uuid lowercase", [16]byte{0xAB, 0xCD, 0xEF, 0x01, 0x23, 0x45, 0x67, 0x89, 0xAB, 0xCD, 0xEF, 0x01, 0x23, 0x45, 0x67, 0x89}, c("uuid", schema.TypeString), "abcdef01-2345-6789-abcd-ef0123456789"},
		{"date", time.Date(1990, 2, 3, 0, 0, 0, 0, time.UTC), c("date", schema.TypeDatetime), "1990-02-03"},
		{"timestamp without zone", time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC), c("timestamp", schema.TypeDatetime), "2024-06-01T12:00:00"},
		{"timestamptz → UTC Z", ts, c("timestamptz", schema.TypeDatetime), "2024-06-01T07:00:45.123456Z"},
		{"time", pgtype.Time{Microseconds: (9*3600 + 5*60 + 7) * 1_000_000, Valid: true}, c("time", schema.TypeString), "09:05:07"},
		{"time with fraction", pgtype.Time{Microseconds: 1_500_000, Valid: true}, c("time", schema.TypeString), "00:00:01.5"},
		{"interval", pgtype.Interval{Months: 18, Days: 3, Microseconds: (4*3600+5*60)*1_000_000 + 6_500_000, Valid: true}, c("interval", schema.TypeString), "P1Y6M3DT4H5M6.5S"},
		{"zero interval", pgtype.Interval{Valid: true}, c("interval", schema.TypeString), "PT0S"},
		{"jsonb embedded", map[string]any{"a": 1.0}, c("jsonb", schema.TypeAny), map[string]any{"a": 1.0}},
		{"jsonb text keeps big numbers exact", `{"n":12345678901234567890}`, c("jsonb", schema.TypeAny), map[string]any{"n": json.Number("12345678901234567890")}},
		{"invalid json text passes through", `{oops`, c("json", schema.TypeAny), `{oops`},
		{"bytea base64", []byte{0x89, 0x50, 0x4e, 0x47}, c("bytea", schema.TypeString), "iVBORw=="},
		{"enum label (read as text)", "paid", c("order_status", schema.TypeString), "paid"},
		{"inet host", netip.MustParsePrefix("10.0.1.2/32"), c("inet", schema.TypeString), "10.0.1.2"},
		{"cidr", netip.MustParsePrefix("10.0.0.0/16"), c("cidr", schema.TypeString), "10.0.0.0/16"},
		{"macaddr", mac, c("macaddr", schema.TypeString), "08:00:2b:01:02:03"},
		{"money (read as numeric)", num("5.00"), c("money", schema.TypeNumber), 5.0},
		{"unsupported as text", "'a':1 'b':2", c("tsvector", schema.TypeString), "'a':1 'b':2"},
	}
	for _, tc := range cases {
		require.Equal(t, tc.want, Value(tc.v, tc.col), tc.name)
	}
}

func TestValue_Arrays(t *testing.T) {
	col := schema.Column{UDTName: "_int4", JSONType: schema.TypeArray, ElementType: schema.TypeInteger, IsArray: true}
	require.Equal(t, []any{int64(1), nil, int64(3)}, Value([]any{int32(1), nil, int32(3)}, col))
	tags := schema.Column{UDTName: "_bpchar", JSONType: schema.TypeArray, ElementType: schema.TypeString, IsArray: true}
	require.Equal(t, []any{"a"}, Value([]any{"a  "}, tags), "element encoding applies")
	require.Equal(t, "{1,2}", Value("{1,2}", col), "unexpected shapes fall back to text")
}

func TestValue_Fallbacks(t *testing.T) {
	require.Equal(t, int64(5), Value(5, c("int4", schema.TypeInteger)))
	require.Equal(t, int64(9), Value(uint32(9), c("oid", schema.TypeInteger)))
	require.Equal(t, "1.2.3.4", Value(netip.MustParseAddr("1.2.3.4"), c("inet", schema.TypeString)))
	require.Equal(t, "[1 2]", Value([]int{1, 2}, c("x", schema.TypeString)))
	require.Nil(t, Value(pgtype.Numeric{}, c("numeric", schema.TypeNumber)), "invalid numeric")
	require.Equal(t, "10", Value(stringer{}, c("x", schema.TypeString)))
	require.Nil(t, Value(pgtype.Numeric{Int: big.NewInt(1), Valid: true, InfinityModifier: pgtype.Infinity}, c("numeric", schema.TypeNumber)))
	require.Equal(t, "-PT0S", "-"+Interval(pgtype.Interval{Valid: true}))
	require.Equal(t, "PT2H", Interval(pgtype.Interval{Microseconds: 2 * 3600 * 1_000_000}))
}

type stringer struct{}

func (stringer) String() string { return "10" }

func TestValueWith_Options(t *testing.T) {
	int8col := c("int8", schema.TypeInteger)
	require.Equal(t, "42", ValueWith(int64(42), int8col, Options{BigintAsString: "always"}))
	require.Equal(t, int64(1)<<60, ValueWith(int64(1)<<60, int8col, Options{BigintAsString: "never"}))
	require.Equal(t, int64(1)<<60, ValueWith(int64(1)<<60, c("int4", schema.TypeInteger), Options{}), "only int8 is affected")
	p := 38
	wide := schema.Column{UDTName: "numeric", JSONType: schema.TypeNumber, NumericPrecision: &p}
	require.Equal(t, json.Number("1.0000000001"), ValueWith(num("1.0000000001"), wide, Options{NumericMode: "number"}))
	narrow := precise(12)
	require.Equal(t, "12.50", ValueWith(num("12.50"), narrow, Options{NumericMode: "string"}))
	ts := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	require.Equal(t, "2024-06-01T12:00:00Z", ValueWith(ts, c("timestamp", schema.TypeDatetime), Options{AssumeTimezone: "UTC"}))
	require.Equal(t, []any{"9007199254740993"}, ValueWith([]any{int64(9007199254740993)}, schema.Column{UDTName: "_int8", JSONType: schema.TypeArray, ElementType: schema.TypeInteger, IsArray: true}, Options{}))
}
