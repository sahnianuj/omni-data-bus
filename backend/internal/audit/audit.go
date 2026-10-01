// Package audit lists the auditable actions, records them, and serves the audit log to
// workspace owners (OC-033).
package audit

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"omnidatabus/internal/store"
)

// Actions are every audited action. Each one is recorded by exactly one code path; the
// coverage test checks that each appears in the code that records it.
var Actions = []string{
	"auth.signup", "auth.login.success", "auth.login.failure", "auth.logout",
	"project.create", "project.update", "project.delete",
	"connection.create", "connection.update", "connection.delete", "connection.test", "connection.discover",
	"keyspace.create", "location.create", "folder.create", "output.path_rejected",
	"mapping.create", "mapping.update", "mapping.automap", "mapping.design", "mapping.delete", "mapping.relocate",
	"job.create", "job.pause", "job.resume", "job.cancel", "job.retry",
	"report.download",
}

// MappingUpdateEvery throttles "mapping.update": the canvas saves often, so one entry per
// mapping per 10 minutes is enough to see who changed it.
const MappingUpdateEvery = 10 * time.Minute

// Recorder writes audit events; a failure is logged, never returned (auditing must not break
// the action it records).
type Recorder struct {
	Store store.Audit
	Now   func() time.Time

	mu   sync.Mutex
	last map[string]time.Time
}

// Record writes one event. meta may be nil; it must not contain secrets.
func (r *Recorder) Record(ctx context.Context, ws store.WorkspaceID, userID, action, entityType, entityID string, meta map[string]string) {
	if r == nil || r.Store == nil {
		return
	}
	var raw json.RawMessage
	if len(meta) > 0 {
		raw, _ = json.Marshal(meta)
	}
	if err := r.Store.Record(ctx, store.AuditEvent{
		WorkspaceID: ws, UserID: userID, Action: action, EntityType: entityType, EntityID: entityID, Metadata: raw,
	}); err != nil {
		slog.WarnContext(ctx, "audit record failed", "action", action, "error", err.Error())
	}
}

// Throttled reports whether key was recorded within every (and, if not, notes it now).
func (r *Recorder) Throttled(key string, every time.Duration) bool {
	if r == nil {
		return false
	}
	now := time.Now()
	if r.Now != nil {
		now = r.Now()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.last == nil {
		r.last = map[string]time.Time{}
	}
	if t, ok := r.last[key]; ok && now.Sub(t) < every {
		return true
	}
	r.last[key] = now
	if len(r.last) > 10_000 { // forget old keys
		for k, t := range r.last {
			if now.Sub(t) >= every {
				delete(r.last, k)
			}
		}
	}
	return false
}
