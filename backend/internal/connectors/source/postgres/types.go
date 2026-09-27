package postgres

import "omnidatabus/internal/schema"

// PGType is a pg_type entry: its name (typname, e.g. "int4", "_int4", "order_status") and kind
// (typtype: b base, e enum, d domain, c composite, r range, m multirange, p pseudo).
type PGType struct {
	Name string
	Kind string
}

// scalarTypes maps base type names to jsonType (architecture §6.4).
var scalarTypes = map[string]string{
	"int2": schema.TypeInteger, "int4": schema.TypeInteger, "int8": schema.TypeInteger, "oid": schema.TypeInteger,
	"numeric": schema.TypeNumber, "float4": schema.TypeNumber, "float8": schema.TypeNumber, "money": schema.TypeNumber,
	"bool": schema.TypeBoolean,
	"text": schema.TypeString, "varchar": schema.TypeString, "bpchar": schema.TypeString, "char": schema.TypeString,
	"citext": schema.TypeString, "name": schema.TypeString, "uuid": schema.TypeString,
	"time": schema.TypeString, "timetz": schema.TypeString, "interval": schema.TypeString, "bytea": schema.TypeString,
	"inet": schema.TypeString, "cidr": schema.TypeString, "macaddr": schema.TypeString, "macaddr8": schema.TypeString,
	"date": schema.TypeDatetime, "timestamp": schema.TypeDatetime, "timestamptz": schema.TypeDatetime,
	"json": schema.TypeAny, "jsonb": schema.TypeAny,
}

// NormalizeType maps a column's type to its jsonType. t must already be resolved past domains
// (see resolveDomain); elem is the element type when t is an array. Types without a JSON
// counterpart (xml, tsvector, geometry, ranges, composites, …) become strings with a warning.
func NormalizeType(t PGType, elem *PGType) (jsonType, elementType, warning string) {
	if elem != nil {
		et, _, w := NormalizeType(*elem, nil)
		return schema.TypeArray, et, w
	}
	if t.Kind == "e" {
		return schema.TypeString, "", ""
	}
	if jt, ok := scalarTypes[t.Name]; ok && (t.Kind == "b" || t.Kind == "") {
		return jt, "", ""
	}
	return schema.TypeString, "", schema.WarnUnsupportedType
}

// resolveDomain returns the domain's base type (and base element type for a domain over an
// array), or t unchanged when it isn't a domain.
func resolveDomain(t PGType, base *PGType) PGType {
	if t.Kind == "d" && base != nil {
		return *base
	}
	return t
}

// typmodInfo derives character length and numeric precision/scale the way
// information_schema.columns does, from the type name and atttypmod.
func typmodInfo(typ string, typmod int) (maxLength, precision, scale *int) {
	p := func(v int) *int { return &v }
	switch typ {
	case "varchar", "bpchar":
		if typmod > 4 {
			maxLength = p(typmod - 4)
		}
	case "numeric":
		if typmod > 4 {
			precision, scale = p(((typmod-4)>>16)&0xffff), p((typmod-4)&0xffff)
		}
	case "int2":
		precision, scale = p(16), p(0)
	case "int4":
		precision, scale = p(32), p(0)
	case "int8":
		precision, scale = p(64), p(0)
	case "float4":
		precision = p(24)
	case "float8":
		precision = p(53)
	}
	return maxLength, precision, scale
}
