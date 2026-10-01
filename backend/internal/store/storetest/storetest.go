// Package storetest is the contract test suite every store.Store implementation must pass.
// memstore runs it in unit tests; cbstore runs it against Couchbase 8.0.3 in integration tests.
package storetest

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"omnidatabus/internal/store"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// Clock is a controllable store.Clock.
type Clock struct {
	mu  sync.Mutex
	now time.Time
}

// NewClock starts at a fixed instant.
func NewClock() *Clock { return &Clock{now: time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)} }

// Now implements store.Clock.
func (c *Clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }

// Advance moves the clock forward.
func (c *Clock) Advance(d time.Duration) { c.mu.Lock(); c.now = c.now.Add(d); c.mu.Unlock() }

// Factory returns a fresh, empty store using clock.
type Factory func(t *testing.T, clock store.Clock) store.Store

// workspaces returns two fresh workspace IDs so subtests are isolated even on a shared store.
func workspaces() (store.WorkspaceID, store.WorkspaceID) {
	id := uuid.NewString()
	return store.WorkspaceID("ws-a-" + id), store.WorkspaceID("ws-b-" + id)
}

// Run executes the whole contract suite.
func Run(t *testing.T, newStore Factory) {
	t.Run("Projects", func(t *testing.T) { RunProjects(t, newStore) })
	t.Run("Connections", func(t *testing.T) { RunConnections(t, newStore) })
	t.Run("Accounts", func(t *testing.T) { RunAccounts(t, newStore) })
	t.Run("Snapshots", func(t *testing.T) { RunSnapshots(t, newStore) })
	t.Run("Mappings", func(t *testing.T) { RunMappings(t, newStore) })
	t.Run("Jobs", func(t *testing.T) { RunJobs(t, newStore) })
	t.Run("Nodes", func(t *testing.T) { RunNodes(t, newStore) })
}

// RunMappings covers OC-035: one mapping per table, versioned updates, listing, cascades.
func RunMappings(t *testing.T, newStore Factory) {
	ctx := context.Background()
	mk := func(p store.Project, schema, table string) store.Mapping {
		return store.Mapping{
			ProjectID: p.ID, SourceSchema: schema, SourceTable: table,
			Target: store.MappingTarget{Bucket: "shop", Scope: "sales", Collection: table, CreateIfMissing: true},
			Spec:   json.RawMessage(`{"docSchema":{"root":{"id":"fld_root","name":"$","type":"object"}}}`), Status: "draft", CreatedBy: "u1",
		}
	}

	t.Run("OC036_Location", func(t *testing.T) {
		wsA, _ := workspaces()
		s := newStore(t, NewClock())
		p, err := s.Projects().Create(ctx, wsA, store.NewProject{Name: "P"})
		require.NoError(t, err)
		// A 1.0-style mapping (target only) is stored and read with a Couchbase location.
		m, err := s.Mappings().Create(ctx, wsA, mk(p, "shop", "orders"))
		require.NoError(t, err)
		got, err := s.Mappings().Get(ctx, wsA, m.ID)
		require.NoError(t, err)
		require.Equal(t, store.Location{Kind: store.KindCouchbase, Bucket: "shop", Scope: "sales", Collection: "orders", CreateIfMissing: true}, got.Location)

		// Updating only the 1.0 target moves the location with it.
		got.Target.Collection = "orders_v2"
		got, err = s.Mappings().Update(ctx, wsA, got)
		require.NoError(t, err)
		require.Equal(t, "orders_v2", got.Location.Collection)

		// A directory location clears the 1.0 target.
		got.SetLocation(store.Location{Kind: store.KindDirectory, Folder: "orders"})
		_, err = s.Mappings().Update(ctx, wsA, got)
		require.NoError(t, err)
		got, err = s.Mappings().Get(ctx, wsA, m.ID)
		require.NoError(t, err)
		require.Equal(t, store.Location{Kind: store.KindDirectory, Folder: "orders"}, got.Location)
		require.Equal(t, store.MappingTarget{}, got.Target)
	})

	t.Run("AC1_CreateListGetUniquePerTable", func(t *testing.T) {
		wsA, wsB := workspaces()
		s := newStore(t, NewClock())
		p, err := s.Projects().Create(ctx, wsA, store.NewProject{Name: "P"})
		require.NoError(t, err)
		a, err := s.Mappings().Create(ctx, wsA, mk(p, "shop", "customer"))
		require.NoError(t, err)
		require.NotEmpty(t, a.ID)
		require.Equal(t, int64(1), a.Version)
		_, err = s.Mappings().Create(ctx, wsA, mk(p, "shop", "Order Details"))
		require.NoError(t, err)
		var ce *store.ConflictError
		_, err = s.Mappings().Create(ctx, wsA, mk(p, "shop", "customer"))
		require.ErrorAs(t, err, &ce, "one mapping per table")
		list, err := s.Mappings().ListForProject(ctx, wsA, p.ID)
		require.NoError(t, err)
		require.Len(t, list, 2)
		require.Equal(t, "Order Details", list[0].SourceTable, "sorted by schema, then table")
		got, err := s.Mappings().Get(ctx, wsA, a.ID)
		require.NoError(t, err)
		require.True(t, got.Target.CreateIfMissing, "OC-014 AC5: createIfMissing persisted")
		require.JSONEq(t, string(a.Spec), string(got.Spec))

		_, err = s.Mappings().Get(ctx, wsB, a.ID)
		require.ErrorIs(t, err, store.ErrNotFound)
		list, err = s.Mappings().ListForProject(ctx, wsB, p.ID)
		require.NoError(t, err)
		require.Empty(t, list)
		_, err = s.Mappings().Create(ctx, wsB, mk(p, "shop", "x"))
		require.ErrorIs(t, err, store.ErrNotFound, "another workspace's project")
	})

	t.Run("AC4_VersionedUpdate", func(t *testing.T) {
		wsA, _ := workspaces()
		clock := NewClock()
		s := newStore(t, clock)
		p, err := s.Projects().Create(ctx, wsA, store.NewProject{Name: "P"})
		require.NoError(t, err)
		m, err := s.Mappings().Create(ctx, wsA, mk(p, "shop", "customer"))
		require.NoError(t, err)
		clock.Advance(time.Minute)
		m.Spec = json.RawMessage(`{"v":2}`)
		m.Status = "ready"
		upd, err := s.Mappings().Update(ctx, wsA, m)
		require.NoError(t, err)
		require.Equal(t, int64(2), upd.Version)
		require.Equal(t, "ready", upd.Status)
		require.True(t, upd.UpdatedAt.After(m.UpdatedAt))
		_, err = s.Mappings().Update(ctx, wsA, m) // still version 1
		require.ErrorIs(t, err, store.ErrVersionConflict)
	})

	t.Run("DeleteAndProjectCascade", func(t *testing.T) {
		wsA, _ := workspaces()
		s := newStore(t, NewClock())
		p, err := s.Projects().Create(ctx, wsA, store.NewProject{Name: "P"})
		require.NoError(t, err)
		a, err := s.Mappings().Create(ctx, wsA, mk(p, "shop", "customer"))
		require.NoError(t, err)
		require.NoError(t, s.Mappings().Delete(ctx, wsA, a.ID))
		_, err = s.Mappings().Get(ctx, wsA, a.ID)
		require.ErrorIs(t, err, store.ErrNotFound)
		require.ErrorIs(t, s.Mappings().Delete(ctx, wsA, a.ID), store.ErrNotFound)
		_, err = s.Mappings().Create(ctx, wsA, mk(p, "shop", "customer"))
		require.NoError(t, err, "the table can be mapped again")
		b, err := s.Mappings().Create(ctx, wsA, mk(p, "shop", "orders"))
		require.NoError(t, err)
		require.NoError(t, s.Projects().Delete(ctx, wsA, p.ID))
		_, err = s.Mappings().Get(ctx, wsA, b.ID)
		require.ErrorIs(t, err, store.ErrNotFound)
	})
}

// RunSnapshots covers OC-010 AC6 (latest flag and retention) and the delete cascades.
func RunSnapshots(t *testing.T, newStore Factory) {
	ctx := context.Background()
	setup := func(t *testing.T) (store.Store, store.WorkspaceID, store.WorkspaceID, store.Project, *Clock) {
		wsA, wsB := workspaces()
		clock := NewClock()
		s := newStore(t, clock)
		p, err := s.Projects().Create(ctx, wsA, store.NewProject{Name: "P"})
		require.NoError(t, err)
		_, err = s.Connections().Upsert(ctx, wsA, p.ID, store.RoleSource, connInput("postgres"))
		require.NoError(t, err)
		return s, wsA, wsB, p, clock
	}
	snap := func(p store.Project, clock *Clock, n int) store.DiscoverySnapshot {
		return store.DiscoverySnapshot{
			ID: uuid.NewString(), ConnectionID: store.ConnectionID(p.ID, store.RoleSource), CapturedAt: clock.Now(),
			DurationMs: int64(n), TableCount: n, Payload: json.RawMessage(`{"database":"shop","n":` + strconv.Itoa(n) + `}`),
		}
	}

	t.Run("AC6_LatestFlagAndRetention", func(t *testing.T) {
		s, wsA, _, p, clock := setup(t)
		connID := store.ConnectionID(p.ID, store.RoleSource)
		_, err := s.Snapshots().Latest(ctx, wsA, connID)
		require.ErrorIs(t, err, store.ErrNotFound, "no snapshot yet")

		var saved []store.DiscoverySnapshot
		for i := 1; i <= 7; i++ {
			clock.Advance(time.Minute)
			sn := snap(p, clock, i)
			require.NoError(t, s.Snapshots().Save(ctx, wsA, sn, 5))
			saved = append(saved, sn)
		}
		latest, err := s.Snapshots().Latest(ctx, wsA, connID)
		require.NoError(t, err)
		require.Equal(t, saved[6].ID, latest.ID)
		require.True(t, latest.IsLatest)
		require.Equal(t, 7, latest.TableCount)
		require.JSONEq(t, string(saved[6].Payload), string(latest.Payload))
		require.True(t, latest.CapturedAt.Equal(saved[6].CapturedAt))

		prev, err := s.Snapshots().Get(ctx, wsA, saved[5].ID)
		require.NoError(t, err)
		require.False(t, prev.IsLatest, "the previous latest is no longer latest")

		ids, err := s.Snapshots().ListIDs(ctx, wsA, connID)
		require.NoError(t, err)
		require.Equal(t, []string{saved[6].ID, saved[5].ID, saved[4].ID, saved[3].ID, saved[2].ID}, ids, "newest first, 5 kept")
		_, err = s.Snapshots().Get(ctx, wsA, saved[0].ID)
		require.ErrorIs(t, err, store.ErrNotFound, "pruned")
	})

	t.Run("AC2_CrossWorkspaceIsNotFound", func(t *testing.T) {
		s, wsA, wsB, p, clock := setup(t)
		sn := snap(p, clock, 1)
		require.NoError(t, s.Snapshots().Save(ctx, wsA, sn, 5))
		_, err := s.Snapshots().Get(ctx, wsB, sn.ID)
		require.ErrorIs(t, err, store.ErrNotFound)
		_, err = s.Snapshots().Latest(ctx, wsB, sn.ConnectionID)
		require.ErrorIs(t, err, store.ErrNotFound)
		require.ErrorIs(t, s.Snapshots().Save(ctx, wsB, snap(p, clock, 2), 5), store.ErrNotFound, "can't save for another workspace's connection")
	})

	t.Run("DeletingConnectionOrProjectRemovesSnapshots", func(t *testing.T) {
		s, wsA, _, p, clock := setup(t)
		sn := snap(p, clock, 1)
		require.NoError(t, s.Snapshots().Save(ctx, wsA, sn, 5))
		require.NoError(t, s.Connections().Delete(ctx, wsA, p.ID, store.RoleSource))
		_, err := s.Snapshots().Get(ctx, wsA, sn.ID)
		require.ErrorIs(t, err, store.ErrNotFound)

		_, err = s.Connections().Upsert(ctx, wsA, p.ID, store.RoleSource, connInput("postgres"))
		require.NoError(t, err)
		_, err = s.Snapshots().Latest(ctx, wsA, sn.ConnectionID)
		require.ErrorIs(t, err, store.ErrNotFound, "a re-created connection starts without snapshots")
		sn2 := snap(p, clock, 2)
		require.NoError(t, s.Snapshots().Save(ctx, wsA, sn2, 5))
		require.NoError(t, s.Projects().Delete(ctx, wsA, p.ID))
		_, err = s.Snapshots().Get(ctx, wsA, sn2.ID)
		require.ErrorIs(t, err, store.ErrNotFound)
	})
}

// RunProjects covers OC-003 AC2, AC4, AC5, AC6 and the project side of AC7.
func RunProjects(t *testing.T, newStore Factory) {
	ctx := context.Background()

	t.Run("CreateAndGet", func(t *testing.T) {
		wsA, wsB := workspaces()
		_ = wsB
		clock := NewClock()
		s := newStore(t, clock)
		p, err := s.Projects().Create(ctx, wsA, store.NewProject{Name: "Customer 360", Description: "d", CreatedBy: "u1"})
		require.NoError(t, err)
		require.NotEmpty(t, p.ID)
		require.Equal(t, wsA, p.WorkspaceID)
		require.True(t, p.CreatedAt.Equal(clock.Now()))
		require.Equal(t, int64(1), p.Version)
		got, err := s.Projects().Get(ctx, wsA, p.ID)
		require.NoError(t, err)
		require.Equal(t, p.Name, got.Name)
		require.Equal(t, "u1", got.CreatedBy)
	})

	t.Run("AC2_CrossWorkspaceGetIsNotFound", func(t *testing.T) {
		wsA, wsB := workspaces()
		_ = wsB
		s := newStore(t, NewClock())
		p, err := s.Projects().Create(ctx, wsA, store.NewProject{Name: "Secret project"})
		require.NoError(t, err)
		_, err = s.Projects().Get(ctx, wsB, p.ID)
		require.ErrorIs(t, err, store.ErrNotFound)
		_, err = s.Projects().Update(ctx, wsB, store.Project{ID: p.ID, Name: "x", Version: p.Version})
		require.ErrorIs(t, err, store.ErrNotFound)
		require.ErrorIs(t, s.Projects().Delete(ctx, wsB, p.ID), store.ErrNotFound)
		items, _, err := s.Projects().List(ctx, wsB, store.Page{})
		require.NoError(t, err)
		require.Empty(t, items)
		_, err = s.Projects().Get(ctx, wsA, "does-not-exist")
		require.ErrorIs(t, err, store.ErrNotFound)
	})

	t.Run("AC4_DuplicateNameWritesNothing", func(t *testing.T) {
		wsA, wsB := workspaces()
		_ = wsB
		s := newStore(t, NewClock())
		_, err := s.Projects().Create(ctx, wsA, store.NewProject{Name: "Orders"})
		require.NoError(t, err)
		_, err = s.Projects().Create(ctx, wsA, store.NewProject{Name: "  orders "})
		require.True(t, store.IsConflict(err, "name"), "got %v", err)
		items, _, err := s.Projects().List(ctx, wsA, store.Page{})
		require.NoError(t, err)
		require.Len(t, items, 1, "the failed create must not leave a project behind")
		_, err = s.Projects().Create(ctx, wsB, store.NewProject{Name: "Orders"})
		require.NoError(t, err, "names are unique per workspace, not globally")
	})

	t.Run("AC4_DeleteCascades", func(t *testing.T) {
		wsA, wsB := workspaces()
		_ = wsB
		s := newStore(t, NewClock())
		p, err := s.Projects().Create(ctx, wsA, store.NewProject{Name: "Temp"})
		require.NoError(t, err)
		_, err = s.Connections().Upsert(ctx, wsA, p.ID, store.RoleSource, connInput("pg"))
		require.NoError(t, err)
		_, err = s.Connections().Upsert(ctx, wsA, p.ID, store.RoleTarget, connInput("cb"))
		require.NoError(t, err)

		require.NoError(t, s.Projects().Delete(ctx, wsA, p.ID))
		_, err = s.Projects().Get(ctx, wsA, p.ID)
		require.ErrorIs(t, err, store.ErrNotFound)
		_, err = s.Connections().Get(ctx, wsA, store.ConnectionID(p.ID, store.RoleSource))
		require.ErrorIs(t, err, store.ErrNotFound, "connections are deleted with the project")
		_, err = s.Projects().Create(ctx, wsA, store.NewProject{Name: "Temp"})
		require.NoError(t, err, "the name lock is released")
	})

	t.Run("AC5_PaginatesAll", func(t *testing.T) {
		wsA, wsB := workspaces()
		_ = wsB
		clock := NewClock()
		s := newStore(t, clock)
		for i := 0; i < 55; i++ {
			_, err := s.Projects().Create(ctx, wsA, store.NewProject{Name: fmt.Sprintf("p-%02d", i)})
			require.NoError(t, err)
			if i%2 == 0 { // some identical timestamps exercise the id tie-break
				clock.Advance(time.Millisecond)
			}
		}
		seen := map[string]bool{}
		cursor, pages := "", 0
		var prev *store.Project
		for {
			items, next, err := s.Projects().List(ctx, wsA, store.Page{Limit: 20, Cursor: cursor})
			require.NoError(t, err)
			pages++
			for i := range items {
				require.False(t, seen[items[i].ID], "duplicate across pages")
				seen[items[i].ID] = true
				if prev != nil {
					require.True(t, store.Before(items[i].CreatedAt, items[i].ID, prev.CreatedAt, prev.ID), "newest first")
				}
				prev = &items[i]
			}
			if next == "" {
				break
			}
			cursor = next
		}
		require.Len(t, seen, 55)
		require.Equal(t, 3, pages)
	})

	t.Run("AC5_InvalidCursor", func(t *testing.T) {
		wsA, wsB := workspaces()
		_ = wsB
		s := newStore(t, NewClock())
		_, _, err := s.Projects().List(ctx, wsA, store.Page{Cursor: "%%%"})
		require.ErrorIs(t, err, store.ErrInvalidCursor)
	})

	t.Run("AC6_UpdatedAtAdvances", func(t *testing.T) {
		wsA, wsB := workspaces()
		_ = wsB
		clock := NewClock()
		s := newStore(t, clock)
		p, err := s.Projects().Create(ctx, wsA, store.NewProject{Name: "Before"})
		require.NoError(t, err)
		created := p.CreatedAt
		clock.Advance(time.Hour)
		p.Name, p.Description = "After", "new"
		u, err := s.Projects().Update(ctx, wsA, p)
		require.NoError(t, err)
		require.True(t, u.CreatedAt.Equal(created))
		require.True(t, u.UpdatedAt.Equal(clock.Now()))
		require.Equal(t, p.Version+1, u.Version)
		require.Equal(t, "After", u.Name)
	})

	t.Run("OptimisticConcurrency", func(t *testing.T) {
		wsA, wsB := workspaces()
		_ = wsB
		s := newStore(t, NewClock())
		p, err := s.Projects().Create(ctx, wsA, store.NewProject{Name: "Shared"})
		require.NoError(t, err)
		first := p
		first.Description = "tab 1"
		_, err = s.Projects().Update(ctx, wsA, first)
		require.NoError(t, err)
		second := p // stale version
		second.Description = "tab 2"
		_, err = s.Projects().Update(ctx, wsA, second)
		require.ErrorIs(t, err, store.ErrVersionConflict)
	})

	t.Run("RenameMovesNameLock", func(t *testing.T) {
		wsA, wsB := workspaces()
		_ = wsB
		s := newStore(t, NewClock())
		p, err := s.Projects().Create(ctx, wsA, store.NewProject{Name: "Alpha"})
		require.NoError(t, err)
		_, err = s.Projects().Create(ctx, wsA, store.NewProject{Name: "Beta"})
		require.NoError(t, err)
		p.Name = "Beta"
		_, err = s.Projects().Update(ctx, wsA, p)
		require.True(t, store.IsConflict(err, "name"))
		p.Name = "Gamma"
		_, err = s.Projects().Update(ctx, wsA, p)
		require.NoError(t, err)
		_, err = s.Projects().Create(ctx, wsA, store.NewProject{Name: "Alpha"})
		require.NoError(t, err, "old name is free again")
	})
}

func connInput(typ string) store.ConnectionInput {
	return store.ConnectionInput{
		Type: typ, Name: typ + " conn", Config: json.RawMessage(`{"host":"h"}`),
		Secret:         store.Sealed{KeyID: "k1", Ciphertext: []byte{1, 2, 3}, Nonce: []byte{4}, WrappedDEK: []byte{5}, DEKNonce: []byte{6}},
		LastTestStatus: "ok", CreatedBy: "u1",
	}
}

// RunConnections covers OC-003 AC7 and tenant isolation for connections.
func RunConnections(t *testing.T, newStore Factory) {
	ctx := context.Background()

	t.Run("AC7_UpsertByProjectRole", func(t *testing.T) {
		wsA, wsB := workspaces()
		_ = wsB
		clock := NewClock()
		s := newStore(t, clock)
		p, err := s.Projects().Create(ctx, wsA, store.NewProject{Name: "P"})
		require.NoError(t, err)

		first, err := s.Connections().Upsert(ctx, wsA, p.ID, store.RoleSource, connInput("postgres"))
		require.NoError(t, err)
		require.Equal(t, store.ConnectionID(p.ID, store.RoleSource), first.ID)
		require.Equal(t, int64(1), first.Version)

		clock.Advance(time.Minute)
		in := connInput("postgres")
		in.Config = json.RawMessage(`{"host":"new"}`)
		in.Secret.Ciphertext = []byte{9, 9}
		second, err := s.Connections().Upsert(ctx, wsA, p.ID, store.RoleSource, in)
		require.NoError(t, err)
		require.Equal(t, first.ID, second.ID, "same ID retained")
		require.True(t, second.CreatedAt.Equal(first.CreatedAt))
		require.True(t, second.UpdatedAt.After(first.UpdatedAt))
		require.Equal(t, int64(2), second.Version)
		require.JSONEq(t, `{"host":"new"}`, string(second.Config))
		require.Equal(t, []byte{9, 9}, second.Secret.Ciphertext)

		list, err := s.Connections().ListForProject(ctx, wsA, p.ID)
		require.NoError(t, err)
		require.Len(t, list, 1, "never two sources")

		_, err = s.Connections().Upsert(ctx, wsA, p.ID, store.RoleTarget, connInput("couchbase"))
		require.NoError(t, err)
		list, err = s.Connections().ListForProject(ctx, wsA, p.ID)
		require.NoError(t, err)
		require.Len(t, list, 2)
	})

	t.Run("UpsertRequiresProjectInWorkspace", func(t *testing.T) {
		wsA, wsB := workspaces()
		_ = wsB
		s := newStore(t, NewClock())
		p, err := s.Projects().Create(ctx, wsA, store.NewProject{Name: "P"})
		require.NoError(t, err)
		_, err = s.Connections().Upsert(ctx, wsB, p.ID, store.RoleSource, connInput("postgres"))
		require.ErrorIs(t, err, store.ErrNotFound)
		_, err = s.Connections().Upsert(ctx, wsA, "missing", store.RoleSource, connInput("postgres"))
		require.ErrorIs(t, err, store.ErrNotFound)
	})

	t.Run("AC2_CrossWorkspaceConnectionIsNotFound", func(t *testing.T) {
		wsA, wsB := workspaces()
		_ = wsB
		s := newStore(t, NewClock())
		p, err := s.Projects().Create(ctx, wsA, store.NewProject{Name: "P"})
		require.NoError(t, err)
		c, err := s.Connections().Upsert(ctx, wsA, p.ID, store.RoleSource, connInput("postgres"))
		require.NoError(t, err)
		_, err = s.Connections().Get(ctx, wsB, c.ID)
		require.ErrorIs(t, err, store.ErrNotFound)
		list, err := s.Connections().ListForProject(ctx, wsB, p.ID)
		require.NoError(t, err)
		require.Empty(t, list)
	})

	t.Run("RewrapListingAndReplaceSecret", func(t *testing.T) {
		wsA, wsB := workspaces()
		s := newStore(t, NewClock())
		pa, err := s.Projects().Create(ctx, wsA, store.NewProject{Name: "P"})
		require.NoError(t, err)
		pb, err := s.Projects().Create(ctx, wsB, store.NewProject{Name: "P"})
		require.NoError(t, err)
		marker := "k-" + string(wsA) // unique key id so a shared store's other rows don't interfere
		in := connInput("postgres")
		in.Secret.KeyID = marker
		ca, err := s.Connections().Upsert(ctx, wsA, pa.ID, store.RoleSource, in)
		require.NoError(t, err)
		cb, err := s.Connections().Upsert(ctx, wsB, pb.ID, store.RoleSource, in)
		require.NoError(t, err)

		mine := func(list []store.Connection) []string {
			var ids []string
			for _, c := range list {
				if c.Secret.KeyID == marker {
					ids = append(ids, c.ID)
				}
			}
			return ids
		}
		list, err := s.Connections().ListForRewrap(ctx, "active-key", "", 1000)
		require.NoError(t, err)
		require.ElementsMatch(t, []string{ca.ID, cb.ID}, mine(list), "lists across workspaces")

		newSealed := in.Secret
		newSealed.KeyID = "active-key"
		require.NoError(t, s.Connections().ReplaceSecret(ctx, ca, newSealed))
		require.ErrorIs(t, s.Connections().ReplaceSecret(ctx, ca, newSealed), store.ErrVersionConflict, "stale version")
		got, err := s.Connections().Get(ctx, wsA, ca.ID)
		require.NoError(t, err)
		require.Equal(t, "active-key", got.Secret.KeyID)

		list, err = s.Connections().ListForRewrap(ctx, "active-key", "", 1000)
		require.NoError(t, err)
		require.ElementsMatch(t, []string{cb.ID}, mine(list))
	})

	t.Run("SetTestResultAndDelete", func(t *testing.T) {
		wsA, wsB := workspaces()
		_ = wsB
		clock := NewClock()
		s := newStore(t, clock)
		p, err := s.Projects().Create(ctx, wsA, store.NewProject{Name: "P"})
		require.NoError(t, err)
		c, err := s.Connections().Upsert(ctx, wsA, p.ID, store.RoleTarget, connInput("couchbase"))
		require.NoError(t, err)
		at := clock.Now().Add(time.Minute)
		require.NoError(t, s.Connections().SetTestResult(ctx, wsA, c.ID, "failed", at))
		got, err := s.Connections().Get(ctx, wsA, c.ID)
		require.NoError(t, err)
		require.Equal(t, "failed", got.LastTestStatus)
		require.NotNil(t, got.LastTestedAt)
		require.True(t, got.LastTestedAt.Equal(at))

		require.NoError(t, s.Connections().Delete(ctx, wsA, p.ID, store.RoleTarget))
		_, err = s.Connections().Get(ctx, wsA, c.ID)
		require.ErrorIs(t, err, store.ErrNotFound)
		require.ErrorIs(t, s.Connections().Delete(ctx, wsA, p.ID, store.RoleTarget), store.ErrNotFound)
	})
}
