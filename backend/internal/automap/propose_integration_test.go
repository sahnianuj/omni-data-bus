//go:build integration

package automap

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors"
	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/connectors/source/postgres"
	"omnidatabus/internal/docschema"
	"omnidatabus/internal/fixtures/pos"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/relations"
	"omnidatabus/internal/schema"
)

// posSource loads the point-of-sale fixture into the dev PostgreSQL (once) and discovers it
// through the PostgreSQL connector, as a project's source connection does (OC-162).
func posSource(t *testing.T) (*schema.Snapshot, source.Session) {
	t.Helper()
	host := os.Getenv("OC_IT_PG_HOST")
	if host == "" {
		t.Skip("set OC_IT_PG_HOST/PORT (the dev stack)")
	}
	port, _ := strconv.Atoi(os.Getenv("OC_IT_PG_PORT"))
	if port == 0 {
		port = 5432
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, fmt.Sprintf("postgres://shop_owner:shop_owner@%s:%d/shop?sslmode=disable", host, port))
	require.NoError(t, err)
	defer func() { _ = conn.Close(ctx) }()
	require.NoError(t, pos.Apply(ctx, conn))

	cfg, _ := json.Marshal(map[string]any{"host": host, "port": port, "database": "shop", "username": "shop_owner", "sslMode": "disable"})
	sess, err := postgres.New().Open(ctx, cfg, connectors.Credentials{Password: "shop_owner"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = sess.Close() })
	snap, err := sess.Discover(ctx, source.DiscoverOptions{Schemas: []string{pos.Schema}})
	require.NoError(t, err)
	return snap, sess
}

func posTables(snap *schema.Snapshot) []mapping.TableRef {
	var out []mapping.TableRef
	for _, sc := range snap.Schemas {
		for _, t := range sc.Tables {
			out = append(out, mapping.TableRef{Schema: sc.Name, Name: t.Name})
		}
	}
	return out
}

// OC-162 AC1: the fixture is a connected source with its keys, unique keys and enums, and the
// relationship graph from carts has its References and Referenced by.
func TestPOSFixture_AC1_Discovery(t *testing.T) {
	snap, _ := posSource(t)
	require.Len(t, posTables(snap), 16)
	carts := snap.Table(pos.Schema, "carts")
	require.NotNil(t, carts)
	require.Equal(t, []string{"id"}, carts.PrimaryKey)
	require.Contains(t, carts.Column("status").EnumValues, "payment_pending")
	require.Equal(t, []string{"cart_id"}, snap.Table(pos.Schema, "saved_orders").PrimaryKey)

	by := map[string]map[string]string{"references": {}, "referencedBy": {}}
	for _, h := range relations.Build(snap, nil).Hops(mapping.TableRef{Schema: pos.Schema, Name: "carts"}) {
		by[h.Direction][h.To.Name] = h.Cardinality
	}
	for _, n := range []string{"cart_items", "customers", "collected_payments", "applied_rewards", "sccs"} {
		require.Equal(t, mapping.CardinalityOneToMany, by["referencedBy"][n], n)
	}
	for _, n := range []string{"saved_orders", "associated_scans"} {
		require.Equal(t, mapping.CardinalityOneToOne, by["referencedBy"][n], "%s: its key is the foreign key", n)
	}
	require.Equal(t, mapping.CardinalityManyToOne, by["references"]["partners"])
	require.Equal(t, mapping.CardinalityManyToOne, by["references"]["stores"])
}

// importCart is the cart design imported as a composed mapping with nothing bound.
func importCart(t *testing.T) *mapping.Spec {
	t.Helper()
	ds, _, err := docschema.Import(docschema.KindJSONSchema, string(pos.Design("cart.schema.json")), "cart.schema.json")
	require.NoError(t, err)
	spec := &mapping.Spec{SpecVersion: 2, Mode: mapping.ModeComposed, DocSchema: ds}
	spec.Normalize()
	return spec
}

// baseline20 is what 2.0 gets from the cart design: rank the driving table, accept the high
// binding suggestions top-down (OC-060's "Accept high & auto-map"), then auto-map all.
func baseline20(t *testing.T, snap *schema.Snapshot) *mapping.Spec {
	t.Helper()
	spec := importCart(t)
	table := func(r mapping.TableRef) *schema.Table { return snap.Table(r.Schema, r.Name) }
	in := SuggestInput{Spec: spec, Root: true, Tables: posTables(snap), Table: table, Links: linksOf(snap)}
	root := SuggestBindings(in).Root
	require.NotEmpty(t, root)
	spec.Root = &mapping.Root{Table: root[0].Table}
	in.Driving, in.Root = root[0].Table, false
	accept(spec, SuggestBindings(in).Suggestions, ConfidenceHigh)
	out, err := RunAll(spec, table(root[0].Table), func(s, n string) *schema.Table { return snap.Table(s, n) })
	require.NoError(t, err)
	spec.FieldMappings = out.FieldMappings
	return spec
}

// OC-162 AC2: the score report on 2.0's behaviour (the baseline in the story).
func TestProposeScore_AC2_Report(t *testing.T) {
	snap, _ := posSource(t)
	r := pos.Score(baseline20(t, snap), pos.LoadExpected())
	t.Logf("2.0 baseline on the pos fixture:\n%s", r)
	require.Len(t, r.Totals, len(pos.Categories))
	require.Equal(t, 1, r.Totals[pos.CatDriving].Total)
}

// proposeCart runs the proposal on the cart design against the pos source.
func proposeCart(t *testing.T, snap *schema.Snapshot) Proposal {
	t.Helper()
	return Propose(ProposeInput{
		Spec: importCart(t), Tables: posTables(snap), Links: linksOf(snap),
		Table: func(r mapping.TableRef) *schema.Table { return snap.Table(r.Schema, r.Name) },
	})
}

// OC-163 AC2: one call proposes the whole mapping, and it beats the 2.0 baseline everywhere.
func TestProposeScore_OC163_AC2_EndToEnd(t *testing.T) {
	snap, _ := posSource(t)
	prop := proposeCart(t, snap)
	got := pos.Score(&prop.Spec, pos.LoadExpected())
	base := pos.Score(baseline20(t, snap), pos.LoadExpected())
	t.Logf("2.5 proposal on the pos fixture:\n%s", got)
	t.Logf("driving: %+v\ncoverage: %+v", prop.Driving, prop.Coverage)
	for _, c := range pos.Categories {
		require.GreaterOrEqual(t, got.Totals[c].Ratio(), base.Totals[c].Ratio(), c)
	}
}

var updatePOS = os.Getenv("OC_UPDATE_POS_SNAPSHOT") == "1"

// The unit tests' snapshot.json is what discovery really returns for the pos schema
// (OC_UPDATE_POS_SNAPSHOT=1 rewrites it).
func TestPOSFixture_SnapshotUpToDate(t *testing.T) {
	snap, _ := posSource(t)
	got, err := json.MarshalIndent(pos.Normalized(snap), "", " ")
	require.NoError(t, err)
	if updatePOS {
		require.NoError(t, os.WriteFile("../fixtures/pos/snapshot.json", append(got, '\n'), 0o600))
	}
	want, err := json.MarshalIndent(pos.Normalized(pos.Snapshot()), "", " ")
	require.NoError(t, err)
	require.JSONEq(t, string(want), string(got), "rerun with OC_UPDATE_POS_SNAPSHOT=1")
}

// OC-171 AC1: discovery recognises "exactly one is set" in both CHECK forms.
func TestDiscover_OC171_AC1_ExclusiveArc(t *testing.T) {
	snap, _ := posSource(t)
	require.Equal(t, [][]string{{"cash_tender_id", "credit_card_id", "scan_and_pay_qr_code_id", "svc_id", "scc_id"}}, snap.Table(pos.Schema, "collected_payments").ExclusiveArcs, "the CASE form")
	require.Equal(t, [][]string{{"svc_id", "scan_and_pay_qr_code_id"}}, snap.Table(pos.Schema, "associated_scans").ExclusiveArcs, "num_nonnulls")
	require.Empty(t, snap.Table(pos.Schema, "svcs").ExclusiveArcs, "other CHECKs are listed, not arcs")
	require.NotEmpty(t, snap.Table(pos.Schema, "svcs").CheckConstraints)
}

// OC-173 AC2: the proposal on the pos fixture (the cart with its payment designs) passes G10.
// A proposal with one wrong high-confidence field fails it, even with the totals passing.
// OC_PROPOSE_SCORE_OUT writes the result bench-gate judges (bench/results/2.5.0).
func TestProposeGate_AC2(t *testing.T) {
	snap, _ := posSource(t)
	files := []docschema.File{{Name: "cart.schema.json", Content: string(pos.Design("cart.schema.json"))}}
	for _, n := range pos.PaymentDesignNames() {
		files = append(files, docschema.File{Name: n, Content: string(pos.PaymentDesigns()[n])})
	}
	ds, _, err := docschema.ImportFiles(files)
	require.NoError(t, err)
	spec := &mapping.Spec{SpecVersion: 2, Mode: mapping.ModeComposed, DocSchema: ds}
	spec.Normalize()
	prop := Propose(ProposeInput{Spec: spec, Tables: posTables(snap), Links: linksOf(snap), Table: func(r mapping.TableRef) *schema.Table { return snap.Table(r.Schema, r.Name) }})
	r := pos.Score(&prop.Spec, pos.LoadExpected())
	t.Logf("G10 on the pos fixture:\n%s", r)
	require.Empty(t, r.Gate(), "G10")
	if out := os.Getenv("OC_PROPOSE_SCORE_OUT"); out != "" {
		res := map[string]any{
			"dataset": "pos", "scenario": "propose-quality", "description": "design-first proposal vs the expected mapping (OC-173)",
			"jobStatus": "completed", "startedAt": time.Now().UTC(), "finishedAt": time.Now().UTC(), "quality": r.Quality(), "samples": []any{},
			// A correctness score is the same on any machine: it counts as a reference run.
			"environment": map[string]string{"reference": "true", "kind": "correctness"},
		}
		b, _ := json.MarshalIndent(res, "", "  ")
		require.NoError(t, os.WriteFile(out, append(b, '\n'), 0o600))
	}

	// A broken proposal: one high-confidence field from the wrong column.
	broken := deepSpec(&prop.Spec)
	for i := range broken.FieldMappings {
		m := &broken.FieldMappings[i]
		if len(m.Sources) == 1 && m.Sources[0].Column == "channel" {
			m.Sources[0].Column, m.Confidence = "order_notes", 0.99
		}
	}
	bad := pos.Score(broken, pos.LoadExpected())
	require.NotEmpty(t, bad.Gate())
	require.Contains(t, bad.Gate()[len(bad.Gate())-1], "wrong high-confidence")
	require.GreaterOrEqual(t, bad.Quality()["fields"], pos.GateMin["fields"], "the totals still pass: the wrong one fails it")
}
