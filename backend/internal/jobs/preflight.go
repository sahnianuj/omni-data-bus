package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"omnidatabus/internal/connectors"
	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/connectors/target"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/mappings"
	"omnidatabus/internal/problem"
	"omnidatabus/internal/schema"
	"omnidatabus/internal/store"
)

// Check statuses (same vocabulary as a connection test).
const (
	CheckOK      = "ok"
	CheckWarn    = "warn"
	CheckFail    = "fail"
	CheckSkipped = "skipped"
)

// Pre-flight warning codes (not blocking).
const (
	WarnNoResume         = "NO_RESUME_SUPPORT"
	WarnCollectionHasDoc = "COLLECTION_NOT_EMPTY"
	WarnInsertExisting   = "INSERT_EXISTING_DOCS"
	WarnKeyNotUnique     = "KEY_NOT_UNIQUE"
	WarnNoSnapshotExport = "SNAPSHOT_EXPORT_UNAVAILABLE"
)

// Check is one line of the pre-flight checklist.
type Check struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Status    string `json:"status"`
	Detail    string `json:"detail,omitempty"`
	Hint      string `json:"hint,omitempty"`
	Code      string `json:"code,omitempty"`
	MappingID string `json:"mappingId,omitempty"`
}

// PlannedMapping is a mapping the job will migrate, with what the Review step shows.
type PlannedMapping struct {
	MappingID string `json:"mappingId"`
	Table     string `json:"table"`
	// Target is the 1.0 Couchbase form of Location (zero for other kinds).
	Target      store.MappingTarget `json:"target"`
	Location    store.Location      `json:"location"`
	RowEstimate int64               `json:"rowEstimate"`
	// TargetEmpty: the location had no documents (or was created) at pre-flight.
	TargetEmpty bool `json:"targetEmpty,omitempty"`

	avgRowBytes int64 // the source table's average row size (a document size estimate)
}

// PreflightResult is the checklist plus the plan it validated.
type PreflightResult struct {
	OK        bool             `json:"ok"`
	Checks    []Check          `json:"checks"`
	Mappings  []PlannedMapping `json:"mappings"`
	TotalRows int64            `json:"totalRows"`
}

// Ports the pre-flight uses; every lookup goes through the project's stored connections.
type (
	// ValidateFunc runs a mapping's validation (mappings.Service.Validate).
	ValidateFunc func(ctx context.Context, ws store.WorkspaceID, projectID, mappingID string) (mappings.ValidationResult, error)
	// TestConnFunc re-tests a stored connection.
	TestConnFunc func(ctx context.Context, ws store.WorkspaceID, userID, connectionID string) (connectors.TestResult, error)
	// OpenSourceFunc opens a stored source repository.
	OpenSourceFunc func(ctx context.Context, ws store.WorkspaceID, connectionID string) (source.Session, store.Connection, error)
	// TargetPreflightFunc runs the target kind's checks for the job's locations
	// (connections.Service.PreflightTarget, OC-036).
	TargetPreflightFunc func(ctx context.Context, ws store.WorkspaceID, connectionID string, locs []target.PreflightLocation) ([]target.LocationPreflight, error)
	// LatestSchemaFunc returns the latest discovery snapshot.
	LatestSchemaFunc func(ctx context.Context, ws store.WorkspaceID, connectionID string) (store.DiscoverySnapshot, *schema.Snapshot, error)
)

// Preflighter runs the pre-flight checks (OC-023).
type Preflighter struct {
	Store      store.Store
	Validate   ValidateFunc
	TestConn   TestConnFunc
	OpenSource OpenSourceFunc
	// Target runs the target kind's location checks (Couchbase: the collection exists or is
	// created, and whether it already holds documents).
	Target  TargetPreflightFunc
	Latest  LatestSchemaFunc
	Timeout time.Duration
}

// selectMappings returns the job's mappings: the requested IDs, or every ready mapping.
func selectMappings(ctx context.Context, st store.Store, ws store.WorkspaceID, projectID string, ids []string) ([]store.Mapping, error) {
	all, err := st.Mappings().ListForProject(ctx, ws, projectID)
	if err != nil {
		return nil, err
	}
	var out []store.Mapping
	if len(ids) == 0 {
		for _, m := range all {
			if m.Status == "ready" {
				out = append(out, m)
			}
		}
	} else {
		byID := map[string]store.Mapping{}
		for _, m := range all {
			byID[m.ID] = m
		}
		for _, id := range ids {
			m, ok := byID[id]
			if !ok {
				return nil, problem.New(http.StatusUnprocessableEntity, problem.CodeValidation, "Unknown mapping",
					"Mapping "+id+" isn't part of this project.")
			}
			out = append(out, m)
		}
	}
	if len(out) == 0 {
		return nil, problem.New(http.StatusUnprocessableEntity, problem.CodeNoReadyMappings, "Nothing ready to migrate",
			"Validate at least one mapping (so it's ready) before starting a migration.")
	}
	return out, nil
}

// Run checks everything the job needs. It creates missing target collections whose mapping
// allows it. It returns the checklist; a failing check makes OK false (no error is returned
// for failed checks, only for problems loading the project).
func (p *Preflighter) Run(ctx context.Context, ws store.WorkspaceID, userID, projectID string, opts Options) (PreflightResult, error) {
	timeout := p.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if _, err := p.Store.Projects().Get(ctx, ws, projectID); err != nil {
		return PreflightResult{}, err
	}
	maps, err := selectMappings(ctx, p.Store, ws, projectID, opts.MappingIDs)
	if err != nil {
		return PreflightResult{}, err
	}
	srcID, tgtID := store.ConnectionID(projectID, store.RoleSource), store.ConnectionID(projectID, store.RoleTarget)

	var (
		mu     sync.Mutex
		checks []Check
		wg     sync.WaitGroup
	)
	add := func(c ...Check) {
		mu.Lock()
		checks = append(checks, c...)
		mu.Unlock()
	}
	var snap *schema.Snapshot
	var canWrite *bool

	wg.Add(3)
	go func() {
		defer wg.Done()
		add(p.connCheck(ctx, ws, userID, srcID, "source", "Source repository reachable"))
	}()
	go func() {
		defer wg.Done()
		c, caps := p.targetCheck(ctx, ws, userID, tgtID)
		add(c)
		mu.Lock()
		canWrite = caps
		mu.Unlock()
	}()
	go func() {
		defer wg.Done()
		add(p.snapshotCheck(ctx, ws, srcID))
	}()
	if p.Latest != nil {
		_, s, err := p.Latest(ctx, ws, srcID)
		if err == nil {
			snap = s
		}
	}
	plan := make([]PlannedMapping, len(maps))
	for i, m := range maps {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pm, cs := p.mappingChecks(ctx, ws, projectID, m, snap)
			plan[i] = pm
			add(cs...)
		}()
	}
	wg.Wait()
	add(p.targetChecks(ctx, ws, tgtID, maps, plan, opts)...)

	if canWrite != nil && !*canWrite {
		add(Check{
			ID: "target.write", Label: "Permission to write documents", Status: CheckFail, Code: "TARGET_PERMISSION_DENIED",
			Detail: "The target credential can't write documents.", Hint: "Grant the Data Writer role on the target bucket.",
		})
	}
	sort.SliceStable(checks, func(i, j int) bool { return checkOrder(checks[i]) < checkOrder(checks[j]) })
	res := PreflightResult{OK: true, Checks: checks, Mappings: plan}
	for _, c := range checks {
		if c.Status == CheckFail {
			res.OK = false
		}
	}
	for _, pm := range plan {
		res.TotalRows += pm.RowEstimate
	}
	if ctx.Err() != nil {
		res.OK = false
		res.Checks = append(res.Checks, Check{
			ID: "timeout", Label: "Checks finished in time", Status: CheckFail,
			Detail: "Pre-flight didn't finish within " + timeout.String() + ".", Hint: "Check that both repositories respond, then try again.",
		})
	}
	return res, nil
}

// checkOrder groups the list: repositories, snapshot, per-mapping checks (by table), warnings.
func checkOrder(c Check) string {
	switch {
	case c.ID == "source":
		return "0"
	case c.ID == "target", c.ID == "target.write":
		return "1" + c.ID
	case c.ID == "snapshot":
		return "2"
	case c.Status == CheckWarn && c.MappingID != "":
		return "4" + c.ID
	}
	return "3" + c.ID
}

func (p *Preflighter) connCheck(ctx context.Context, ws store.WorkspaceID, userID, id, checkID, label string) Check {
	res, err := p.TestConn(ctx, ws, userID, id)
	return testToCheck(checkID, label, res, err)
}

func (p *Preflighter) targetCheck(ctx context.Context, ws store.WorkspaceID, userID, id string) (Check, *bool) {
	res, err := p.TestConn(ctx, ws, userID, id)
	c := testToCheck("target", "Target repository reachable", res, err)
	var caps struct {
		CanWriteDocs *bool `json:"canWriteDocs"`
	}
	if err == nil && len(res.Capabilities) > 0 {
		_ = json.Unmarshal(res.Capabilities, &caps)
	}
	return c, caps.CanWriteDocs
}

func testToCheck(id, label string, res connectors.TestResult, err error) Check {
	c := Check{ID: id, Label: label, Status: CheckOK}
	if err != nil {
		c.Status, c.Detail = CheckFail, errMessage(err)
		return c
	}
	if !res.OK {
		c.Status = CheckFail
		for _, s := range res.Steps {
			if s.Status == connectors.StepFail {
				c.Detail, c.Hint, c.Code = s.Detail, s.Hint, s.Code
				break
			}
		}
		return c
	}
	for _, s := range res.Steps {
		if s.Status == connectors.StepWarn && c.Detail == "" {
			c.Status, c.Detail, c.Hint = CheckWarn, s.Detail, s.Hint
		}
	}
	return c
}

func errMessage(err error) string {
	var pr problem.Problemer
	if errors.As(err, &pr) {
		p := pr.Problem()
		if p.Detail != "" {
			return p.Detail
		}
		return p.Title
	}
	return err.Error()
}

// snapshotCheck verifies that one consistent snapshot can be shared by parallel readers.
func (p *Preflighter) snapshotCheck(ctx context.Context, ws store.WorkspaceID, srcID string) Check {
	c := Check{ID: "snapshot", Label: "Consistent snapshot export", Status: CheckOK}
	if p.OpenSource == nil {
		c.Status = CheckSkipped
		return c
	}
	sess, _, err := p.OpenSource(ctx, ws, srcID)
	if err != nil {
		c.Status, c.Detail = CheckSkipped, "Couldn't open the source: "+errMessage(err)
		return c
	}
	defer func() { _ = sess.Close() }()
	prober, ok := sess.(source.SnapshotProber)
	if !ok {
		c.Status = CheckSkipped
		return c
	}
	if err := prober.ProbeSnapshotExport(ctx); err != nil {
		c.Status, c.Code = CheckWarn, WarnNoSnapshotExport
		c.Detail = "Snapshot export isn't available (" + errMessage(err) + "), so each table is read by a single reader."
		c.Hint = "On a hot standby, enable hot_standby_feedback, or read from the primary."
	}
	return c
}

func (p *Preflighter) mappingChecks(ctx context.Context, ws store.WorkspaceID, projectID string, m store.Mapping, snap *schema.Snapshot) (PlannedMapping, []Check) {
	srcSchema, srcTable := m.Driving() // a composed mapping's driving table (OC-047)
	table := srcSchema + "." + srcTable
	pm := PlannedMapping{MappingID: m.ID, Table: table, Target: m.Target, Location: m.Where()}
	var out []Check
	id := func(s string) string { return "mapping." + table + "." + s }

	// 3. Validation, table existence and privilege, key template.
	v := Check{ID: id("valid"), Label: table + ": mapping is valid", Status: CheckOK, MappingID: m.ID}
	if res, err := p.Validate(ctx, ws, projectID, m.ID); err != nil {
		v.Status, v.Detail = CheckFail, errMessage(err)
	} else {
		var errs []string
		for _, i := range res.Issues {
			if i.Severity == mapping.SeverityError && i.Code != mapping.CodeTargetKeyspaceMissing {
				errs = append(errs, i.Message)
			}
		}
		if len(errs) > 0 {
			v.Status = CheckFail
			v.Detail = fmt.Sprintf("%d problem(s): %s", len(errs), errs[0])
			v.Hint = "Open the mapping and fix the issues listed under Validate."
		}
	}
	out = append(out, v)

	var t *schema.Table
	if snap != nil {
		t = snap.Table(srcSchema, srcTable)
	}
	tc := Check{ID: id("table"), Label: table + ": source table readable", Status: CheckOK, MappingID: m.ID}
	switch {
	case snap == nil:
		tc.Status, tc.Detail = CheckSkipped, "The source schema hasn't been discovered."
	case t == nil:
		tc.Status, tc.Detail, tc.Hint = CheckFail, "The table no longer exists in the source.", "Refresh the schema, then fix or delete the mapping."
	case !t.Readable:
		tc.Status, tc.Detail, tc.Hint = CheckFail, "The source user can't SELECT from this table.", "GRANT SELECT on the table to the source user."
	}
	out = append(out, tc)
	if t != nil && t.RowEstimate != nil {
		pm.RowEstimate = *t.RowEstimate
		if *t.RowEstimate > 0 && t.SizeBytes > 0 {
			pm.avgRowBytes = t.SizeBytes / *t.RowEstimate
		}
	}

	// 4. The target location checks run once for every mapping afterwards (targetChecks).

	// 6. Warnings.
	if t != nil {
		if len(t.PrimaryKey) == 0 && !hasNotNullUnique(t) {
			out = append(out, Check{
				ID: id("resume"), Label: table + ": resumable", Status: CheckWarn, Code: WarnNoResume, MappingID: m.ID,
				Detail: "The table has no primary key, so a paused or interrupted run restarts it from the beginning.",
			})
		}
		var spec mapping.Spec
		if json.Unmarshal(m.Spec, &spec) == nil {
			if kt, err := mapping.ParseKeyTemplate(spec.Key.Template); err == nil {
				if a := mapping.AnalyzeKey(kt, t); !a.Unique {
					out = append(out, Check{
						ID: id("key"), Label: table + ": unique document keys", Status: CheckWarn, Code: WarnKeyNotUnique,
						MappingID: m.ID, Detail: "The key template " + spec.Key.Template + " may produce the same key for different rows; later rows overwrite earlier ones.",
						Hint: "Include the primary key columns in the key template.",
					})
				}
			}
		}
	}
	return pm, out
}

func hasNotNullUnique(t *schema.Table) bool {
	for _, u := range t.UniqueConstraints {
		ok := len(u) > 0
		for _, c := range u {
			if col := t.Column(c); col == nil || col.Nullable {
				ok = false
			}
		}
		if ok {
			return true
		}
	}
	return false
}

// targetChecks runs the target kind's checks for every mapping's location (OC-036): for
// Couchbase, the collection exists or is created, and a warning when it already holds
// documents. Check IDs and labels are prefixed with the mapping's table, as in 1.0.
func (p *Preflighter) targetChecks(ctx context.Context, ws store.WorkspaceID, tgtID string, maps []store.Mapping, plan []PlannedMapping, opts Options) []Check {
	locs := make([]target.PreflightLocation, len(maps))
	for i, m := range maps {
		locs[i] = target.PreflightLocation{
			MappingID: m.ID, Name: plan[i].Table, Location: m.Where(), CreateIfMissing: m.Where().CreateIfMissing,
			WriteMode: opts.WriteMode, EstimatedDocs: plan[i].RowEstimate, AvgDocBytes: plan[i].avgRowBytes,
		}
	}
	prefix := func(i int, c target.PreflightCheck) Check {
		table := plan[i].Table
		return Check{
			ID: "mapping." + table + "." + c.ID, Label: table + ": " + c.Label, Status: c.Status,
			Detail: c.Detail, Hint: c.Hint, Code: c.Code, MappingID: maps[i].ID,
		}
	}
	res, err := p.Target(ctx, ws, tgtID, locs)
	if err != nil || len(res) != len(maps) {
		if err == nil {
			err = errors.New("the target returned no result for some mappings")
		}
		out := make([]Check, len(maps))
		for i := range maps {
			out[i] = prefix(i, target.PreflightCheck{ID: "keyspace", Label: "target " + locs[i].Location.String(), Status: CheckFail, Detail: errMessage(err)})
		}
		return out
	}
	var out []Check
	for i, r := range res {
		plan[i].TargetEmpty = r.Empty
		for _, c := range r.Checks {
			out = append(out, prefix(i, c))
		}
	}
	return out
}

// failedChecks summarizes failures for the PREFLIGHT_FAILED problem.
func failedChecks(res PreflightResult) string {
	var names []string
	for _, c := range res.Checks {
		if c.Status == CheckFail {
			names = append(names, c.Label)
		}
	}
	return strings.Join(names, "; ")
}
