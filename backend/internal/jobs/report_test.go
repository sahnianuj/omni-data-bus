package jobs

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/store"
)

func TestReport_AC4_NoSecrets(t *testing.T) {
	f := newFixture(t)
	const password, cbPassword = "s3cr3t-Pg!", "hunter2-cb"
	// Repositories whose configs name users and even a connection string with credentials.
	_, err := f.st.Connections().Upsert(ctx, ws, f.project.ID, store.RoleSource, store.ConnectionInput{
		Type: "postgres", Config: json.RawMessage(`{"host":"db.internal","port":5432,"database":"shop","username":"shop_owner"}`),
		Secret: store.Sealed{Ciphertext: []byte(password)}, LastTestStatus: "ok",
	})
	require.NoError(t, err)
	_, err = f.st.Connections().Upsert(ctx, ws, f.project.ID, store.RoleTarget, store.ConnectionInput{
		Type: "couchbase", Config: json.RawMessage(`{"connectionString":"couchbases://admin:` + cbPassword + `@cb.example.com/?network=external","username":"admin"}`),
		Secret: store.Sealed{Ciphertext: []byte(cbPassword)}, LastTestStatus: "ok",
	})
	require.NoError(t, err)
	v, _, err := f.svc.Create(ctx, ws, "u1", f.project.ID, DefaultOptions())
	require.NoError(t, err)
	require.NoError(t, f.st.Jobs().PutReport(ctx, v.ID, json.RawMessage(`{"mappings":{"`+v.MappingIDs[0]+`":{"mappingId":"`+v.MappingIDs[0]+`","result":"passed","counts":{"sourceRows":10,"reconciled":true,"warnings":[],"messages":[]}}}}`)))

	rep, err := f.svc.Report(ctx, ws, v.ID)
	require.NoError(t, err)
	require.Equal(t, "db.internal:5432", rep.Job.Source.Host)
	require.Equal(t, "shop", rep.Job.Source.Database)
	require.Equal(t, "cb.example.com", rep.Job.Target.Host)
	require.Equal(t, "passed", rep.Mappings[0].Verification.Result)
	require.EqualValues(t, 10, rep.Mappings[0].RowsRead)
	require.Equal(t, "customer::{customer_id}", rep.Mappings[0].KeyTemplate, "from the frozen spec")
	require.Contains(t, rep.Specs, v.MappingIDs[0])

	js, _ := json.Marshal(rep)
	csvOut, err := rep.CSV()
	require.NoError(t, err)
	htmlOut, err := rep.HTML()
	require.NoError(t, err)
	for name, out := range map[string]string{"json": string(js), "csv": string(csvOut), "html": string(htmlOut)} {
		for _, secret := range []string{password, cbPassword, "shop_owner", "admin:"} {
			require.False(t, strings.Contains(out, secret), "%s report contains %q", name, secret)
		}
	}
	require.NotContains(t, string(js), `"specs":null`)
	require.NotContains(t, string(js), "targetEmpty", "bookkeeping isn't in the options shown")

	_, err = f.svc.Report(ctx, "other-ws", v.ID)
	require.ErrorIs(t, err, store.ErrNotFound)
}
