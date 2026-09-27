//go:build integration

package cbstore

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"omnidatabus/internal/events"
	"omnidatabus/internal/store"
)

// TestEvents_AC5_Integration_CrossConnection: a worker on node A writes progress through its
// connection; a Hub on node B (its own connection) delivers it within 2 s (OC-027 AC5).
func TestEvents_AC5_Integration_CrossConnection(t *testing.T) {
	ctx := context.Background()
	nodeB, err := Open(ctx, shared.cfg, nil)
	require.NoError(t, err)
	defer func() { _ = nodeB.Close() }()

	ws := store.WorkspaceID("ws-ev-" + uuid.NewString())
	p, err := shared.Projects().Create(ctx, ws, store.NewProject{Name: "events"})
	require.NoError(t, err)
	j, err := shared.Jobs().Create(ctx, ws, store.Job{ProjectID: p.ID, Options: json.RawMessage(`{}`)})
	require.NoError(t, err)
	require.NoError(t, shared.Jobs().PutProgress(ctx, j.ID, json.RawMessage(`{"status":"running","seq":1}`)))

	hub := events.NewHub(nodeB.Jobs(), 200*time.Millisecond)
	sub := hub.Subscribe(j.ID)
	defer sub.Close()
	wait := func() []events.Event {
		select {
		case <-sub.Notify():
			return sub.Drain()
		case <-time.After(2 * time.Second):
			t.Fatal("no event within 2 s")
			return nil
		}
	}
	wait() // the current document
	start := time.Now()
	require.NoError(t, shared.Jobs().PutProgress(ctx, j.ID, json.RawMessage(`{"status":"running","seq":2,"processed":500}`)))
	evs := wait()
	require.Contains(t, string(evs[len(evs)-1].Data), `"processed":500`)
	t.Logf("delivered across connections in %s", time.Since(start))
}
