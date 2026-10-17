package postgres

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/schema"
)

// userSchemas is the namespace predicate every discovery query shares: system schemas are
// never discovered, and $1 (text[], empty = all) narrows to an include list. No user input
// is ever interpolated into these statements.
const userSchemas = `n.nspname NOT IN ('pg_catalog', 'information_schema')
  AND n.nspname NOT LIKE 'pg\_toast%' AND n.nspname NOT LIKE 'pg\_temp\_%'
  AND (COALESCE(cardinality($1::text[]), 0) = 0 OR n.nspname = ANY($1::text[]))`

const relkinds = `c.relkind IN ('r', 'p', 'v', 'm', 'f')`

var (
	countSQL = `SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE ` + relkinds + ` AND NOT c.relispartition AND ` + userSchemas

	serverSQL = `SELECT current_setting('server_version'), current_database()`

	relationsSQL = `
SELECT c.oid::int8, n.nspname, c.relname, c.relkind::text, c.reltuples::float8,
       c.relpages::int8, pg_relation_size(c.oid) / current_setting('block_size')::int8,
       COALESCE(pg_stat_get_live_tuples(c.oid), 0), pg_total_relation_size(c.oid), obj_description(c.oid, 'pg_class'),
       c.relispartition, COALESCE(inh.inhparent::int8, 0),
       has_schema_privilege(n.oid, 'USAGE') AND has_table_privilege(c.oid, 'SELECT')
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
LEFT JOIN pg_inherits inh ON c.relispartition AND inh.inhrelid = c.oid
WHERE ` + relkinds + ` AND ` + userSchemas

	// Domains resolve to their base type (ft); arrays expose their element type (et).
	columnsSQL = `
SELECT a.attrelid::int8, a.attnum::int4, a.attname, format_type(a.atttypid, a.atttypmod),
       t.typname, t.typtype::text, ft.typname, ft.typtype::text,
       COALESCE(et.typname, ''), COALESCE(et.typtype::text, ''),
       CASE WHEN ft.typtype = 'e' THEN ft.oid::int8 WHEN et.typtype = 'e' THEN et.oid::int8 ELSE 0 END,
       CASE WHEN t.typtype = 'd' AND a.atttypmod = -1 THEN t.typtypmod ELSE a.atttypmod END,
       a.attnotnull, pg_get_expr(ad.adbin, ad.adrelid), a.attidentity::text, a.attgenerated::text,
       col_description(a.attrelid, a.attnum)
FROM pg_attribute a
JOIN pg_class c ON c.oid = a.attrelid
JOIN pg_namespace n ON n.oid = c.relnamespace
JOIN pg_type t ON t.oid = a.atttypid
JOIN pg_type ft ON ft.oid = CASE WHEN t.typtype = 'd' THEN t.typbasetype ELSE t.oid END
LEFT JOIN pg_type et ON ft.typcategory = 'A' AND et.oid = ft.typelem
LEFT JOIN pg_attrdef ad ON ad.adrelid = a.attrelid AND ad.adnum = a.attnum
WHERE a.attnum > 0 AND NOT a.attisdropped AND ` + relkinds + ` AND NOT c.relispartition AND ` + userSchemas

	enumsSQL = `SELECT enumtypid::int8, array_agg(enumlabel::text ORDER BY enumsortorder) FROM pg_enum GROUP BY enumtypid`

	// Multi-column keys keep their declared order via unnest … WITH ORDINALITY.
	constraintsSQL = `
SELECT con.conrelid::int8, con.conname, con.contype::text,
       ARRAY(SELECT a.attname::text FROM unnest(con.conkey) WITH ORDINALITY k(attnum, ord)
             JOIN pg_attribute a ON a.attrelid = con.conrelid AND a.attnum = k.attnum ORDER BY k.ord),
       COALESCE(fn.nspname, ''), COALESCE(fc.relname, ''),
       ARRAY(SELECT a.attname::text FROM unnest(con.confkey) WITH ORDINALITY k(attnum, ord)
             JOIN pg_attribute a ON a.attrelid = con.confrelid AND a.attnum = k.attnum ORDER BY k.ord)
FROM pg_constraint con
JOIN pg_class c ON c.oid = con.conrelid
JOIN pg_namespace n ON n.oid = c.relnamespace
LEFT JOIN pg_class fc ON fc.oid = con.confrelid
LEFT JOIN pg_namespace fn ON fn.oid = fc.relnamespace
WHERE con.contype IN ('p', 'u', 'f') AND NOT c.relispartition AND ` + userSchemas

	// Only key columns (not INCLUDE columns); expression parts (attnum 0) are reported as "expr".
	// A partial unique index (with a WHERE) is not reported as unique: it allows duplicate keys
	// outside its predicate.
	indexesSQL = `
SELECT i.indrelid::int8, ic.relname, i.indisunique AND i.indpred IS NULL, i.indisprimary,
       ARRAY(SELECT CASE WHEN k.attnum = 0 THEN 'expr' ELSE a.attname::text END
             FROM unnest(i.indkey::int2[]) WITH ORDINALITY k(attnum, ord)
             LEFT JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = k.attnum
             WHERE k.ord <= i.indnkeyatts ORDER BY k.ord)
FROM pg_index i
JOIN pg_class ic ON ic.oid = i.indexrelid
JOIN pg_class c ON c.oid = i.indrelid
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE NOT c.relispartition AND ` + userSchemas
)

// Raw catalog rows, as read by the queries above. AssembleSnapshot turns them into a Snapshot.
type (
	rawCatalog struct {
		Database, ServerVersion string
		Relations               []rawRelation
		Columns                 []rawColumn
		Constraints             []rawConstraint
		Indexes                 []rawIndex
		Enums                   map[int64][]string
	}
	rawRelation struct {
		OID          int64
		Schema, Name string
		Kind         string // relkind
		RelTuples    float64
		RelPages     int64  // heap pages when reltuples was last computed
		Pages        int64  // heap pages now
		LiveTuples   int64  // the statistics collector's live-row count, kept current between ANALYZEs
		ExactRows    *int64 // count(*), for tables small enough to count during discovery
		Size         int64
		Comment      *string
		IsPartition  bool
		Parent       int64
		Readable     bool
	}
	rawColumn struct {
		RelOID    int64
		Num       int
		Name      string
		Display   string
		Declared  PGType // as declared (may be a domain)
		Effective PGType // domains resolved to their base
		Elem      PGType // array element; Name "" when not an array
		EnumOID   int64
		Typmod    int
		NotNull   bool
		Default   *string
		Identity  string
		Generated string
		Comment   *string
	}
	rawConstraint struct {
		RelOID     int64
		Name       string
		Type       string // p, u, f
		Columns    []string
		RefSchema  string
		RefTable   string
		RefColumns []string
	}
	rawIndex struct {
		RelOID          int64
		Name            string
		Unique, Primary bool
		Columns         []string
	}
)

var kindNames = map[string]string{
	"r": schema.KindTable, "p": schema.KindPartitionedTable, "v": schema.KindView,
	"m": schema.KindMaterializedView, "f": schema.KindForeignTable,
}

// excluded reports whether a table matches any exclude glob, tested against both its name and
// its qualified schema.table name.
func excluded(patterns []string, schemaName, table string) bool {
	for _, p := range patterns {
		if ok, _ := path.Match(p, table); ok {
			return true
		}
		if ok, _ := path.Match(p, schemaName+"."+table); ok {
			return true
		}
	}
	return false
}

// AssembleSnapshot builds the snapshot from raw catalog rows. It is pure: all catalog access
// happens in Discover, so the assembly rules are unit-tested with fixture rows.
func AssembleSnapshot(raw rawCatalog, opts source.DiscoverOptions) *schema.Snapshot {
	tables := map[int64]*schema.Table{}
	schemaOf := map[int64]string{}
	partitions := map[int64][]rawRelation{}
	var order []int64
	for _, r := range raw.Relations {
		if r.IsPartition {
			partitions[r.Parent] = append(partitions[r.Parent], r)
			continue
		}
		if excluded(opts.ExcludePatterns, r.Schema, r.Name) {
			continue
		}
		t := &schema.Table{
			Name: r.Name, Kind: kindNames[r.Kind], SizeBytes: r.Size, Readable: r.Readable,
			Columns: []schema.Column{}, PrimaryKey: []string{}, UniqueConstraints: [][]string{},
			ForeignKeys: []schema.ForeignKey{}, ReferencedBy: []schema.Reference{}, Indexes: []schema.Index{},
		}
		if r.Comment != nil {
			t.Comment = *r.Comment
		}
		if r.Kind != "v" && r.Kind != "f" && r.Kind != "p" {
			setEstimate(t, r)
		}
		tables[r.OID], schemaOf[r.OID] = t, r.Schema
		order = append(order, r.OID)
	}
	// Partitioned tables: their own reltuples is 0; the estimate is the partitions' sum.
	for parent, parts := range partitions {
		t := tables[parent]
		if t == nil {
			continue
		}
		var sum int64
		exact := true
		for _, p := range parts {
			t.Partitions = append(t.Partitions, p.Name)
			t.SizeBytes += p.Size
			var pt schema.Table
			setEstimate(&pt, p)
			if pt.RowEstimate == nil {
				t.StatsStale = true
			} else {
				sum += *pt.RowEstimate
				exact = exact && pt.RowCountExact
			}
		}
		if !t.StatsStale {
			t.RowEstimate, t.RowCountExact = &sum, exact
		}
	}

	for _, c := range raw.Columns {
		t := tables[c.RelOID]
		if t == nil {
			continue
		}
		t.Columns = append(t.Columns, buildColumn(c, raw.Enums))
	}

	type refKey struct{ schema, table string }
	byName := map[refKey]int64{}
	for oid, t := range tables {
		byName[refKey{schemaOf[oid], t.Name}] = oid
	}
	for _, con := range raw.Constraints {
		t := tables[con.RelOID]
		if t == nil {
			continue
		}
		switch con.Type {
		case "p":
			t.PrimaryKey = con.Columns
		case "u":
			t.UniqueConstraints = append(t.UniqueConstraints, con.Columns)
		case "f":
			t.ForeignKeys = append(t.ForeignKeys, schema.ForeignKey{
				Name: con.Name, Columns: con.Columns, RefSchema: con.RefSchema, RefTable: con.RefTable, RefColumns: con.RefColumns,
			})
			if parent := tables[byName[refKey{con.RefSchema, con.RefTable}]]; parent != nil {
				parent.ReferencedBy = append(parent.ReferencedBy, schema.Reference{
					Schema: schemaOf[con.RelOID], Table: t.Name, Name: con.Name, Columns: con.Columns, RefColumns: con.RefColumns,
				})
			}
		}
	}
	for _, ix := range raw.Indexes {
		if t := tables[ix.RelOID]; t != nil {
			t.Indexes = append(t.Indexes, schema.Index{Name: ix.Name, Columns: ix.Columns, Unique: ix.Unique, Primary: ix.Primary})
		}
	}

	snap := &schema.Snapshot{Database: raw.Database, ServerVersion: raw.ServerVersion, Schemas: []schema.Schema{}}
	schemaIdx := map[string]int{}
	for _, oid := range order {
		t := tables[oid]
		markKeys(t)
		name := schemaOf[oid]
		i, ok := schemaIdx[name]
		if !ok {
			i = len(snap.Schemas)
			schemaIdx[name] = i
			snap.Schemas = append(snap.Schemas, schema.Schema{Name: name})
		}
		snap.Schemas[i].Tables = append(snap.Schemas[i].Tables, *t)
	}
	snap.Sort()
	for i := range snap.Schemas {
		for j := range snap.Schemas[i].Tables {
			t := &snap.Schemas[i].Tables[j]
			sortRefs(t)
		}
	}
	return snap
}

// setEstimate sets a table's row count: exact when counted, otherwise estimated the way the
// planner does, scaling reltuples' density to the heap's current size. reltuples alone goes
// stale between ANALYZEs, and small tables can stay at 0 indefinitely because autovacuum only
// re-analyzes after ~50 changed rows.
func setEstimate(t *schema.Table, r rawRelation) {
	var n int64
	switch {
	case r.ExactRows != nil:
		n, t.RowCountExact = *r.ExactRows, true
	case r.RelTuples > 0 && r.RelPages > 0 && r.Pages > 0:
		n = int64(math.Round(r.RelTuples / float64(r.RelPages) * float64(r.Pages)))
	case r.RelTuples <= 0 && r.Pages > 0: // statistics predate the data
		if r.LiveTuples <= 0 {
			t.StatsStale = true
			return
		}
		n = r.LiveTuples
	case r.RelTuples < 0: // never vacuumed or analyzed (PostgreSQL 14+)
		t.StatsStale = true
		return
	default:
		n = int64(r.RelTuples)
	}
	t.RowEstimate = &n
}

const (
	exactCountMaxPages    = 128  // 1 MiB of heap at the default 8 KiB block size
	exactCountBudgetPages = 8192 // 64 MiB scanned per discovery, at most
	exactCountChunk       = 200  // tables per UNION ALL statement
)

// countSmallTables counts the rows of readable tables up to exactCountMaxPages, smallest
// first within the budget, so small tables show real numbers instead of estimates. Counting
// is best effort: on the first error (a lock timeout, say) the rest keep their estimates.
func countSmallTables(ctx context.Context, q querier, rels []rawRelation, opts source.DiscoverOptions) {
	var picks []int
	for i, r := range rels {
		if (r.Kind == "r" || r.Kind == "m") && r.Readable && r.Pages <= exactCountMaxPages && !excluded(opts.ExcludePatterns, r.Schema, r.Name) {
			picks = append(picks, i)
		}
	}
	slices.SortStableFunc(picks, func(a, b int) int { return cmp.Compare(rels[a].Pages, rels[b].Pages) })
	budget := int64(exactCountBudgetPages)
	var queue []int
	for _, i := range picks {
		if rels[i].Pages == 0 { // an empty heap holds no rows
			zero := int64(0)
			rels[i].ExactRows = &zero
			continue
		}
		if budget -= rels[i].Pages; budget < 0 {
			break
		}
		queue = append(queue, i)
	}
	for len(queue) > 0 {
		chunk := queue[:min(exactCountChunk, len(queue))]
		queue = queue[len(chunk):]
		parts := make([]string, len(chunk))
		for j, i := range chunk {
			parts[j] = "SELECT " + strconv.Itoa(i) + "::int4, count(*) FROM " + pgx.Identifier{rels[i].Schema, rels[i].Name}.Sanitize()
		}
		rows, err := q.Query(ctx, strings.Join(parts, " UNION ALL "))
		if err != nil {
			return
		}
		for rows.Next() {
			var i int
			var n int64
			if rows.Scan(&i, &n) != nil {
				break
			}
			rels[i].ExactRows = &n
		}
		rows.Close()
		if rows.Err() != nil {
			return
		}
	}
}

func buildColumn(c rawColumn, enums map[int64][]string) schema.Column {
	var elem *PGType
	if c.Elem.Name != "" {
		elem = &c.Elem
	}
	jt, et, warn := NormalizeType(c.Effective, elem)
	maxLen, prec, scale := typmodInfo(c.Effective.Name, c.Typmod)
	if elem != nil {
		maxLen, prec, scale = typmodInfo(elem.Name, c.Typmod)
	}
	col := schema.Column{
		Name: c.Name, Ordinal: c.Num, DataType: c.Display, UDTName: c.Effective.Name,
		Nullable: !c.NotNull, Default: c.Default, IsIdentity: c.Identity != "", IsGenerated: c.Generated != "",
		MaxLength: maxLen, NumericPrecision: prec, NumericScale: scale,
		IsArray: elem != nil, ElementType: et, EnumValues: enums[c.EnumOID], JSONType: jt, Warning: warn,
	}
	if c.Comment != nil {
		col.Comment = *c.Comment
	}
	return col
}

// markKeys flags primary-key and single-column-unique columns.
func markKeys(t *schema.Table) {
	unique := map[string]bool{}
	if len(t.PrimaryKey) == 1 {
		unique[t.PrimaryKey[0]] = true
	}
	for _, u := range t.UniqueConstraints {
		if len(u) == 1 {
			unique[u[0]] = true
		}
	}
	for _, ix := range t.Indexes {
		if ix.Unique && len(ix.Columns) == 1 {
			unique[ix.Columns[0]] = true
		}
	}
	pk := map[string]bool{}
	for _, c := range t.PrimaryKey {
		pk[c] = true
	}
	for i := range t.Columns {
		t.Columns[i].IsPrimaryKey = pk[t.Columns[i].Name]
		t.Columns[i].IsUnique = unique[t.Columns[i].Name]
	}
}

// sortRefs orders constraint-derived lists so snapshots compare stably.
func sortRefs(t *schema.Table) {
	sortBy(t.ForeignKeys, func(f schema.ForeignKey) string { return f.Name })
	sortBy(t.ReferencedBy, func(r schema.Reference) string { return r.Schema + "." + r.Table + "." + r.Name })
	sortBy(t.Indexes, func(i schema.Index) string { return i.Name })
	sortBy(t.UniqueConstraints, func(u []string) string { return strings.Join(u, ",") })
}

func sortBy[T any](s []T, key func(T) string) {
	slices.SortStableFunc(s, func(a, b T) int { return strings.Compare(key(a), key(b)) })
}

// EstimateRelations counts the relations discovery would return, to choose sync or async.
func (s *pgSession) EstimateRelations(ctx context.Context, opts source.DiscoverOptions) (int, error) {
	var n int
	err := s.conn.QueryRow(ctx, countSQL, schemaParam(opts)).Scan(&n)
	return n, err
}

func schemaParam(opts source.DiscoverOptions) []string {
	if opts.Schemas == nil {
		return []string{} // a nil slice would be sent as NULL
	}
	return opts.Schemas
}

// Discover reads the catalog in one read-only REPEATABLE READ transaction (a consistent view)
// with ~6 set-based queries, whatever the number of tables.
func (s *pgSession) Discover(ctx context.Context, opts source.DiscoverOptions) (*schema.Snapshot, error) {
	tx, err := s.conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, "SET LOCAL statement_timeout = '60s'"); err != nil {
		return nil, err
	}
	raw, err := readCatalog(ctx, tx, schemaParam(opts))
	if err != nil {
		return nil, err
	}
	// Counting waits on table locks; a table being altered keeps its estimate instead.
	if _, err := tx.Exec(ctx, "SET LOCAL lock_timeout = '2s'"); err == nil {
		countSmallTables(ctx, tx, raw.Relations, opts)
	}
	return AssembleSnapshot(raw, opts), nil
}

type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func readCatalog(ctx context.Context, q querier, schemas []string) (rawCatalog, error) {
	raw := rawCatalog{Enums: map[int64][]string{}}
	if err := q.QueryRow(ctx, serverSQL).Scan(&raw.ServerVersion, &raw.Database); err != nil {
		return raw, fmt.Errorf("server info: %w", err)
	}
	if i := strings.IndexByte(raw.ServerVersion, ' '); i > 0 {
		raw.ServerVersion = raw.ServerVersion[:i]
	}
	var err error
	if raw.Relations, err = collect(ctx, q, relationsSQL, schemas, func(r pgx.Row, v *rawRelation) error {
		return r.Scan(&v.OID, &v.Schema, &v.Name, &v.Kind, &v.RelTuples, &v.RelPages, &v.Pages, &v.LiveTuples, &v.Size, &v.Comment, &v.IsPartition, &v.Parent, &v.Readable)
	}); err != nil {
		return raw, fmt.Errorf("relations: %w", err)
	}
	if raw.Columns, err = collect(ctx, q, columnsSQL, schemas, func(r pgx.Row, v *rawColumn) error {
		var identity, generated string
		err := r.Scan(&v.RelOID, &v.Num, &v.Name, &v.Display, &v.Declared.Name, &v.Declared.Kind, &v.Effective.Name, &v.Effective.Kind,
			&v.Elem.Name, &v.Elem.Kind, &v.EnumOID, &v.Typmod, &v.NotNull, &v.Default, &identity, &generated, &v.Comment)
		v.Identity, v.Generated = strings.TrimSpace(identity), strings.TrimSpace(generated)
		return err
	}); err != nil {
		return raw, fmt.Errorf("columns: %w", err)
	}
	if raw.Constraints, err = collect(ctx, q, constraintsSQL, schemas, func(r pgx.Row, v *rawConstraint) error {
		return r.Scan(&v.RelOID, &v.Name, &v.Type, &v.Columns, &v.RefSchema, &v.RefTable, &v.RefColumns)
	}); err != nil {
		return raw, fmt.Errorf("constraints: %w", err)
	}
	if raw.Indexes, err = collect(ctx, q, indexesSQL, schemas, func(r pgx.Row, v *rawIndex) error {
		return r.Scan(&v.RelOID, &v.Name, &v.Unique, &v.Primary, &v.Columns)
	}); err != nil {
		return raw, fmt.Errorf("indexes: %w", err)
	}
	rows, err := q.Query(ctx, enumsSQL)
	if err != nil {
		return raw, fmt.Errorf("enums: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var oid int64
		var labels []string
		if err := rows.Scan(&oid, &labels); err != nil {
			return raw, err
		}
		raw.Enums[oid] = labels
	}
	return raw, rows.Err()
}

func collect[T any](ctx context.Context, q querier, sql string, schemas []string, scan func(pgx.Row, *T) error) ([]T, error) {
	rows, err := q.Query(ctx, sql, schemas)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []T
	for rows.Next() {
		var v T
		if err := scan(rows, &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
