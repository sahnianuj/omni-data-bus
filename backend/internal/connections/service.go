package connections

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"omnidatabus/internal/connectors"
	"omnidatabus/internal/connectors/source"
	"omnidatabus/internal/connectors/target"
	"omnidatabus/internal/crypto"
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
	sec, err := MergeSecrets(Secrets{}, patch)
	if err != nil {
		return connectors.TestResult{}, err
	}
	return c.Test(ctx, cfg, Creds(sec)), nil
}

// SaveInput is a PUT /projects/{id}/connections/{role} body.
type SaveInput struct {
	Type    string
	Name    string
	Config  json.RawMessage
	Secrets SecretsPatch
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
	if !created {
		if existing.Type != in.Type {
			cur = Secrets{} // switching database type: old credentials don't apply
		} else if cur, err = Open(s.sealer, existing); err != nil {
			return store.Connection{}, false, err
		}
	}
	sec, err := MergeSecrets(cur, in.Secrets)
	if err != nil {
		return store.Connection{}, false, err
	}
	if sec.Password == "" {
		return store.Connection{}, false, requiredPassword()
	}
	sealed, err := Seal(s.sealer, ws, projectID, id, sec)
	if err != nil {
		return store.Connection{}, false, err
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
