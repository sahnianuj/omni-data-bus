// Package schema holds the discovered source-schema model (architecture §6.1). It is shared by
// source connectors (which produce it), the discovery service (which stores and diffs it) and
// the mapper (which reads it), so it depends on nothing else in the module.
package schema

import "sort"

// Relation kinds.
const (
	KindTable            = "table"
	KindPartitionedTable = "partitioned_table"
	KindView             = "view"
	KindMaterializedView = "materialized_view"
	KindForeignTable     = "foreign_table"
)

// Normalized logical types (architecture §6.4).
const (
	TypeInteger  = "integer"
	TypeNumber   = "number"
	TypeBoolean  = "boolean"
	TypeString   = "string"
	TypeDatetime = "datetime"
	TypeAny      = "any"
	TypeArray    = "array"
)

// WarnUnsupportedType marks a column whose values are sent as their text form.
const WarnUnsupportedType = "UNSUPPORTED_TYPE_AS_TEXT"

// Snapshot is one discovery run's result.
type Snapshot struct {
	Database      string   `json:"database"`
	ServerVersion string   `json:"serverVersion"`
	Schemas       []Schema `json:"schemas"`
}

// Schema is a namespace with its relations, sorted by name.
type Schema struct {
	Name   string  `json:"name"`
	Tables []Table `json:"tables"`
}

// Table is a table, view, materialized view, partitioned table or foreign table.
type Table struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	// RowEstimate is nil when the planner has no statistics (never analyzed): StatsStale is then true.
	RowEstimate *int64 `json:"rowEstimate"`
	StatsStale  bool   `json:"statsStale,omitempty"`
	SizeBytes   int64  `json:"sizeBytes"`
	Comment     string `json:"comment,omitempty"`
	// Readable is false when the connection's user can't SELECT from it.
	Readable          bool         `json:"readable"`
	Partitions        []string     `json:"partitions,omitempty"`
	Columns           []Column     `json:"columns"`
	PrimaryKey        []string     `json:"primaryKey"`
	UniqueConstraints [][]string   `json:"uniqueConstraints"`
	ForeignKeys       []ForeignKey `json:"foreignKeys"`
	ReferencedBy      []Reference  `json:"referencedBy"`
	Indexes           []Index      `json:"indexes"`
}

// Column is one table column.
type Column struct {
	Name             string   `json:"name"`
	Ordinal          int      `json:"ordinal"`
	DataType         string   `json:"dataType"` // display type, e.g. "numeric(12,2)"
	UDTName          string   `json:"udtName"`  // e.g. "numeric", "_int4"
	Nullable         bool     `json:"nullable"`
	Default          *string  `json:"default"`
	IsPrimaryKey     bool     `json:"isPrimaryKey"`
	IsUnique         bool     `json:"isUnique"`
	IsIdentity       bool     `json:"isIdentity,omitempty"`
	IsGenerated      bool     `json:"isGenerated,omitempty"`
	MaxLength        *int     `json:"maxLength"`
	NumericPrecision *int     `json:"numericPrecision"`
	NumericScale     *int     `json:"numericScale"`
	IsArray          bool     `json:"isArray"`
	ElementType      string   `json:"elementType,omitempty"` // jsonType of array elements
	EnumValues       []string `json:"enumValues"`
	Comment          string   `json:"comment,omitempty"`
	JSONType         string   `json:"jsonType"`
	Warning          string   `json:"warning,omitempty"`
}

// ForeignKey references another table; Columns and RefColumns are in constraint order.
type ForeignKey struct {
	Name       string   `json:"name"`
	Columns    []string `json:"columns"`
	RefSchema  string   `json:"refSchema"`
	RefTable   string   `json:"refTable"`
	RefColumns []string `json:"refColumns"`
}

// Reference is the reverse of a foreign key: a table whose FK points here.
type Reference struct {
	Schema     string   `json:"schema"`
	Table      string   `json:"table"`
	Name       string   `json:"name"`
	Columns    []string `json:"columns"`
	RefColumns []string `json:"refColumns"`
}

// Index is a table index; expression parts are reported as "expr".
type Index struct {
	Name    string   `json:"name"`
	Columns []string `json:"columns"`
	Unique  bool     `json:"unique"`
	Primary bool     `json:"primary,omitempty"`
}

// Table returns the named table, or nil.
func (s *Snapshot) Table(schema, table string) *Table {
	for i := range s.Schemas {
		if s.Schemas[i].Name != schema {
			continue
		}
		for j := range s.Schemas[i].Tables {
			if s.Schemas[i].Tables[j].Name == table {
				return &s.Schemas[i].Tables[j]
			}
		}
	}
	return nil
}

// Column returns the named column, or nil.
func (t *Table) Column(name string) *Column {
	for i := range t.Columns {
		if t.Columns[i].Name == name {
			return &t.Columns[i]
		}
	}
	return nil
}

// TableCount is the number of relations across all schemas.
func (s *Snapshot) TableCount() int {
	n := 0
	for _, sc := range s.Schemas {
		n += len(sc.Tables)
	}
	return n
}

// Summary is the light tree shape returned by GET …/schema?summary=true.
type Summary struct {
	Database      string             `json:"database"`
	ServerVersion string             `json:"serverVersion"`
	Schemas       []NamespaceSummary `json:"schemas"`
}

// NamespaceSummary lists a schema's tables without column detail.
type NamespaceSummary struct {
	Name   string         `json:"name"`
	Tables []TableSummary `json:"tables"`
}

// TableSummary is a table without columns, keys or indexes.
type TableSummary struct {
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	RowEstimate *int64 `json:"rowEstimate"`
	StatsStale  bool   `json:"statsStale,omitempty"`
	SizeBytes   int64  `json:"sizeBytes"`
	ColumnCount int    `json:"columnCount"`
	HasPK       bool   `json:"hasPrimaryKey"`
	Readable    bool   `json:"readable"`
}

// Summarize drops column, key and index detail.
func (s *Snapshot) Summarize() Summary {
	out := Summary{Database: s.Database, ServerVersion: s.ServerVersion, Schemas: make([]NamespaceSummary, 0, len(s.Schemas))}
	for _, sc := range s.Schemas {
		ss := NamespaceSummary{Name: sc.Name, Tables: make([]TableSummary, 0, len(sc.Tables))}
		for _, t := range sc.Tables {
			ss.Tables = append(ss.Tables, TableSummary{
				Name: t.Name, Kind: t.Kind, RowEstimate: t.RowEstimate, StatsStale: t.StatsStale, SizeBytes: t.SizeBytes,
				ColumnCount: len(t.Columns), HasPK: len(t.PrimaryKey) > 0, Readable: t.Readable,
			})
		}
		out.Schemas = append(out.Schemas, ss)
	}
	return out
}

// Sort orders schemas and tables by name, and columns by ordinal, so snapshots compare stably.
func (s *Snapshot) Sort() {
	sort.Slice(s.Schemas, func(i, j int) bool { return s.Schemas[i].Name < s.Schemas[j].Name })
	for i := range s.Schemas {
		ts := s.Schemas[i].Tables
		sort.Slice(ts, func(a, b int) bool { return ts[a].Name < ts[b].Name })
		for j := range ts {
			cs := ts[j].Columns
			sort.Slice(cs, func(a, b int) bool { return cs[a].Ordinal < cs[b].Ordinal })
		}
	}
}
