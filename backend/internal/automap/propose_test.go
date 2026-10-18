package automap

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/docschema"
	"omnidatabus/internal/fixtures/pos"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/schema"
)

func cartSpec(t *testing.T) *mapping.Spec {
	t.Helper()
	ds, _, err := docschema.Import(docschema.KindJSONSchema, string(pos.Design("cart.schema.json")), "cart.schema.json")
	require.NoError(t, err)
	spec := &mapping.Spec{SpecVersion: 2, Mode: mapping.ModeComposed, DocSchema: ds}
	spec.Normalize()
	return spec
}

// posProposal is the cart design's proposal on the pos fixture's discovered schema.
func posProposal(t *testing.T, mod func(*ProposeInput)) (Proposal, map[string]ProposalItem) {
	t.Helper()
	snap := pos.Snapshot()
	spec := cartSpec(t)
	in := ProposeInput{Spec: spec, Tables: pos.Tables(snap), Links: linksOf(snap), Table: func(r mapping.TableRef) *schema.Table { return snap.Table(r.Schema, r.Name) }}
	if mod != nil {
		mod(&in)
	}
	p := Propose(in)
	by := map[string]ProposalItem{}
	for _, it := range p.Items {
		by[it.Path] = it
	}
	return p, by
}

func mappingFor(t *testing.T, p Proposal, it ProposalItem) mapping.FieldMapping {
	t.Helper()
	for _, m := range p.Spec.FieldMappings {
		if m.TargetFieldID == it.FieldID {
			return m
		}
	}
	t.Fatalf("no mapping for %s", it.Path)
	return mapping.FieldMapping{}
}

func relationOf(p Proposal, id string) *mapping.Relation {
	for i := range p.Spec.Relations {
		if p.Spec.Relations[i].ID == id {
			return &p.Spec.Relations[i]
		}
	}
	return nil
}

func exprOf(m mapping.FieldMapping) string {
	for _, s := range m.Transforms {
		if s.Op == "expression" {
			e, _ := s.Args["expr"].(string)
			return e
		}
	}
	return ""
}

// OC-163 AC1: the cart design drives from pos.carts, high, ahead of the tables that also have
// a cart_id or fields alike.
func TestPropose_AC1_DrivingTable(t *testing.T) {
	p, _ := posProposal(t, nil)
	require.Equal(t, "pos.carts", p.Driving[0].Table.String())
	require.GreaterOrEqual(t, p.Driving[0].Score, bandHigh)
	require.Contains(t, p.Driving[0].Reasons, "key: cart::{uuid} ↔ carts.id")
	require.Contains(t, p.Driving[0].Reasons, "reach 5/5 containers")
	for _, alt := range p.Driving[1:] {
		require.Less(t, alt.Score, p.Driving[0].Score-0.2, alt.Table.String())
	}
	require.Equal(t, "pos.carts", p.Spec.Root.Table.String())
	require.Equal(t, "cart::{id}", p.Spec.Key.Template)
}

// OC-163 AC2 (unit): every container gets a binding or a reason, every field a source or a hint.
func TestPropose_AC2_EveryNodeAccountedFor(t *testing.T) {
	p, by := posProposal(t, nil)
	for _, path := range []string{"customer", "partner", "items[]", "items[].modifiers[]", "items[].appliedRewards[]"} {
		require.Equal(t, StatusBound, by[path].Status, path)
	}
	require.Equal(t, StatusFromParent, by["pricing"].Status)
	seen := map[string]bool{}
	for _, it := range p.Items {
		seen[it.FieldID] = true
	}
	p.Spec.DocSchema.Walk(func(f *docschema.DocField, path string, _ *docschema.DocField) {
		if strings.HasSuffix(path, "[]") || f.ID == docschema.RootID {
			return // an array's item node is its array's
		}
		require.True(t, seen[f.ID], "%s has no item", path)
	})
	require.Equal(t, p.Coverage.Fields, p.Coverage.Proposed+p.Coverage.NotPopulated+p.Coverage.Unresolved)
}

// OC-164 AC1: an object from a referenced-by table takes its first row; one-to-one tables are
// classified so.
func TestProposeBindings_AC1_ReferencedByObject(t *testing.T) {
	p, by := posProposal(t, nil)
	c := by["customer"]
	require.Equal(t, BandHigh, c.Band)
	require.Contains(t, c.Reasons, "object ← one-to-many: first row")
	require.Equal(t, "↙ customers 1:N · first", c.Summary)
	r := relationOf(p, c.RelationIDs[0])
	require.Equal(t, "customers", r.Table.Name)
	require.Equal(t, mapping.EmbedOneToMany, r.Kind)
	require.Equal(t, docschema.AdapterFirst, p.Spec.DocSchema.Find(c.FieldID).Binding.Adapter)
	require.Equal(t, []mapping.OrderColumn{{Column: "id"}}, r.OrderBy, "first by the primary key: customers has no timestamps")

	snap := pos.Snapshot()
	cards := map[string]string{}
	for _, l := range linksOf(snap)(mapping.TableRef{Schema: pos.Schema, Name: "carts"}) {
		cards[l.Table.Name] = l.Cardinality
	}
	require.Equal(t, mapping.CardinalityOneToOne, cards["saved_orders"])
	require.Equal(t, mapping.CardinalityOneToOne, cards["associated_scans"])
	require.Equal(t, mapping.CardinalityOneToMany, cards["customers"])
}

// OC-164 AC2: two levels down.
func TestProposeBindings_AC2_Depth(t *testing.T) {
	p, by := posProposal(t, nil)
	items, mods := by["items[]"], by["items[].modifiers[]"]
	require.Equal(t, BandHigh, items.Band)
	require.NotEqual(t, BandLow, mods.Band, "modifiers ~ customizations share no word: medium is fair")
	m := relationOf(p, mods.RelationIDs[0])
	require.Equal(t, "customizations", m.Table.Name)
	require.Equal(t, items.RelationIDs[0], m.Parent(), "under the items relation")
}

// OC-164 AC3: an array over a many-to-one is wrapped, and its reasons name the join.
func TestProposeBindings_AC3_WrappedArray(t *testing.T) {
	p, by := posProposal(t, nil)
	ar := by["items[].appliedRewards[]"]
	r := relationOf(p, ar.RelationIDs[0])
	require.Equal(t, "applied_rewards", r.Table.Name)
	require.Equal(t, []mapping.JoinPair{{Parent: "applied_reward_id", Child: "id"}}, r.Join)
	require.Equal(t, docschema.AdapterWrap, p.Spec.DocSchema.Find(ar.FieldID).Binding.Adapter)
	require.Contains(t, ar.Reasons, "array ← many-to-one: 0 or 1 item")
}

// OC-164 AC4: below the depth limit, nothing is bound and the reason says so.
func TestProposeBindings_AC4_DepthLimit(t *testing.T) {
	_, by := posProposal(t, func(in *ProposeInput) { in.MaxDepth = 1 })
	mods := by["items[].modifiers[]"]
	require.Equal(t, StatusUnresolved, mods.Status)
	require.Equal(t, CodeDepthLimit, mods.Code)
	require.Contains(t, mods.Reasons[0], "Deepest level reached (1 of 1)")
	require.Equal(t, StatusBound, by["items[]"].Status, "one level is allowed")
}

// OC-165 AC1: flat fields from a one-to-one table, through one lookup, without a node.
func TestPropose_OC165_AC1_OneToOneLookup(t *testing.T) {
	p, by := posProposal(t, nil)
	saved, kind := by["savedAt"], by["orderCollectionType"]
	require.Equal(t, StatusLookup, saved.Status)
	require.Equal(t, "← saved_orders.saved_at", saved.Summary)
	require.Equal(t, "← saved_orders.order_collection_type", kind.Summary)
	ms, mk := mappingFor(t, p, saved), mappingFor(t, p, kind)
	require.Equal(t, ms.Sources[0].Relation, mk.Sources[0].Relation, "one lookup for both")
	r := relationOf(p, ms.Sources[0].Relation)
	require.True(t, r.LookupOnly)
	require.Equal(t, "saved_orders", r.Table.Name)
	for _, rel := range p.Spec.Relations {
		require.False(t, rel.Table.Name == "saved_orders" && !rel.LookupOnly, "no savedOrder object")
	}
}

// OC-165 AC4: a value the column can't hold comes from the one-to-one table.
func TestPropose_OC165_AC4_DerivedStatus(t *testing.T) {
	p, by := posProposal(t, nil)
	st := by["status"]
	require.Equal(t, StatusComputed, st.Status)
	require.Equal(t, BandMedium, st.Band)
	require.Equal(t, "saved_orders.cart_id != nil ? 'saved' : row.status", exprOf(mappingFor(t, p, st)))
	require.Contains(t, st.Reasons[0], "the design allows 'saved'")
	require.NotEmpty(t, st.RelationIDs, "it reads the saved_orders lookup")
}

// OC-166 AC2: the proposal follows the design's constants, not-populated fields, key and sources.
func TestPropose_OC166_AC2_Hints(t *testing.T) {
	p, by := posProposal(t, nil)
	require.Equal(t, StatusConstant, by["type"].Status)
	require.Equal(t, `= "cart"`, by["type"].Summary)
	require.Equal(t, "= 1", by["version"].Summary)
	for _, path := range []string{"customerNamePrompt", "selectedPaymentID", "customer.userID", "items[].priceOverrideReason", "pricing.label"} {
		require.Equal(t, StatusNotPopulated, by[path].Status, path)
		require.Equal(t, mapping.StateNotPopulated, mappingFor(t, p, by[path]).State, path)
	}
	require.Equal(t, StatusNotPopulated, by["receipt"].Status)
	require.Equal(t, StatusNotPopulated, by["receipt.generationDate"].Status, "inside a container the design leaves empty")
	require.Contains(t, by["savedAt"].Reasons, "the design names saved_orders.saved_at")
	require.Equal(t, "cart::{id}", p.Spec.Key.Template)
	with := mapping.ComputeStats(&p.Spec, nil).MappedTargetLeaves
	leaf := map[string]bool{}
	for _, l := range p.Spec.DocSchema.Leaves() {
		leaf[l.FieldID] = true // stats count the document's own fields, not array items'
	}
	np := 0
	without := p.Spec
	without.FieldMappings = nil
	for _, m := range p.Spec.FieldMappings {
		if m.State == mapping.StateNotPopulated {
			if leaf[m.TargetFieldID] {
				np++
			}
			continue
		}
		without.FieldMappings = append(without.FieldMappings, m)
	}
	require.Equal(t, with-np, mapping.ComputeStats(&without, nil).MappedTargetLeaves, "not populated counts as done")
}

// OC-167 AC1: dollars become cents, an amount its display string.
func TestConversions_AC1_CentsAndDisplay(t *testing.T) {
	p, by := posProposal(t, nil)
	price := mappingFor(t, p, by["items[].price"])
	require.Equal(t, "price", price.Sources[0].Column)
	require.Equal(t, centsExpr, exprOf(price))
	require.Equal(t, "round", price.Transforms[1].Op)
	require.Equal(t, "← cart_items.price × 100", by["items[].price"].Summary)
	require.Contains(t, by["items[].price"].Reasons, "× 100: numeric amounts (2 decimals) → whole cents")
	mod := mappingFor(t, p, by["items[].modifiers[].price"])
	require.Equal(t, centsExpr, exprOf(mod))
	disp := mappingFor(t, p, by["pricing.totalToDisplay"])
	require.Equal(t, "toString", disp.Transforms[0].Op)
}

// OC-167 AC2: one column for several fields, and a name made of two.
func TestPropose_OC167_AC2_SharedAndConcat(t *testing.T) {
	p, by := posProposal(t, nil)
	for _, f := range []string{"label", "value", "valueString"} {
		m := mappingFor(t, p, by["items[].modifiers[]."+f])
		require.Equal(t, "name", m.Sources[0].Column, f)
	}
	require.Contains(t, by["items[].modifiers[].value"].Reasons, "the design says value mirrors label")
	name := by["partner.name"]
	require.Equal(t, StatusComputed, name.Status)
	require.Equal(t, BandMedium, name.Band)
	require.Equal(t, "concat(row.first_name, ' ', row.last_name)", exprOf(mappingFor(t, p, name)))
}

// OC-167 AC3: a total from the description, and a constant it states.
func TestPropose_OC167_AC3_AggregateAndConstant(t *testing.T) {
	p, by := posProposal(t, nil)
	tip := by["pricing.tipInCents"]
	require.Equal(t, StatusAggregate, tip.Status)
	require.Len(t, p.Spec.Aggregates, 1)
	a := p.Spec.Aggregates[0]
	require.Equal(t, "sum", a.Fn)
	require.Equal(t, "tip_in_cents", a.Column)
	r := relationOf(p, a.RelationID)
	require.Equal(t, "collected_payments", r.Table.Name)
	require.Empty(t, r.TargetFieldID, "a summary-only relation, not an embed")
	q := by["items[].quantity"]
	require.Equal(t, StatusConstant, q.Status)
	require.Equal(t, BandMedium, q.Band)
	require.Equal(t, "= 1", q.Summary)
}

// OC-167 AC4: no silent wrong conversion.
func TestConversions_AC4_Mismatch(t *testing.T) {
	text := &schema.Column{Name: "code", JSONType: schema.TypeString, UDTName: "text"}
	ts, _, why, ok := convert(text, &docschema.DocField{Name: "code", Type: docschema.Integer})
	require.True(t, ok)
	require.Equal(t, 0.5, ts, "lossy: at most medium")
	require.Contains(t, why, "may lose data")
	_, _, _, ok = convert(&schema.Column{Name: "flag", JSONType: schema.TypeBoolean}, &docschema.DocField{Name: "flag", Type: docschema.Datetime})
	require.False(t, ok, "boolean → datetime: no conversion at all")
	two := 2
	num := &schema.Column{Name: "amount", JSONType: schema.TypeNumber, UDTName: "numeric", NumericScale: &two}
	ts, steps, reason, ok2 := convert(num, &docschema.DocField{Name: "amount", Type: docschema.Integer})
	require.True(t, ok2)
	require.Equal(t, 0.85, ts)
	require.Equal(t, centsExpr, steps[0].Args["expr"])
	require.Contains(t, reason, "whole cents")
	_, _, _, ok = convert(&schema.Column{Name: "tags", JSONType: schema.TypeArray, IsArray: true}, &docschema.DocField{Name: "tags", Type: docschema.Array})
	require.True(t, ok)
}

// OC-169 AC1: unresolved fields say why.
func TestPropose_OC169_AC1_Reasons(t *testing.T) {
	_, by := posProposal(t, nil)
	up := by["updatedAt"]
	require.Equal(t, StatusUnresolved, up.Status)
	require.Equal(t, CodeNoColumn, up.Code)
	require.True(t, up.Required)
	require.Contains(t, up.Candidates, "carts.last_priced_at", "an unused datetime column of the row")
	tax := by["taxDataID"]
	require.Equal(t, CodeReferenceTemplate, tax.Code)
	require.Equal(t, "concat('tax_data::', row.id)", tax.Fix)
	require.Equal(t, StatusComputed, tax.Status, "applied, for review")
	require.Equal(t, "concat('cart::', row.id)", by["id"].Fix)
}

// OC-163 AC4 (determinism): the same input gives the same proposal.
func TestPropose_Deterministic(t *testing.T) {
	spec := cartSpec(t)
	same := func(in *ProposeInput) { in.Spec = spec }
	a, _ := posProposal(t, same)
	b, _ := posProposal(t, same)
	require.Equal(t, a, b)
	require.Equal(t, 1, len(spec.Relations)+1, "the input spec is untouched")
}

// stableIDs gives the design's fields ids from their paths, so a proposal is the same file
// every run (the web tests' fixture).
func stableIDs(spec *mapping.Spec) {
	ids := map[string]string{}
	spec.DocSchema.Walk(func(f *docschema.DocField, path string, _ *docschema.DocField) {
		id := shortID("fld_", path)
		ids[f.ID], f.ID = id, id
	})
	for i := range spec.DocSchema.ConstantHints {
		spec.DocSchema.ConstantHints[i].FieldID = ids[spec.DocSchema.ConstantHints[i].FieldID]
	}
	if spec.DocSchema.Imported != nil {
		spec.DocSchema.Imported.At = time.Date(2026, 10, 17, 8, 0, 0, 0, time.UTC)
	}
}

// The web tests' design-first fixture (web/testdata/design-first): the pos snapshot, the cart
// design and its proposal. OC_UPDATE_WEB_FIXTURES=1 rewrites them.
func TestPropose_WebFixture(t *testing.T) {
	spec := cartSpec(t)
	stableIDs(spec)
	p, _ := posProposal(t, func(in *ProposeInput) { in.Spec = spec })
	// The cart with its payment designs: payments[] has variants (OC-171).
	bundle := []docschema.File{{Name: "cart.schema.json", Content: string(pos.Design("cart.schema.json"))}}
	for _, n := range pos.PaymentDesignNames() {
		bundle = append(bundle, docschema.File{Name: n, Content: string(pos.PaymentDesigns()[n])})
	}
	ds, _, err := docschema.ImportFiles(bundle)
	require.NoError(t, err)
	withPay := &mapping.Spec{SpecVersion: 2, Mode: mapping.ModeComposed, DocSchema: ds}
	withPay.Normalize()
	stableIDs(withPay)
	pp, byPay := posProposal(t, func(in *ProposeInput) { in.Spec = withPay })
	require.Len(t, relationOf(pp, byPay["payments[]"].RelationIDs[0]).Variants, 5, "variants from the snapshot's exclusive arc")
	dir := "../../../web/testdata/design-first"
	files := map[string]any{"pos-snapshot.json": pos.Normalized(pos.Snapshot()), "cart-design.json": spec.DocSchema, "cart-proposal.json": p, "cart-payments-proposal.json": pp}
	for name, v := range files {
		got, err := json.MarshalIndent(v, "", " ")
		require.NoError(t, err)
		got = append(got, '\n')
		if os.Getenv("OC_UPDATE_WEB_FIXTURES") == "1" {
			require.NoError(t, os.MkdirAll(dir, 0o750))
			require.NoError(t, os.WriteFile(filepath.Join(dir, name), got, 0o600))
		}
		want, err := os.ReadFile(filepath.Join(dir, name))
		require.NoError(t, err, "rerun with OC_UPDATE_WEB_FIXTURES=1")
		require.JSONEq(t, string(want), string(got), "%s is stale: rerun with OC_UPDATE_WEB_FIXTURES=1", name)
	}
}

// OC-170 AC2: proposing on an existing mapping (base) keeps what it binds and maps, says so,
// and proposes only what's missing.
func TestPropose_OC170_AC2_BaseCurrent(t *testing.T) {
	spec := cartSpec(t)
	first, by := posProposal(t, func(in *ProposeInput) { in.Spec = spec })
	// The user's mapping: items bound, a hand-made channel, nothing else.
	base := deepSpec(&first.Spec)
	keep := map[string]bool{by["items[]"].RelationIDs[0]: true}
	var rels []mapping.Relation
	for _, r := range base.Relations {
		if keep[r.ID] {
			rels = append(rels, r)
		}
	}
	base.Relations, base.Aggregates = rels, nil
	channel := by["channel"].FieldID
	base.FieldMappings = []mapping.FieldMapping{{ID: "map_mine", TargetFieldID: channel, Sources: []mapping.SourceRef{{Column: "store_number"}}, Transforms: []mapping.Step{}, Origin: mapping.OriginManual, State: mapping.StateActive}}
	clearBindings(&base.DocSchema.Root)
	base.DocSchema.Find(by["items[]"].FieldID).Binding = &docschema.Binding{RelationID: rels[0].ID}

	p, after := posProposal(t, func(in *ProposeInput) { in.Spec, in.Base = base, true })
	require.Equal(t, StatusAlreadyMapped, after["items[]"].Status)
	require.Equal(t, StatusAlreadyMapped, after["channel"].Status)
	m := mappingFor(t, p, after["channel"])
	require.Equal(t, "map_mine", m.ID, "the user's mapping is kept, even where the proposal would differ")
	require.Equal(t, "store_number", m.Sources[0].Column)
	require.Equal(t, StatusBound, after["customer"].Status, "what was missing is proposed")
	require.Equal(t, StatusConverted, after["items[].price"].Status, "fields of a kept binding are proposed")
	require.Equal(t, "pos.carts", p.Spec.Root.Table.String())
}
