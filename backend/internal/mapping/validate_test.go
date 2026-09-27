package mapping

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/docschema"
	"omnidatabus/internal/schema"
)

func custTable() *schema.Table {
	return &schema.Table{Name: "customer", PrimaryKey: []string{"customer_id"}, Columns: []schema.Column{
		{Name: "customer_id", UDTName: "int4", JSONType: schema.TypeInteger, IsPrimaryKey: true},
		{Name: "first_name", UDTName: "text", JSONType: schema.TypeString},
		{Name: "age_text", UDTName: "text", JSONType: schema.TypeString},
		{Name: "notes", UDTName: "text", JSONType: schema.TypeString},
		{Name: "profile", UDTName: "jsonb", JSONType: schema.TypeAny},
	}}
}

func f(id, name string, t docschema.FieldType, required bool) docschema.DocField {
	return docschema.DocField{ID: id, Name: name, Type: t, Required: required}
}

func baseSpec() Spec {
	s := Spec{
		DocSchema: docschema.Schema{Root: docschema.DocField{ID: docschema.RootID, Name: "$", Type: docschema.Object, Children: []docschema.DocField{
			f("f_id", "customerId", docschema.Integer, true),
			f("f_first", "firstName", docschema.String, false),
			f("f_age", "age", docschema.Integer, false),
			f("f_email", "email", docschema.String, true),
			f("f_desc", "description", docschema.String, false),
			{ID: "f_prof", Name: "profile", Type: docschema.Object, Children: []docschema.DocField{}},
		}}},
		Key:       KeySpec{Template: "customer::{customer_id}"},
		TypeField: TypeField{Enabled: true, Name: "type", Value: "customer"},
		FieldMappings: []FieldMapping{
			{ID: "m1", TargetFieldID: "f_id", Sources: []SourceRef{{Column: "customer_id"}}, Origin: OriginAuto, State: StateActive, Confidence: 0.97},
			{ID: "m2", TargetFieldID: "f_first", Sources: []SourceRef{{Column: "first_name"}}, Origin: OriginAuto, State: StateActive, Confidence: 0.97},
			{ID: "m3", TargetFieldID: "f_prof", Sources: []SourceRef{{Column: "profile"}}, Origin: OriginManual, State: StateActive},
		},
	}
	s.Normalize()
	return s
}

func codesOf(issues []Issue) []string {
	out := []string{}
	for _, i := range issues {
		out = append(out, i.Severity+":"+i.Code+"@"+i.Path)
	}
	return out
}

func TestValidate_AC1_AggregatesAllRules(t *testing.T) {
	s := baseSpec()
	s.FieldMappings = append(s.FieldMappings,
		FieldMapping{ID: "m4", TargetFieldID: "f_age", Sources: []SourceRef{{Column: "age_text"}}, Origin: OriginManual, State: StateActive},        // lossy, no transform
		FieldMapping{ID: "m5", TargetFieldID: "f_desc", Sources: []SourceRef{{Column: "notes"}}, Origin: OriginSuggested, State: StatePending},      // pending
		FieldMapping{ID: "m6", TargetFieldID: "f_first", Sources: []SourceRef{{Column: "dropped_col"}}, Origin: OriginManual, State: StateRejected}, // rejected: ignored
	)
	s.FieldMappings[1].Sources = []SourceRef{{Column: "legacy_name"}} // dropped from the source
	missing := false
	issues := Validate(&s, Context{Table: custTable(), TargetExists: &missing, Target: Target{Bucket: "b", Scope: "s", Collection: "c"}})
	require.Equal(t, []string{
		"error:REQUIRED_FIELD_UNMAPPED@email",
		"error:SOURCE_COLUMN_MISSING@firstName",
		"error:TARGET_KEYSPACE_MISSING@target",
		"warning:LOSSY_CONVERSION@age",
		"warning:SUGGESTIONS_PENDING@fieldMappings",
		"info:UNMAPPED_SOURCE_COLUMNS@source",
	}, codesOf(issues))
	require.False(t, Ready(issues))
	require.Equal(t, StatusStale, Status(issues))
	require.Equal(t, "parseInteger", issues[3].Fix.Step.Op)
	require.Equal(t, "mark-optional", issues[0].Fix.Kind)
}

func TestValidate_AC1_ReadyWhenOnlyWarnings(t *testing.T) {
	s := baseSpec()
	s.DocSchema.Root.Children[3].Required = false
	s.Key.Template = "customer::{first_name}" // may collide: a warning
	issues := Validate(&s, Context{Table: custTable()})
	require.True(t, Ready(issues))
	require.Equal(t, StatusReady, Status(issues))
	require.Contains(t, codesOf(issues), "warning:KEY_MAY_COLLIDE@key")
}

func TestValidate_AC1_DeterministicOrder(t *testing.T) {
	s := baseSpec()
	first := codesOf(Validate(&s, Context{Table: custTable()}))
	for range 20 {
		require.Equal(t, first, codesOf(Validate(&s, Context{Table: custTable()})))
	}
}

func TestValidate_KeyAndStructure(t *testing.T) {
	s := baseSpec()
	s.DocSchema.Root.Children[3].Required = false
	s.Key.Template = ""
	require.Contains(t, codesOf(Validate(&s, Context{Table: custTable()})), "error:KEY_TEMPLATE_MISSING@key")
	s.Key.Template = "c::{oops"
	require.Contains(t, codesOf(Validate(&s, Context{Table: custTable()})), "error:KEY_TEMPLATE_INVALID@key")
	s.Key.Template = "c::{gone}"
	require.Contains(t, codesOf(Validate(&s, Context{Table: custTable()})), "error:SOURCE_COLUMN_MISSING@key")
	s.Key.Template = "{$uuid}"
	require.Contains(t, codesOf(Validate(&s, Context{Table: custTable()})), "warning:NON_IDEMPOTENT_KEY@key")

	s = baseSpec()
	s.FieldMappings = append(s.FieldMappings,
		FieldMapping{ID: "m1", TargetFieldID: "f_id", Sources: []SourceRef{{Column: "customer_id"}}, Origin: OriginManual, State: StateActive},
		FieldMapping{ID: "m9", TargetFieldID: "f_email", Sources: []SourceRef{{Column: "profile"}}, Origin: OriginManual, State: "weird"},
		FieldMapping{ID: "m10", TargetFieldID: "f_age", Sources: []SourceRef{{Column: "profile"}}, Origin: OriginManual, State: StateActive},
		FieldMapping{ID: "m11", TargetFieldID: "f_desc", Sources: []SourceRef{{Column: "customer_id"}}, Origin: OriginManual, State: StateActive},
	)
	s.FieldMappings = append(s.FieldMappings, FieldMapping{ID: "m12", TargetFieldID: "f_x", Origin: OriginManual, State: StateActive})
	got := codesOf(Validate(&s, Context{Table: custTable()}))
	require.Contains(t, got, "error:INVALID_MAPPING@customerId")
	require.Contains(t, got, "error:DUPLICATE_TARGET@customerId")
	require.Contains(t, got, "error:INVALID_MAPPING@email")
	require.Contains(t, got, "error:UNKNOWN_TARGET_FIELD@fieldMappings.m12")
	require.Contains(t, got, "warning:LOSSY_CONVERSION@age", "any → integer is lossy")
	s.FieldMappings[len(s.FieldMappings)-2].TargetFieldID = "f_prof"
	s.DocSchema.Root.Children[1].Type = docschema.Array // firstName becomes an array without items
	require.Contains(t, codesOf(Validate(&s, Context{Table: custTable()})), "error:ARRAY_WITHOUT_ITEMS@firstName")
}

func TestSpecValidate_AC5_TypeFieldCollision(t *testing.T) {
	s := baseSpec()
	s.DocSchema.Root.Children[3].Required = false
	s.TypeField.Name = "firstName"
	require.Contains(t, codesOf(Validate(&s, Context{Table: custTable()})), "error:TYPE_FIELD_COLLISION@typeField")
	s.TypeField.Name = ""
	require.Contains(t, codesOf(Validate(&s, Context{Table: custTable()})), "error:TYPE_FIELD_INVALID@typeField")
	s.TypeField.Enabled = false
	require.True(t, Ready(Validate(&s, Context{Table: custTable()})))
}

func TestSpecValidate_AC9_UnknownReferences(t *testing.T) {
	s := baseSpec()
	s.FieldMappings = append(s.FieldMappings,
		FieldMapping{ID: "bad1", TargetFieldID: "f_nope", Sources: []SourceRef{{Column: "first_name"}}},
		FieldMapping{ID: "bad2", TargetFieldID: "f_first", Sources: []SourceRef{{Column: "no_such_col"}}},
	)
	issues := CheckReferences(&s, custTable(), nil, nil)
	require.Equal(t, []string{"error:UNKNOWN_TARGET_FIELD@fieldMappings.bad1", "error:SOURCE_COLUMN_MISSING@fieldMappings.bad2"}, codesOf(issues))

	// A column the stored spec already used survives drift (it can be re-mapped later).
	prev := baseSpec()
	prev.FieldMappings[1].Sources = []SourceRef{{Column: "no_such_col"}}
	require.Len(t, CheckReferences(&s, custTable(), &prev, nil), 1)
}

func TestMappingStatus_AC3_Table(t *testing.T) {
	s := baseSpec()
	s.Key.Template = ""
	require.Equal(t, StatusDraft, Status(Validate(&s, Context{Table: custTable()})), "no key template → draft")
	s = baseSpec()
	s.DocSchema.Root.Children[3].Required = false
	require.Equal(t, StatusReady, Status(Validate(&s, Context{Table: custTable()})))
	tb := custTable()
	tb.Columns = tb.Columns[:1] // first_name dropped from the source
	require.Equal(t, StatusStale, Status(Validate(&s, Context{Table: tb})))
}

func TestMappingSpec_AC6_CreateIfMissingPersisted(t *testing.T) {
	b, err := json.Marshal(Target{Bucket: "shop", Scope: "sales", Collection: "orders", CreateIfMissing: true})
	require.NoError(t, err)
	require.JSONEq(t, `{"bucket":"shop","scope":"sales","collection":"orders","createIfMissing":true}`, string(b))
	var back Target
	require.NoError(t, json.Unmarshal(b, &back))
	require.True(t, back.CreateIfMissing)
}

func TestReattachMappings_AC7_ByPath(t *testing.T) {
	old := baseSpec()
	next := docschema.Schema{Root: docschema.DocField{ID: docschema.RootID, Name: "$", Type: docschema.Object, Children: []docschema.DocField{
		f("n_id", "customerId", docschema.Integer, true),
		f("n_email", "email", docschema.String, false),
		{ID: "n_prof", Name: "profile", Type: docschema.Object, Children: []docschema.DocField{}},
	}}}
	kept, orphans := ReattachByPath(&old.DocSchema, &next, append(old.FieldMappings, FieldMapping{ID: "gone", TargetFieldID: "f_unknown"}))
	require.Len(t, kept, 2)
	require.Equal(t, "n_id", kept[0].TargetFieldID)
	require.Equal(t, "n_prof", kept[1].TargetFieldID)
	require.Equal(t, []string{"m2", "gone"}, []string{orphans[0].ID, orphans[1].ID}, "firstName no longer exists")
}

func TestMergeDesigns_KeepsExistingAddsNew(t *testing.T) {
	old := baseSpec()
	incoming := docschema.Schema{Root: docschema.DocField{ID: docschema.RootID, Name: "$", Type: docschema.Object, Children: []docschema.DocField{
		f("x1", "customerId", docschema.String, false), // exists: existing type wins
		f("x2", "loyaltyTier", docschema.String, false),
		{ID: "x3", Name: "profile", Type: docschema.Object, Children: []docschema.DocField{f("x4", "bio", docschema.String, false)}},
	}}}
	merged := MergeDesigns(&old.DocSchema, &incoming)
	require.Len(t, merged.Root.Children, 7)
	require.Equal(t, docschema.Integer, merged.Root.Children[0].Type)
	require.Equal(t, "f_id", merged.Root.Children[0].ID, "existing IDs kept")
	require.Equal(t, "bio", merged.Root.Children[5].Children[0].Name)
	require.Len(t, old.DocSchema.Root.Children[5].Children, 0, "the original is untouched")
	require.Empty(t, docschema.Validate(&merged))
}

func TestSpecValidate_AC7_EmbedErrors(t *testing.T) {
	snap := shopSnap()
	customer := snap.Table("shop", "customer")
	ok := embedSpec()
	for _, i := range Validate(&ok, Context{Table: customer, Related: snapRel(snap)}) {
		require.NotContains(t, i.Code, "EMBED", i.Message)
	}

	type tc struct {
		name   string
		mutate func(s *Spec)
		code   string
		embed  string
		field  string
	}
	cases := []tc{
		{"join column missing from the child", func(s *Spec) { s.Embeds[0].Join[0].Child = "addr_id" }, CodeEmbedJoinInvalid, "emb_1", "f_addr"},
		{"join column missing from the parent", func(s *Spec) { s.Embeds[1].Join[0].Parent = "cust_id" }, CodeEmbedJoinInvalid, "emb_2", "f_orders"},
		{"type mismatch int4 vs text", func(s *Spec) { s.Embeds[0].Join[0] = JoinPair{Parent: "name", Child: "address_id"} }, CodeEmbedJoinInvalid, "emb_1", "f_addr"},
		{"array target for many-to-one", func(s *Spec) { s.Embeds[0].TargetFieldID = "f_orders" }, CodeEmbedTargetKind, "emb_1", "f_orders"},
		{"object target for one-to-many", func(s *Spec) { s.Embeds[1].TargetFieldID = "f_addr"; s.Embeds[0].TargetFieldID = "f_id" }, CodeEmbedTargetKind, "emb_2", "f_addr"},
		{"duplicate target", func(s *Spec) { s.Embeds = append(s.Embeds, s.Embeds[0]); s.Embeds[2].ID = "emb_3" }, CodeEmbedDuplicate, "emb_3", "f_addr"},
		{"unknown table", func(s *Spec) { s.Embeds[0].Table.Name = "adress" }, CodeEmbedTableMissing, "emb_1", "f_addr"},
		{"order column missing", func(s *Spec) { s.Embeds[1].OrderBy[0].Column = "placed_at" }, CodeEmbedJoinInvalid, "emb_2", "f_orders"},
		{"parent column into an embed field", func(s *Spec) { s.FieldMappings[1].Sources[0].Embed = "" }, CodeEmbedScope, "", "f_city"},
		{"embed column into another embed", func(s *Spec) { s.FieldMappings[2].Sources[0].Embed = "emb_1" }, CodeEmbedScope, "", "f_total"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := embedSpec()
			c.mutate(&s)
			var found *Issue
			for _, i := range Validate(&s, Context{Table: customer, Related: snapRel(snap)}) {
				if i.Code == c.code && i.Severity == SeverityError {
					found = &i
					break
				}
			}
			require.NotNil(t, found, "want %s", c.code)
			require.Equal(t, c.field, found.FieldID)
			if c.embed != "" {
				require.Contains(t, found.Message, "Embed "+c.embed+":")
			}
		})
	}

	// Embedded sources are checked against their table.
	s := embedSpec()
	s.FieldMappings[2].Sources[0].Column = "amount"
	require.Contains(t, codesOf(Validate(&s, Context{Table: customer, Related: snapRel(snap)})), "error:"+CodeSourceColumnMissing+"@orders[].total")
	require.NotEmpty(t, CheckReferences(&s, customer, nil, snapRel(snap)))
	s.FieldMappings[2].Sources[0].Column = "total"
	require.Empty(t, CheckReferences(&s, customer, nil, snapRel(snap)))
}
