// Package pos is the point-of-sale fixture of design-first mapping (OC-162): a source schema
// shaped like a real customer's order database (generic names, synthetic rows), the customer's
// cart and payment designs, and the mapping a person would build from them. Tests reach the
// schema as a connected source (a PostgreSQL connection and discovery), never by reading the
// DDL, and score proposals against Expected.
package pos

import (
	"context"
	"crypto/md5" //nolint:gosec // see uid
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"omnidatabus/internal/schema"
)

// Schema is the PostgreSQL schema the fixture creates; CRM and Support are the variant
// fixtures by type column and as a union of tables (OC-171).
const (
	Schema  = "pos"
	CRM     = "crm"
	Support = "support"
)

//go:embed schema.sql expected.json designs
var files embed.FS

// SQL is the fixture's DDL and rows (also mounted into the dev stack's PostgreSQL).
func SQL() string { return mustRead("schema.sql") }

// Design returns a design file, e.g. "cart.schema.json" or "payments/scc.schema.json".
func Design(name string) []byte { return []byte(mustRead("designs/" + name)) }

// PaymentDesigns are the payment variant designs, by file name, in name order.
func PaymentDesigns() map[string][]byte {
	out := map[string][]byte{}
	entries, err := fs.ReadDir(files, "designs/payments")
	if err != nil {
		panic(err)
	}
	for _, e := range entries {
		out["payments/"+e.Name()] = Design("payments/" + e.Name())
	}
	return out
}

// PaymentDesignNames lists PaymentDesigns' keys in order.
func PaymentDesignNames() []string {
	var out []string
	for k := range PaymentDesigns() {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func mustRead(name string) string {
	b, err := files.ReadFile(name)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// Apply creates the pos schema and its rows unless pos.carts already exists. Concurrent test
// packages serialise on an advisory lock, so only one of them loads it.
func Apply(ctx context.Context, conn *pgx.Conn) error {
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock(162162)"); err != nil {
		return err
	}
	defer func() { _, _ = conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock(162162)") }()
	var exists bool
	// The last table the seed creates: present means everything is (OC-171 added crm, support).
	if err := conn.QueryRow(ctx, "SELECT to_regclass('pos.carts') IS NOT NULL AND to_regclass('support.attachments') IS NOT NULL").Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}
	if _, err := conn.Exec(ctx, SQL()); err != nil {
		return fmt.Errorf("pos fixture: %w", err)
	}
	return nil
}

// Expected is the mapping a person would build for the cart design (expected.json).
type Expected struct {
	Driving  string                `json:"driving"`
	Key      string                `json:"key"`
	Bindings map[string]ExpBinding `json:"bindings"`
	Fields   map[string]ExpField   `json:"fields"`
	order    []string              // field paths in file order
	bOrder   []string              // binding paths in file order
}

// ExpBinding is what fills a container: a table (kind, adapter), the parent's own columns, or
// nothing (not populated). Variants names the discriminator of a variant array (OC-171).
type ExpBinding struct {
	Table        string `json:"table,omitempty"`
	Kind         string `json:"kind,omitempty"`
	Adapter      string `json:"adapter,omitempty"`
	FromParent   bool   `json:"fromParent,omitempty"`
	NotPopulated bool   `json:"notPopulated,omitempty"`
	Variants     string `json:"variants,omitempty"`
}

// ExpField is what fills a field: a column (table.column) with an optional transform (cents,
// toString), a constant, an expression, an aggregate (fn(table.column)), nothing on purpose
// (not populated) or nothing at all (unresolved: no source exists).
type ExpField struct {
	Source       string `json:"source,omitempty"`
	Transform    string `json:"transform,omitempty"`
	Constant     any    `json:"constant,omitempty"`
	Expression   string `json:"expression,omitempty"`
	Aggregate    string `json:"aggregate,omitempty"`
	NotPopulated bool   `json:"notPopulated,omitempty"`
	Unresolved   bool   `json:"unresolved,omitempty"`
}

// Category is the scoring bucket of an expected field.
func (f ExpField) Category() string {
	switch {
	case f.NotPopulated:
		return CatNotPopulated
	case f.Unresolved:
		return CatUnresolved
	case f.Constant != nil:
		return CatConstant
	case f.Expression != "" || f.Aggregate != "":
		return CatComputed
	case f.Transform != "":
		return CatConverted
	}
	return CatDirect
}

// Score categories, in report order.
const (
	CatDriving      = "driving"
	CatBindings     = "bindings"
	CatDirect       = "fields.direct"
	CatConverted    = "fields.converted"
	CatComputed     = "fields.computed"
	CatConstant     = "fields.constant"
	CatNotPopulated = "fields.notPopulated"
	CatUnresolved   = "unresolved"
)

// Categories lists the score categories in report order.
var Categories = []string{CatDriving, CatBindings, CatDirect, CatConverted, CatComputed, CatConstant, CatNotPopulated, CatUnresolved}

// LoadExpected parses expected.json, keeping the file's order for reports.
func LoadExpected() Expected {
	raw := []byte(mustRead("expected.json"))
	var e Expected
	if err := json.Unmarshal(raw, &e); err != nil {
		panic(err)
	}
	e.order = orderedKeys(raw, "fields")
	e.bOrder = orderedKeys(raw, "bindings")
	return e
}

// orderedKeys returns the keys of the object at top-level key k, in document order.
func orderedKeys(raw []byte, k string) []string {
	var top map[string]json.RawMessage
	_ = json.Unmarshal(raw, &top)
	dec := json.NewDecoder(strings.NewReader(string(top[k])))
	var out []string
	depth := 0
	expectKey := false
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case json.Delim:
			if t == '{' || t == '[' {
				depth++
				expectKey = depth == 1 && t == '{'
			} else {
				depth--
				expectKey = depth == 1
			}
			continue
		case string:
			if depth == 1 && expectKey {
				out = append(out, t)
				expectKey = false
				continue
			}
		}
		if depth == 1 {
			expectKey = true
		}
	}
	return out
}

// uid mirrors the seed's pos.uid(kind, n): md5(kind || n) formatted as a uuid.
func uid(kind string, n int) string {
	sum := md5.Sum([]byte(kind + strconv.Itoa(n))) //nolint:gosec // the seed's deterministic ids
	h := hex.EncodeToString(sum[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

//go:embed snapshot.json
var snapshotJSON []byte

// Snapshot is what discovery returns for the pos schema (snapshot.json), for unit tests that
// don't reach PostgreSQL. TestPOSFixture_SnapshotUpToDate keeps it equal to a real discovery.
func Snapshot() *schema.Snapshot {
	var s schema.Snapshot
	if err := json.Unmarshal(snapshotJSON, &s); err != nil {
		panic(err)
	}
	return &s
}

// Normalized is a snapshot with what changes between discoveries (row estimates, server
// version) cleared, as snapshot.json stores it.
func Normalized(s *schema.Snapshot) *schema.Snapshot {
	out := *s
	out.ServerVersion = ""
	out.Schemas = append([]schema.Schema(nil), s.Schemas...)
	for i := range out.Schemas {
		out.Schemas[i].Tables = append([]schema.Table(nil), out.Schemas[i].Tables...)
		for j := range out.Schemas[i].Tables {
			t := &out.Schemas[i].Tables[j]
			t.RowEstimate, t.StatsStale = nil, false
		}
	}
	return &out
}
