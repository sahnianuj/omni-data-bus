package mappings

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/mapping"
	"omnidatabus/internal/store"
)

// v1Customer is a 1.0 spec as 1.0 stored it: embeds[] and a source {embed}.
const v1Customer = `{
  "docSchema": {"root": {"id": "fld_root", "name": "$", "type": "object", "children": [
    {"id": "f_id", "name": "customerId", "type": "integer"},
    {"id": "f_orders", "name": "orders", "type": "array", "items": {"id": "f_o", "name": "[]", "type": "object", "children": [
      {"id": "f_total", "name": "total", "type": "number"}]}}]}, "source": "generated"},
  "key": {"template": "customer::{customer_id}"},
  "typeField": {"enabled": false, "name": "type", "value": ""},
  "nullHandling": "include",
  "fieldMappings": [
    {"id": "m_id", "targetFieldId": "f_id", "sources": [{"column": "customer_id"}], "transforms": [], "origin": "auto", "state": "active"},
    {"id": "m_total", "targetFieldId": "f_total", "sources": [{"column": "total", "embed": "emb_o"}], "transforms": [], "origin": "auto", "state": "active"}],
  "embeds": [{"id": "emb_o", "kind": "one-to-many", "table": {"schema": "shop", "name": "orders"},
    "join": [{"parent": "customer_id", "child": "customer_id"}], "targetFieldId": "f_orders"}],
  "ignoredColumns": []
}`

// OC-037 AC3 (service level; the Couchbase store is covered by the integration test): reading a
// stored 1.0 mapping returns v2 without writing it, so the version (ETag) doesn't move; the next
// save stores v2.
func TestMappingsRepo_AC3_UpgradeOnReadNoWrite(t *testing.T) {
	f := newFixture(t, true)
	withOrders(f)
	m, err := f.st.Mappings().Create(ctx, ws, store.Mapping{
		ProjectID: f.project, SourceSchema: "shop", SourceTable: "customer", Spec: json.RawMessage(v1Customer), Status: "draft",
		Target: store.MappingTarget{Bucket: "shop", Scope: "sales", Collection: "customer"},
	})
	require.NoError(t, err)
	h := router(f, store.RoleEditor)
	url := "/api/v1/projects/" + f.project + "/mappings/" + m.ID

	for range 2 {
		rec := call(h, "GET", url, "")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		require.Equal(t, `"1"`, rec.Header().Get("ETag"), "a read never writes")
		var v View
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &v))
		require.Equal(t, mapping.SpecVersion, v.Spec.SpecVersion)
		require.Equal(t, "emb_o", v.Spec.Relations[0].ID)
		require.Nil(t, v.Spec.Relations[0].ParentID)
		require.Equal(t, "emb_o", v.Spec.FieldMappings[1].Sources[0].Relation)
		require.Equal(t, &mapping.Root{Table: mapping.TableRef{Schema: "shop", Name: "customer"}}, v.Spec.Root)
		require.Equal(t, "couchbase", v.Location.Kind)
		require.NotContains(t, rec.Body.String(), `"embeds"`)
	}
	stored, err := f.st.Mappings().Get(ctx, ws, m.ID)
	require.NoError(t, err)
	require.Equal(t, 1, mapping.VersionOf(stored.Spec), "still v1 in the store")
	require.EqualValues(t, 1, stored.Version)

	// Saving the spec it read writes v2 and bumps the version once.
	var v View
	require.NoError(t, json.Unmarshal(call(h, "GET", url, "").Body.Bytes(), &v))
	body, _ := json.Marshal(map[string]any{"spec": v.Spec})
	rec := call(h, "PUT", url, string(body), "If-Match", `"1"`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, `"2"`, rec.Header().Get("ETag"))
	stored, err = f.st.Mappings().Get(ctx, ws, m.ID)
	require.NoError(t, err)
	require.Equal(t, mapping.SpecVersion, mapping.VersionOf(stored.Spec))
	require.NotContains(t, string(stored.Spec), `"embeds"`)
	require.NotContains(t, string(stored.Spec), `"embed"`)

	// A 1.0 client (an old browser tab) still saves {embed} sources and embeds[]: accepted and stored as v2.
	rec = call(h, "PUT", url, `{"spec":`+v1Customer+`}`, "If-Match", `"2"`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	stored, _ = f.st.Mappings().Get(ctx, ws, m.ID)
	require.Equal(t, mapping.SpecVersion, mapping.VersionOf(stored.Spec))
	require.Contains(t, string(stored.Spec), `"relation":"emb_o"`)
}

// OC-037: relation features the resolver doesn't support yet are refused on save.
func TestValidate_OC037_UnsupportedRelation(t *testing.T) {
	f := newFixture(t, true)
	withOrders(f)
	v, err := f.svc.Create(ctx, ws, "u1", f.project, CreateInput{SourceSchema: "shop", SourceTable: "customer", Target: target1()})
	require.NoError(t, err)
	spec := v.Spec
	parent := "nope"
	spec.Relations = append(spec.Relations, mapping.Relation{ID: "r1", ParentID: &parent, Kind: mapping.EmbedManyToOne, Table: mapping.TableRef{Schema: "shop", Name: "orders"}})
	got, err := f.svc.Update(ctx, ws, "u1", f.project, v.ID, 0, UpdateInput{Spec: spec})
	require.NoError(t, err)
	var codes []string
	for _, i := range got.Issues {
		codes = append(codes, i.Code)
	}
	require.Contains(t, codes, mapping.CodeRelationParentMissing)
	require.Equal(t, "draft", got.Status)
}
