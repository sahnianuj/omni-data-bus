package store

import (
	"context"
	"encoding/json"
	"time"
)

// Job statuses (architecture §6.3).
const (
	JobQueued              = "queued"
	JobRunning             = "running"
	JobPausing             = "pausing"
	JobPaused              = "paused"
	JobCancelling          = "cancelling"
	JobCancelled           = "cancelled"
	JobCompleted           = "completed"
	JobCompletedWithErrors = "completed_with_errors"
	JobFailed              = "failed"
)

// JobTerminal reports whether status is final (the project's active-job lock is released).
func JobTerminal(status string) bool {
	switch status {
	case JobCancelled, JobCompleted, JobCompletedWithErrors, JobFailed:
		return true
	}
	return false
}

// Job is a migration run of some of a project's mappings (OC-023). Options, Checkpoint and
// Progress are opaque to the store (internal/jobs, internal/pipeline and internal/progress own them).
type Job struct {
	ID          string      `json:"id"`
	WorkspaceID WorkspaceID `json:"workspaceId"`
	ProjectID   string      `json:"projectId"`
	Status      string      `json:"status"`
	// Reason is the machine code for how the job stopped (e.g. ERROR_THRESHOLD_EXCEEDED).
	Reason       string          `json:"reason,omitempty"`
	ReasonDetail string          `json:"reasonDetail,omitempty"`
	Options      json.RawMessage `json:"options"`
	MappingIDs   []string        `json:"mappingIds"`
	Checkpoint   json.RawMessage `json:"checkpoint,omitempty"`
	Progress     json.RawMessage `json:"progress,omitempty"`
	// ErrorsTruncated is set when more errors happened than were stored (OC-026).
	ErrorsTruncated bool       `json:"errorsTruncated,omitempty"`
	ClaimedBy       string     `json:"claimedBy,omitempty"`
	HeartbeatAt     *time.Time `json:"heartbeatAt,omitempty"`
	RetryOf         string     `json:"retryOf,omitempty"`
	CreatedBy       string     `json:"createdBy"`
	CreatedAt       time.Time  `json:"createdAt"`
	UpdatedAt       time.Time  `json:"updatedAt"`
	StartedAt       *time.Time `json:"startedAt,omitempty"`
	FinishedAt      *time.Time `json:"finishedAt,omitempty"`
	// Version increases on every write; Update requires the version that was read (CAS).
	Version int64 `json:"version"`
}

// JobError is one dead-lettered row (OC-026). Key: <jobId>::<seq>.
type JobError struct {
	JobID       string          `json:"jobId"`
	WorkspaceID WorkspaceID     `json:"workspaceId"`
	Seq         int64           `json:"seq"`
	MappingID   string          `json:"mappingId"`
	Stage       string          `json:"stage"` // read | transform | write
	Code        string          `json:"code"`
	Message     string          `json:"message"`
	FieldPath   string          `json:"fieldPath,omitempty"`
	SourceKey   json.RawMessage `json:"sourceKey,omitempty"`
	Sample      json.RawMessage `json:"sample,omitempty"`
	At          time.Time       `json:"at"`
}

// JobErrorFilter narrows ListErrors; empty fields match everything.
type JobErrorFilter struct {
	Stage     string
	Code      string
	MappingID string
}

// JobErrorCount is one (stage, code) group of a job's stored errors.
type JobErrorCount struct {
	Stage   string   `json:"stage"`
	Code    string   `json:"code"`
	Count   int64    `json:"count"`
	Example JobError `json:"example"`
}

// JobEvent is one entry of a job's timeline (OC-028 AC7).
type JobEvent struct {
	At      time.Time       `json:"at"`
	Kind    string          `json:"kind"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// Jobs stores migration jobs and their progress, errors, timelines and worker leases.
type Jobs interface {
	// Create stores a queued job and takes the project's active-job lock; while another job of
	// the project is active it returns &ConflictError{Field: "activeJob"}.
	Create(ctx context.Context, ws WorkspaceID, j Job) (Job, error)
	Get(ctx context.Context, ws WorkspaceID, id string) (Job, error)
	// GetAny reads a job in any workspace (workers only).
	GetAny(ctx context.Context, id string) (Job, error)
	// List returns the workspace's jobs, newest first; projectID "" means every project.
	List(ctx context.Context, ws WorkspaceID, projectID string, page Page) (items []Job, nextCursor string, err error)
	// Update replaces the job if j.Version is still current, else ErrVersionConflict. Reaching a
	// terminal status releases the project's active-job lock; leaving one (resuming a failed
	// job) takes it back, or returns &ConflictError{Field: "activeJob"}.
	Update(ctx context.Context, j Job) (Job, error)
	// ListByStatus lists jobs in any workspace with one of statuses, oldest first (workers).
	ListByStatus(ctx context.Context, statuses []string, limit int) ([]Job, error)
	// ActiveForProject returns the project's non-terminal job, or ErrNotFound.
	ActiveForProject(ctx context.Context, ws WorkspaceID, projectID string) (Job, error)

	// PutProgress stores the latest progress document (OC-027); GetProgress returns it with a
	// revision that changes on every put (the CAS), or ErrNotFound.
	PutProgress(ctx context.Context, jobID string, doc json.RawMessage) error
	GetProgress(ctx context.Context, jobID string) (doc json.RawMessage, rev uint64, err error)

	// PutReport stores the job's verification results (OC-030, job_reports); GetReport returns
	// them, or ErrNotFound.
	PutReport(ctx context.Context, jobID string, doc json.RawMessage) error
	GetReport(ctx context.Context, jobID string) (json.RawMessage, error)

	// AddErrors stores dead-lettered rows; retention is the document expiry (0 = none).
	AddErrors(ctx context.Context, errs []JobError, retention time.Duration) error
	// ListErrors pages through a job's stored errors in Seq order; the cursor is opaque.
	ListErrors(ctx context.Context, ws WorkspaceID, jobID string, f JobErrorFilter, page Page) ([]JobError, string, error)
	SummarizeErrors(ctx context.Context, ws WorkspaceID, jobID string) ([]JobErrorCount, error)

	AppendEvent(ctx context.Context, jobID string, e JobEvent) error
	ListEvents(ctx context.Context, ws WorkspaceID, jobID string) ([]JobEvent, error)

	// AcquireLease takes or renews the named lease for holder until now+ttl; false when another
	// holder's lease hasn't expired.
	AcquireLease(ctx context.Context, name, holder string, ttl time.Duration) (bool, error)
}
