package app

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"os"
	"time"

	"omnidatabus/internal/config"
	"omnidatabus/internal/store"
)

// NodeHeartbeatInterval is how often a node refreshes its entry in the node registry (OC-058);
// an entry counts for store.NodeTTL (60 s), so three missed beats drop a node.
const NodeHeartbeatInterval = 20 * time.Second

// NodeBeat is this process's heartbeat: a unique ID, its role and its build.
func NodeBeat(cfg config.Config, build BuildInfo) store.NodeBeat {
	host, _ := os.Hostname()
	role := cfg.Role
	if role == "" {
		role = store.NodeRoleAll
	}
	return store.NodeBeat{
		ID:   fmt.Sprintf("%s-%d-%04x", host, os.Getpid(), rand.IntN(0xffff)), //nolint:gosec // an identifier, not a secret
		Role: role, Version: build.Version, Commit: build.Commit,
	}
}

// Heartbeat records b now and every interval until ctx is done. Failures are logged, never
// fatal: build info is informational.
func Heartbeat(ctx context.Context, nodes store.Nodes, b store.NodeBeat, interval time.Duration, logger *slog.Logger) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if err := nodes.Beat(ctx, b); err != nil && ctx.Err() == nil {
			logger.Warn("node heartbeat failed", "error", err.Error())
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
