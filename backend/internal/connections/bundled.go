package connections

import (
	"context"
	"encoding/json"
	"net/http"

	"omnidatabus/internal/connectors"
	"omnidatabus/internal/problem"
	"omnidatabus/internal/store"
)

// Bundled is the Couchbase node bundled in the all-in-one image (OC-072), offered as a target.
type Bundled struct {
	ConnStr  string
	Username string
	Password string
	Buckets  []string
}

// BundledInfo is the bundled target as GET /target-kinds describes it (no credentials).
type BundledInfo struct {
	Kind    string   `json:"kind"`
	Label   string   `json:"label"`
	Buckets []string `json:"buckets"`
}

// BundledName names a project's bundled target connection.
const BundledName = "Bundled Couchbase"

// ErrNoBundled is returned outside the all-in-one image.
var ErrNoBundled = problem.New(http.StatusNotFound, problem.CodeNotFound, "No bundled Couchbase",
	"This server doesn't run with a bundled Couchbase node.")

// WithBundled offers the bundled node as a target; a zero ConnStr offers nothing.
func (s *Service) WithBundled(b Bundled) *Service {
	if b.ConnStr != "" {
		s.bundled = &b
	}
	return s
}

// IsBundled reports whether c is the bundled node (OC-072): saved by "Use the bundled
// Couchbase", or pointing at this server's bundled connection string. Such a target means nothing
// outside the container, so it can't be exported (OC-050).
func (s *Service) IsBundled(c store.Connection) bool {
	if c.Name == BundledName {
		return true
	}
	if s.bundled == nil || c.Type != store.KindCouchbase {
		return false
	}
	var cfg struct {
		ConnStr string `json:"connectionString"`
	}
	return json.Unmarshal(c.Config, &cfg) == nil && cfg.ConnStr == s.bundled.ConnStr
}

// BundledTarget describes the bundled target, or nil when there is none.
func (s *Service) BundledTarget() *BundledInfo {
	if s.bundled == nil {
		return nil
	}
	return &BundledInfo{Kind: store.KindCouchbase, Label: BundledName + " (this container)", Buckets: s.bundled.Buckets}
}

// SaveBundled saves the bundled node as the project's target and tests it. The password comes
// from the server's own settings, so it never passes through the browser; it is stored sealed
// like any other credential.
func (s *Service) SaveBundled(ctx context.Context, ws store.WorkspaceID, userID, projectID string) (store.Connection, bool, connectors.TestResult, error) {
	if s.bundled == nil {
		return store.Connection{}, false, connectors.TestResult{}, ErrNoBundled
	}
	cfg, err := json.Marshal(map[string]string{"connectionString": s.bundled.ConnStr, "username": s.bundled.Username, "deployment": "self-managed"})
	if err != nil {
		return store.Connection{}, false, connectors.TestResult{}, err
	}
	pw := s.bundled.Password
	conn, created, err := s.Save(ctx, ws, userID, projectID, store.RoleTarget, SaveInput{
		Type: store.KindCouchbase, Name: BundledName, Config: cfg, Secrets: SecretsPatch{Password: &pw},
	})
	if err != nil {
		return store.Connection{}, false, connectors.TestResult{}, err
	}
	res, err := s.TestStored(ctx, ws, userID, conn.ID)
	if err != nil {
		return store.Connection{}, false, connectors.TestResult{}, err
	}
	if conn, err = s.store.Connections().Get(ctx, ws, conn.ID); err != nil {
		return store.Connection{}, false, connectors.TestResult{}, err
	}
	return conn, created, res, nil
}
