package postgres

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"

	"omnidatabus/internal/connectors/source"
)

// pgSession is an open source connection. cc (a copy of its config) opens the extra
// connections that parallel readers need (OC-024).
type pgSession struct {
	conn *pgx.Conn
	cc   *pgx.ConnConfig
}

func connectPgx(ctx context.Context, cc *pgx.ConnConfig) (session, error) {
	conn, err := pgx.ConnectConfig(ctx, cc)
	if err != nil {
		return nil, err
	}
	return &pgSession{conn: conn, cc: cc.Copy()}, nil
}

func (s *pgSession) Ping(ctx context.Context) (source.ServerInfo, error) {
	var version, db, user string
	err := s.conn.QueryRow(ctx, `SELECT current_setting('server_version'), current_database(), current_user`).Scan(&version, &db, &user)
	if err != nil {
		return source.ServerInfo{}, err
	}
	if i := strings.IndexByte(version, ' '); i > 0 { // "16.3 (Debian 16.3-1.pgdg120+1)"
		version = version[:i]
	}
	return source.ServerInfo{Product: "PostgreSQL", Version: version, Database: db, User: user}, nil
}

// unreadableSQL lists user tables the current role can't read (no SELECT, or no schema USAGE).
const unreadableSQL = `
SELECT n.nspname || '.' || c.relname
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE c.relkind IN ('r', 'p', 'v', 'm', 'f')
  AND n.nspname NOT IN ('pg_catalog', 'information_schema')
  AND n.nspname NOT LIKE 'pg\_toast%' AND n.nspname NOT LIKE 'pg\_temp\_%'
  AND (NOT has_schema_privilege(n.oid, 'USAGE') OR NOT has_table_privilege(c.oid, 'SELECT'))
ORDER BY 1
LIMIT 1000`

const roleSQL = `
SELECT r.rolsuper,
       EXISTS (
         SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
         WHERE c.relkind IN ('r', 'p')
           AND n.nspname NOT IN ('pg_catalog', 'information_schema')
           AND has_schema_privilege(n.oid, 'USAGE')
           AND has_table_privilege(c.oid, 'INSERT, UPDATE, DELETE')
       )
FROM pg_roles r WHERE r.rolname = current_user`

func (s *pgSession) CheckPrivileges(ctx context.Context) (source.PrivilegeReport, error) {
	var rep source.PrivilegeReport
	rows, err := s.conn.Query(ctx, unreadableSQL)
	if err != nil {
		return rep, err
	}
	tables, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return rep, err
	}
	rep.UnreadableTables = tables
	if err := s.conn.QueryRow(ctx, roleSQL).Scan(&rep.Superuser, &rep.CanWrite); err != nil {
		return rep, err
	}
	return rep, nil
}

func (s *pgSession) Close() error { return s.conn.Close(context.Background()) }
