package export

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connections"
	"omnidatabus/internal/docschema"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/problem"
	"omnidatabus/internal/store"
)

func encode(t *testing.T, b Bundle) []byte {
	t.Helper()
	raw, err := Encode(b)
	require.NoError(t, err)
	return raw
}

func TestBundle_SchemaFileMatchesAPI(t *testing.T) {
	api, err := os.ReadFile("../../../api/bundle.schema.json")
	require.NoError(t, err)
	require.Equal(t, string(api), string(bundleSchemaJSON), "internal/export/bundle.schema.json must be a copy of api/bundle.schema.json")
}

func TestBundle_AC1_SchemaValid(t *testing.T) {
	f := newFixture(t)
	for _, req := range []Request{externalReq(), {CredentialMode: ModeSealed, Passphrase: passphrase}, {CredentialMode: ModePlaintext}} {
		out := f.build(t, req)
		raw := encode(t, out.Bundle)
		require.Empty(t, SchemaErrors(raw), req.CredentialMode)
		got, err := Parse(raw, "2.0.0")
		require.NoError(t, err, req.CredentialMode)
		require.Len(t, got.Mappings, 2)
		require.NoError(t, got.Verify())
	}
}

func TestBundle_AC1_Deterministic(t *testing.T) {
	f := newFixture(t)
	a := encode(t, f.build(t, externalReq()).Bundle)
	f.b.Now = func() time.Time { return time.Date(2026, 10, 2, 8, 30, 0, 0, time.UTC) }
	b := encode(t, f.build(t, externalReq()).Bundle)
	require.NotEqual(t, a, b, "createdAt differs, and with it the integrity hash")
	strip := func(raw []byte) string {
		var m map[string]any
		require.NoError(t, json.Unmarshal(raw, &m))
		delete(m, "createdAt")
		delete(m, "integrity")
		c, err := Canonical(m)
		require.NoError(t, err)
		return string(c)
	}
	require.Equal(t, strip(a), strip(b))
	// The file is canonical JSON, indented: re-encoding a parsed bundle gives the same bytes.
	got, err := Parse(a, "2.0.0")
	require.NoError(t, err)
	require.Equal(t, a, encode(t, *got))
}

func TestBundle_AC3_NoSecretsScanner(t *testing.T) {
	f := newFixture(t)
	for _, req := range []Request{externalReq(), {CredentialMode: ModeSealed, Passphrase: passphrase}} {
		out := f.build(t, req)
		raw := encode(t, out.Bundle)
		require.ElementsMatch(t, []string{pgPassword, cbPassword}, out.Secrets)
		require.Empty(t, ScanForSecrets(raw, pgPassword, cbPassword, passphrase), req.CredentialMode)
	}
	// The scanner itself finds plaintext credentials.
	raw := encode(t, f.build(t, Request{CredentialMode: ModePlaintext}).Bundle)
	require.NotEmpty(t, ScanForSecrets(raw, pgPassword))
	require.NotEmpty(t, ScanForSecrets([]byte(`{"password":"x"}`)))
	require.NotEmpty(t, ScanForSecrets([]byte("-----BEGIN RSA PRIVATE KEY-----")))

	// External refs name env vars, never values.
	b := f.build(t, externalReq()).Bundle
	require.Equal(t, map[string]string{"password": "env:ODB_SOURCE_PASSWORD"}, b.Source.Credentials.Refs)
	require.Equal(t, map[string]string{"password": "env:ODB_TARGET_PASSWORD"}, b.Target.Credentials.Refs)
}

func TestBundle_AC3_IncludesUsedRelationships(t *testing.T) {
	f := newFixture(t)
	b := f.build(t, externalReq()).Bundle
	require.Len(t, b.Relationships, 1)
	require.Equal(t, f.usedRel, b.Relationships[0].ID)
	// order_items not ready: it isn't exported, and the relationship isn't used.
	f.draft(t, f.items)
	b = f.build(t, Request{CredentialMode: ModeExternal}).Bundle
	require.Empty(t, b.Relationships)
	require.Len(t, b.Mappings, 1)
}

func TestBundle_AC3_StripsDesignTimeData(t *testing.T) {
	f := newFixture(t)
	var spec mapping.Spec
	require.NoError(t, json.Unmarshal(f.orders.Spec, &spec))
	spec.DocSchema.Reference = json.RawMessage(`{"id":1,"status":"PAID"}`)
	spec.DocSchema.ConstantHints = []docschema.ConstantHint{{FieldID: "f1", Value: json.RawMessage(`"PAID"`)}}
	spec.DocSchema.Root.Children[0].Example = json.RawMessage(`42`)
	f.orders.Spec, _ = json.Marshal(spec)
	_, err := f.st.Mappings().Update(ctx, ws, f.orders)
	require.NoError(t, err)

	raw := encode(t, f.build(t, externalReq()).Bundle)
	for _, s := range []string{`"reference"`, `"constantHints"`, `"example"`} {
		require.NotContains(t, string(raw), s)
	}
}

// TestBundle_AC4_TargetAsConfigured: the bundle carries the project's own target and every
// ready mapping; changing either happens in the tool's steps (user decision, 2026-10-02).
func TestBundle_AC4_TargetAsConfigured(t *testing.T) {
	f := newFixture(t)
	b := f.build(t, externalReq()).Bundle
	require.Equal(t, store.KindCouchbase, b.Target.Type)
	require.JSONEq(t, `{"connectionString":"couchbases://cb.example.com","username":"cbuser","deployment":"self-managed"}`, string(b.Target.Config))
	require.Equal(t, "shop", b.Mappings[0].Location.Bucket)

	f.toDirectory(t, "/data/out")
	b = f.build(t, Request{CredentialMode: ModeSealed, Passphrase: passphrase}).Bundle
	require.Equal(t, store.KindDirectory, b.Target.Type)
	require.Nil(t, b.Target.Credentials, "a directory has no credentials")
	var cfg map[string]any
	require.NoError(t, json.Unmarshal(b.Target.Config, &cfg))
	require.Equal(t, "/data/out", cfg["path"], "the runner can write elsewhere with --target-path / ODB_TARGET_PATH")
	for _, m := range b.Mappings {
		require.Equal(t, store.KindDirectory, m.Location.Kind)
	}
	// Keys don't change with the target kind (user decision, 2026-09-29).
	require.Contains(t, string(b.Mappings[0].Spec), `"template":"orders::{id}"`)
	require.Len(t, b.Mappings, 2, "every ready mapping")
}

func TestExports_AC6_BundledTargetRefused(t *testing.T) {
	f := newFixture(t)
	tgt, err := f.st.Connections().Get(ctx, ws, store.ConnectionID(f.project.ID, store.RoleTarget))
	require.NoError(t, err)
	_, err = f.st.Connections().Upsert(ctx, ws, f.project.ID, store.RoleTarget, store.ConnectionInput{
		Type: tgt.Type, Name: connections.BundledName, Config: json.RawMessage(`{"connectionString":"couchbase://127.0.0.1","username":"Administrator"}`),
		Secret: tgt.Secret, SecretMeta: tgt.SecretMeta,
	})
	require.NoError(t, err)
	_, err = f.b.Build(ctx, ws, "u", f.project.ID, externalReq())
	require.True(t, problem.HasCode(err, problem.CodeBundledTarget), "%v", err)
	require.Contains(t, err.Error(), "Target step")
}

func TestExports_NothingReady(t *testing.T) {
	f := newFixture(t)
	f.draft(t, f.orders)
	f.draft(t, f.items)
	_, err := f.b.Build(ctx, ws, "u", f.project.ID, externalReq())
	require.True(t, problem.HasCode(err, problem.CodeNoReadyMappings), "%v", err)
}

func TestExports_AC6_InvalidMappingRefused(t *testing.T) {
	f := newFixture(t)
	var spec mapping.Spec
	require.NoError(t, json.Unmarshal(f.orders.Spec, &spec))
	spec.Key.Template = "orders::{missing_column}"
	f.orders.Spec, _ = json.Marshal(spec)
	_, err := f.st.Mappings().Update(ctx, ws, f.orders)
	require.NoError(t, err)
	_, err = f.b.Build(ctx, ws, "u", f.project.ID, externalReq())
	require.True(t, problem.HasCode(err, problem.CodeMappingInvalid), "%v", err)
	require.Contains(t, err.Error(), "public.orders")
}

func TestBundle_PlanHashIgnoresCredentialsAndTime(t *testing.T) {
	f := newFixture(t)
	a := f.build(t, externalReq()).Bundle
	f.b.Now = time.Now
	b := f.build(t, Request{CredentialMode: ModeSealed, Passphrase: passphrase}).Bundle
	require.Equal(t, a.PlanHash(), b.PlanHash())
	f.draft(t, f.items)
	c := f.build(t, Request{CredentialMode: ModeExternal}).Bundle
	require.NotEqual(t, a.PlanHash(), c.PlanHash())
}

func TestBundle_EncodeIsIndentedCanonical(t *testing.T) {
	f := newFixture(t)
	raw := encode(t, f.build(t, externalReq()).Bundle)
	require.True(t, bytes.HasPrefix(raw, []byte("{\n  \"bundleVersion\": 1,\n")), string(raw[:40]))
	require.True(t, strings.HasSuffix(string(raw), "}\n"))
}
