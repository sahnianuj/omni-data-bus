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
	MappingID   string              `json:"mappingId"`
	Table       string              `json:"table"`
	Target      store.MappingTarget `json:"target"`
	RowEstimate int64               `json:"rowEstimate"`
	// TargetEmpty: the collection had no documents (or was created) at pre-flight.
	TargetEmpty bool `json:"targetEmpty,omitempty"`
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
	// OpenKeyspacesFunc opens a stored target keyspace browser.
	OpenKeyspacesFunc func(ctx context.Context, ws store.WorkspaceID, connectionID string) (target.Keyspaces, store.Connection, error)
	// LatestSchemaFunc returns the latest discovery snapshot.
	LatestSchemaFunc func(ctx context.Context, ws store.WorkspaceID, connectionID string) (store.DiscoverySnapshot, *schema.Snapshot, error)
)

// Preflighter runs the pre-flight checks (OC-023).
type Preflighter struct {
	Store         store.Store
	Validate      ValidateFunc
	TestConn      TestConnFunc
	OpenSource    OpenSourceFunc
	OpenKeyspaces OpenKeyspacesFunc
	Latest        LatestSchemaFunc
	Timeout       time.Duration
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
			pm, cs := p.mappingChecks(ctx, ws, projectID, tgtID, m, snap, opts)
			plan[i] = pm
			add(cs...)
		}()
	}
	wg.Wait()

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
	c := testToCheck("target", "Target repository reachable (KV service)", res, err)
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

func (p *Preflighter) mappingChecks(ctx context.Context, ws store.WorkspaceID, projectID, tgtID string, m store.Mapping, snap *schema.Snapshot, opts Options) (PlannedMapping, []Check) {
	table := m.SourceSchema + "." + m.SourceTable
	pm := PlannedMapping{MappingID: m.ID, Table: table, Target: m.Target}
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
		t = snap.Table(m.SourceSchema, m.SourceTable)
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
	}

	// 4. Target keyspace exists (or is created now), and it's empty for insert mode.
	ksChecks, empty := p.keyspaceCheck(ctx, ws, tgtID, m, opts, id)
	out = append(out, ksChecks...)
	pm.TargetEmpty = empty

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

// keyspaceCheck also reports whether the collection is known to be empty (verification then
// compares its count, OC-030).
func (p *Preflighter) keyspaceCheck(ctx context.Context, ws store.WorkspaceID, tgtID string, m store.Mapping, opts Options, id func(string) string) ([]Check, bool) {
	table := m.SourceSchema + "." + m.SourceTable
	ks := m.Target.Bucket + "." + m.Target.Scope + "." + m.Target.Collection
	c := Check{ID: id("keyspace"), Label: table + ": target " + ks, Status: CheckOK, MappingID: m.ID}
	keyspaces, _, err := p.OpenKeyspaces(ctx, ws, tgtID)
	if err != nil {
		c.Status, c.Detail = CheckFail, errMessage(err)
		return []Check{c}, false
	}
	info, err := keyspaces.Collection(ctx, m.Target.Bucket, m.Target.Scope, m.Target.Collection)
	switch {
	case err == nil:
	case problem.HasCode(err, problem.CodeNotFound) && m.Target.CreateIfMissing:
		if cerr := createKeyspace(ctx, keyspaces, m.Target); cerr != nil {
			c.Status, c.Detail = CheckFail, "Couldn't create the collection: "+errMessage(cerr)
			c.Hint = "Create it in Couchbase, or give the target credential permission to manage scopes and collections."
			return []Check{c}, false
		}
		c.Detail = "Created " + ks + "."
		return []Check{c}, true
	case problem.HasCode(err, problem.CodeNotFound):
		c.Status, c.Code = CheckFail, mapping.CodeTargetKeyspaceMissing
		c.Detail, c.Hint = "The collection doesn't exist.", "Create it, or turn on \"Create when migration starts\" in the Tables step."
		return []Check{c}, false
	default:
		c.Status, c.Detail = CheckFail, errMessage(err)
		return []Check{c}, false
	}
	var out []Check
	if info.DocCountEstimate != nil && *info.DocCountEstimate > 0 {
		w := Check{
			ID: id("empty"), Label: table + ": target collection is empty", Status: CheckWarn, Code: WarnCollectionHasDoc, MappingID: m.ID,
			Detail: fmt.Sprintf("%s already holds about %d documents.", ks, *info.DocCountEstimate),
		}
		if opts.WriteMode == WriteInsert {
			w.Code = WarnInsertExisting
			w.Detail += " Insert mode skips documents whose keys already exist."
		}
		out = append(out, w)
	}
	return append([]Check{c}, out...), info.DocCountEstimate != nil && *info.DocCountEstimate == 0
}

// createKeyspace creates the scope (if missing) and the collection.
func createKeyspace(ctx context.Context, ks target.Keyspaces, t store.MappingTarget) error {
	scopes, err := ks.ListScopes(ctx, t.Bucket)
	if err != nil {
		return err
	}
	found := false
	for _, s := range scopes {
		if s.Name == t.Scope {
			found = true
		}
	}
	if !found {
		if err := ks.CreateScope(ctx, t.Bucket, t.Scope); err != nil && !errors.Is(err, target.ErrPending) && !problem.HasCode(err, problem.CodeKeyspaceExists) {
			return err
		}
	}
	err = ks.CreateCollection(ctx, t.Bucket, t.Scope, target.CollectionSpec{Name: t.Collection})
	if err != nil && (errors.Is(err, target.ErrPending) || problem.HasCode(err, problem.CodeKeyspaceExists)) {
		return nil
	}
	return err
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
