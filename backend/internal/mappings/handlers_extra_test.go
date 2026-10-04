package mappings

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/store"
)

func TestMappingHandlers_BadRequestsAndUnknownMappings(t *testing.T) {
	f := newFixture(t, true)
	h := router(f, store.RoleEditor)
	base := "/api/v1/projects/" + f.project + "/mappings"
	v := f.create(t, "customer")
	m := base + "/" + v.ID

	for _, c := range []struct{ method, path string }{
		{"POST", base},
		{"POST", base + ":bulk"},
		{"PUT", m},
		{"POST", m + "/automap"},
		{"POST", m + "/embeds"},
		{"POST", m + "/design"},
		{"POST", m + "/preview"},
		{"POST", m + "/suggest-bindings"},
		{"POST", "/api/v1/doc-schemas/export"},
		{"POST", "/api/v1/doc-schemas/reconcile"},
	} {
		rec := call(h, c.method, c.path, "{")
		require.Equal(t, http.StatusBadRequest, rec.Code, c.method+" "+c.path+": "+rec.Body.String())
	}
	for _, path := range []string{m + "/automap", m + "/embeds", m + "/design"} {
		rec := call(h, "POST", path, "{}", "If-Match", "nope")
		require.Equal(t, http.StatusBadRequest, rec.Code, path)
	}

	gone := base + "/nope"
	for _, c := range []struct{ method, path, body string }{
		{"PUT", gone, `{"spec":{}}`},
		{"POST", gone + "/automap", ""},
		{"POST", gone + "/validate", ""},
		{"POST", gone + "/preview", ""},
		{"POST", gone + "/suggest-bindings", ""},
		{"GET", gone + "/mapping-sheet", ""},
		{"POST", gone + "/embeds", embedOrders},
		{"POST", gone + "/design", `{"strategy":"merge","docSchema":{"root":{"id":"fld_root","name":"$","type":"object"}}}`},
	} {
		rec := call(h, c.method, c.path, c.body)
		require.Contains(t, []int{http.StatusNotFound, http.StatusUnprocessableEntity}, rec.Code, c.method+" "+c.path+": "+rec.Body.String())
	}
	rec := call(h, "POST", "/api/v1/projects/nope/mappings", `{"sourceSchema":"shop","sourceTable":"customer","target":{"bucket":"shop","scope":"sales"}}`)
	require.Equal(t, http.StatusNotFound, rec.Code)
	rec = call(h, "POST", "/api/v1/projects/nope/mappings:bulk", `{"items":[{"sourceSchema":"shop","sourceTable":"customer","target":{"bucket":"shop","scope":"sales"}}]}`)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

const embedOrders = `{"kind":"one-to-many","table":{"schema":"shop","name":"orders"},` +
	`"join":[{"parent":"customer_id","child":"customer_id"}],"targetName":"orders"}`

func TestAddEmbedHandler(t *testing.T) {
	f := newFixture(t, true)
	withOrders(f)
	h := router(f, store.RoleEditor)
	v := f.create(t, "customer")
	embeds := "/api/v1/projects/" + f.project + "/mappings/" + v.ID + "/embeds"
	rec := call(h, "POST", embeds, embedOrders, "If-Match", `"1"`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, `"2"`, rec.Header().Get("ETag"))
	require.Contains(t, rec.Body.String(), `"kind":"one-to-many"`)
	require.Equal(t, http.StatusPreconditionFailed, call(h, "POST", embeds, embedOrders, "If-Match", `"1"`).Code)
}

func TestMappingSheetHandler(t *testing.T) {
	f := newFixture(t, true)
	h := router(f, store.RoleViewer)
	v := f.create(t, "customer")
	sheet := "/api/v1/projects/" + f.project + "/mappings/" + v.ID + "/mapping-sheet"

	rec := call(h, "GET", sheet, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "text/csv; charset=utf-8", rec.Header().Get("Content-Type"))
	require.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	require.Contains(t, rec.Header().Get("Content-Disposition"), `filename="shop-customer-mapping-sheet-`)
	require.True(t, strings.HasSuffix(rec.Header().Get("Content-Disposition"), `.csv"`))
	require.NotEmpty(t, rec.Body.String())

	rec = call(h, "GET", sheet+"?format=md", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "text/markdown; charset=utf-8", rec.Header().Get("Content-Type"))
	require.Contains(t, rec.Body.String(), "shop.customer")

	require.Equal(t, http.StatusBadRequest, call(h, "GET", sheet+"?format=pdf", "").Code)
}

func TestReconcileAndExportHandlers(t *testing.T) {
	f := newFixture(t, true)
	h := router(f, store.RoleViewer)
	current := `{"root":{"id":"fld_root","name":"$","type":"object","children":[{"id":"f1","name":"name","type":"string"}]}}`
	incoming := `{"root":{"id":"fld_root","name":"$","type":"object","children":[{"id":"x","name":"name","type":"string"},{"id":"y","name":"tier","type":"string"}]}}`

	rec := call(h, "POST", "/api/v1/doc-schemas/reconcile", `{"current":`+current+`,"incoming":`+incoming+`}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), `"id":"f1"`, "the current ID is kept")
	require.Contains(t, rec.Body.String(), `"diff"`)

	dup := `{"root":{"id":"fld_root","name":"$","type":"object","children":[{"id":"a","name":"n","type":"string"},{"id":"b","name":"n","type":"string"}]}}`
	rec = call(h, "POST", "/api/v1/doc-schemas/reconcile", `{"current":`+dup+`,"incoming":`+dup+`}`)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, "an invalid result: "+rec.Body.String())

	rec = call(h, "POST", "/api/v1/doc-schemas/export", `{"docSchema":`+current+`}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "application/schema+json", rec.Header().Get("Content-Type"))
	require.Contains(t, rec.Body.String(), `"name"`)
}

func TestSuggestBindingsHandler(t *testing.T) {
	f := newFixture(t, true)
	h := router(f, store.RoleViewer)
	v := f.create(t, "customer")
	rec := call(h, "POST", "/api/v1/projects/"+f.project+"/mappings/"+v.ID+"/suggest-bindings", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
}
