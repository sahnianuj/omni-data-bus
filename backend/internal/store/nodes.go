package store

import (
	"context"
	"sort"
	"time"
)

// Node roles as reported in heartbeats (OC-058). A node running with OC_ROLE=all is both.
const (
	NodeRoleAPI    = "api"
	NodeRoleWorker = "worker"
	NodeRoleAll    = "all"
)

// NodeTTL is how long a heartbeat counts: a node that stops beating drops out after it.
const NodeTTL = 60 * time.Second

// NodeBeat is one running node's latest heartbeat.
type NodeBeat struct {
	ID      string    `json:"id"`
	Role    string    `json:"role"`
	Version string    `json:"version"`
	Commit  string    `json:"commit"`
	SeenAt  time.Time `json:"seenAt"`
}

// Nodes is the registry of running API and worker nodes and the build each runs (OC-058).
type Nodes interface {
	// Beat records (or refreshes) b and forgets heartbeats older than NodeTTL.
	Beat(ctx context.Context, b NodeBeat) error
	// Live returns the heartbeats seen within NodeTTL of now, ordered by ID.
	Live(ctx context.Context) ([]NodeBeat, error)
}

// LiveBeats keeps the beats seen within NodeTTL of now, ordered by ID (shared by the stores).
func LiveBeats(beats map[string]NodeBeat, now time.Time) []NodeBeat {
	out := make([]NodeBeat, 0, len(beats))
	for _, b := range beats {
		if now.Sub(b.SeenAt) < NodeTTL {
			out = append(out, b)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// VersionsByRole groups the distinct versions of live nodes by role ("api", "worker"), sorted;
// an "all" node counts for both. consistent is false when more than one version is running.
func VersionsByRole(beats []NodeBeat) (byRole map[string][]string, consistent bool) {
	seen := map[string]map[string]bool{NodeRoleAPI: {}, NodeRoleWorker: {}}
	all := map[string]bool{}
	for _, b := range beats {
		roles := []string{b.Role}
		if b.Role == NodeRoleAll {
			roles = []string{NodeRoleAPI, NodeRoleWorker}
		}
		for _, r := range roles {
			if seen[r] == nil {
				seen[r] = map[string]bool{}
			}
			seen[r][b.Version] = true
		}
		all[b.Version] = true
	}
	byRole = map[string][]string{}
	for r, vs := range seen {
		list := make([]string, 0, len(vs))
		for v := range vs {
			list = append(list, v)
		}
		sort.Strings(list)
		byRole[r] = list
	}
	return byRole, len(all) <= 1
}
