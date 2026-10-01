package storetest

import (
	"context"
	"testing"
	"time"

	"omnidatabus/internal/store"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// RunNodes covers the node registry behind build info (OC-058 AC4).
func RunNodes(t *testing.T, newStore Factory) {
	ctx := context.Background()
	t.Run("AC4_VersionsAndExpiry", func(t *testing.T) {
		clock := NewClock()
		s := newStore(t, clock)
		id := uuid.NewString()
		api, worker, old := "api-"+id, "worker-"+id, "old-"+id
		mine := func() []store.NodeBeat {
			live, err := s.Nodes().Live(ctx)
			require.NoError(t, err)
			var out []store.NodeBeat
			for _, b := range live { // the integration store is shared with other tests
				if b.ID == api || b.ID == worker || b.ID == old {
					out = append(out, b)
				}
			}
			return out
		}
		require.NoError(t, s.Nodes().Beat(ctx, store.NodeBeat{ID: old, Role: store.NodeRoleWorker, Version: "1.0.1", Commit: "aaa"}))
		clock.Advance(30 * time.Second)
		require.NoError(t, s.Nodes().Beat(ctx, store.NodeBeat{ID: api, Role: store.NodeRoleAPI, Version: "2.0.0", Commit: "bbb"}))
		require.NoError(t, s.Nodes().Beat(ctx, store.NodeBeat{ID: worker, Role: store.NodeRoleWorker, Version: "2.0.0", Commit: "bbb"}))

		live := mine()
		require.Len(t, live, 3)
		byRole, consistent := store.VersionsByRole(live)
		require.False(t, consistent)
		require.Equal(t, []string{"1.0.1", "2.0.0"}, byRole[store.NodeRoleWorker])
		require.Equal(t, clock.Now(), live[0].SeenAt.UTC(), "SeenAt is set by the store")

		// The 1.0.1 worker stops beating; 40 s later (70 s after its last beat) it no longer counts.
		clock.Advance(40 * time.Second)
		require.NoError(t, s.Nodes().Beat(ctx, store.NodeBeat{ID: api, Role: store.NodeRoleAPI, Version: "2.0.0"}))
		require.NoError(t, s.Nodes().Beat(ctx, store.NodeBeat{ID: worker, Role: store.NodeRoleWorker, Version: "2.0.0"}))
		live = mine()
		require.Len(t, live, 2)
		_, consistent = store.VersionsByRole(live)
		require.True(t, consistent)
	})
}
