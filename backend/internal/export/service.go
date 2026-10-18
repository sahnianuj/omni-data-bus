package export

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"omnidatabus/internal/audit"
	"omnidatabus/internal/auth"
	"omnidatabus/internal/httpapi"
	"omnidatabus/internal/problem"
	"omnidatabus/internal/progress"
	"omnidatabus/internal/store"
	"omnidatabus/internal/verify"
)

// Export rate limit: per user (OC-052).
const (
	MaxExportsPerHour = 10
	exportWindow      = time.Hour
)

// Runtimes of an export.
const (
	RuntimeBundle = "bundle" // older requests: now the same download as script
	RuntimeScript = "script" // the export folder: config.json, mappings.json, README.md (OC-182)
)

// Service runs exports (OC-050, OC-052, OC-053) and their history and reports (OC-055).
type Service struct {
	Builder *Builder
	Store   store.Store
	Audit   *audit.Recorder
	Script  ScriptConfig
	Version string
	// PlanHash is a mapping's current plan hash (mappings.Service.PlanHash), for "changed since".
	PlanHash func(ctx context.Context, ws store.WorkspaceID, m store.Mapping) (string, error)
	Now      func() time.Time

	once    sync.Once
	limiter *httpapi.WindowLimiter
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) limit() *httpapi.WindowLimiter {
	s.once.Do(func() { s.limiter = httpapi.NewWindowLimiter(s.now) })
	return s.limiter
}

func forbidden(code, title, detail string) problem.Problem {
	return problem.New(http.StatusForbidden, code, title, detail)
}

// Input is the export API's body (POST /projects/{id}/exports): only how to package the export.
// What is exported is the project as configured — its source, target, saved credentials and
// ready mappings (user decision, 2026-10-02). The connection details and credentials go in
// config.json beside the script, where operators change them per environment (OC-155); there is
// no passphrase or password confirmation (OC-154). Changing the project happens in its steps.
type Input struct {
	// IncludeOdb adds odb for this platform as bin/odb, for a machine without internet access
	// (OC-182). Nil: no binary; the export runs with the odb installed on the machine.
	IncludeOdb *Platform `json:"includeOdb,omitempty"`
	// JobOptions tune the run; omitted fields keep the defaults.
	JobOptions json.RawMessage `json:"jobOptions,omitempty"`
	// Runtime and Script are the older request's packaging, still accepted (OC-160): every
	// export is now the same folder, and script runtime "embedded" means IncludeOdb.
	Runtime string `json:"runtime,omitempty"`
	Script  Script `json:"script"`
}

// include is the platform to include odb for ("" = none), from either request form.
func (in Input) include() string {
	switch {
	case in.IncludeOdb != nil:
		return in.IncludeOdb.String()
	case in.Script.Runtime == "embedded":
		return Platform{OS: in.Script.OS, Arch: in.Script.Arch}.String()
	}
	return ""
}

// Export builds the requested artifact, records it in the history and audits it. Credentials
// are only in the artifact's config.json, never in the history, the audit or the logs.
func (s *Service) Export(ctx context.Context, p auth.Principal, projectID string, in Input) (Artifact, store.ExportRecord, error) {
	mode := ModeConfig
	req := Request{CredentialMode: mode, Runtime: RuntimeScript, JobOptions: in.JobOptions}
	include := s.Script.Dist.Preferred(in.include())
	if p.Role.Rank() < store.RoleEditor.Rank() {
		return Artifact{}, store.ExportRecord{}, forbidden(problem.CodeForbiddenRole, "Not allowed", "Exporting needs the editor role.")
	}
	if err := checkInput(in, include); err != nil {
		return Artifact{}, store.ExportRecord{}, err
	}
	if ok, wait := s.limit().Allow("export:"+p.UserID, MaxExportsPerHour, exportWindow); !ok {
		pr := problem.New(http.StatusTooManyRequests, problem.CodeRateLimited, "Too many exports",
			fmt.Sprintf("At most %d exports an hour. Try again in %d minutes.", MaxExportsPerHour, int(wait.Minutes())+1))
		return Artifact{}, store.ExportRecord{}, pr
	}
	user, err := s.Store.Accounts().User(ctx, p.UserID)
	if err != nil {
		return Artifact{}, store.ExportRecord{}, err
	}
	built, err := s.Builder.Build(ctx, p.WorkspaceID, user.Email, projectID, req)
	if err != nil {
		return Artifact{}, store.ExportRecord{}, err
	}
	b := built.Bundle
	raw, err := Encode(b)
	if err != nil {
		return Artifact{}, store.ExportRecord{}, err
	}
	art, err := RenderScript(ctx, b, raw, built.RunConfig, include, s.Script, s.Version)
	if fe, ok := isFetchError(err); ok {
		return Artifact{}, store.ExportRecord{}, problem.New(http.StatusBadGateway, fe.Code, "Couldn't get odb for "+PlatformLabel(fe.Platform), fe.Message)
	}
	if err != nil {
		if strings.Contains(err.Error(), "larger than") {
			return Artifact{}, store.ExportRecord{}, problem.New(http.StatusUnprocessableEntity, problem.CodeExportTooLarge, "Export too large", err.Error())
		}
		return Artifact{}, store.ExportRecord{}, unprocessable(problem.CodeValidation, "Can't build the export", err.Error())
	}

	rec := store.ExportRecord{
		ProjectID: projectID, CreatedBy: p.UserID, CreatedByEmail: user.Email, CreatedAt: b.CreatedAt,
		PlanHashes: map[string]string{}, TargetType: b.Target.Type,
		CredentialMode: mode, Runtime: req.Runtime, OdbVersion: s.Version, BundleSHA256: b.SHA256(), PlanHash: b.PlanHash(),
	}
	for _, m := range b.Mappings {
		rec.MappingIDs = append(rec.MappingIDs, m.ID)
		rec.PlanHashes[m.ID] = m.PlanHash
	}
	if rec, err = s.Store.Exports().Create(ctx, p.WorkspaceID, rec); err != nil {
		return Artifact{}, store.ExportRecord{}, err
	}
	s.Audit.Record(ctx, p.WorkspaceID, p.UserID, "bundle.exported", "project", projectID, map[string]string{
		"projectId": projectID, "exportId": rec.ID, "mappingIds": strings.Join(rec.MappingIDs, ","),
		"credentialMode": mode, "runtime": req.Runtime, "bundleSha256": rec.BundleSHA256,
	})
	return art, rec, nil
}

// checkRequest applies the guard rails that don't need the bundle (OC-052 AC4).
func checkInput(in Input, include string) error {
	switch in.Runtime {
	case "", RuntimeBundle, RuntimeScript:
	default:
		return unprocessable(problem.CodeValidation, "Invalid runtime", `runtime is "bundle" or "script" (both give the same download now).`)
	}
	switch in.Script.Runtime {
	case "", "auto", "binary", "docker", "embedded":
	default:
		return unprocessable(problem.CodeValidation, "Invalid runtime", "The script runtime is auto, binary, docker or embedded.")
	}
	if include != "" && !slices.Contains(Platforms, include) {
		return unprocessable(problem.CodeValidation, "Choose a platform", "Including odb needs one of these machines: "+strings.Join(Platforms, ", ")+".")
	}
	return nil
}

// HistoryItem is an export with whether the project changed since (OC-055 AC3).
type HistoryItem struct {
	store.ExportRecord
	// Changed lists the exported mappings whose plan changed or that were deleted since.
	Changed []string `json:"changed"`
}

// History lists a project's exports, newest first, each compared with the current mappings.
func (s *Service) History(ctx context.Context, ws store.WorkspaceID, projectID string, page store.Page) ([]HistoryItem, string, error) {
	if _, err := s.Store.Projects().Get(ctx, ws, projectID); err != nil {
		return nil, "", err
	}
	recs, next, err := s.Store.Exports().List(ctx, ws, projectID, page)
	if err != nil {
		return nil, "", err
	}
	current := map[string]string{} // mapping ID → plan hash now ("" = can't compile)
	maps, err := s.Store.Mappings().ListForProject(ctx, ws, projectID)
	if err != nil {
		return nil, "", err
	}
	names := map[string]string{}
	for _, m := range maps {
		names[m.ID] = m.SourceSchema + "." + m.SourceTable
		if s.PlanHash != nil {
			h, _ := s.PlanHash(ctx, ws, m)
			current[m.ID] = h
		}
	}
	out := make([]HistoryItem, 0, len(recs))
	for _, r := range recs {
		it := HistoryItem{ExportRecord: r, Changed: []string{}}
		for _, id := range r.MappingIDs {
			h, ok := current[id]
			switch {
			case !ok:
				it.Changed = append(it.Changed, id+" (deleted)")
			case h != r.PlanHashes[id]:
				it.Changed = append(it.Changed, names[id])
			}
		}
		out = append(out, it)
	}
	return out, next, nil
}

//go:embed report.schema.json
var reportSchemaJSON []byte

var reportSchema = sync.OnceValues(func() (*jsonschema.Schema, error) {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(reportSchemaJSON))
	if err != nil {
		return nil, err
	}
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	if err := c.AddResource("report.schema.json", doc); err != nil {
		return nil, err
	}
	return c.Compile("report.schema.json")
})

// MaxReportBytes caps an imported report.json.
const MaxReportBytes = 5 << 20

// External is the summary of an imported run, kept on its job (store.Job.External).
type External struct {
	RunID        string          `json:"runId"`
	BundleSHA256 string          `json:"bundleSha256"`
	ExportID     string          `json:"exportId"`
	Odb          ReportBuild     `json:"odb"`
	ExitCode     int             `json:"exitCode"`
	DurationSec  float64         `json:"durationSec"`
	DocsPerSec   float64         `json:"docsPerSec"`
	Errors       ReportErrors    `json:"errors"`
	ImportedBy   string          `json:"importedBy"`
	ImportedAt   time.Time       `json:"importedAt"`
	Mappings     []ReportMapping `json:"mappings"`
}

// ImportReport stores an external run's report.json as a read-only job (OC-055 AC4). The
// report must come from a bundle exported from this project.
func (s *Service) ImportReport(ctx context.Context, p auth.Principal, projectID string, raw []byte) (store.Job, error) {
	if p.Role.Rank() < store.RoleEditor.Rank() {
		return store.Job{}, forbidden(problem.CodeForbiddenRole, "Not allowed", "Importing a run report needs the editor role.")
	}
	if _, err := s.Store.Projects().Get(ctx, p.WorkspaceID, projectID); err != nil {
		return store.Job{}, err
	}
	if errs := reportErrors(raw); len(errs) > 0 {
		pr := unprocessable(problem.CodeValidation, "Not a run report", "The file isn't a report.json written by odb run.")
		for _, e := range errs {
			pr.FieldErrors = append(pr.FieldErrors, problem.FieldError{Field: e.field, Message: e.msg})
		}
		return store.Job{}, pr
	}
	var rep Report
	if err := json.Unmarshal(raw, &rep); err != nil {
		return store.Job{}, unprocessable(problem.CodeValidation, "Not a run report", err.Error())
	}
	exp, err := s.Store.Exports().FindByBundle(ctx, p.WorkspaceID, projectID, rep.BundleSHA256)
	if errors.Is(err, store.ErrNotFound) {
		return store.Job{}, unprocessable(problem.CodeReportUnknownBundle, "Report of another export",
			"This report is from a bundle that wasn't exported from this project (bundle "+short(rep.BundleSHA256)+").")
	}
	if err != nil {
		return store.Job{}, err
	}
	if len(rep.Errors.Samples) > maxSamples {
		rep.Errors.Samples = rep.Errors.Samples[:maxSamples]
	}
	for i := range rep.Errors.Samples {
		if len(rep.Errors.Samples[i].Sample) > 4096 {
			rep.Errors.Samples[i].Sample = nil // the 4 KB sample limit (OC-026)
		}
	}
	ext, _ := json.Marshal(External{
		RunID: rep.RunID, BundleSHA256: rep.BundleSHA256, ExportID: exp.ID, Odb: rep.Odb, ExitCode: rep.ExitCode,
		DurationSec: rep.DurationSec, DocsPerSec: rep.DocsPerSec, Errors: rep.Errors, ImportedBy: p.UserID, ImportedAt: s.now(), Mappings: rep.Mappings,
	})
	// The report has no source totals: a finished run is 100 %, a stopped one shows its counts.
	finishedRun := rep.Status == store.JobCompleted || rep.Status == store.JobCompletedWithErrors
	percent := 0.0
	if finishedRun {
		percent = 100
	}
	snap := progress.Snapshot{
		Status: rep.Status, TS: rep.FinishedAt, Total: rep.Totals.Read, TotalKind: progress.TotalExact, Processed: rep.Totals.Read,
		Percent: percent, Written: rep.Totals.Written, Skipped: rep.Totals.Skipped, Failed: rep.Totals.Failed, RowsRead: rep.Totals.Read,
		Throughput: rep.DocsPerSec, PeakThroughput: rep.DocsPerSec, ElapsedSeconds: int64(rep.DurationSec), ErrorsByCode: []progress.CodeCount{},
	}
	for _, c := range rep.Errors.ByCode {
		snap.ErrorsByCode = append(snap.ErrorsByCode, progress.CodeCount{Code: c.Code, Count: c.Count})
	}
	var ids []string
	for _, m := range rep.Mappings {
		ids = append(ids, m.ID)
		snap.Mappings = append(snap.Mappings, progress.MappingProgress{
			MappingID: m.ID, Table: m.Table, Target: m.Target, Phase: m.Phase, Processed: m.Read, Total: m.Read,
			TotalKind: progress.TotalExact, Percent: percent, Written: m.Written, Skipped: m.Skipped, Failed: m.Failed,
		})
	}
	started, finished := rep.StartedAt, rep.FinishedAt
	j := store.Job{
		ProjectID: projectID, Status: rep.Status, Reason: rep.Reason, ReasonDetail: rep.ReasonDetail, MappingIDs: ids,
		Options: json.RawMessage(`{}`), CreatedBy: p.UserID, StartedAt: &started, FinishedAt: &finished,
		Origin: OriginExternal, External: ext,
	}
	j, err = s.Store.Jobs().Create(ctx, p.WorkspaceID, j)
	if err != nil {
		return store.Job{}, err
	}
	snap.JobID = j.ID
	doc, _ := json.Marshal(snap)
	_ = s.Store.Jobs().PutProgress(ctx, j.ID, doc)
	if len(rep.Verification) > 0 && string(rep.Verification) != "null" {
		// The verification results, as the tool's own jobs store them (OC-030).
		if raw, err := json.Marshal(verify.ParseReport(rep.Verification)); err == nil {
			_ = s.Store.Jobs().PutReport(ctx, j.ID, raw)
		}
	}
	_ = s.Store.Jobs().AppendEvent(ctx, j.ID, store.JobEvent{
		At: s.now(), Kind: "imported",
		Message: fmt.Sprintf("Imported the report of an external run (odb %s, run %s)", rep.Odb.Version, rep.RunID),
	})
	s.Audit.Record(ctx, p.WorkspaceID, p.UserID, "run.imported", "job", j.ID, map[string]string{
		"projectId": projectID, "exportId": exp.ID, "bundleSha256": rep.BundleSHA256, "runId": rep.RunID,
	})
	j.Progress = doc
	return j, nil
}

// OriginExternal marks imported runs (jobs.OriginExternal).
const OriginExternal = "external"

const maxSamples = 100

type fieldErr struct{ field, msg string }

func reportErrors(raw []byte) []fieldErr {
	sch, err := reportSchema()
	if err != nil {
		return []fieldErr{{"", err.Error()}}
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return []fieldErr{{"", "not JSON: " + err.Error()}}
	}
	verr := sch.Validate(inst)
	if verr == nil {
		return nil
	}
	var ve *jsonschema.ValidationError
	if !errors.As(verr, &ve) {
		return []fieldErr{{"", verr.Error()}}
	}
	var out []fieldErr
	for _, u := range ve.BasicOutput().Errors {
		if u.Error == nil || u.InstanceLocation == "" && len(out) > 0 {
			continue
		}
		field := strings.TrimPrefix(strings.ReplaceAll(u.InstanceLocation, "/", "."), ".")
		out = append(out, fieldErr{field, u.Error.String()})
		if len(out) == 20 {
			break
		}
	}
	if len(out) == 0 {
		out = []fieldErr{{"", verr.Error()}}
	}
	return out
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12] + "…"
	}
	return sha
}

// Options describes what this server can export (GET /exports/options).
type Options struct {
	Version     string `json:"version"`
	Image       string `json:"image"`
	DownloadURL string `json:"downloadUrl"`
	// Downloadable lists platforms whose checksum is known (the README links their download).
	Downloadable []string `json:"downloadable"`
	// Embeddable lists platforms this server can include odb for (bin/odb, OC-182).
	Embeddable []string `json:"embeddable"`
	// Signed lists the platforms whose odb is signed for its OS; on the others macOS or Windows
	// warns before running it (a development build, OC-185).
	Signed []string `json:"signed"`
	// Machines is every platform with whether this server can add its odb now, on first use
	// (downloaded and verified), or not, and why (OC-188).
	Machines   []Machine `json:"machines"`
	MaxPerHour int       `json:"maxPerHour"`
}

// Describe returns the server's export options.
func (s *Service) Describe() Options {
	o := Options{
		Version: s.Version, Image: s.Script.Image, DownloadURL: s.Script.DownloadURL,
		Downloadable: []string{}, MaxPerHour: MaxExportsPerHour, Machines: s.Script.Dist.Machines(),
	}
	o.Embeddable = availableOrOnDemand(o.Machines)
	for p := range s.Script.Dist.Checksums() {
		o.Downloadable = append(o.Downloadable, p)
	}
	slices.Sort(o.Downloadable)
	if o.Embeddable == nil {
		o.Embeddable = []string{}
	}
	o.Signed = s.Script.Dist.Signed()
	if o.Signed == nil {
		o.Signed = []string{}
	}
	return o
}
