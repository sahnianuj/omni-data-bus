//go:build integration

package headless

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
	"omnidatabus/internal/fixtures/pos"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/automap"
	"omnidatabus/internal/docschema"
	"omnidatabus/internal/export"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/mappings"
	"omnidatabus/internal/store"
)

// TestParity_AC3_GoldenMappings (OC-056): five golden mappings — flat rows, a composite key, a
// many-to-one plus a one-to-many embed, a two-level path plus a many-to-many, and every
// PostgreSQL type — run by a server job and by `odb run` against the same source: every
// document is byte-identical. The orders mappings also carry template computed fields at the
// root and in array items (OC-136 AC6).
func TestParity_AC3_GoldenMappings(t *testing.T) {
	ctx := context.Background()
	serverDir := t.TempDir()
	loadPOS(t)
	tl := newBareTool(t, serverDir, "shop", "sales", "pos")
	create := func(schemaName, table, folder string, embeds ...automap.EmbedInput) string {
		v, err := tl.maps.Create(ctx, itWS, "u1", tl.project.ID, mappings.CreateInput{
			SourceSchema: schemaName, SourceTable: table, Location: &mapping.Location{Kind: store.KindDirectory, Folder: folder, CreateIfMissing: true},
		})
		require.NoError(t, err, table)
		for _, e := range embeds {
			_, err = tl.maps.AddEmbed(ctx, itWS, "u1", tl.project.ID, v.ID, 0, e)
			require.NoError(t, err, table)
		}
		if len(embeds) > 0 {
			addComputed(t, tl, v.ID)
		}
		return tl.ready(t, v.ID).ID
	}
	tref := func(s, n string) mapping.TableRef { return mapping.TableRef{Schema: s, Name: n} }
	folders := []string{"customers", "order_details", "orders", "sales_orders", "products", "carts"}
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
	create("shop", "product", folders[4])                  // json, xml, money, intervals, arrays, times…
	proposeCart(t, tl, create("pos", "carts", folders[5])) // design-first, payment variants (OC-165, OC-171)

	o := tl.runServer(t)
	require.Nil(t, o.Fatal)
	require.True(t, o.Finished)

	runnerDir := t.TempDir()
	b := tl.export(t, runnerDir, export.Request{CredentialMode: export.ModeSealed, Passphrase: passphrase})
	require.Len(t, b.Mappings, 6)
	var stdout, stderr bytes.Buffer
	rep, code, err := Run(ctx, runConfig(b, t.TempDir(), &stdout, &stderr))
	require.NoError(t, err, stderr.String())
	require.Equal(t, ExitOK, code, stderr.String())
	require.Len(t, rep.Mappings, 6)

	server, runner := documents(t, serverDir), documents(t, runnerDir)
	perFolder := map[string]int{}
	for path := range server {
		perFolder[path[:bytes.IndexByte([]byte(path), '/')]]++
	}
	for _, f := range folders {
		require.Positive(t, perFolder[f], "documents in %s", f)
	}
	require.Equal(t, len(server), len(runner), "same number of documents")
	computed := 0
	for path, doc := range server {
		require.Equal(t, string(doc), string(runner[path]), path)
		if bytes.Contains(doc, []byte(`"ref":"REF-`)) {
			computed++
		}
	}
	require.Positive(t, computed, "documents with computed fields")
}

// addComputed adds template computed fields (OC-136): "ref" at the root from its first scalar
// field, and "position" in the items of its first array of objects, from the item's first
// scalar, its index and the root's ref (a computed field reading another).
func addComputed(t *testing.T, tl *tool, id string) {
	t.Helper()
	ctx := context.Background()
	v, err := tl.maps.Get(ctx, itWS, tl.project.ID, id)
	require.NoError(t, err)
	spec := v.Spec
	firstScalar := func(fs []docschema.DocField) string {
		for _, f := range fs {
			if f.Type != docschema.Object && f.Type != docschema.Array {
				return f.Name
			}
		}
		return ""
	}
	root := firstScalar(spec.DocSchema.Root.Children)
	require.NotEmpty(t, root)
	spec.DocSchema.Root.Children = append(spec.DocSchema.Root.Children, docschema.DocField{ID: "fld_cmp_ref", Name: "ref", Type: docschema.String})
	spec.FieldMappings = append(spec.FieldMappings, mapping.FieldMapping{
		ID: "map_cmp_ref", TargetFieldID: "fld_cmp_ref", State: mapping.StateActive, Origin: mapping.OriginManual,
		Sources: []mapping.SourceRef{}, Transforms: []mapping.Step{}, Computed: &mapping.Computed{Lang: mapping.ComputedTemplate, Expr: "REF-${doc." + root + "}"},
	})
	for i := range spec.DocSchema.Root.Children {
		f := &spec.DocSchema.Root.Children[i]
		if f.Type != docschema.Array || f.Items == nil || f.Items.Type != docschema.Object {
			continue
		}
		item := firstScalar(f.Items.Children)
		f.Items.Children = append(f.Items.Children, docschema.DocField{ID: "fld_cmp_pos", Name: "position", Type: docschema.String})
		spec.FieldMappings = append(spec.FieldMappings, mapping.FieldMapping{
			ID: "map_cmp_pos", TargetFieldID: "fld_cmp_pos", State: mapping.StateActive, Origin: mapping.OriginManual,
			Sources: []mapping.SourceRef{}, Transforms: []mapping.Step{}, Computed: &mapping.Computed{Lang: mapping.ComputedTemplate, Expr: "${parent.ref}#${index}:${self." + item + "}"},
		})
		break
	}
	_, err = tl.maps.Update(ctx, itWS, "u1", tl.project.ID, id, v.Version, mappings.UpdateInput{Spec: spec})
	require.NoError(t, err)
}

// loadPOS loads the design-first fixture into the dev stack's PostgreSQL (OC-162).
func loadPOS(t *testing.T) {
	t.Helper()
	cfg, pass := pgEnv(t)
	var c struct {
		Host string `json:"host"`
		Port int    `json:"port"`
	}
	require.NoError(t, json.Unmarshal(cfg, &c))
	conn, err := pgx.Connect(context.Background(), fmt.Sprintf("postgres://shop_owner:%s@%s:%d/shop?sslmode=disable", pass, c.Host, c.Port))
	require.NoError(t, err)
	defer func() { _ = conn.Close(context.Background()) }()
	require.NoError(t, pos.Apply(context.Background(), conn))
}

// proposeCart replaces a mapping of pos.carts by the proposal for the cart and payment designs
// (OC-163, OC-171), with the fields nothing fills marked optional, as a review would.
func proposeCart(t *testing.T, tl *tool, id string) {
	t.Helper()
	ctx := context.Background()
	files := []docschema.File{{Name: "cart.schema.json", Content: string(pos.Design("cart.schema.json"))}}
	for _, n := range pos.PaymentDesignNames() {
		files = append(files, docschema.File{Name: n, Content: string(pos.PaymentDesigns()[n])})
	}
	ds, _, err := docschema.ImportFiles(files)
	require.NoError(t, err)
	v, err := tl.maps.Get(ctx, itWS, tl.project.ID, id)
	require.NoError(t, err)
	spec := v.Spec
	spec.Mode, spec.DocSchema, spec.Relations, spec.FieldMappings = mapping.ModeComposed, ds, nil, nil
	p, err := tl.maps.Propose(ctx, itWS, tl.project.ID, id, mappings.ProposeInput{SpecOverride: &spec})
	require.NoError(t, err)
	for _, it := range p.Items {
		if it.Status == automap.StatusUnresolved {
			if f := p.Spec.DocSchema.Find(it.FieldID); f != nil {
				f.Required = false
			}
		}
	}
	_, err = tl.maps.Update(ctx, itWS, "u1", tl.project.ID, id, v.Version, mappings.UpdateInput{Spec: p.Spec})
	require.NoError(t, err)
	tl.ready(t, id)
}
