// Package projects implements migration projects and their project-owned repositories (OC-007).
package projects

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"omnidatabus/internal/connections"
	"omnidatabus/internal/crypto"
	"omnidatabus/internal/problem"
	"omnidatabus/internal/store"
)

// Status is a project's derived state.
type Status string

// Project statuses: draft → configured (repositories tested) → ready (mappings ready, OC-035),
// then running / completed / failed from the latest job (OC-023).
const (
	StatusDraft      Status = "draft"
	StatusConfigured Status = "configured"
	StatusReady      Status = "ready"
	StatusRunning    Status = "running"
	StatusCompleted  Status = "completed"
	StatusFailed     Status = "failed"
)

// ErrNameTaken is returned for a duplicate project name in the workspace.
var ErrNameTaken = func() problem.Problem {
	p := problem.New(http.StatusConflict, problem.CodeProjectNameTaken, "Name already used", "A project with this name already exists in your workspace.")
	p.FieldErrors = []problem.FieldError{{Field: "name", Message: "is already used by another project"}}
	return p
}()

// ErrJobRunning blocks deleting a project while a migration runs.
var ErrJobRunning = problem.New(http.StatusConflict, problem.CodeProjectJobRunning, "Migration in progress",
	"This project has a running migration. Pause or cancel it first.")

// JobGuard reports whether a project has an active job. Jobs arrive in OC-023; until then NoJobs.
type JobGuard interface {
	HasActiveJob(ctx context.Context, ws store.WorkspaceID, projectID string) (bool, error)
}

// NoJobs is the JobGuard used before the job engine exists.
type NoJobs struct{}

// HasActiveJob implements JobGuard.
func (NoJobs) HasActiveJob(context.Context, store.WorkspaceID, string) (bool, error) {
	return false, nil
}

// Service implements project use cases.
type Service struct {
	store  store.Store
	sealer crypto.Sealer
	jobs   JobGuard
}

// NewService builds the projects service.
func NewService(st store.Store, sealer crypto.Sealer, jobs JobGuard) *Service {
	if jobs == nil {
		jobs = NoJobs{}
	}
	return &Service{store: st, sealer: sealer, jobs: jobs}
}

// Summary is a project with its repositories and derived status.
type Summary struct {
	Project       store.Project
	Source        *store.Connection
	Target        *store.Connection
	Status        Status
	Mappings      int
	ReadyMappings int
	// LatestJob is the project's newest job, if any.
	LatestJob *store.Job
}

// DeriveStatus is draft until both repositories are saved and their last test passed (a test
// that passed with warnings, e.g. unreadable tables, counts as passed).
func DeriveStatus(source, target *store.Connection) Status {
	ok := func(c *store.Connection) bool {
		return c != nil && (c.LastTestStatus == "ok" || c.LastTestStatus == "warn")
	}
	if ok(source) && ok(target) {
		return StatusConfigured
	}
	return StatusDraft
}

// Create makes a new, empty project.
func (s *Service) Create(ctx context.Context, ws store.WorkspaceID, userID, name, description string) (Summary, error) {
	p, err := s.store.Projects().Create(ctx, ws, store.NewProject{Name: strings.TrimSpace(name), Description: strings.TrimSpace(description), CreatedBy: userID})
	if store.IsConflict(err, "name") {
		return Summary{}, ErrNameTaken
	}
	if err != nil {
		return Summary{}, err
	}
	s.audit(ctx, ws, userID, "project.create", p.ID, map[string]string{"name": p.Name})
	return Summary{Project: p, Status: StatusDraft}, nil
}

// Get returns a project with its repositories.
func (s *Service) Get(ctx context.Context, ws store.WorkspaceID, id string) (Summary, error) {
	p, err := s.store.Projects().Get(ctx, ws, id)
	if err != nil {
		return Summary{}, err
	}
	return s.summarize(ctx, ws, p)
}

// List returns one page of projects, newest first.
func (s *Service) List(ctx context.Context, ws store.WorkspaceID, page store.Page) ([]Summary, string, error) {
	items, next, err := s.store.Projects().List(ctx, ws, page)
	if err != nil {
		return nil, "", err
	}
	out := make([]Summary, 0, len(items))
	for _, p := range items {
		sum, err := s.summarize(ctx, ws, p)
		if err != nil {
			return nil, "", err
		}
		out = append(out, sum)
	}
	return out, next, nil
}

func (s *Service) summarize(ctx context.Context, ws store.WorkspaceID, p store.Project) (Summary, error) {
	conns, err := s.store.Connections().ListForProject(ctx, ws, p.ID)
	if err != nil {
		return Summary{}, err
	}
	sum := Summary{Project: p}
	for i := range conns {
		switch conns[i].Role {
		case store.RoleSource:
			sum.Source = &conns[i]
		case store.RoleTarget:
			sum.Target = &conns[i]
		}
	}
	sum.Status = DeriveStatus(sum.Source, sum.Target)
	ms, err := s.store.Mappings().ListForProject(ctx, ws, p.ID)
	if err != nil {
		return Summary{}, err
	}
	sum.Mappings = len(ms)
	for _, m := range ms {
		if m.Status == "ready" {
			sum.ReadyMappings++
		}
	}
	// OC-035 AC3: ready once configured with ≥ 1 mapping and every mapping ready.
	if sum.Status == StatusConfigured && sum.Mappings > 0 && sum.ReadyMappings == sum.Mappings {
		sum.Status = StatusReady
	}
	jobs, _, err := s.store.Jobs().List(ctx, ws, p.ID, store.Page{Limit: 1})
	if err != nil {
		return Summary{}, err
	}
	if len(jobs) > 0 {
		j := jobs[0]
		sum.LatestJob = &j
		switch {
		case !store.JobTerminal(j.Status):
			sum.Status = StatusRunning
		case j.Status == store.JobCompleted || j.Status == store.JobCompletedWithErrors:
			sum.Status = StatusCompleted
		case j.Status == store.JobFailed:
			sum.Status = StatusFailed
		}
	}
	return sum, nil
}

// Update changes name/description; version must be the one the client read.
func (s *Service) Update(ctx context.Context, ws store.WorkspaceID, userID, id string, name, description *string, version int64) (Summary, error) {
	cur, err := s.store.Projects().Get(ctx, ws, id)
	if err != nil {
		return Summary{}, err
	}
	if name != nil {
		cur.Name = strings.TrimSpace(*name)
	}
	if description != nil {
		cur.Description = strings.TrimSpace(*description)
	}
	cur.Version = version
	p, err := s.store.Projects().Update(ctx, ws, cur)
	if store.IsConflict(err, "name") {
		return Summary{}, ErrNameTaken
	}
	if err != nil {
		return Summary{}, err
	}
	s.audit(ctx, ws, userID, "project.update", p.ID, nil)
	return s.summarize(ctx, ws, p)
}

// Delete removes the project with its repositories and their sealed credentials.
func (s *Service) Delete(ctx context.Context, ws store.WorkspaceID, userID, id string) error {
	p, err := s.store.Projects().Get(ctx, ws, id)
	if err != nil {
		return err
	}
	running, err := s.jobs.HasActiveJob(ctx, ws, id)
	if err != nil {
		return err
	}
	if running {
		return ErrJobRunning
	}
	if err := s.store.Projects().Delete(ctx, ws, id); err != nil {
		return err
	}
	s.audit(ctx, ws, userID, "project.delete", id, map[string]string{"name": p.Name})
	return nil
}

// Resolved is a repository with its decrypted credentials, for immediate use only.
type Resolved struct {
	Connection store.Connection
	Secrets    connections.Secrets
}

// Context is a project with its stored repositories resolved (OC-007 AC3): discovery, previews,
// keyspace operations and jobs use it, so users never re-enter credentials.
type Context struct {
	Project store.Project
	Source  *Resolved
	Target  *Resolved
}

// Resolve loads a project and decrypts its stored repositories' credentials.
func (s *Service) Resolve(ctx context.Context, ws store.WorkspaceID, projectID string) (Context, error) {
	sum, err := s.Get(ctx, ws, projectID)
	if err != nil {
		return Context{}, err
	}
	out := Context{Project: sum.Project}
	for _, pair := range []struct {
		conn *store.Connection
		dst  **Resolved
	}{{sum.Source, &out.Source}, {sum.Target, &out.Target}} {
		if pair.conn == nil {
			continue
		}
		sec, err := connections.Open(s.sealer, *pair.conn)
		if err != nil {
			return Context{}, err
		}
		*pair.dst = &Resolved{Connection: *pair.conn, Secrets: sec}
	}
	return out, nil
}

// Stats are the dashboard aggregates.
type Stats struct {
	Projects           int `json:"projects"`
	RunningJobs        int `json:"runningJobs"`
	DocsMigrated30d    int `json:"docsMigrated30d"`
	RepositoriesOK     int `json:"repositoriesHealthy"`
	RepositoriesTotal  int `json:"repositoriesTotal"`
	ConfiguredProjects int `json:"configuredProjects"`
}

// Dashboard computes workspace aggregates (up to 1,000 projects).
func (s *Service) Dashboard(ctx context.Context, ws store.WorkspaceID) (Stats, []Summary, error) {
	var all []Summary
	cursor := ""
	for len(all) < 1000 {
		page, next, err := s.List(ctx, ws, store.Page{Limit: 100, Cursor: cursor})
		if err != nil {
			return Stats{}, nil, err
		}
		all = append(all, page...)
		if next == "" {
			break
		}
		cursor = next
	}
	var st Stats
	st.Projects = len(all)
	for _, p := range all {
		for _, c := range []*store.Connection{p.Source, p.Target} {
			if c == nil {
				continue
			}
			st.RepositoriesTotal++
			if c.LastTestStatus == "ok" {
				st.RepositoriesOK++
			}
		}
		if p.Status == StatusConfigured {
			st.ConfiguredProjects++
		}
	}
	recent := all
	if len(recent) > 3 {
		recent = recent[:3]
	}
	return st, recent, nil
}

func (s *Service) audit(ctx context.Context, ws store.WorkspaceID, user, action, id string, meta map[string]string) {
	var raw json.RawMessage
	if meta != nil {
		raw, _ = json.Marshal(meta)
	}
	if err := s.store.Audit().Record(ctx, store.AuditEvent{WorkspaceID: ws, UserID: user, Action: action, EntityType: "project", EntityID: id, Metadata: raw}); err != nil {
		slog.WarnContext(ctx, "audit record failed", "action", action, "error", err.Error())
	}
}

// IsNotFound is a convenience for handlers.
func IsNotFound(err error) bool { return errors.Is(err, store.ErrNotFound) }
