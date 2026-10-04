//go:build integration

package headless

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/automap"
	"omnidatabus/internal/export"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/mappings"
	"omnidatabus/internal/store"
)

// TestParity_AC3_GoldenMappings (OC-056): five golden mappings — flat rows, a composite key, a
// many-to-one plus a one-to-many embed, a two-level path plus a many-to-many, and every
// PostgreSQL type — run by a server job and by `odb run` against the same source: every
// document is byte-identical.
func TestParity_AC3_GoldenMappings(t *testing.T) {
	ctx := context.Background()
	serverDir := t.TempDir()
	tl := newBareTool(t, serverDir, "shop", "sales")
	create := func(schemaName, table, folder string, embeds ...automap.EmbedInput) string {
		v, err := tl.maps.Create(ctx, itWS, "u1", tl.project.ID, mappings.CreateInput{
			SourceSchema: schemaName, SourceTable: table, Location: &mapping.Location{Kind: store.KindDirectory, Folder: folder, CreateIfMissing: true},
		})
		require.NoError(t, err, table)
		for _, e := range embeds {
			_, err = tl.maps.AddEmbed(ctx, itWS, "u1", tl.project.ID, v.ID, 0, e)
			require.NoError(t, err, table)
		}
		return tl.ready(t, v.ID).ID
	}
	tref := func(s, n string) mapping.TableRef { return mapping.TableRef{Schema: s, Name: n} }
	folders := []string{"customers", "order_details", "orders", "sales_orders", "products"}
	create("shop", "customer", folders[0])
	create("shop", "Order Details", folders[1]) // key order_details::{order_id}::{line_no}
	create("shop", "orders", folders[2],
		automap.EmbedInput{Kind: mapping.EmbedManyToOne, Table: tref("shop", "customer"), Join: []mapping.JoinPair{{Parent: "customer_id", Child: "customer_id"}}, TargetName: "customer", Generate: true},
		automap.EmbedInput{Kind: mapping.EmbedOneToMany, Table: tref("shop", "Order Details"), Join: []mapping.JoinPair{{Parent: "order_id", Child: "order_id"}}, TargetName: "items", Generate: true},
	)
	create("sales", "orders", folders[3],
		automap.EmbedInput{
			Kind: mapping.EmbedOneToMany, Table: tref("sales", "order_items"), Join: []mapping.JoinPair{{Parent: "order_id", Child: "order_id"}}, TargetName: "items", Generate: true,
			Then: []automap.EmbedInput{{Kind: mapping.EmbedManyToOne, Table: tref("sales", "products"), Join: []mapping.JoinPair{{Parent: "product_id", Child: "product_id"}}, TargetName: "product", Generate: true}},
		},
		automap.EmbedInput{
			Kind: mapping.RelationManyToMany, Table: tref("sales", "tags"), TargetName: "tags", Generate: true,
			Via: &mapping.Via{Table: tref("sales", "order_tags"), ParentJoin: []mapping.ViaJoin{{Parent: "order_id", Via: "order_id"}}, ChildJoin: []mapping.ViaJoin{{Via: "tag_id", Child: "tag_id"}}},
		},
	)
	create("shop", "product", folders[4]) // json, xml, money, intervals, arrays, times…

	o := tl.runServer(t)
	require.Nil(t, o.Fatal)
	require.True(t, o.Finished)

	runnerDir := t.TempDir()
	b := tl.export(t, runnerDir, export.Request{CredentialMode: export.ModeSealed, Passphrase: passphrase})
	require.Len(t, b.Mappings, 5)
	var stdout, stderr bytes.Buffer
	rep, code, err := Run(ctx, runConfig(b, t.TempDir(), &stdout, &stderr))
	require.NoError(t, err, stderr.String())
	require.Equal(t, ExitOK, code, stderr.String())
	require.Len(t, rep.Mappings, 5)

	server, runner := documents(t, serverDir), documents(t, runnerDir)
	perFolder := map[string]int{}
	for path := range server {
		perFolder[path[:bytes.IndexByte([]byte(path), '/')]]++
	}
	for _, f := range folders {
		require.Positive(t, perFolder[f], "documents in %s", f)
	}
	require.Equal(t, len(server), len(runner), "same number of documents")
	for path, doc := range server {
		require.Equal(t, string(doc), string(runner[path]), path)
	}
}
