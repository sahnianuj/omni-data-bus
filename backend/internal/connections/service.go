package connections

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"omnidatabus/internal/connectors"
	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/connectors/target"
	"omnidatabus/internal/crypto"
	"omnidatabus/internal/mapping"
	"omnidatabus/internal/naming"
	"omnidatabus/internal/problem"
	"omnidatabus/internal/store"
)

// JobGuard blocks credential changes while a migration runs (jobs arrive in OC-023).
type JobGuard interface {
	HasActiveJob(ctx context.Context, ws store.WorkspaceID, projectID string) (bool, error)
}

type noJobs struct{}

func (noJobs) HasActiveJob(context.Context, store.WorkspaceID, string) (bool, error) {
	return false, nil
}

// ErrJobRunning blocks repository changes during a migration.
var ErrJobRunning = problem.New(http.StatusConflict, problem.CodeProjectJobRunning, "Migration in progress",
	"Credentials can't change while this project's migration is running.")

// Normalizer is implemented by connectors that can apply config defaults and name a connection.
type Normalizer interface {
	Normalize(cfg json.RawMessage) (json.RawMessage, string, error)
}

// Service saves, tests and removes a project's repositories (OC-009, OC-012).
type Service struct {
	store   store.Store
	sealer  crypto.Sealer
	sources *source.Registry
	targets *target.Registry
	jobs    JobGuard
	clock   store.Clock
}

// NewService builds the connections service.
func NewService(st store.Store, sealer crypto.Sealer, sources *source.Registry, targets *target.Registry, jobs JobGuard, clock store.Clock) *Service {
	if jobs == nil {
		jobs = noJobs{}
	}
	if clock == nil {
		clock = store.SystemClock{}
	}
	return &Service{store: st, sealer: sealer, sources: sources, targets: targets, jobs: jobs, clock: clock}
}

type connector interface {
	Validate(cfg json.RawMessage) error
	connectors.Tester
}

func (s *Service) connector(role store.Role, typ string) (connector, error) {
	if role == store.RoleSource {
		return s.sources.Get(typ)
	}
	return s.targets.Get(typ)
}

// Creds converts plaintext secrets for a connector.
func Creds(sec Secrets) connectors.Credentials {
	return connectors.Credentials{Password: sec.Password, CACertPEM: sec.CACertPEM, ClientCertPEM: sec.ClientCertPEM, ClientKeyPEM: sec.ClientKeyPEM}
}

// TestUnsaved runs a connection test on form values; nothing is stored (OC-009 AC6).
func (s *Service) TestUnsaved(ctx context.Context, role store.Role, typ string, cfg json.RawMessage, patch SecretsPatch) (connectors.TestResult, error) {
	c, err := s.connector(role, typ)
	if err != nil {
		return connectors.TestResult{}, err
	}
	if tc, ok := c.(target.Connector); ok && role == store.RoleTarget {
		if err := kindEnabled(tc); err != nil {
			return connectors.TestResult{}, err
		}
	}
	sec, err := MergeSecrets(Secrets{}, patch)
	if err != nil {
		return connectors.TestResult{}, err
	}
	return c.Test(ctx, cfg, Creds(sec)), nil
}

// SaveInput is a PUT /projects/{id}/connections/{role} body.
type SaveInput struct {
	Type     string
	Name     string
	Config   json.RawMessage
	Secrets  SecretsPatch
	Relocate *Relocate
}

// Relocate moves the project's mappings to the new kind when the target kind changes (OC-068).
// Each mapping keeps its collection or folder name; Couchbase also needs the bucket and scope.
type Relocate struct {
	Bucket string `json:"bucket,omitempty"`
	Scope  string `json:"scope,omitempty"`
}

// Save creates or replaces the project's repository for role, keeping its ID. Omitted secrets
// keep the stored ones; any config or secret change resets the test status to untested.
func (s *Service) Save(ctx context.Context, ws store.WorkspaceID, userID, projectID string, role store.Role, in SaveInput) (store.Connection, bool, error) {
	c, err := s.connector(role, in.Type)
	if err != nil {
		return store.Connection{}, false, err
	}
	if err := s.guard(ctx, ws, projectID); err != nil {
		return store.Connection{}, false, err
	}
	needsSecret := true
	if role == store.RoleTarget {
		tc, _ := c.(target.Connector)
		if err := kindEnabled(tc); err != nil {
			return store.Connection{}, false, err
		}
		needsSecret = tc.Capabilities().RequiresCredentials
	}
	cfg, defaultName := in.Config, ""
	if n, ok := c.(Normalizer); ok {
		if cfg, defaultName, err = n.Normalize(in.Config); err != nil {
			return store.Connection{}, false, err
		}
	} else if err := c.Validate(in.Config); err != nil {
		return store.Connection{}, false, err
	}
	id := store.ConnectionID(projectID, role)
	existing, getErr := s.store.Connections().Get(ctx, ws, id)
	created := getErr != nil
	var cur Secrets
	var moves []move
	if !created {
		if existing.Type != in.Type {
			if role == store.RoleTarget {
				if moves, err = s.kindChange(ctx, ws, projectID, existing.Type, in.Type, in.Relocate); err != nil {
					return store.Connection{}, false, err
				}
			}
			cur = Secrets{} // switching database type: old credentials don't apply
		} else if cur, err = Open(s.sealer, existing); err != nil {
			return store.Connection{}, false, err
		}
	}
	sec, err := MergeSecrets(cur, in.Secrets)
	if err != nil {
		return store.Connection{}, false, err
	}
	var sealed store.Sealed
	switch {
	case !needsSecret:
		sec = Secrets{} // a directory has config only, no secret (OC-038)
	case sec.Password == "":
		return store.Connection{}, false, requiredPassword()
	default:
		if sealed, err = Seal(s.sealer, ws, projectID, id, sec); err != nil {
			return store.Connection{}, false, err
		}
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = defaultName
	}
	status, testedAt := "untested", (*store.Connection)(nil)
	if !created && existing.Type == in.Type && jsonEqual(existing.Config, cfg) && sec == cur {
		status, testedAt = existing.LastTestStatus, &existing // nothing that affects connectivity changed
	}
	input := store.ConnectionInput{Type: in.Type, Name: name, Config: cfg, Secret: sealed, SecretMeta: Meta(sec), LastTestStatus: status, CreatedBy: userID}
	if testedAt != nil {
		input.LastTestedAt = testedAt.LastTestedAt
	}
	if err := s.relocate(ctx, ws, userID, moves); err != nil {
		return store.Connection{}, false, err
	}
	conn, err := s.store.Connections().Upsert(ctx, ws, projectID, role, input)
	if err != nil {
		return store.Connection{}, false, err
	}
	action := "connection.update"
	if created {
		action = "connection.create"
	}
	s.audit(ctx, ws, userID, action, conn)
	return conn, created, nil
}

// TestStored re-tests a saved repository with its stored credentials and records the outcome.
func (s *Service) TestStored(ctx context.Context, ws store.WorkspaceID, userID, id string) (connectors.TestResult, error) {
	conn, err := s.store.Connections().Get(ctx, ws, id)
	if err != nil {
		return connectors.TestResult{}, err
	}
	c, err := s.connector(conn.Role, conn.Type)
	if err != nil {
		return connectors.TestResult{}, err
	}
	sec, err := Open(s.sealer, conn)
	if err != nil {
		return connectors.TestResult{}, err
	}
	res := c.Test(ctx, conn.Config, Creds(sec))
	status := Outcome(res)
	if err := s.store.Connections().SetTestResult(ctx, ws, id, status, s.clock.Now()); err != nil {
		return connectors.TestResult{}, err
	}
	s.audit(ctx, ws, userID, "connection.test", conn)
	return res, nil
}

// ErrNotSource is returned when a source-only operation targets another repository.
var ErrNotSource = problem.New(http.StatusUnprocessableEntity, problem.CodeValidation, "Not a source repository",
	"Schema discovery and previews run on the project's source repository.")

// OpenSource opens a session on a stored source repository with its stored credentials, which
// never leave the server (OC-007 AC3). The caller closes the session.
func (s *Service) OpenSource(ctx context.Context, ws store.WorkspaceID, id string) (source.Session, store.Connection, error) {
	conn, err := s.store.Connections().Get(ctx, ws, id)
	if err != nil {
		return nil, store.Connection{}, err
	}
	if conn.Role != store.RoleSource {
		return nil, conn, ErrNotSource
	}
	c, err := s.sources.Get(conn.Type)
	if err != nil {
		return nil, conn, err
	}
	sec, err := Open(s.sealer, conn)
	if err != nil {
		return nil, conn, err
	}
	sess, err := c.Open(ctx, conn.Config, Creds(sec))
	if err != nil {
		return nil, conn, err
	}
	return sess, conn, nil
}

// ErrNotTarget is returned when a target-only operation names another repository.
var ErrNotTarget = problem.New(http.StatusUnprocessableEntity, problem.CodeValidation, "Not a target repository",
	"Buckets, scopes and collections are browsed on the project's target repository.")

// OpenKeyspaces opens the keyspace browser of a stored target repository (OC-013). The
// connection is pooled by repository ID and version, so an edit reconnects.
func (s *Service) OpenKeyspaces(ctx context.Context, ws store.WorkspaceID, id string) (target.Keyspaces, store.Connection, error) {
	conn, err := s.store.Connections().Get(ctx, ws, id)
	if err != nil {
		return nil, store.Connection{}, err
	}
	if conn.Role != store.RoleTarget {
		return nil, conn, ErrNotTarget
	}
	c, err := s.targets.Get(conn.Type)
	if err != nil {
		return nil, conn, err
	}
	kb, ok := c.(target.KeyspaceBrowser)
	if !ok {
		return nil, conn, problem.New(http.StatusUnprocessableEntity, problem.CodeUnsupportedTarget, "Not supported", conn.Type+" targets have no keyspaces to browse.")
	}
	sec, err := Open(s.sealer, conn)
	if err != nil {
		return nil, conn, err
	}
	ks, err := kb.Keyspaces(ctx, conn.ID, strconv.FormatInt(conn.Version, 10), conn.Config, Creds(sec))
	if err != nil {
		return nil, conn, err
	}
	return ks, conn, nil
}

// Outcome summarizes a test result as ok, warn or failed.
func Outcome(res connectors.TestResult) string {
	if !res.OK {
		return "failed"
	}
	for _, st := range res.Steps {
		if st.Status == connectors.StepWarn {
			return "warn"
		}
	}
	return "ok"
}

// Get returns a repository.
func (s *Service) Get(ctx context.Context, ws store.WorkspaceID, id string) (store.Connection, error) {
	return s.store.Connections().Get(ctx, ws, id)
}

// Delete removes a project's repository and its stored credentials.
func (s *Service) Delete(ctx context.Context, ws store.WorkspaceID, userID, projectID string, role store.Role) error {
	if err := s.guard(ctx, ws, projectID); err != nil {
		return err
	}
	conn, err := s.store.Connections().Get(ctx, ws, store.ConnectionID(projectID, role))
	if err != nil {
		return err
	}
	if err := s.store.Connections().Delete(ctx, ws, projectID, role); err != nil {
		return err
	}
	s.audit(ctx, ws, userID, "connection.delete", conn)
	return nil
}

func (s *Service) guard(ctx context.Context, ws store.WorkspaceID, projectID string) error {
	if _, err := s.store.Projects().Get(ctx, ws, projectID); err != nil {
		return err
	}
	running, err := s.jobs.HasActiveJob(ctx, ws, projectID)
	if err != nil {
		return err
	}
	if running {
		return ErrJobRunning
	}
	return nil
}

func (s *Service) audit(ctx context.Context, ws store.WorkspaceID, user, action string, c store.Connection) {
	meta, _ := json.Marshal(map[string]string{"projectId": c.ProjectID, "role": string(c.Role), "type": c.Type})
	if err := s.store.Audit().Record(ctx, store.AuditEvent{
		WorkspaceID: ws, UserID: user, Action: action,
		EntityType: "connection", EntityID: c.ID, Metadata: meta,
	}); err != nil {
		slog.WarnContext(ctx, "audit record failed", "action", action, "error", err.Error())
	}
}

func jsonEqual(a, b json.RawMessage) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	ab, _ := json.Marshal(x)
	bb, _ := json.Marshal(y)
	return string(ab) == string(bb)
}

func requiredPassword() problem.Problem {
	p := problem.New(http.StatusUnprocessableEntity, problem.CodeValidation, "Invalid request", "A password is required.")
	p.FieldErrors = []problem.FieldError{{Field: "secrets.password", Message: "is required"}}
	return p
}

// OpenWriter opens a document writer on a stored target repository with a dedicated
// connection for the job (OC-025).
func (s *Service) OpenWriter(ctx context.Context, ws store.WorkspaceID, id string, opts target.WriteOptions) (target.DocWriter, error) {
	conn, err := s.store.Connections().Get(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	if conn.Role != store.RoleTarget {
		return nil, ErrNotTarget
	}
	c, err := s.targets.Get(conn.Type)
	if err != nil {
		return nil, err
	}
	wo, ok := c.(target.WriterOpener)
	if !ok {
		return nil, problem.New(http.StatusUnprocessableEntity, problem.CodeUnsupportedTarget, "Not supported", conn.Type+" targets can't receive documents.")
	}
	sec, err := Open(s.sealer, conn)
	if err != nil {
		return nil, err
	}
	return wo.OpenWriter(ctx, conn.Config, Creds(sec), opts)
}

// kindEnabled refuses a target kind the server has switched off (a directory target without
// allowed roots, OC-038) with TARGET_KIND_DISABLED.
func kindEnabled(c target.Connector) error {
	a, ok := c.(target.Availability)
	if !ok {
		return nil
	}
	if enabled, why := a.Available(); !enabled {
		return problem.New(http.StatusUnprocessableEntity, problem.CodeTargetKindDisabled, "Target kind disabled", why)
	}
	return nil
}

// move is a mapping and the location it moves to when the target kind changes (OC-068).
type move struct {
	m  store.Mapping
	to store.Location
}

// kindChange guards a target kind change: a Couchbase keyspace means nothing to a directory and
// vice versa, so mappings with a location of another kind block it (OC-036 AC3) unless rel says
// to move them (OC-068). It returns the moves, each validated for the new kind.
func (s *Service) kindChange(ctx context.Context, ws store.WorkspaceID, projectID, from, to string, rel *Relocate) ([]move, error) {
	maps, err := s.store.Mappings().ListForProject(ctx, ws, projectID)
	if err != nil {
		return nil, err
	}
	var using []string
	var moves []move
	for _, m := range maps {
		at := m.Where()
		if at.IsZero() || at.Kind == to {
			continue
		}
		using = append(using, m.SourceSchema+"."+m.SourceTable+" → "+at.String())
		if rel != nil {
			l := relocated(m, at, to, *rel)
			if err := mapping.ValidateLocation(l, to); err != nil {
				return nil, err
			}
			moves = append(moves, move{m: m, to: l})
		}
	}
	if len(using) == 0 || rel != nil {
		return moves, nil
	}
	sort.Strings(using)
	p := problem.New(http.StatusConflict, problem.CodeTargetKindInUse, "Target kind in use",
		fmt.Sprintf("The target can't change from %s to %s while %d mapping(s) have a %s location: %s. Move them to the new target, or delete them first.",
			from, to, len(using), from, strings.Join(using, "; ")))
	return nil, p
}

// relocated is m's location on a target of kind to: the same collection or folder name, made
// valid for that kind (OC-068).
func relocated(m store.Mapping, at store.Location, to string, rel Relocate) store.Location {
	name := at.Collection
	if at.Kind == store.KindDirectory {
		segs := strings.Split(strings.Trim(at.Folder, "/"), "/")
		name = segs[len(segs)-1]
	}
	if name == "" {
		name = m.SourceTable
	}
	if to == store.KindDirectory {
		return store.Location{Kind: to, Folder: naming.FolderName(name), CreateIfMissing: true}
	}
	return store.Location{
		Kind: to, Bucket: strings.TrimSpace(rel.Bucket), Scope: strings.TrimSpace(rel.Scope),
		Collection: naming.CollectionName(name), CreateIfMissing: true,
	}
}

// relocate saves each move and audits it as mapping.relocate (OC-068).
func (s *Service) relocate(ctx context.Context, ws store.WorkspaceID, userID string, moves []move) error {
	for _, mv := range moves {
		from := mv.m.Where()
		mv.m.SetLocation(mv.to)
		if _, err := s.store.Mappings().Update(ctx, ws, mv.m); err != nil {
			return err
		}
		meta, _ := json.Marshal(map[string]string{"projectId": mv.m.ProjectID, "from": from.String(), "to": mv.to.String()})
		if err := s.store.Audit().Record(ctx, store.AuditEvent{
			WorkspaceID: ws, UserID: userID, Action: "mapping.relocate",
			EntityType: "mapping", EntityID: mv.m.ID, Metadata: meta,
		}); err != nil {
			slog.WarnContext(ctx, "audit record failed", "action", "mapping.relocate", "error", err.Error())
		}
	}
	return nil
}

// Target is a stored target connection opened for a port call.
type Target struct {
	Connector target.Connector
	Conn      target.Conn
	Stored    store.Connection
}

// Kind is the connection's target kind.
func (t Target) Kind() string { return t.Stored.Type }

func unsupported(kind, what string) problem.Problem {
	return problem.New(http.StatusUnprocessableEntity, problem.CodeUnsupportedTarget, "Not supported", kind+" targets "+what+".")
}

// OpenTarget loads a project's stored target and decrypts its credentials for a port call.
func (s *Service) OpenTarget(ctx context.Context, ws store.WorkspaceID, id string) (Target, error) {
	conn, err := s.store.Connections().Get(ctx, ws, id)
	if err != nil {
		return Target{}, err
	}
	if conn.Role != store.RoleTarget {
		return Target{}, ErrNotTarget
	}
	c, err := s.targets.Get(conn.Type)
	if err != nil {
		return Target{}, err
	}
	sec, err := Open(s.sealer, conn)
	if err != nil {
		return Target{}, err
	}
	return Target{Connector: c, Stored: conn, Conn: target.Conn{
		ID: conn.ID, Version: strconv.FormatInt(conn.Version, 10), Config: conn.Config, Creds: Creds(sec),
	}}, nil
}

func (s *Service) browser(ctx context.Context, ws store.WorkspaceID, id string) (target.LocationBrowser, Target, error) {
	t, err := s.OpenTarget(ctx, ws, id)
	if err != nil {
		return nil, t, err
	}
	b, ok := t.Connector.(target.LocationBrowser)
	if !ok {
		return nil, t, unsupported(t.Kind(), "have no locations to browse")
	}
	return b, t, nil
}

// ListLocations lists the children of parent on a stored target (OC-036).
func (s *Service) ListLocations(ctx context.Context, ws store.WorkspaceID, id, parentPath string) ([]target.LocationNode, Target, error) {
	b, t, err := s.browser(ctx, ws, id)
	if err != nil {
		return nil, t, err
	}
	nodes, err := b.ListLocations(ctx, t.Conn, store.LocationFromPath(t.Kind(), parentPath))
	return nodes, t, err
}

// StatLocation describes one location of a stored target (NOT_FOUND when it doesn't exist).
func (s *Service) StatLocation(ctx context.Context, ws store.WorkspaceID, id string, loc store.Location) (target.LocationNode, error) {
	b, t, err := s.browser(ctx, ws, id)
	if err != nil {
		return target.LocationNode{}, err
	}
	loc.Kind = t.Kind()
	return b.StatLocation(ctx, t.Conn, loc)
}

// CreateLocation creates a location on a stored target; target.ErrPending when the kind is
// still making it available.
func (s *Service) CreateLocation(ctx context.Context, ws store.WorkspaceID, id string, loc store.Location) (Target, error) {
	b, t, err := s.browser(ctx, ws, id)
	if err != nil {
		return t, err
	}
	if !t.Connector.Capabilities().SupportsCreateLocation {
		return t, unsupported(t.Kind(), "can't create locations from the tool")
	}
	loc.Kind = t.Kind()
	return t, b.CreateLocation(ctx, t.Conn, loc)
}

// PreflightTarget runs the target kind's pre-flight checks for a job's locations (OC-036).
func (s *Service) PreflightTarget(ctx context.Context, ws store.WorkspaceID, id string, locs []target.PreflightLocation) ([]target.LocationPreflight, error) {
	t, err := s.OpenTarget(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	p, ok := t.Connector.(target.Preflighter)
	if !ok {
		return nil, unsupported(t.Kind(), "have no pre-flight checks")
	}
	return p.Preflight(ctx, t.Conn, locs), nil
}

// Capabilities returns the capabilities of the kind of a stored target.
func (s *Service) Capabilities(ctx context.Context, ws store.WorkspaceID, id string) (target.Capabilities, string, error) {
	conn, err := s.store.Connections().Get(ctx, ws, id)
	if err != nil {
		return target.Capabilities{}, "", err
	}
	c, err := s.targets.Get(conn.Type)
	if err != nil {
		return target.Capabilities{}, "", err
	}
	return c.Capabilities(), conn.Type, nil
}

// TargetKinds lists the registered target kinds with capabilities (GET /target-kinds).
func (s *Service) TargetKinds() []target.KindInfo { return target.Kinds(s.targets) }

func (s *Service) folderPicker() (target.FolderPicker, error) {
	c, err := s.targets.Get(store.KindDirectory)
	if err != nil {
		return nil, err
	}
	if err := kindEnabled(c); err != nil {
		return nil, err
	}
	p, ok := c.(target.FolderPicker)
	if !ok {
		return nil, unsupported(store.KindDirectory, "have no folder picker")
	}
	return p, nil
}

// DirectoryFolders lists what the Target step's folder picker shows (OC-038): the areas a
// directory target may be in (path ""), or a folder's visible sub-folders.
func (s *Service) DirectoryFolders(path string) (target.FolderListing, error) {
	p, err := s.folderPicker()
	if err != nil {
		return target.FolderListing{}, err
	}
	return p.Folders(path)
}

// MakeDirectoryFolder creates a folder from the picker (audited as folder.create).
func (s *Service) MakeDirectoryFolder(ctx context.Context, ws store.WorkspaceID, userID, parent, name string) (string, error) {
	p, err := s.folderPicker()
	if err != nil {
		return "", err
	}
	path, err := p.MakeFolder(parent, name)
	if err != nil {
		return "", err
	}
	meta, _ := json.Marshal(map[string]string{"path": path})
	if err := s.store.Audit().Record(ctx, store.AuditEvent{
		WorkspaceID: ws, UserID: userID, Action: "folder.create", EntityType: "folder", EntityID: path, Metadata: meta,
	}); err != nil {
		slog.WarnContext(ctx, "audit record failed", "action", "folder.create", "error", err.Error())
	}
	return path, nil
}

// CaseSensitive reports whether a stored target tells keys apart by case; kinds that don't
// probe it (Couchbase) always do.
func (s *Service) CaseSensitive(ctx context.Context, ws store.WorkspaceID, id string) (bool, error) {
	t, err := s.OpenTarget(ctx, ws, id)
	if err != nil {
		return true, err
	}
	p, ok := t.Connector.(target.CaseProber)
	if !ok {
		return true, nil
	}
	return p.CaseSensitive(ctx, t.Conn)
}

// CountDocuments counts a location's documents exactly when the kind can (target.Counter),
// else from its location browser (verification, OC-030/OC-041).
func (s *Service) CountDocuments(ctx context.Context, ws store.WorkspaceID, id string, loc store.Location) (int64, error) {
	t, err := s.OpenTarget(ctx, ws, id)
	if err != nil {
		return 0, err
	}
	loc.Kind = t.Kind()
	if c, ok := t.Connector.(target.Counter); ok {
		return c.CountDocuments(ctx, t.Conn, loc)
	}
	n, err := s.StatLocation(ctx, ws, id, loc)
	if err != nil {
		return 0, err
	}
	if n.DocCount == nil {
		return 0, errors.New("the document count isn't available")
	}
	return *n.DocCount, nil
}

// FindDocument reads a migrated document by key from a stored target (OC-039/OC-041).
func (s *Service) FindDocument(ctx context.Context, ws store.WorkspaceID, id string, loc store.Location, key string) (target.Document, error) {
	t, err := s.OpenTarget(ctx, ws, id)
	if err != nil {
		return target.Document{}, err
	}
	f, ok := t.Connector.(target.DocumentFinder)
	if !ok {
		return target.Document{}, unsupported(t.Kind(), "can't look documents up")
	}
	loc.Kind = t.Kind()
	return f.FindDocument(ctx, t.Conn, loc, key)
}

// ListOutput pages through what a mapping wrote (directory targets, OC-041).
func (s *Service) ListOutput(ctx context.Context, ws store.WorkspaceID, id string, loc store.Location, mappingID, cursor string, limit int) ([]target.OutputItem, string, error) {
	t, err := s.OpenTarget(ctx, ws, id)
	if err != nil {
		return nil, "", err
	}
	l, ok := t.Connector.(target.OutputLister)
	if !ok {
		return nil, "", unsupported(t.Kind(), "can't list their output; look documents up by key instead")
	}
	loc.Kind = t.Kind()
	return l.ListOutput(ctx, t.Conn, loc, mappingID, cursor, limit)
}
