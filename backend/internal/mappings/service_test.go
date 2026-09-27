package mappings

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"

	"omnidatabus/internal/auth"
	"omnidatabus/internal/connectors"
	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/connectors/source/sourcetest"
	"omnidatabus/internal/connectors/target"
	"omnidatabus/internal/docschema"
	"omnidatabus/internal/httpapi"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/problem"
	"omnidatabus/internal/projects"
	"omnidatabus/internal/schema"
	"omnidatabus/internal/store"
	"omnidatabus/internal/store/memstore"
	"omnidatabus/internal/store/storetest"
	"omnidatabus/internal/transform"
)

var ctx = context.Background()

const ws store.WorkspaceID = "ws-1"

func est(n int64) *int64 { return &n }

func shopSnapshot() *schema.Snapshot {
	return &schema.Snapshot{Database: "shop", Schemas: []schema.Schema{{Name: "shop", Tables: []schema.Table{
		{Name: "customer", PrimaryKey: []string{"customer_id"}, RowEstimate: est(2000), Readable: true, Columns: []schema.Column{
			{Name: "customer_id", Ordinal: 1, UDTName: "int4", JSONType: schema.TypeInteger, IsPrimaryKey: true},
			{Name: "first_name", Ordinal: 2, UDTName: "text", JSONType: schema.TypeString},
			{Name: "email_address", Ordinal: 3, UDTName: "text", JSONType: schema.TypeString, Nullable: true},
			{Name: "age_text", Ordinal: 4, UDTName: "text", JSONType: schema.TypeString, Nullable: true},
		}},
		{Name: "Order Details", PrimaryKey: []string{"order_id", "line_no"}, RowEstimate: est(30000), Readable: true, Columns: []schema.Column{
			{Name: "order_id", Ordinal: 1, UDTName: "int8", JSONType: schema.TypeInteger, IsPrimaryKey: true},
			{Name: "line_no", Ordinal: 2, UDTName: "int2", JSONType: schema.TypeInteger, IsPrimaryKey: true},
		}},
	}}}}
}

type fakeSchemas struct{ snap *schema.Snapshot }

func (f *fakeSchemas) Latest(context.Context, store.WorkspaceID, string) (store.DiscoverySnapshot, *schema.Snapshot, error) {
	if f.snap == nil {
		return store.DiscoverySnapshot{}, nil, problem.New(http.StatusNotFound, problem.CodeSchemaNotDiscovered, "Not discovered", "")
	}
	return store.DiscoverySnapshot{ID: "snap"}, f.snap, nil
}

// fakeKS is a target that records every call; no create is ever expected from a preview.
type fakeKS struct {
	exists   map[string]bool
	missing  bool // Collection() → 404
	creates  int
	checked  []string
	sample   []json.RawMessage
	sampleOK bool
}

func (f *fakeKS) ListBuckets(context.Context) (target.BucketList, error) {
	return target.BucketList{}, nil
}

func (f *fakeKS) ListScopes(context.Context, string) ([]target.ScopeInfo, error) {
	return nil, nil
}

func (f *fakeKS) Collection(_ context.Context, _, _, c string) (target.CollectionInfo, error) {
	if f.missing {
		return target.CollectionInfo{}, problem.New(http.StatusNotFound, problem.CodeNotFound, "Collection not found", "")
	}
	return target.CollectionInfo{Name: c}, nil
}

func (f *fakeKS) CreateBucket(context.Context, target.BucketSpec) error { f.creates++; return nil }
func (f *fakeKS) CreateScope(context.Context, string, string) error     { f.creates++; return nil }
func (f *fakeKS) CreateCollection(context.Context, string, string, target.CollectionSpec) error {
	f.creates++
	return nil
}

func (f *fakeKS) SampleDocuments(context.Context, string, string, string, int) ([]json.RawMessage, string, error) {
	if !f.sampleOK {
		return nil, "", problem.New(http.StatusUnprocessableEntity, problem.CodeSamplingUnavailable, "Can't sample", "")
	}
	return f.sample, "query", nil
}

func (f *fakeKS) ExistingKeys(_ context.Context, _, _, _ string, keys []string) (map[string]bool, error) {
	f.checked = keys
	out := map[string]bool{}
	for _, k := range keys {
		out[k] = f.exists[k]
	}
	return out, nil
}

type fixture struct {
	svc     *Service
	st      *memstore.Store
	schemas *fakeSchemas
	src     *sourcetest.Fake
	ks      *fakeKS
	project string
}

func newFixture(t *testing.T, withRepos bool) *fixture {
	t.Helper()
	st := memstore.New(storetest.NewClock())
	p, err := st.Projects().Create(ctx, ws, store.NewProject{Name: "P"})
	require.NoError(t, err)
	if withRepos {
		for _, role := range []store.Role{store.RoleSource, store.RoleTarget} {
			_, err := st.Connections().Upsert(ctx, ws, p.ID, role, store.ConnectionInput{Type: "fake", Config: json.RawMessage(`{}`), LastTestStatus: "ok"})
			require.NoError(t, err)
		}
	}
	f := &fixture{st: st, schemas: &fakeSchemas{snap: shopSnapshot()}, src: &sourcetest.Fake{}, ks: &fakeKS{exists: map[string]bool{}}, project: p.ID}
	openSource := func(ctx context.Context, _ store.WorkspaceID, id string) (source.Session, store.Connection, error) {
		sess, err := f.src.Open(ctx, nil, connectors.Credentials{})
		return sess, store.Connection{ID: id}, err
	}
	openKS := func(context.Context, store.WorkspaceID, string) (target.Keyspaces, store.Connection, error) {
		return f.ks, store.Connection{}, nil
	}
	f.svc = NewService(st, f.schemas, openSource, openKS)
	return f
}

func target1() mapping.Target { return mapping.Target{Bucket: "shop", Scope: "sales"} }

func (f *fixture) create(t *testing.T, table string) View {
	t.Helper()
	v, err := f.svc.Create(ctx, ws, "u1", f.project, CreateInput{SourceSchema: "shop", SourceTable: table, Target: target1()})
	require.NoError(t, err)
	return v
}

func TestSanitizeCollectionName_AC1(t *testing.T) {
	cases := map[string]string{
		"Order Details":          "order_details",
		"customer":               "customer",
		"CustomerOrders":         "customer_orders",
		"_hidden":                "hidden",
		"%pct":                   "pct",
		"ümlaut Größe":           "mlaut_gr_e",
		strings.Repeat("a", 300): strings.Repeat("a", 251),
		"___":                    "collection",
		"order-items":            "order_items",
	}
	for in, want := range cases {
		require.Equal(t, want, SanitizeCollectionName(in), in)
	}
}

func TestBulkCreateMappings_AC1_GeneratesSchemaAndAutomap(t *testing.T) {
	f := newFixture(t, true)
	res, err := f.svc.BulkCreate(ctx, ws, "u1", f.project, []CreateInput{
		{SourceSchema: "shop", SourceTable: "customer", Target: target1()},
		{SourceSchema: "shop", SourceTable: "Order Details", Target: target1()},
		{SourceSchema: "shop", SourceTable: "missing", Target: target1()},
	})
	require.NoError(t, err)
	require.Len(t, res, 3)
	require.Equal(t, "customer", res[0].Mapping.Target.Collection)
	require.Equal(t, "order_details", res[1].Mapping.Target.Collection)
	require.Equal(t, problem.CodeNotFound, res[2].Error.Code, "one table's failure doesn't stop the others")
	require.NotNil(t, res[0].Mapping.AutomapStats)
	require.Equal(t, 4, res[0].Mapping.AutomapStats.TotalSourceColumns)
	require.Equal(t, 4, res[0].Mapping.AutomapStats.AutoMapped, "generated fields auto-map at 0.97+")

	v, err := f.svc.Get(ctx, ws, f.project, res[0].Mapping.ID)
	require.NoError(t, err)
	require.Equal(t, "customer::{customer_id}", v.Spec.Key.Template)
	require.Equal(t, mapping.TypeField{Enabled: true, Name: "type", Value: "customer"}, v.Spec.TypeField)
	require.Len(t, v.Spec.DocSchema.Root.Children, 4)
	require.Equal(t, mapping.StatusReady, v.Status)
	require.NotEmpty(t, v.Explain)
	od, _ := f.svc.Get(ctx, ws, f.project, res[1].Mapping.ID)
	require.Equal(t, "order_details::{order_id}::{line_no}", od.Spec.Key.Template)

	_, err = f.svc.Create(ctx, ws, "u1", f.project, CreateInput{SourceSchema: "shop", SourceTable: "customer", Target: target1()})
	require.True(t, problem.HasCode(err, problem.CodeMappingExists))
	list, err := f.svc.List(ctx, ws, f.project)
	require.NoError(t, err)
	require.Len(t, list, 2)
}

func TestMappingsCreate_AC2_RequiresRepositories(t *testing.T) {
	f := newFixture(t, false)
	_, err := f.svc.Create(ctx, ws, "u1", f.project, CreateInput{SourceSchema: "shop", SourceTable: "customer"})
	require.True(t, problem.HasCode(err, problem.CodeRepositoriesIncomplete))
	_, err = f.st.Connections().Upsert(ctx, ws, f.project, store.RoleSource, store.ConnectionInput{Type: "fake", Config: json.RawMessage(`{}`)})
	require.NoError(t, err)
	_, err = f.svc.Create(ctx, ws, "u1", f.project, CreateInput{SourceSchema: "shop", SourceTable: "customer"})
	require.True(t, problem.HasCode(err, problem.CodeRepositoriesIncomplete), "target still missing")
	_, err = f.svc.BulkCreate(ctx, ws, "u1", f.project, []CreateInput{{SourceSchema: "shop", SourceTable: "customer"}})
	require.True(t, problem.HasCode(err, problem.CodeRepositoriesIncomplete))

	g := newFixture(t, true)
	g.schemas.snap = nil
	_, err = g.svc.Create(ctx, ws, "u1", g.project, CreateInput{SourceSchema: "shop", SourceTable: "customer"})
	require.True(t, problem.HasCode(err, problem.CodeSchemaNotDiscovered))
}

func TestProjectStatus_AC3_ReadyWithMappings(t *testing.T) {
	f := newFixture(t, true)
	psvc := projects.NewService(f.st, nil, nil)
	sum, err := psvc.Get(ctx, ws, f.project)
	require.NoError(t, err)
	require.Equal(t, projects.StatusConfigured, sum.Status)
	v := f.create(t, "customer")
	sum, _ = psvc.Get(ctx, ws, f.project)
	require.Equal(t, projects.StatusReady, sum.Status)
	require.Equal(t, 1, sum.ReadyMappings)

	v.Spec.Key.Template = "" // draft
	_, err = f.svc.Update(ctx, ws, "u1", f.project, v.ID, v.Version, UpdateInput{Spec: v.Spec})
	require.NoError(t, err)
	sum, _ = psvc.Get(ctx, ws, f.project)
	require.Equal(t, projects.StatusConfigured, sum.Status)
	require.Equal(t, 1, sum.Mappings)
	require.Equal(t, 0, sum.ReadyMappings)
}

func TestMappingsRepo_AC4_VersionMismatch412(t *testing.T) {
	f := newFixture(t, true)
	v := f.create(t, "customer")
	saved, err := f.svc.Update(ctx, ws, "u1", f.project, v.ID, v.Version, UpdateInput{Spec: v.Spec})
	require.NoError(t, err)
	require.Equal(t, v.Version+1, saved.Version)
	_, err = f.svc.Update(ctx, ws, "u1", f.project, v.ID, v.Version, UpdateInput{Spec: v.Spec})
	p := problem.From(err)
	require.Equal(t, http.StatusPreconditionFailed, p.Status)
	require.Equal(t, problem.CodeMappingVersionConflict, p.Code)
}

func TestMappingSpec_AC6_CreateIfMissingPersisted(t *testing.T) {
	f := newFixture(t, true)
	in := CreateInput{SourceSchema: "shop", SourceTable: "customer", Target: mapping.Target{Bucket: "shop", Scope: "sales", Collection: "people", CreateIfMissing: true}}
	v, err := f.svc.Create(ctx, ws, "u1", f.project, in)
	require.NoError(t, err)
	got, _ := f.svc.Get(ctx, ws, f.project, v.ID)
	require.True(t, got.Target.CreateIfMissing)
	require.Equal(t, "people", got.Target.Collection)
	require.Zero(t, f.ks.creates, "nothing is created until the job's pre-flight")
}

func TestSpecUpdate_AC9_UnknownReferences422(t *testing.T) {
	f := newFixture(t, true)
	v := f.create(t, "customer")
	spec := v.Spec
	spec.FieldMappings = append(spec.FieldMappings, mapping.FieldMapping{ID: "bad", TargetFieldID: "fld_nope", Sources: []mapping.SourceRef{{Column: "first_name"}}, State: mapping.StateActive})
	_, err := f.svc.Update(ctx, ws, "u1", f.project, v.ID, 0, UpdateInput{Spec: spec})
	p := problem.From(err)
	require.Equal(t, http.StatusUnprocessableEntity, p.Status)
	require.Equal(t, problem.CodeMappingInvalid, p.Code)
	require.Equal(t, "fieldMappings.bad", p.FieldErrors[0].Field)
}

func TestAutomap_AC9_ResetAndSnapshot(t *testing.T) {
	f := newFixture(t, true)
	v := f.create(t, "customer")
	spec := v.Spec
	spec.FieldMappings = nil // user cleared everything
	cleared, err := f.svc.Update(ctx, ws, "u1", f.project, v.ID, 0, UpdateInput{Spec: spec})
	require.NoError(t, err)
	require.Zero(t, cleared.Stats.AutoMapped)
	require.Equal(t, 4, cleared.AutomapStats.AutoMapped, "the automap snapshot survives edits")
	res, err := f.svc.Automap(ctx, ws, "u1", f.project, v.ID, cleared.Version, AutomapInput{Mode: "fill"})
	require.NoError(t, err)
	require.Equal(t, 4, res.Stats.AutoMapped)
	require.Equal(t, cleared.Version+1, res.Version)
	require.NotEmpty(t, res.Explain)
	_, err = f.svc.Automap(ctx, ws, "u1", f.project, v.ID, cleared.Version, AutomapInput{})
	require.True(t, problem.HasCode(err, problem.CodeMappingVersionConflict))
}

func TestReplaceDesign_AC7_ReattachAndOrphans(t *testing.T) {
	f := newFixture(t, true)
	v := f.create(t, "customer")
	next := docschema.Schema{Root: docschema.DocField{ID: docschema.RootID, Name: "$", Type: docschema.Object, Children: []docschema.DocField{
		{ID: "n1", Name: "customerId", Type: docschema.Integer},
		{ID: "n2", Name: "contact", Type: docschema.Object, Children: []docschema.DocField{{ID: "n3", Name: "email", Type: docschema.String}}},
	}}}
	res, err := f.svc.ReplaceDesign(ctx, ws, "u1", f.project, v.ID, 0, DesignInput{DocSchema: next, Strategy: "replace"})
	require.NoError(t, err)
	require.Len(t, res.Orphans, 3, "firstName, emailAddress and ageText disappeared")
	paths := res.Mapping.Spec.DocSchema.Paths()
	var mapped []string
	for _, m := range res.Mapping.Spec.FieldMappings {
		mapped = append(mapped, m.Sources[0].Column+"→"+paths[m.TargetFieldID])
	}
	require.ElementsMatch(t, []string{"customer_id→customerId", "email_address→contact.email"}, mapped, "kept by path, then auto-map fills contact.email")

	merged, err := f.svc.ReplaceDesign(ctx, ws, "u1", f.project, v.ID, 0, DesignInput{Strategy: "merge", DocSchema: docschema.Schema{Root: docschema.DocField{
		ID: docschema.RootID, Name: "$", Type: docschema.Object, Children: []docschema.DocField{{ID: "x1", Name: "loyaltyTier", Type: docschema.String}},
	}}})
	require.NoError(t, err)
	require.Len(t, merged.Mapping.Spec.DocSchema.Root.Children, 3)
	require.Empty(t, merged.Orphans)

	_, err = f.svc.ReplaceDesign(ctx, ws, "u1", f.project, v.ID, 0, DesignInput{Strategy: "replace", DocSchema: docschema.Schema{Root: docschema.DocField{ID: "r", Type: docschema.String}}})
	require.True(t, problem.HasCode(err, problem.CodeValidation))
}

func TestValidate_TargetKeyspaceAndCompileIssues(t *testing.T) {
	f := newFixture(t, true)
	v := f.create(t, "customer")
	res, err := f.svc.Validate(ctx, ws, f.project, v.ID)
	require.NoError(t, err)
	require.True(t, res.Ready)

	f.ks.missing = true
	res, _ = f.svc.Validate(ctx, ws, f.project, v.ID)
	require.False(t, res.Ready)
	require.Equal(t, mapping.CodeTargetKeyspaceMissing, res.Issues[0].Code)

	spec := v.Spec
	spec.FieldMappings[1].Transforms = []mapping.Step{{Op: "replace", Args: map[string]any{"pattern": "(", "regex": true}}}
	saved, err := f.svc.Update(ctx, ws, "u1", f.project, v.ID, 0, UpdateInput{Spec: spec})
	require.NoError(t, err)
	require.Equal(t, mapping.StatusDraft, saved.Status, "a bad regex is a compile error")
	require.Equal(t, mapping.CodeTransformInvalid, saved.Issues[0].Code)
}

// --- Preview (OC-022) ---

func customerRows() [][]any {
	return [][]any{
		{int32(1), " Ann ", "ann@example.com", "30"},
		{int32(2), "Bob", nil, "forty"},
		{int32(3), "Cy", "cy@example.com", "25"},
	}
}

func TestPreview_AC3_UsesCompiledProgram(t *testing.T) {
	f := newFixture(t, true)
	f.src.PreviewRows = customerRows()
	v := f.create(t, "customer")
	res, err := f.svc.Preview(ctx, ws, f.project, v.ID, PreviewInput{})
	require.NoError(t, err)
	require.Len(t, res.Items, 3)
	prog, issues := transform.Compile(&v.Spec, shopSnapshot().Table("shop", "customer"), transform.Options{})
	require.Empty(t, issues)
	for i, row := range customerRows() {
		key, doc, _, rerr := prog.Apply(row, nil)
		require.Nil(t, rerr)
		require.Equal(t, key, res.Items[i].Key)
		require.Equal(t, string(doc), string(res.Items[i].Document), "byte-identical to the pipeline")
	}
	require.Equal(t, "customer::1", res.Items[0].Key)
	require.Equal(t, PreviewSummary{OK: 3, AvgSizeBytes: res.Summary.AvgSizeBytes, MaxSizeBytes: res.Summary.MaxSizeBytes}, *res.Summary)
	require.Equal(t, map[string]any{"customer_id": int64(1), "first_name": " Ann ", "email_address": "ann@example.com", "age_text": "30"}, res.Items[0].SourceRow)
	require.Equal(t, []string{"customer_id"}, f.src.LastPreview.OrderBy, "first mode orders by the PK")
	require.Equal(t, 10, f.src.LastPreview.TimeoutSec)
}

func TestPreview_AC3_NoTargetWrites(t *testing.T) {
	f := newFixture(t, true)
	f.src.PreviewRows = customerRows()
	v := f.create(t, "customer")
	_, err := f.svc.Preview(ctx, ws, f.project, v.ID, PreviewInput{CheckKeys: true})
	require.NoError(t, err)
	require.Zero(t, f.ks.creates)
}

func TestPreview_AC3_SpecOverride(t *testing.T) {
	f := newFixture(t, true)
	f.src.PreviewRows = customerRows()
	v := f.create(t, "customer")
	unsaved := v.Spec
	unsaved.Key.Template = "c::{first_name|trim|lower}"
	res, err := f.svc.Preview(ctx, ws, f.project, v.ID, PreviewInput{SpecOverride: &unsaved})
	require.NoError(t, err)
	require.Equal(t, "c::ann", res.Items[0].Key)
	stored, _ := f.svc.Get(ctx, ws, f.project, v.ID)
	require.Equal(t, "customer::{customer_id}", stored.Spec.Key.Template, "nothing saved")
}

func TestPreview_AC4_LimitCap(t *testing.T) {
	f := newFixture(t, true)
	v := f.create(t, "customer")
	for _, c := range []struct{ in, want int }{{500, 50}, {0, 10}, {-3, 1}, {25, 25}} {
		_, err := f.svc.Preview(ctx, ws, f.project, v.ID, PreviewInput{Limit: c.in})
		require.NoError(t, err)
		require.Equal(t, c.want, f.src.LastPreview.Limit, "limit %d", c.in)
	}
	_, err := f.svc.Preview(ctx, ws, f.project, v.ID, PreviewInput{SampleMode: "random"})
	require.NoError(t, err)
	require.True(t, f.src.LastPreview.Random)
	require.Equal(t, int64(2000), f.src.LastPreview.RowEstimate)

	f.src.PreviewErr = &pgconn.PgError{Code: "57014", Message: "canceling statement due to statement timeout"}
	_, err = f.svc.Preview(ctx, ws, f.project, v.ID, PreviewInput{})
	require.True(t, problem.HasCode(err, problem.CodePreviewTimeout))
	f.src.PreviewErr = errors.New("connection reset")
	_, err = f.svc.Preview(ctx, ws, f.project, v.ID, PreviewInput{})
	require.True(t, strings.HasPrefix(problem.From(err).Code, "SOURCE_"))
}

func TestPreview_AC5_RowErrorIsolated(t *testing.T) {
	f := newFixture(t, true)
	f.src.PreviewRows = customerRows()
	v := f.create(t, "customer")
	spec := v.Spec
	for i, m := range spec.FieldMappings {
		if m.Sources[0].Column == "age_text" {
			spec.FieldMappings[i].Transforms = []mapping.Step{{Op: "parseInteger", Args: map[string]any{"strict": true}}}
			spec.DocSchema.Find(m.TargetFieldID).Type = docschema.Integer
		}
	}
	res, err := f.svc.Preview(ctx, ws, f.project, v.ID, PreviewInput{SpecOverride: &spec})
	require.NoError(t, err)
	require.Equal(t, 2, res.Summary.OK)
	require.Equal(t, 1, res.Summary.Errors)
	require.Equal(t, "ageText", res.Items[1].Error.FieldPath)
	require.Equal(t, transform.CodeParseFailed, res.Items[1].Error.Code)
	require.Nil(t, res.Items[0].Error)
}

func TestPreview_AC6_TraceAndFieldPreview(t *testing.T) {
	f := newFixture(t, true)
	f.src.PreviewRows = customerRows()
	v := f.create(t, "customer")
	spec := v.Spec
	var firstID string
	for i, m := range spec.FieldMappings {
		if m.Sources[0].Column == "first_name" {
			spec.FieldMappings[i].Transforms = []mapping.Step{{Op: "trim"}, {Op: "upper"}}
			firstID = m.ID
		}
	}
	res, err := f.svc.Preview(ctx, ws, f.project, v.ID, PreviewInput{SpecOverride: &spec})
	require.NoError(t, err)
	require.Equal(t, []transform.TraceStep{{Step: "source", Value: " Ann "}, {Step: "trim", Value: "Ann"}, {Step: "upper", Value: "ANN"}}, res.Items[0].Trace["firstName"])

	fp, err := f.svc.Preview(ctx, ws, f.project, v.ID, PreviewInput{SpecOverride: &spec, FieldMappingID: firstID, Limit: 3})
	require.NoError(t, err)
	require.Len(t, fp.Fields, 3)
	require.Equal(t, " Ann ", fp.Fields[0].Before)
	require.Equal(t, "ANN", fp.Fields[0].After)
	_, err = f.svc.Preview(ctx, ws, f.project, v.ID, PreviewInput{FieldMappingID: "nope"})
	require.True(t, problem.HasCode(err, problem.CodeNotFound))
}

func TestPreview_AC7_KeyCollisions(t *testing.T) {
	f := newFixture(t, true)
	f.src.PreviewRows = customerRows()
	f.ks.exists = map[string]bool{"customer::1": true, "customer::3": true}
	v := f.create(t, "customer")
	res, err := f.svc.Preview(ctx, ws, f.project, v.ID, PreviewInput{CheckKeys: true, WriteMode: "insert"})
	require.NoError(t, err)
	require.Equal(t, 2, res.Summary.Collisions)
	require.True(t, *res.Items[0].KeyExists)
	require.False(t, *res.Items[1].KeyExists)
	require.Equal(t, "insert", res.WriteMode)
	require.Equal(t, []string{"customer::1", "customer::2", "customer::3"}, f.ks.checked)
	res, _ = f.svc.Preview(ctx, ws, f.project, v.ID, PreviewInput{})
	require.Nil(t, res.Items[0].KeyExists, "only when asked")
	require.Equal(t, "upsert", res.WriteMode)
}

// --- HTTP ---

func router(f *fixture, role store.MemberRole) http.Handler {
	withPrincipal := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), auth.Principal{UserID: "u1", WorkspaceID: ws, Role: role})))
		})
	}
	return httpapi.NewRouter(httpapi.Options{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Features: []httpapi.Registrar{Handlers{Svc: f.svc}},
		Middleware: []func(http.Handler) http.Handler{withPrincipal},
	})
}

func call(h http.Handler, method, path, body string, headers ...string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	h.ServeHTTP(rec, req)
	return rec
}

func TestMappingHandlers_CRUD(t *testing.T) {
	f := newFixture(t, true)
	h := router(f, store.RoleEditor)
	base := "/api/v1/projects/" + f.project + "/mappings"
	rec := call(h, "POST", base, `{"sourceSchema":"shop","sourceTable":"customer","target":{"bucket":"shop","scope":"sales"}}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	require.Equal(t, `"1"`, rec.Header().Get("ETag"))
	var v View
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &v))

	rec = call(h, "POST", base+":bulk", `{"items":[{"sourceSchema":"shop","sourceTable":"Order Details","target":{"bucket":"shop","scope":"sales"}}]}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), `"collection":"order_details"`)

	rec = call(h, "GET", base, "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"sourceTable":"Order Details"`)
	require.NotContains(t, rec.Body.String(), `"spec"`, "the list has no specs")

	body, _ := json.Marshal(UpdateInput{Spec: v.Spec})
	rec = call(h, "PUT", base+"/"+v.ID, string(body), "If-Match", `"1"`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, `"2"`, rec.Header().Get("ETag"))
	rec = call(h, "PUT", base+"/"+v.ID, string(body), "If-Match", `"1"`)
	require.Equal(t, http.StatusPreconditionFailed, rec.Code)
	require.Equal(t, http.StatusBadRequest, call(h, "PUT", base+"/"+v.ID, string(body), "If-Match", "nope").Code)

	require.Equal(t, http.StatusOK, call(h, "POST", base+"/"+v.ID+"/automap", `{"mode":"reset"}`).Code)
	require.Equal(t, http.StatusOK, call(h, "POST", base+"/"+v.ID+"/automap", "").Code)
	require.Equal(t, http.StatusUnprocessableEntity, call(h, "POST", base+"/"+v.ID+"/automap", `{"mode":"wild"}`).Code)
	require.Equal(t, http.StatusOK, call(h, "POST", base+"/"+v.ID+"/validate", "").Code)
	rec = call(h, "POST", base+"/"+v.ID+"/design", `{"strategy":"merge","docSchema":{"root":{"id":"fld_root","name":"$","type":"object","children":[{"id":"z","name":"tier","type":"string"}]}}}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), `"orphans":[]`)

	f.src.PreviewRows = customerRows()
	rec = call(h, "POST", base+"/"+v.ID+"/preview", `{"limit":2}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "no-store", rec.Header().Get("Cache-Control"))

	require.Equal(t, http.StatusNoContent, call(h, "DELETE", base+"/"+v.ID, "").Code)
	require.Equal(t, http.StatusNotFound, call(h, "GET", base+"/"+v.ID, "").Code)
	require.Equal(t, http.StatusNotFound, call(h, "DELETE", base+"/"+v.ID, "").Code)

	viewer := router(f, store.RoleViewer)
	require.Equal(t, http.StatusForbidden, call(viewer, "POST", base, `{"sourceSchema":"shop","sourceTable":"customer"}`).Code)
	require.Equal(t, http.StatusOK, call(viewer, "GET", base, "").Code)
}

func TestMappingHandlers_OtherProjectIsNotFound(t *testing.T) {
	f := newFixture(t, true)
	v := f.create(t, "customer")
	other, err := f.st.Projects().Create(ctx, ws, store.NewProject{Name: "Other"})
	require.NoError(t, err)
	h := router(f, store.RoleEditor)
	require.Equal(t, http.StatusNotFound, call(h, "GET", "/api/v1/projects/"+other.ID+"/mappings/"+v.ID, "").Code)
	require.Equal(t, http.StatusNotFound, call(h, "DELETE", "/api/v1/projects/"+other.ID+"/mappings/"+v.ID, "").Code)
	rec := call(h, "GET", "/api/v1/projects/"+other.ID+"/mappings", "")
	require.JSONEq(t, `{"items":[]}`, rec.Body.String())
	require.Equal(t, http.StatusNotFound, call(h, "GET", "/api/v1/projects/nope/mappings", "").Code)
}

func TestInferHandlers_AC2_AC3(t *testing.T) {
	f := newFixture(t, true)
	h := router(f, store.RoleViewer)
	body, _ := json.Marshal(map[string]string{"kind": "json-sample", "content": `[{"a":1},{"a":2.5,"b":"2024-01-01T00:00:00Z"}]`})
	rec := call(h, "POST", "/api/v1/doc-schemas/infer", string(body))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var res InferResult
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &res))
	require.Equal(t, docschema.Number, res.DocSchema.Root.Children[0].Type)

	body, _ = json.Marshal(map[string]string{"kind": "json-schema", "content": `{"type":"object","properties":{"x":{"type":"string"}}}`})
	require.Equal(t, http.StatusOK, call(h, "POST", "/api/v1/doc-schemas/infer", string(body)).Code)
	body, _ = json.Marshal(map[string]string{"kind": "json-sample", "content": "{oops"})
	rec = call(h, "POST", "/api/v1/doc-schemas/infer", string(body))
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	require.Contains(t, rec.Body.String(), "line 1")
	require.Equal(t, http.StatusUnprocessableEntity, call(h, "POST", "/api/v1/doc-schemas/infer", `{"kind":"xml","content":"x"}`).Code)
	require.Equal(t, http.StatusBadRequest, call(h, "POST", "/api/v1/doc-schemas/infer", `nope`).Code)

	rec = call(h, "POST", "/api/v1/doc-schemas/export", `{"docSchema":{"root":{"id":"fld_root","name":"$","type":"object","children":[{"id":"a","name":"a","type":"integer","required":true}]}}}`)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "application/schema+json", rec.Header().Get("Content-Type"))
	require.Contains(t, rec.Body.String(), `"required": [`)

	url := "/api/v1/connections/p1%3A%3Atarget/buckets/shop/scopes/sales/collections/customer/infer-schema"
	rec = call(h, "POST", url, "")
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	require.Contains(t, rec.Body.String(), problem.CodeSamplingUnavailable)
	f.ks.sampleOK = true
	require.Equal(t, http.StatusUnprocessableEntity, call(h, "POST", url, "").Code, "no documents")
	f.ks.sample = []json.RawMessage{json.RawMessage(`{"name":"x","at":"2024-01-01T00:00:00Z"}`)}
	rec = call(h, "POST", url, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), `"source":"collection-sample"`)
	require.Contains(t, rec.Body.String(), `"method":"query"`)
	_ = time.Second
}

func TestAutomapHandler_AC9_ReturnsSpecStatsExplain(t *testing.T) {
	f := newFixture(t, true)
	v := f.create(t, "customer")
	h := router(f, store.RoleEditor)
	base := "/api/v1/projects/" + f.project + "/mappings/" + v.ID

	// Start from an unmapped spec so "fill" has everything to do.
	cleared := v.Spec
	cleared.FieldMappings = nil
	body, _ := json.Marshal(UpdateInput{Spec: cleared})
	require.Equal(t, http.StatusOK, call(h, "PUT", base, string(body), "If-Match", `"1"`).Code)

	rec := call(h, "POST", base+"/automap", `{"mode":"fill"}`, "If-Match", `"2"`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, `"3"`, rec.Header().Get("ETag"))
	var out View
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.EqualValues(t, 3, out.Version)
	require.NotEmpty(t, out.Spec.FieldMappings)
	require.NotNil(t, out.AutomapStats)
	require.Positive(t, out.AutomapStats.AutoMapped)
	require.Equal(t, out.AutomapStats.AutoMapped, out.Stats.AutoMapped)
	for _, m := range out.Spec.FieldMappings {
		if m.Origin == mapping.OriginAuto || m.Origin == mapping.OriginSuggested {
			e, ok := out.Explain[m.ID]
			require.True(t, ok, "explain for %s", m.ID)
			require.NotEmpty(t, e.Reason)
		}
	}
}

func TestAutomapHandler_AC9_VersionConflict(t *testing.T) {
	f := newFixture(t, true)
	v := f.create(t, "customer")
	h := router(f, store.RoleEditor)
	base := "/api/v1/projects/" + f.project + "/mappings/" + v.ID
	require.Equal(t, http.StatusOK, call(h, "POST", base+"/automap", `{"mode":"reset"}`, "If-Match", `"1"`).Code)

	rec := call(h, "POST", base+"/automap", `{"mode":"fill"}`, "If-Match", `"1"`)
	require.Equal(t, http.StatusPreconditionFailed, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), string(problem.CodeMappingVersionConflict))
	got, err := f.svc.Get(ctx, ws, f.project, v.ID)
	require.NoError(t, err)
	require.EqualValues(t, 2, got.Version, "a rejected automap writes nothing")
}

func TestMappings_UpdateAuditIsThrottledPerMapping(t *testing.T) {
	f := newFixture(t, true)
	now := time.Unix(1_800_000_000, 0)
	f.svc.now = func() time.Time { return now }
	v := f.create(t, "customer")
	count := func() int {
		evs, _, err := f.st.Audit().Search(ctx, ws, store.AuditFilter{Action: "mapping.update"}, store.Page{Limit: 100})
		require.NoError(t, err)
		return len(evs)
	}
	for range 5 { // the canvas autosaves
		saved, err := f.svc.Update(ctx, ws, "u1", f.project, v.ID, v.Version, UpdateInput{Spec: v.Spec})
		require.NoError(t, err)
		v = saved
		now = now.Add(time.Minute)
	}
	require.Equal(t, 1, count(), "one entry per mapping per 10 minutes")
	now = now.Add(10 * time.Minute)
	_, err := f.svc.Update(ctx, ws, "u1", f.project, v.ID, v.Version, UpdateInput{Spec: v.Spec})
	require.NoError(t, err)
	require.Equal(t, 2, count())
}
