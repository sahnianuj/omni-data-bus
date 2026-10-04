package storetest

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/store"
)

// RunRelationships covers OC-042 virtual relationships: CRUD, ordering, versioned updates,
// tenant isolation and the project-delete cascade.
func RunRelationships(t *testing.T, newStore Factory) {
	ctx := context.Background()
	mk := func(p store.Project, name string) store.Relationship {
		return store.Relationship{
			ProjectID: p.ID, Name: name, Cardinality: "many-to-one", CreatedBy: "u1",
			From: store.RelationshipEnd{Schema: "shop", Table: "orders", Columns: []string{"customer_id"}},
			To:   store.RelationshipEnd{Schema: "shop", Table: "customer", Columns: []string{"customer_id"}},
		}
	}

	t.Run("CreateGetListInCreationOrder", func(t *testing.T) {
		wsA, wsB := workspaces()
		clock := NewClock()
		s := newStore(t, clock)
		p, err := s.Projects().Create(ctx, wsA, store.NewProject{Name: "P"})
		require.NoError(t, err)

		a, err := s.Relationships().Create(ctx, wsA, mk(p, "orders→customer"))
		require.NoError(t, err)
		require.NotEmpty(t, a.ID)
		require.Equal(t, int64(1), a.Version)
		require.Equal(t, wsA, a.WorkspaceID)
		clock.Advance(time.Second)
		b := mk(p, "items→orders")
		b.ID = "vrel_fixed"
		b.Detected = &store.RelationshipDetection{MaxPerKey: 3, FromMaxPerKey: 1, SampledRows: 100, Suggested: "one-to-many", At: clock.Now()}
		b, err = s.Relationships().Create(ctx, wsA, b)
		require.NoError(t, err)
		require.Equal(t, "vrel_fixed", b.ID, "a caller-chosen ID is kept")

		var ce *store.ConflictError
		_, err = s.Relationships().Create(ctx, wsA, b)
		require.ErrorAs(t, err, &ce, "duplicate ID")

		got, err := s.Relationships().Get(ctx, wsA, p.ID, b.ID)
		require.NoError(t, err)
		require.Equal(t, b.From, got.From)
		require.Equal(t, b.To, got.To)
		require.NotNil(t, got.Detected)
		require.Equal(t, int64(3), got.Detected.MaxPerKey)
		require.Equal(t, "one-to-many", got.Detected.Suggested)

		list, err := s.Relationships().ListForProject(ctx, wsA, p.ID)
		require.NoError(t, err)
		require.Len(t, list, 2)
		require.Equal(t, []string{a.ID, b.ID}, []string{list[0].ID, list[1].ID}, "oldest first")

		// Another workspace sees nothing and can't create in this project.
		_, err = s.Relationships().Get(ctx, wsB, p.ID, a.ID)
		require.ErrorIs(t, err, store.ErrNotFound)
		list, err = s.Relationships().ListForProject(ctx, wsB, p.ID)
		require.NoError(t, err)
		require.Empty(t, list)
		_, err = s.Relationships().Create(ctx, wsB, mk(p, "x"))
		require.ErrorIs(t, err, store.ErrNotFound, "another workspace's project")
		_, err = s.Relationships().Get(ctx, wsA, p.ID, "vrel_missing")
		require.ErrorIs(t, err, store.ErrNotFound)
	})

	t.Run("VersionedUpdate", func(t *testing.T) {
		wsA, wsB := workspaces()
		clock := NewClock()
		s := newStore(t, clock)
		p, err := s.Projects().Create(ctx, wsA, store.NewProject{Name: "P"})
		require.NoError(t, err)
		r, err := s.Relationships().Create(ctx, wsA, mk(p, "orders→customer"))
		require.NoError(t, err)

		clock.Advance(time.Minute)
		r.Name, r.Cardinality = "renamed", "one-to-one"
		r.To.Columns = []string{"id"}
		upd, err := s.Relationships().Update(ctx, wsA, r)
		require.NoError(t, err)
		require.Equal(t, int64(2), upd.Version)
		require.Equal(t, "renamed", upd.Name)
		require.True(t, upd.UpdatedAt.After(upd.CreatedAt))
		got, err := s.Relationships().Get(ctx, wsA, p.ID, r.ID)
		require.NoError(t, err)
		require.Equal(t, "one-to-one", got.Cardinality)
		require.Equal(t, []string{"id"}, got.To.Columns)

		_, err = s.Relationships().Update(ctx, wsA, r) // still version 1
		require.ErrorIs(t, err, store.ErrVersionConflict)
		_, err = s.Relationships().Update(ctx, wsB, upd)
		require.ErrorIs(t, err, store.ErrNotFound)
	})

	t.Run("DeleteAndProjectCascade", func(t *testing.T) {
		wsA, wsB := workspaces()
		s := newStore(t, NewClock())
		p, err := s.Projects().Create(ctx, wsA, store.NewProject{Name: "P"})
		require.NoError(t, err)
		a, err := s.Relationships().Create(ctx, wsA, mk(p, "a"))
		require.NoError(t, err)
		b, err := s.Relationships().Create(ctx, wsA, mk(p, "b"))
		require.NoError(t, err)

		require.ErrorIs(t, s.Relationships().Delete(ctx, wsB, p.ID, a.ID), store.ErrNotFound)
		require.NoError(t, s.Relationships().Delete(ctx, wsA, p.ID, a.ID))
		require.ErrorIs(t, s.Relationships().Delete(ctx, wsA, p.ID, a.ID), store.ErrNotFound)
		list, err := s.Relationships().ListForProject(ctx, wsA, p.ID)
		require.NoError(t, err)
		require.Len(t, list, 1)
		require.Equal(t, b.ID, list[0].ID)

		require.NoError(t, s.Projects().Delete(ctx, wsA, p.ID))
		_, err = s.Relationships().Get(ctx, wsA, p.ID, b.ID)
		require.ErrorIs(t, err, store.ErrNotFound)
		list, err = s.Relationships().ListForProject(ctx, wsA, p.ID)
		require.NoError(t, err)
		require.Empty(t, list)
	})
}
