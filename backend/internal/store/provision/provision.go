// Package provision creates and upgrades the metadata store's scope, collections and indexes
// through ordered, versioned, idempotent steps (the Couchbase equivalent of SQL migrations).
package provision

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Sentinel errors an Env maps its driver errors to.
var (
	ErrExists    = errors.New("already exists")
	ErrForbidden = errors.New("insufficient permissions")
	// ErrConcurrent means another node advanced the schema version first.
	ErrConcurrent = errors.New("schema version changed concurrently")
)

// NeedsAdminError lists what an administrator must create by hand (e.g. in the Capella UI).
type NeedsAdminError struct{ Items []string }

func (e *NeedsAdminError) Error() string {
	return "provisioning needs an administrator to create:\n  - " + strings.Join(e.Items, "\n  - ")
}

// Env is the storage-specific side of provisioning. cbstore implements it for Couchbase.
type Env interface {
	Scope() string
	// Keyspace returns the fully qualified, quoted keyspace of a collection.
	Keyspace(collection string) string
	CurrentVersion(ctx context.Context) (int, error)
	// SetVersion moves the stored version from `from` to `to`; ErrConcurrent if it isn't `from`.
	SetVersion(ctx context.Context, from, to int) error
	CreateScope(ctx context.Context, scope string) error
	CreateCollection(ctx context.Context, scope, collection string) error
	WaitCollection(ctx context.Context, scope, collection string) error
	Exec(ctx context.Context, statement string) error
}

// Step is one versioned provisioning change. Apply must be idempotent.
type Step struct {
	Version int
	Name    string
	Apply   func(ctx context.Context, env Env) error
	// Plan describes what Apply creates, for `admin provision --print`.
	Plan func(env Env) []string
}

// Collections is every collection in the metadata scope (architecture §5).
var Collections = []string{
	"system", "users", "workspaces", "members", "sessions", "uniques", "projects", "connections",
	"discovery_snapshots", "mappings", "jobs", "job_progress", "job_errors", "job_reports",
	"audit", "leases", "rate_limits",
}

// Indexes are the GSI index definitions; #{name} is replaced by the collection's keyspace.
var Indexes = []string{
	"CREATE INDEX IF NOT EXISTS idx_projects_ws ON #{projects}(workspaceId, createdAt DESC, id DESC)",
	"CREATE INDEX IF NOT EXISTS idx_members_user ON #{members}(userId)",
	"CREATE INDEX IF NOT EXISTS idx_connections_project ON #{connections}(projectId, workspaceId)",
	"CREATE INDEX IF NOT EXISTS idx_audit_ws ON #{audit}(workspaceId, createdAt DESC)",
	"CREATE INDEX IF NOT EXISTS idx_connections_keyid ON #{connections}(secret.keyId, id)",
}

// JobIndexes back the job queries (OC-023, OC-026): the claim loop and reaper by status, job
// history by workspace/project, and the error browser by job.
var JobIndexes = []string{
	"CREATE INDEX IF NOT EXISTS idx_jobs_status ON #{jobs}(status, createdAt, id) WHERE type = 'job'",
	"CREATE INDEX IF NOT EXISTS idx_jobs_ws ON #{jobs}(workspaceId, projectId, createdAt DESC, id DESC) WHERE type = 'job'",
	"CREATE INDEX IF NOT EXISTS idx_job_errors_job ON #{job_errors}(jobId, seq, stage, code, mappingId) WHERE type = 'job_error'",
}

// Expand replaces #{collection} placeholders with env keyspaces.
func Expand(env Env, statement string) string {
	out := statement
	for _, c := range Collections {
		out = strings.ReplaceAll(out, "#{"+c+"}", env.Keyspace(c))
	}
	return out
}

// Steps returns the ordered provisioning steps.
func Steps() []Step {
	return []Step{
		{
			Version: 1, Name: "collections",
			Plan: func(env Env) []string {
				items := []string{"scope " + env.Scope()}
				for _, c := range Collections {
					items = append(items, "collection "+env.Scope()+"."+c)
				}
				return items
			},
			Apply: func(ctx context.Context, env Env) error {
				var missing []string
				if err := env.CreateScope(ctx, env.Scope()); !ok(err) {
					if !errors.Is(err, ErrForbidden) {
						return fmt.Errorf("create scope %s: %w", env.Scope(), err)
					}
					missing = append(missing, "scope "+env.Scope())
				}
				for _, c := range Collections {
					err := env.CreateCollection(ctx, env.Scope(), c)
					switch {
					case ok(err):
						if err := env.WaitCollection(ctx, env.Scope(), c); err != nil {
							return fmt.Errorf("wait for collection %s: %w", c, err)
						}
					case errors.Is(err, ErrForbidden):
						missing = append(missing, "collection "+env.Scope()+"."+c)
					default:
						return fmt.Errorf("create collection %s: %w", c, err)
					}
				}
				if len(missing) > 0 {
					return &NeedsAdminError{Items: missing}
				}
				return nil
			},
		},
		indexStep(2, "indexes", Indexes),
		indexStep(3, "job indexes", JobIndexes),
	}
}

// indexStep creates GSI indexes; ones the credential may not create are listed for an admin.
func indexStep(version int, name string, indexes []string) Step {
	return Step{
		Version: version, Name: name,
		Plan: func(env Env) []string {
			items := make([]string, 0, len(indexes))
			for _, s := range indexes {
				items = append(items, Expand(env, s))
			}
			return items
		},
		Apply: func(ctx context.Context, env Env) error {
			var missing []string
			for _, s := range indexes {
				stmt := Expand(env, s)
				err := env.Exec(ctx, stmt)
				switch {
				case ok(err):
				case errors.Is(err, ErrForbidden):
					missing = append(missing, stmt)
				default:
					return fmt.Errorf("create index: %w", err)
				}
			}
			if len(missing) > 0 {
				return &NeedsAdminError{Items: missing}
			}
			return nil
		},
	}
}

func ok(err error) bool { return err == nil || errors.Is(err, ErrExists) }

// Result reports what Run did.
type Result struct {
	From, To int
	Applied  []string
}

// Run applies every step newer than the stored version, in order.
func Run(ctx context.Context, env Env, steps []Step) (Result, error) {
	current, err := env.CurrentVersion(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("read schema version: %w", err)
	}
	res := Result{From: current, To: current}
	for _, st := range steps {
		if st.Version <= current {
			continue
		}
		if err := st.Apply(ctx, env); err != nil {
			return res, err
		}
		if err := env.SetVersion(ctx, current, st.Version); err != nil {
			if errors.Is(err, ErrConcurrent) {
				// Another node is provisioning too; steps are idempotent, so re-read and continue.
				if current, err = env.CurrentVersion(ctx); err != nil {
					return res, err
				}
				res.To = current
				continue
			}
			return res, fmt.Errorf("record schema version %d: %w", st.Version, err)
		}
		current = st.Version
		res.To = current
		res.Applied = append(res.Applied, fmt.Sprintf("%d %s", st.Version, st.Name))
	}
	return res, nil
}

// Plan lists everything the steps create.
func Plan(env Env, steps []Step) []string {
	var items []string
	for _, st := range steps {
		items = append(items, st.Plan(env)...)
	}
	return items
}
