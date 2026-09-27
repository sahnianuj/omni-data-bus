package postgres

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/schema"
)

// fakeRows serves fixed rows; Scan assigns each value to the matching destination pointer.
type fakeRows struct {
	rows [][]any
	i    int
	err  error
}

func (r *fakeRows) Close()                                       {}
func (r *fakeRows) Err() error                                   { return r.err }
func (r *fakeRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *fakeRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *fakeRows) Values() ([]any, error)                       { return r.rows[r.i-1], nil }
func (r *fakeRows) RawValues() [][]byte                          { return nil }
func (r *fakeRows) Conn() *pgx.Conn                              { return nil }
func (r *fakeRows) TypeMap() *pgtype.Map                         { return nil }

func (r *fakeRows) Next() bool {
	if r.i >= len(r.rows) {
		return false
	}
	r.i++
	return true
}

func (r *fakeRows) Scan(dest ...any) error {
	row := r.rows[r.i-1]
	if len(dest) != len(row) {
		return errors.New("fakeRows: column count mismatch")
	}
	for i, d := range dest {
		target := reflect.ValueOf(d).Elem()
		if row[i] == nil {
			target.Set(reflect.Zero(target.Type()))
			continue
		}
		v := reflect.ValueOf(row[i])
		if target.Kind() == reflect.Pointer { // nullable column, e.g. *string
			p := reflect.New(target.Type().Elem())
			p.Elem().Set(v.Convert(target.Type().Elem()))
			target.Set(p)
			continue
		}
		target.Set(v.Convert(target.Type())) // like pgx, e.g. int4 → int
	}
	return nil
}

type fakeRow struct{ rows *fakeRows }

func (r fakeRow) Scan(dest ...any) error {
	if !r.rows.Next() {
		return pgx.ErrNoRows
	}
	return r.rows.Scan(dest...)
}

// fakeQuerier answers each catalog statement with canned rows and records the arguments.
type fakeQuerier struct {
	results map[string][][]any
	fail    string // statement that returns an error
	args    [][]any
}

func (q *fakeQuerier) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	q.args = append(q.args, args)
	if sql == q.fail {
		return nil, errors.New("query failed")
	}
	return &fakeRows{rows: q.results[sql]}, nil
}

func (q *fakeQuerier) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	rows, _ := q.Query(ctx, sql, args...)
	if rows == nil {
		return fakeRow{rows: &fakeRows{}}
	}
	return fakeRow{rows: rows.(*fakeRows)}
}

func catalogQuerier() *fakeQuerier {
	return &fakeQuerier{results: map[string][][]any{
		serverSQL: {{"16.3 (Debian 16.3-1.pgdg120+1)", "shop"}},
		relationsSQL: {
			{int64(1), "shop", "customer", "r", float64(2000), int64(8192), "People", false, int64(0), true},
			{int64(2), "shop", "orders", "r", float64(-1), int64(8192), nil, false, int64(0), true},
		},
		columnsSQL: {
			{int64(1), int32(1), "customer_id", "integer", "int4", "b", "int4", "b", "", "", int64(0), int32(-1), true, nil, "", "", nil},
			{int64(2), int32(1), "order_id", "bigint", "int8", "b", "int8", "b", "", "", int64(0), int32(-1), true, "nextval('seq')", "a", "", "The key"},
			{int64(2), int32(2), "status", "shop.order_status", "order_status", "e", "order_status", "e", "", "", int64(77), int32(-1), true, nil, "", "", nil},
			{int64(2), int32(3), "customer_id", "integer", "int4", "b", "int4", "b", "", "", int64(0), int32(-1), false, nil, "", "s", nil},
		},
		constraintsSQL: {
			{int64(1), "customer_pkey", "p", []string{"customer_id"}, "", "", []string{}},
			{int64(2), "orders_customer_fk", "f", []string{"customer_id"}, "shop", "customer", []string{"customer_id"}},
		},
		indexesSQL: {{int64(1), "customer_pkey", true, true, []string{"customer_id"}}},
		enumsSQL:   {{int64(77), []string{"pending", "paid"}}},
	}}
}

func TestReadCatalog_AC1_ScansEveryQuery(t *testing.T) {
	q := catalogQuerier()
	raw, err := readCatalog(context.Background(), q, []string{"shop"})
	require.NoError(t, err)
	require.Equal(t, "16.3", raw.ServerVersion, "distribution suffix trimmed")
	require.Equal(t, "shop", raw.Database)
	require.Len(t, raw.Relations, 2)
	require.Len(t, raw.Columns, 4)
	require.Equal(t, "a", raw.Columns[1].Identity)
	require.Equal(t, "s", raw.Columns[3].Generated)
	for _, args := range q.args[1:] {
		if len(args) > 0 {
			require.Equal(t, []any{[]string{"shop"}}, args, "the schema filter is always a bound parameter")
		}
	}

	snap := AssembleSnapshot(raw, source.DiscoverOptions{})
	orders := snap.Table("shop", "orders")
	require.True(t, orders.StatsStale)
	require.Equal(t, []string{"pending", "paid"}, orders.Column("status").EnumValues)
	require.Equal(t, schema.TypeString, orders.Column("status").JSONType)
	require.True(t, orders.Column("order_id").IsIdentity)
	require.Equal(t, "The key", orders.Column("order_id").Comment)
	require.True(t, orders.Column("customer_id").IsGenerated)
	require.Equal(t, "People", snap.Table("shop", "customer").Comment)
	require.Len(t, snap.Table("shop", "customer").ReferencedBy, 1)
}

func TestReadCatalog_ErrorsNameTheFailingQuery(t *testing.T) {
	for sql, name := range map[string]string{relationsSQL: "relations", columnsSQL: "columns", constraintsSQL: "constraints", indexesSQL: "indexes", enumsSQL: "enums"} {
		q := catalogQuerier()
		q.fail = sql
		_, err := readCatalog(context.Background(), q, nil)
		require.ErrorContains(t, err, name)
	}
	q := catalogQuerier()
	q.results[serverSQL] = nil
	_, err := readCatalog(context.Background(), q, nil)
	require.ErrorContains(t, err, "server info")

	// A scan failure surfaces too.
	q = catalogQuerier()
	q.results[indexesSQL] = [][]any{{int64(1)}}
	_, err = readCatalog(context.Background(), q, nil)
	require.ErrorContains(t, err, "indexes")
	q = catalogQuerier()
	q.results[enumsSQL] = [][]any{{int64(1)}}
	_, err = readCatalog(context.Background(), q, nil)
	require.Error(t, err)
}
