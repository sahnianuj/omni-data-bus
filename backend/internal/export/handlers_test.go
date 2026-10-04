package export

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/audit"
	"omnidatabus/internal/auth"
	"omnidatabus/internal/httpapi"
	"omnidatabus/internal/jobs"
	"omnidatabus/internal/problem"
	"omnidatabus/internal/store"
)

// api is the export API over the fixture, with a fixed caller.
type api struct {
	f      *fixture
	svc    *Service
	h      http.Handler
	who    *auth.Principal
	logs   *bytes.Buffer
	now    time.Time
	userID string
}

func newAPI(t *testing.T) *api {
	t.Helper()
	f := newFixture(t)
	a := &api{f: f, logs: &bytes.Buffer{}, now: time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC), userID: "u-editor"}
	require.NoError(t, f.st.Accounts().Signup(ctx, store.NewAccount{
		User:      store.User{ID: a.userID, Email: "editor@example.com", DisplayName: "Ed"},
		Workspace: store.Workspace{ID: string(ws), Name: "W"},
		Session:   store.Session{ID: "s1", UserID: a.userID, WorkspaceID: string(ws), ExpiresAt: a.now.Add(time.Hour)},
	}))
	stepped := a.now.Add(-time.Minute)
	a.who = &auth.Principal{UserID: a.userID, WorkspaceID: ws, Role: store.RoleEditor, SessionID: "s1", StepUpAt: &stepped}
	f.b.Now = func() time.Time { return a.now }
	a.svc = &Service{
		Builder: f.b, Store: f.st, Audit: &audit.Recorder{Store: f.st.Audit()}, Script: testScriptConfig(t), Version: "2.0.0",
		PlanHash: f.maps.PlanHash, Now: func() time.Time { return a.now },
	}
	withUser := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), *a.who)))
		})
	}
	logger := slog.New(slog.NewJSONHandler(a.logs, nil))
	slog.SetDefault(logger)
	a.h = httpapi.NewRouter(httpapi.Options{
		Logger: logger, Middleware: []func(http.Handler) http.Handler{httpapi.BodyLimit, withUser},
		Features: []httpapi.Registrar{Handlers{Svc: a.svc}},
	})
	return a
}

func (a *api) do(method, path string, body any) *httptest.ResponseRecorder {
	var r io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		r = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, "/api/v1"+path, r)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	a.h.ServeHTTP(rec, req)
	return rec
}

func (a *api) export(req Input) *httptest.ResponseRecorder {
	return a.do(http.MethodPost, "/projects/"+a.f.project.ID+"/exports", req)
}

func problemOf(t *testing.T, rec *httptest.ResponseRecorder) problem.Problem {
	t.Helper()
	var p problem.Problem
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p), rec.Body.String())
	return p
}

func sealedReq() Input {
	return Input{Passphrase: passphrase, Runtime: RuntimeScript}
}

func TestExportsAPI_AC1_ZipContents(t *testing.T) {
	a := newAPI(t)
	rec := a.export(sealedReq())
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	require.Equal(t, "application/zip", rec.Header().Get("Content-Type"))
	require.Contains(t, rec.Header().Get("Content-Disposition"), `filename="odb-export-shop-docs-20261001.zip"`)
	require.Len(t, rec.Header().Get("X-Odb-Bundle-Sha256"), 64)
	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	require.NoError(t, err)
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	require.ElementsMatch(t, []string{"migrate.sh", "README.md"}, names)

	// runtime bundle: the .odb.json alone.
	rec = a.export(Input{Passphrase: passphrase, Runtime: RuntimeBundle})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	_, err = Parse(rec.Body.Bytes(), "2.0.0")
	require.NoError(t, err)
}

func TestExportsAPI_AC4_Authorization(t *testing.T) {
	a := newAPI(t)
	// No step-up within 5 minutes.
	old := a.now.Add(-6 * time.Minute)
	a.who.StepUpAt = &old
	rec := a.export(sealedReq())
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Equal(t, problem.CodeStepUpRequired, problemOf(t, rec).Code)
	a.who.StepUpAt = nil
	require.Equal(t, problem.CodeStepUpRequired, problemOf(t, a.export(sealedReq())).Code)

	// A viewer can't export (nor generate passphrases).
	recent := a.now
	a.who.StepUpAt, a.who.Role = &recent, store.RoleViewer
	require.Equal(t, http.StatusForbidden, a.export(sealedReq()).Code)
	require.Equal(t, http.StatusForbidden, a.do(http.MethodPost, "/exports/passphrase", nil).Code)
	require.Equal(t, http.StatusOK, a.do(http.MethodGet, "/projects/"+a.f.project.ID+"/exports", nil).Code, "viewers see the history")

	// What is exported is the project as configured, sealed: the API takes nothing else.
	a.who.Role = store.RoleEditor
	for _, extra := range []map[string]any{
		{"mappingIds": []string{a.f.orders.ID}},
		{"targetOverride": map[string]any{"type": "directory", "config": map[string]any{"path": "/out"}}},
		{"credentialMode": "plaintext"},
		{"refs": map[string]any{"source": map[string]any{"password": "env:X"}}},
	} {
		body := map[string]any{"passphrase": passphrase, "runtime": RuntimeBundle}
		for k, v := range extra {
			body[k] = v
		}
		rec = a.do(http.MethodPost, "/projects/"+a.f.project.ID+"/exports", body)
		require.Equal(t, http.StatusBadRequest, rec.Code, "%v: %s", extra, rec.Body.String())
	}

	// An unknown runtime: 422.
	rec = a.export(Input{Passphrase: passphrase, Runtime: "zip"})
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

func TestExportsAPI_AC4_PassphraseStrength(t *testing.T) {
	a := newAPI(t)
	for _, weak := range []string{"", "short", "aaaaaaaaaaaaaaaa", "password1234"} {
		rec := a.export(Input{Passphrase: weak})
		require.Equal(t, http.StatusUnprocessableEntity, rec.Code, weak)
		require.Equal(t, problem.CodePassphraseWeak, problemOf(t, rec).Code, weak)
	}
	// Generate gives a strong one; it isn't stored anywhere.
	rec := a.do(http.MethodPost, "/exports/passphrase", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var gen struct{ Passphrase string }
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &gen))
	require.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	rec = a.export(Input{Passphrase: gen.Passphrase})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
}

func TestExportsAPI_RateLimit(t *testing.T) {
	a := newAPI(t)
	for range MaxExportsPerHour {
		require.Equal(t, http.StatusCreated, a.export(Input{Passphrase: passphrase}).Code)
	}
	rec := a.export(Input{Passphrase: passphrase})
	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	a.now = a.now.Add(61 * time.Minute)
	stepped := a.now
	a.who.StepUpAt = &stepped
	require.Equal(t, http.StatusCreated, a.export(Input{Passphrase: passphrase}).Code)
}

func TestExportsAPI_AC6_AuditEvent(t *testing.T) {
	a := newAPI(t)
	rec := a.export(sealedReq())
	require.Equal(t, http.StatusCreated, rec.Code)
	events, err := a.f.st.Audit().List(ctx, ws, 50)
	require.NoError(t, err)
	var found []store.AuditEvent
	for _, e := range events {
		if e.Action == "bundle.exported" {
			found = append(found, e)
		}
	}
	require.Len(t, found, 1)
	var meta map[string]string
	require.NoError(t, json.Unmarshal(found[0].Metadata, &meta))
	require.Equal(t, ModeSealed, meta["credentialMode"])
	require.Equal(t, RuntimeScript, meta["runtime"])
	require.Equal(t, rec.Header().Get("X-Odb-Bundle-Sha256"), meta["bundleSha256"])
	require.Equal(t, a.f.orders.ID+","+a.f.items.ID, meta["mappingIds"])
	raw, _ := json.Marshal(found[0])
	require.Empty(t, ScanForSecrets(raw, pgPassword, cbPassword, passphrase))
}

// TestExport_AC3_NoLeakScanner: in sealed and external modes no credential or passphrase is in
// the download, the history, the audit log, the server logs or any API response.
func TestExport_AC3_NoLeakScanner(t *testing.T) {
	a := newAPI(t)
	var artifacts [][]byte
	for _, req := range []Input{sealedReq(), {Passphrase: passphrase, Runtime: RuntimeBundle}} {
		rec := a.export(req)
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		artifacts = append(artifacts, rec.Body.Bytes())
		if req.Runtime == RuntimeScript {
			zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
			require.NoError(t, err)
			for _, f := range zr.File {
				rc, _ := f.Open()
				data, _ := io.ReadAll(rc)
				_ = rc.Close()
				artifacts = append(artifacts, data)
			}
		}
	}
	hist := a.do(http.MethodGet, "/projects/"+a.f.project.ID+"/exports", nil)
	artifacts = append(artifacts, hist.Body.Bytes())
	events, _ := a.f.st.Audit().List(ctx, ws, 100)
	for _, e := range events {
		raw, _ := json.Marshal(e)
		artifacts = append(artifacts, raw)
	}
	recs, _, _ := a.f.st.Exports().List(ctx, ws, a.f.project.ID, store.Page{Limit: 100})
	for _, r := range recs {
		raw, _ := json.Marshal(r)
		artifacts = append(artifacts, raw)
	}
	artifacts = append(artifacts, a.logs.Bytes())
	for i, data := range artifacts {
		require.Empty(t, ScanForSecrets(data, pgPassword, cbPassword, passphrase), "artifact %d", i)
	}
}

// TestExportsAPI_SealsSavedCredentials: the bundle carries the project's saved source and
// target credentials, readable only with the passphrase.
func TestExportsAPI_SealsSavedCredentials(t *testing.T) {
	a := newAPI(t)
	rec := a.export(Input{Passphrase: passphrase, Runtime: RuntimeBundle})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	b, err := Parse(rec.Body.Bytes(), "2.0.0")
	require.NoError(t, err)
	require.Equal(t, ModeSealed, b.Source.Credentials.Mode)
	src, err := Unseal(b.Source.Credentials, passphrase, b.Integrity.ContentSHA256)
	require.NoError(t, err)
	require.Equal(t, pgPassword, src["password"])
	tgt, err := Unseal(b.Target.Credentials, passphrase, b.Integrity.ContentSHA256)
	require.NoError(t, err)
	require.Equal(t, cbPassword, tgt["password"])
	require.Empty(t, ScanForSecrets(rec.Body.Bytes(), pgPassword, cbPassword, passphrase))
}

func TestExportsHistory_AC3_ListAndCompare(t *testing.T) {
	a := newAPI(t)
	for _, rt := range []string{RuntimeScript, RuntimeBundle, RuntimeScript} {
		a.now = a.now.Add(time.Minute)
		stepped := a.now
		a.who.StepUpAt = &stepped
		require.Equal(t, http.StatusCreated, a.export(Input{Passphrase: passphrase, Runtime: rt}).Code)
	}
	var page struct{ Items []HistoryItem }
	rec := a.do(http.MethodGet, "/projects/"+a.f.project.ID+"/exports", nil)
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &page))
	require.Len(t, page.Items, 3)
	require.Equal(t, []string{RuntimeScript, RuntimeBundle, RuntimeScript}, []string{page.Items[0].Runtime, page.Items[1].Runtime, page.Items[2].Runtime})
	require.Equal(t, ModeSealed, page.Items[0].CredentialMode)
	require.True(t, page.Items[0].CreatedAt.After(page.Items[1].CreatedAt), "newest first")
	require.Len(t, page.Items[0].BundleSHA256, 64)
	require.Equal(t, "editor@example.com", page.Items[0].CreatedByEmail)
	require.Empty(t, page.Items[0].Changed)

	// Edit a mapping: every export now says it changed.
	m, err := a.f.st.Mappings().Get(ctx, ws, a.f.orders.ID)
	require.NoError(t, err)
	m.Spec = json.RawMessage(strings.Replace(string(m.Spec), `"template":"orders::{id}"`, `"template":"order::{id}"`, 1))
	_, err = a.f.st.Mappings().Update(ctx, ws, m)
	require.NoError(t, err)
	rec = a.do(http.MethodGet, "/projects/"+a.f.project.ID+"/exports", nil)
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &page))
	require.Equal(t, []string{"public.orders"}, page.Items[0].Changed)
}

// report is a report.json from a run of bundle b.
func report(b Bundle, status string) []byte {
	r := Report{
		ReportVersion: ReportVersion, RunID: "run-1", BundleSHA256: b.SHA256(), PlanHash: b.PlanHash(), Project: b.Project,
		Odb: ReportBuild{Version: "2.0.0", Commit: "abc"}, Status: status, ExitCode: 2,
		StartedAt: time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC), FinishedAt: time.Date(2026, 10, 2, 9, 5, 0, 0, time.UTC), DurationSec: 300,
		Totals: ReportTotals{Read: 1000, Written: 990, Failed: 10}, DocsPerSec: 3.3,
		Mappings: []ReportMapping{{ID: b.Mappings[0].ID, Name: "orders", Table: "public.orders", Target: "orders/", Phase: "done", Read: 1000, Written: 990, Failed: 10}},
		Errors:   ReportErrors{Total: 10, ByCode: []ErrorCount{{Stage: "transform", Code: "TYPE_MISMATCH", Count: 10}}, Samples: []store.JobError{{JobID: "j", Seq: 1, Stage: "transform", Code: "TYPE_MISMATCH", Message: "bad"}}},
	}
	raw, _ := json.Marshal(r)
	return raw
}

func (a *api) importReport(raw []byte) *httptest.ResponseRecorder {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("report", "report.json")
	_, _ = fw.Write(raw)
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+a.f.project.ID+"/runs/import", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	a.h.ServeHTTP(rec, req)
	return rec
}

func TestRunsImport_AC4_Valid(t *testing.T) {
	a := newAPI(t)
	rec := a.export(Input{Passphrase: passphrase})
	require.Equal(t, http.StatusCreated, rec.Code)
	exportID := rec.Header().Get("X-Odb-Export-Id")
	b, err := Parse(rec.Body.Bytes(), "2.0.0")
	require.NoError(t, err)

	rec = a.importReport(report(*b, store.JobCompletedWithErrors))
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var j store.Job
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &j))
	require.Equal(t, OriginExternal, j.Origin)
	require.Equal(t, store.JobCompletedWithErrors, j.Status)
	stored, err := a.f.st.Jobs().Get(ctx, ws, j.ID)
	require.NoError(t, err)
	var ext External
	require.NoError(t, json.Unmarshal(stored.External, &ext))
	require.Equal(t, "run-1", ext.RunID)
	require.Equal(t, exportID, ext.ExportID)
	require.EqualValues(t, 10, ext.Errors.Total)
	doc, _, err := a.f.st.Jobs().GetProgress(ctx, j.ID)
	require.NoError(t, err)
	require.Contains(t, string(doc), `"written":990`)
	// It doesn't hold the project's active-job lock.
	_, err = a.f.st.Jobs().ActiveForProject(ctx, ws, a.f.project.ID)
	require.ErrorIs(t, err, store.ErrNotFound)
}

func TestRunsImport_AC4_UnknownBundleAndSchema(t *testing.T) {
	a := newAPI(t)
	// A bundle of another project (never exported here).
	other := newFixture(t).build(t, externalReq()).Bundle
	rec := a.importReport(report(other, store.JobCompleted))
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	require.Equal(t, problem.CodeReportUnknownBundle, problemOf(t, rec).Code)

	// Not a report: 422 with field errors.
	rec = a.importReport([]byte(`{"reportVersion":1,"runId":"","status":"exploded"}`))
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	p := problemOf(t, rec)
	require.Equal(t, problem.CodeValidation, p.Code)
	require.NotEmpty(t, p.FieldErrors)
	var fields []string
	for _, fe := range p.FieldErrors {
		fields = append(fields, fe.Field)
	}
	require.Contains(t, strings.Join(fields, " "), "status")
	rec = a.importReport([]byte(`not json`))
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

func TestReportSchemaFileMatchesAPI(t *testing.T) {
	api, err := readAPIFile("report.schema.json")
	require.NoError(t, err)
	require.Equal(t, string(api), string(reportSchemaJSON))
}

func readAPIFile(name string) ([]byte, error) { return os.ReadFile("../../../api/" + name) }

func TestExportsAPI_Options(t *testing.T) {
	a := newAPI(t)
	rec := a.do(http.MethodGet, "/exports/options", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var o Options
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &o))
	require.Equal(t, []string{"darwin_arm64", "linux_amd64"}, o.Downloadable, "from SHA256SUMS")
	require.Contains(t, o.Embeddable, runtime.GOOS+"_"+runtime.GOARCH, "the test dist's tarball, or this server's own binary")
	require.Equal(t, MaxExportsPerHour, o.MaxPerHour)
	require.Equal(t, 300, o.StepUpWindowSec)
}

func TestBundle_OptionsRoundTripAndSecrets(t *testing.T) {
	o := OptionsFrom(jobsDefault())
	require.Equal(t, jobsDefault(), o.Jobs())
	s := ToConnectionSecrets(Secrets{"password": "p", "caCertPem": "c"})
	require.Equal(t, "p", s.Password)
	require.Equal(t, "c", s.CACertPEM)
	require.Equal(t, "nullable", nullability(true))
	require.Equal(t, "not null", nullability(false))
	require.Equal(t, "json", family("jsonb"))
	require.Equal(t, "binary", family("bytea"))
	require.Equal(t, "BUNDLE_INVALID: m ("+"[a b]"+")", (&Error{Code: CodeInvalid, Message: "m", Details: []string{"a", "b"}}).Error())
	require.Equal(t, "abcdefabcdef…", short("abcdefabcdefabcdef"))
	require.Equal(t, "ab", short("ab"))
}

func jobsDefault() jobs.Options {
	o := jobs.DefaultOptions()
	n := 500
	o.MaxDocsPerSecond = &n
	return o
}
