package discovery

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/problem"
	"omnidatabus/internal/schema"
	"omnidatabus/internal/store"
)

// previewSnapshot has shop.events with a composite PK, a timestamptz, a bytea and a text column,
// plus billing.invoice that the repository user can't read.
func previewSnapshot() *schema.Snapshot {
	events := schema.Table{
		Name: "events", Kind: schema.KindTable, Readable: true, PrimaryKey: []string{"id", "part"},
		Columns: []schema.Column{
			{Name: "id", Ordinal: 1, UDTName: "int8", DataType: "bigint", JSONType: schema.TypeInteger},
			{Name: "part", Ordinal: 2, UDTName: "int2", DataType: "smallint", JSONType: schema.TypeInteger},
			{Name: "at", Ordinal: 3, UDTName: "timestamptz", DataType: "timestamp with time zone", JSONType: schema.TypeDatetime},
			{Name: "blob", Ordinal: 4, UDTName: "bytea", DataType: "bytea", JSONType: schema.TypeString},
			{Name: "notes", Ordinal: 5, UDTName: "text", DataType: "text", JSONType: schema.TypeString},
		},
	}
	invoice := schema.Table{Name: "invoice", Kind: schema.KindTable, Readable: false, Columns: []schema.Column{{Name: "id", UDTName: "int8", JSONType: schema.TypeInteger}}}
	return &schema.Snapshot{Database: "shop", Schemas: []schema.Schema{
		{Name: "billing", Tables: []schema.Table{invoice}},
		{Name: "shop", Tables: []schema.Table{events}},
	}}
}

func discovered(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	f.fake.Snapshot = previewSnapshot()
	_, err := f.svc.Discover(ctx, ws, "u1", f.connID, source.DiscoverOptions{})
	require.NoError(t, err)
	return f
}

func TestPreview_AC5_UsesPipelineEncoders(t *testing.T) {
	f := discovered(t)
	at := time.Date(2024, 1, 2, 3, 4, 5, 0, time.FixedZone("X", 3600))
	longText := strings.Repeat("é", 1500)
	bigBlob := make([]byte, 300)
	f.fake.PreviewRows = [][]any{
		{int64(1), int16(2), at, []byte{0x89, 0x50}, nil},
		{int64(2), int16(1), nil, bigBlob, longText},
	}
	res, err := f.svc.Preview(ctx, ws, f.connID, "shop", "events", "", 0)
	require.NoError(t, err)
	require.Equal(t, []string{"id", "part"}, res.OrderBy, "ordered by the primary key by default")
	require.Equal(t, source.DefaultPreviewLimit, res.Limit)
	require.Equal(t, []string{"id", "part"}, f.fake.LastPreview.OrderBy)
	require.Len(t, res.Columns, 5)
	require.Equal(t, PreviewColumn{Name: "at", DataType: "timestamp with time zone", JSONType: schema.TypeDatetime}, res.Columns[2])

	row := res.Rows[0]
	require.Equal(t, Cell{V: int64(1), T: schema.TypeInteger}, row[0])
	require.Equal(t, Cell{V: "2024-01-02T02:04:05Z", T: schema.TypeDatetime}, row[2], "timestamptz → ISO UTC Z")
	require.Equal(t, Cell{V: "iVA=", T: schema.TypeString}, row[3], "bytea → base64")
	require.Nil(t, row[4].V)

	row = res.Rows[1]
	require.True(t, row[3].Truncated, "bytea over 256 bytes")
	require.True(t, row[4].Truncated, "text over 1 KB")
	require.Equal(t, longText, row[4].V, "the full value (≤ 64 KB) is sent for Expand")

	b, err := json.Marshal(res.Rows[0][0])
	require.NoError(t, err)
	require.JSONEq(t, `{"v":1,"t":"integer"}`, string(b))
}

func TestPreview_AC5_LimitCappedAndOrderBy(t *testing.T) {
	f := discovered(t)
	res, err := f.svc.Preview(ctx, ws, f.connID, "shop", "events", "at", 1000)
	require.NoError(t, err)
	require.Equal(t, source.MaxPreviewLimit, res.Limit)
	require.Equal(t, source.MaxPreviewLimit, f.fake.LastPreview.Limit)
	require.Equal(t, []string{"at"}, f.fake.LastPreview.OrderBy)
}

func TestPreview_HugeValuesAreCapped(t *testing.T) {
	c := cell(strings.Repeat("a", maxStringLen+10), schema.Column{UDTName: "text", JSONType: schema.TypeString})
	require.Len(t, c.V, maxStringLen)
	c = cell(strings.Repeat("é", maxStringLen), schema.Column{UDTName: "text", JSONType: schema.TypeString})
	require.LessOrEqual(t, len(c.V.(string)), maxStringLen)
	require.True(t, strings.HasSuffix(c.V.(string), "é"), "cut on a rune boundary")
	c = cell(make([]byte, maxBytesLen+10), schema.Column{UDTName: "bytea", JSONType: schema.TypeString})
	require.Len(t, c.V, maxBytesLen/3*4, "base64 of the capped bytes")
}

func TestPreview_Errors(t *testing.T) {
	f := discovered(t)
	_, err := f.svc.Preview(ctx, ws, f.connID, "shop", "nope", "", 0)
	require.True(t, problem.HasCode(err, problem.CodeNotFound))

	_, err = f.svc.Preview(ctx, ws, f.connID, "billing", "invoice", "", 0)
	require.True(t, problem.HasCode(err, "SOURCE_PERMISSION_DENIED"), "unreadable tables fail before querying")

	f.fake.PreviewErr = errors.New("canceling statement due to statement timeout")
	_, err = f.svc.Preview(ctx, ws, f.connID, "shop", "events", "", 0)
	require.Error(t, err)
	require.True(t, strings.HasPrefix(problem.From(err).Code, "SOURCE_"))

	f.fake.OpenErr = errors.New("down")
	_, err = f.svc.Preview(ctx, ws, f.connID, "shop", "events", "", 0)
	require.Error(t, err)

	g := newFixture(t)
	_, err = g.svc.Preview(ctx, ws, g.connID, "shop", "events", "", 0)
	require.True(t, problem.HasCode(err, problem.CodeSchemaNotDiscovered))
}

func TestPreviewHandler_AC5_UnknownOrderBy422(t *testing.T) {
	f := discovered(t)
	h := router(f, store.RoleViewer)
	base := "/api/v1/connections/" + f.connID + "/tables/shop/events/preview"

	rec := call(h, "GET", base+"?orderBy=nope", "")
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	require.Contains(t, rec.Body.String(), `"field":"orderBy"`)

	require.Equal(t, http.StatusUnprocessableEntity, call(h, "GET", base+"?limit=abc", "").Code)
	require.Equal(t, http.StatusUnprocessableEntity, call(h, "GET", base+"?limit=0", "").Code)

	f.fake.PreviewRows = [][]any{{int64(1), int16(1), nil, nil, "x"}}
	rec = call(h, "GET", base+"?limit=10&orderBy=id", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	var res PreviewResult
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &res))
	require.Equal(t, 10, res.Limit)
	require.Len(t, res.Rows, 1)

	// Percent-encoded names (spaces, quotes) are decoded.
	require.Equal(t, http.StatusNotFound, call(h, "GET", "/api/v1/connections/"+f.connID+"/tables/shop/Order%20Details/preview", "").Code)
}
