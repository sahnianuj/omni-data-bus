package pos

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"

	"omnidatabus/internal/connectors"
	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/connectors/source/postgres"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/schema"
)

// Source loads the fixture into the dev stack's PostgreSQL (host:port, database shop) when
// missing, then opens it through the PostgreSQL connector and discovers the pos schema, as a
// project's source connection does. The caller closes the session.
func Source(ctx context.Context, host string, port int, schemas ...string) (*schema.Snapshot, source.Session, error) {
	if len(schemas) == 0 {
		schemas = []string{Schema}
	}
	conn, err := pgx.Connect(ctx, fmt.Sprintf("postgres://shop_owner:shop_owner@%s:%d/shop?sslmode=disable", host, port))
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = conn.Close(ctx) }()
	if err := Apply(ctx, conn); err != nil {
		return nil, nil, err
	}
	cfg, _ := json.Marshal(map[string]any{"host": host, "port": port, "database": "shop", "username": "shop_owner", "sslMode": "disable"})
	sess, err := postgres.New().Open(ctx, cfg, connectors.Credentials{Password: "shop_owner"})
	if err != nil {
		return nil, nil, err
	}
	snap, err := sess.Discover(ctx, source.DiscoverOptions{Schemas: schemas})
	if err != nil {
		_ = sess.Close()
		return nil, nil, err
	}
	return snap, sess, nil
}

// Tables are the snapshot's tables.
func Tables(snap *schema.Snapshot) []mapping.TableRef {
	var out []mapping.TableRef
	for _, sc := range snap.Schemas {
		for _, t := range sc.Tables {
			out = append(out, mapping.TableRef{Schema: sc.Name, Name: t.Name})
		}
	}
	return out
}

// CartID is the id of fixture cart n (1–200): md5('cart' || n) as a uuid.
func CartID(n int) string { return uid("cart", n) }
