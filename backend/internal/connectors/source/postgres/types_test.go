package postgres

import (
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/schema"
)

func baseType(name string) PGType { return PGType{Name: name, Kind: "b"} }

// One case per row of architecture §6.4.
func TestNormalizeType_AC2_Matrix(t *testing.T) {
	cases := []struct {
		pg   string
		want string
	}{
		{"int2", schema.TypeInteger},
		{"int4", schema.TypeInteger},
		{"int8", schema.TypeInteger},
		{"numeric", schema.TypeNumber},
		{"float4", schema.TypeNumber},
		{"float8", schema.TypeNumber},
		{"bool", schema.TypeBoolean},
		{"text", schema.TypeString},
		{"varchar", schema.TypeString},
		{"bpchar", schema.TypeString},
		{"citext", schema.TypeString},
		{"name", schema.TypeString},
		{"uuid", schema.TypeString},
		{"date", schema.TypeDatetime},
		{"timestamp", schema.TypeDatetime},
		{"timestamptz", schema.TypeDatetime},
		{"time", schema.TypeString},
		{"timetz", schema.TypeString},
		{"interval", schema.TypeString},
		{"json", schema.TypeAny},
		{"jsonb", schema.TypeAny},
		{"bytea", schema.TypeString},
		{"inet", schema.TypeString},
		{"cidr", schema.TypeString},
		{"macaddr", schema.TypeString},
		{"money", schema.TypeNumber},
	}
	for _, c := range cases {
		jt, et, warn := NormalizeType(baseType(c.pg), nil)
		require.Equal(t, c.want, jt, c.pg)
		require.Empty(t, et, c.pg)
		require.Empty(t, warn, c.pg)
	}
	// citext is an extension type: still typtype 'b'.
	jt, _, _ := NormalizeType(PGType{Name: "citext", Kind: "b"}, nil)
	require.Equal(t, schema.TypeString, jt)

	// Arrays: JSON array of the element encoding.
	jt, et, warn := NormalizeType(baseType("_int4"), &PGType{Name: "int4", Kind: "b"})
	require.Equal(t, schema.TypeArray, jt)
	require.Equal(t, schema.TypeInteger, et)
	require.Empty(t, warn)

	// Enums: their label.
	jt, _, warn = NormalizeType(PGType{Name: "order_status", Kind: "e"}, nil)
	require.Equal(t, schema.TypeString, jt)
	require.Empty(t, warn)
}

func TestNormalizeType_AC2_DomainResolvesToBase(t *testing.T) {
	numericBase := baseType("numeric")
	resolved := resolveDomain(PGType{Name: "positive_amount", Kind: "d"}, &numericBase)
	jt, _, warn := NormalizeType(resolved, nil)
	require.Equal(t, schema.TypeNumber, jt)
	require.Empty(t, warn)
	require.Equal(t, baseType("int4"), resolveDomain(baseType("int4"), nil), "non-domains are unchanged")

	// numeric(38,10): precision and scale decoded from atttypmod.
	typmod := (38<<16 | 10) + 4
	_, prec, scale := typmodInfo("numeric", typmod)
	require.Equal(t, 38, *prec)
	require.Equal(t, 10, *scale)
	maxLen, _, _ := typmodInfo("varchar", 80+4)
	require.Equal(t, 80, *maxLen)
	maxLen, prec, _ = typmodInfo("text", -1)
	require.Nil(t, maxLen)
	require.Nil(t, prec)
}

func TestNormalizeType_AC2_ArrayOfEnum(t *testing.T) {
	jt, et, warn := NormalizeType(baseType("_order_status"), &PGType{Name: "order_status", Kind: "e"})
	require.Equal(t, schema.TypeArray, jt)
	require.Equal(t, schema.TypeString, et)
	require.Empty(t, warn)
}

func TestNormalizeType_AC2_UnsupportedWarns(t *testing.T) {
	for _, typ := range []PGType{baseType("tsvector"), baseType("xml"), baseType("geometry"), {Name: "int4range", Kind: "r"}, {Name: "address_t", Kind: "c"}} {
		jt, _, warn := NormalizeType(typ, nil)
		require.Equal(t, schema.TypeString, jt, typ.Name)
		require.Equal(t, schema.WarnUnsupportedType, warn, typ.Name)
	}
	_, et, warn := NormalizeType(baseType("_tsvector"), &PGType{Name: "tsvector", Kind: "b"})
	require.Equal(t, schema.TypeString, et)
	require.Equal(t, schema.WarnUnsupportedType, warn, "arrays inherit the element warning")
}
