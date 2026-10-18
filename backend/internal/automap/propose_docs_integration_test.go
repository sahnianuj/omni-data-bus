//go:build integration

package automap_test

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/automap"
	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/docschema"
	"omnidatabus/internal/fixtures/pos"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/plan"
	"omnidatabus/internal/preview"
	"omnidatabus/internal/related"
	"omnidatabus/internal/relations"
	"omnidatabus/internal/schema"
)

func posOrSkip(t *testing.T, schemas ...string) (*schema.Snapshot, source.Session) {
	t.Helper()
	host := os.Getenv("OC_IT_PG_HOST")
	if host == "" {
		t.Skip("set OC_IT_PG_HOST/PORT (the dev stack)")
	}
	port, _ := strconv.Atoi(os.Getenv("OC_IT_PG_PORT"))
	if port == 0 {
		port = 5432
	}
	snap, sess, err := pos.Source(context.Background(), host, port, schemas...)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sess.Close() })
	return snap, sess
}

func links(snap *schema.Snapshot) automap.Links {
	g := relations.Build(snap, nil)
	return func(from mapping.TableRef) []automap.Link {
		var out []automap.Link
		for _, h := range g.Hops(from) {
			out = append(out, automap.Link{Kind: h.Kind, Table: h.To, Join: h.Join, Virtual: h.Virtual, Cardinality: h.Cardinality, Direction: h.Direction})
		}
		return out
	}
}

// ProposeCart is the cart design's proposal on the pos source.
func proposeCart(t *testing.T, snap *schema.Snapshot) automap.Proposal {
	t.Helper()
	ds, _, err := docschema.Import(docschema.KindJSONSchema, string(pos.Design("cart.schema.json")), "cart.schema.json")
	require.NoError(t, err)
	return proposeDesign(t, snap, ds)
}

func proposeDesign(t *testing.T, snap *schema.Snapshot, ds docschema.Schema, sess ...source.Session) automap.Proposal {
	t.Helper()
	var values func(mapping.TableRef, string, int) []string
	if len(sess) > 0 {
		values = func(r mapping.TableRef, col string, n int) []string {
			return source.DistinctValues(context.Background(), sess[0], r.Schema, snap.Table(r.Schema, r.Name), col, n)
		}
	}
	spec := &mapping.Spec{SpecVersion: 2, Mode: mapping.ModeComposed, DocSchema: ds}
	spec.Normalize()
	return automap.Propose(automap.ProposeInput{
		Spec: spec, Tables: pos.Tables(snap), Links: links(snap), Values: values,
		Table: func(r mapping.TableRef) *schema.Table { return snap.Table(r.Schema, r.Name) },
	})
}

// documents renders the given carts with the spec, through the same plan and reads as the
// preview and a migration (OC-037, OC-049).
func documents(t *testing.T, snap *schema.Snapshot, sess source.Session, spec *mapping.Spec, carts ...int) []map[string]any {
	keys := make([]any, len(carts))
	for i, n := range carts {
		keys[i] = pos.CartID(n)
	}
	return documentsOf(t, snap, sess, spec, keys...)
}

// documentsOf renders the rows whose primary key "id" is each key.
func documentsOf(t *testing.T, snap *schema.Snapshot, sess source.Session, spec *mapping.Spec, keys ...any) []map[string]any {
	t.Helper()
	ctx := context.Background()
	rel := func(s, n string) *schema.Table { return snap.Table(s, n) }
	drv := spec.Root.Table
	tb := snap.Table(drv.Schema, drv.Name)
	pl, issues, err := plan.Compile(plan.Input{Spec: spec, Root: drv, Table: tb, Related: rel})
	require.NoError(t, err)
	require.NotNil(t, pl.Program, "compile issues: %+v", issues)
	rs, err := sess.(source.SnapshotReader).BeginRead(ctx, "odb it", false)
	require.NoError(t, err)
	defer func() { _ = rs.Close() }()
	var out []map[string]any
	for _, n := range keys {
		rows, err := preview.RowsByKey(ctx, rs, drv, tb, map[string]any{"id": n})
		require.NoError(t, err)
		require.Len(t, rows, 1, "row %v", n)
		rr, fails, _, err := preview.Related(ctx, rs, pl, spec, rows)
		require.NoError(t, err)
		require.Empty(t, fails)
		_, doc, _, _, rerr := pl.Program.ApplyTraceWith(rows[0], related.Row(rr, 0), nil)
		require.Nil(t, rerr, "row %v", n)
		var m map[string]any
		require.NoError(t, json.Unmarshal(doc, &m))
		out = append(out, m)
	}
	return out
}

// OC-163 AC2, OC-165 AC1/AC2/AC4, OC-167 AC1–AC3: the proposal is a valid mapping whose
// documents have the design's values: cents, display strings, the saved status from the
// one-to-one table, the first customer, the partner's full name, the tip total.
func TestPropose_OC163_Documents(t *testing.T) {
	snap, sess := posOrSkip(t)
	prop := proposeCart(t, snap)
	spec := &prop.Spec
	ctx := mapping.Context{Table: snap.Table(pos.Schema, "carts"), Related: func(s, n string) *schema.Table { return snap.Table(s, n) }}
	unresolved := map[string]bool{}
	for _, it := range prop.Items {
		if it.Status == automap.StatusUnresolved {
			unresolved[it.FieldID] = true
		}
	}
	for _, is := range mapping.Validate(spec, ctx) {
		if is.Code == mapping.CodeRequiredFieldUnmapped && unresolved[is.FieldID] {
			continue // updatedAt: no column holds it; the review asks for it (OC-169)
		}
		require.NotEqual(t, mapping.SeverityError, is.Severity, "%+v", is)
	}
	for _, l := range spec.DocSchema.Leaves() {
		if unresolved[l.FieldID] && l.Required {
			f := spec.DocSchema.Find(l.FieldID)
			f.Required = false // what the user's "mark optional" does, to render documents
		}
	}
	require.Equal(t, "cart::{id}", spec.Key.Template)

	docs := documents(t, snap, sess, spec, 20, 11, 34)
	closed, saved, twoCustomers := docs[0], docs[1], docs[2]

	require.Equal(t, "cart", closed["type"])
	require.EqualValues(t, 1, closed["version"])
	require.Equal(t, "cart::"+pos.CartID(20), closed["id"])
	require.Equal(t, "closed", closed["status"])
	require.Equal(t, "drive_thru", closed["channel"])
	require.Nil(t, closed["savedAt"])
	items := closed["items"].([]any)
	require.NotEmpty(t, items)
	first := items[0].(map[string]any)
	price := first["price"].(float64)
	require.Equal(t, float64(int64(price)), price, "a whole number of cents")
	require.Greater(t, price, 100.0, "cents, not dollars")
	require.EqualValues(t, 1, first["quantity"])
	require.Len(t, first["appliedRewards"].([]any), 1, "cart 20's first item has a reward (wrapped N:1)")
	pricing := closed["pricing"].(map[string]any)
	require.EqualValues(t, 100, pricing["tipInCents"], "sum of the payments' tips")
	require.IsType(t, "", pricing["totalToDisplay"])
	require.Regexp(t, `^\d+\.\d{1,2}$`, pricing["totalToDisplay"])
	partner := closed["partner"].(map[string]any)
	require.Regexp(t, `^\w+ \w+$`, partner["name"], "first and last name")
	require.Contains(t, closed, "receipt")
	require.Nil(t, closed["receipt"], "not populated: null, not an object of nulls")
	require.Nil(t, closed["associatedScan"])

	require.Equal(t, "saved", saved["status"], "an open cart with a saved_orders row")
	require.NotNil(t, saved["savedAt"])
	require.NotNil(t, saved["orderCollectionType"])

	cust := twoCustomers["customer"].(map[string]any)
	require.NotEmpty(t, cust["firstName"], "the first of the cart's two customers")
}

func byPath(p automap.Proposal) map[string]automap.ProposalItem {
	out := map[string]automap.ProposalItem{}
	for _, it := range p.Items {
		out[it.Path] = it
	}
	return out
}

func relationFor(p automap.Proposal, id string) *mapping.Relation {
	for i := range p.Spec.Relations {
		if p.Spec.Relations[i].ID == id {
			return &p.Spec.Relations[i]
		}
	}
	return nil
}

// OC-171 AC2/AC3, pattern A: the cart with its payment designs; payments[] reads
// collected_payments, each row in its tender's shape.
func TestPropose_OC171_Arc(t *testing.T) {
	snap, sess := posOrSkip(t)
	files := []docschema.File{{Name: "cart.schema.json", Content: string(pos.Design("cart.schema.json"))}}
	for _, n := range pos.PaymentDesignNames() {
		files = append(files, docschema.File{Name: n, Content: string(pos.PaymentDesigns()[n])})
	}
	ds, _, err := docschema.ImportFiles(files)
	require.NoError(t, err)
	prop := proposeDesign(t, snap, ds)
	by := byPath(prop)
	pay := by["payments[]"]
	require.Equal(t, automap.StatusBound, pay.Status, "%+v", pay)
	r := relationFor(prop, pay.RelationIDs[0])
	require.Equal(t, "collected_payments", r.Table.Name)
	require.Len(t, r.Variants, 5)
	when := map[string]string{}
	for _, v := range r.Variants {
		when[ds.Find(v.FieldID).Name] = v.When
	}
	require.Equal(t, map[string]string{"SCC": "scc_id", "SVC": "svc_id", "CREDIT_CARD": "credit_card_id", "CASH": "cash_tender_id", "SCAN_AND_PAY": "scan_and_pay_qr_code_id"}, when)
	require.Equal(t, "← collected_payments.charged_amount_in_cents", findItem(prop, "payments[].SCC.amountChargedInCents", "SCC").Summary)
	require.Equal(t, `= "SCC"`, findItem(prop, "payments[].SCC.paymentType", "SCC").Summary)
	require.Equal(t, "← sccs.encrypted_scc_number", findItem(prop, "payments[].SCC.encryptedNumber", "SCC").Summary)
	require.Equal(t, "← svcs.encrypted_svc_number", findItem(prop, "payments[].SVC.encryptedNumber", "SVC").Summary, "each variant reads its own table")

	spec := &prop.Spec
	relax(spec, prop)
	docs := documents(t, snap, sess, spec, 40, 18) // cart 40: an SCC payment and a credit card; 18: cash
	pays := docs[0]["payments"].([]any)
	require.Len(t, pays, 2)
	kinds := map[string]map[string]any{}
	for _, x := range pays {
		m := x.(map[string]any)
		kinds[m["paymentType"].(string)] = m
	}
	require.Contains(t, kinds, "SCC")
	require.Contains(t, kinds, "CREDIT_CARD")
	require.Equal(t, "enc-scc-40", kinds["SCC"]["encryptedNumber"])
	require.NotContains(t, kinds["SCC"], "cardBrand", "only its variant's fields")
	require.Equal(t, "MC", kinds["CREDIT_CARD"]["cardBrand"], "cart 40: brand 1 + 40 % 3")
	cash := docs[1]["payments"].([]any)
	require.Len(t, cash, 1)
	require.Equal(t, "CASH", cash[0].(map[string]any)["paymentType"])
	require.EqualValues(t, 2000, cash[0].(map[string]any)["amountTenderedInCents"])
}

// findItem is the item at path inside variant name (variant fields share their paths).
func findItem(p automap.Proposal, path, variant string) automap.ProposalItem {
	for _, it := range p.Items {
		if it.Path == path && strings.Contains(it.Summary+strings.Join(it.Reasons, ""), "") {
			f := p.Spec.DocSchema.Find(it.FieldID)
			if f == nil {
				continue
			}
			var parent string
			p.Spec.DocSchema.Walk(func(x *docschema.DocField, _ string, par *docschema.DocField) {
				if x.ID == it.FieldID && par != nil {
					parent = par.Name
				}
			})
			if parent == variant {
				return it
			}
		}
	}
	return automap.ProposalItem{}
}

// relax marks required fields nothing fills optional, as the user's review would.
func relax(spec *mapping.Spec, prop automap.Proposal) {
	for _, it := range prop.Items {
		if it.Status == automap.StatusUnresolved {
			if f := spec.DocSchema.Find(it.FieldID); f != nil {
				f.Required = false
			}
		}
	}
}

// OC-171 AC2/AC3/AC4, pattern B: contact methods by their kind column, postal with its detail
// table; a fax row is skipped and counted.
func TestPropose_OC171_TypeColumn(t *testing.T) {
	snap, sess := posOrSkip(t, pos.CRM)
	ds, _, err := docschema.Import(docschema.KindJSONSchema, string(pos.Design("variants/contact.schema.json")), "contact.schema.json")
	require.NoError(t, err)
	prop := proposeDesign(t, snap, ds, sess)
	require.Equal(t, "crm.customers", prop.Spec.Root.Table.String())
	by := byPath(prop)
	cm := by["contactMethods[]"]
	r := relationFor(prop, cm.RelationIDs[0])
	require.Equal(t, "contact_methods", r.Table.Name)
	require.Len(t, r.Variants, 3)
	for _, v := range r.Variants {
		require.Equal(t, "kind", v.When)
	}
	require.Equal(t, "← postal_addresses.city", findItem(prop, "contactMethods[].postal.city", "postal").Summary)
	// address ~ value shares no word: unresolved, with the row's unused text column offered.
	email := findItem(prop, "contactMethods[].email.address", "email")
	require.Equal(t, automap.StatusUnresolved, email.Status)
	require.Contains(t, email.Candidates, "contact_methods.value")

	docs := documentsOf(t, snap, sess, &prop.Spec, int32(7), int32(8))
	methods := docs[0]["contactMethods"].([]any)
	var kinds []string
	for _, m := range methods {
		kinds = append(kinds, m.(map[string]any)["kind"].(string))
	}
	require.NotContains(t, kinds, "fax", "a row of no variant is skipped")
	for _, m := range docs[1]["contactMethods"].([]any) {
		x := m.(map[string]any)
		if x["kind"] == "postal" {
			require.NotEmpty(t, x["city"])
			require.NotContains(t, x, "address")
		}
	}
}

// OC-171 AC2/AC3, pattern C: a ticket's activity is the union of three tables, by created_at.
func TestPropose_OC171_Union(t *testing.T) {
	snap, sess := posOrSkip(t, pos.Support)
	ds, _, err := docschema.Import(docschema.KindJSONSchema, string(pos.Design("variants/ticket.schema.json")), "ticket.schema.json")
	require.NoError(t, err)
	prop := proposeDesign(t, snap, ds)
	require.Equal(t, "support.tickets", prop.Spec.Root.Table.String())
	act := byPath(prop)["activity[]"]
	require.Equal(t, automap.StatusBound, act.Status, "%+v", act)
	require.Contains(t, act.Reasons, "merged by created_at")
	require.Len(t, act.RelationIDs, 3)

	docs := documentsOf(t, snap, sess, &prop.Spec, int32(6))
	items := docs[0]["activity"].([]any)
	var types []string
	last := ""
	for _, x := range items {
		m := x.(map[string]any)
		types = append(types, m["type"].(string))
		at := m["createdAt"].(string)
		require.GreaterOrEqual(t, at, last, "oldest first")
		last = at
	}
	require.Equal(t, []string{"attachment", "comment", "statusChange", "comment", "statusChange"}, types, "ticket 6: attachment at +2 min, then comments and changes")
}
