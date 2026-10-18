package mappings

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/automap"
	"omnidatabus/internal/store"
)

// OC-163 AC4: a viewer can ask for a proposal, and it changes nothing; New mapping from a
// design proposes before any mapping exists.
func TestProposeHandler_AC4_ReadOnly(t *testing.T) {
	f := newFixture(t, true)
	h := router(f, store.RoleViewer)
	v := f.create(t, "customer")
	before, err := f.svc.Get(ctx, ws, f.project, v.ID)
	require.NoError(t, err)

	rec := call(h, "POST", "/api/v1/projects/"+f.project+"/mappings/"+v.ID+"/propose", `{"base":true}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	var p automap.Proposal
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	require.NotEmpty(t, p.Driving)
	after, err := f.svc.Get(ctx, ws, f.project, v.ID)
	require.NoError(t, err)
	require.Equal(t, before.Version, after.Version, "nothing saved")

	design := `{"docSchema":{"source":"json-schema","root":{"id":"fld_root","name":"$","type":"object","children":[` +
		`{"id":"f_name","name":"name","type":"string"},{"id":"f_email","name":"email","type":"string"}]}}}`
	rec = call(h, "POST", "/api/v1/projects/"+f.project+"/mappings/propose", design)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	require.Equal(t, "customer", p.Driving[0].Table.Name)
	require.Equal(t, http.StatusUnprocessableEntity, call(h, "POST", "/api/v1/projects/"+f.project+"/mappings/propose", `{"docSchema":{"root":{"id":"fld_root","name":"$","type":"object"}}}`).Code)
	require.Equal(t, http.StatusNotFound, call(h, "POST", "/api/v1/projects/"+f.project+"/mappings/"+v.ID+"/propose", `{"drivingTable":"shop.nope"}`).Code)
}
