package storetest

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"omnidatabus/internal/store"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func newAccount(clock *Clock, email string) store.NewAccount {
	now := clock.Now()
	u := store.User{ID: uuid.NewString(), Email: email, PasswordHash: "$argon2id$x", DisplayName: "Ada", CreatedAt: now} //nolint:gosec // test fixture, not a credential
	ws := store.Workspace{ID: "ws-" + uuid.NewString(), Name: "Ada's workspace", CreatedAt: now}
	return store.NewAccount{User: u, Workspace: ws, Session: store.Session{
		ID: "sess-" + uuid.NewString(), UserID: u.ID, WorkspaceID: ws.ID, CSRFToken: "csrf", CreatedAt: now,
		LastSeenAt: now, ExpiresAt: now.Add(7 * 24 * time.Hour),
	}}
}

// RunAccounts covers users, workspaces, memberships, sessions and audit (OC-005).
func RunAccounts(t *testing.T, newStore Factory) {
	ctx := context.Background()

	t.Run("SignupCreatesEverything", func(t *testing.T) {
		clock := NewClock()
		s := newStore(t, clock)
		a := newAccount(clock, "Ada."+uuid.NewString()[:8]+"@Example.com ")
		require.NoError(t, s.Accounts().Signup(ctx, a))

		u, err := s.Accounts().UserByEmail(ctx, a.User.Email)
		require.NoError(t, err)
		require.Equal(t, a.User.ID, u.ID)
		require.Equal(t, store.NormalizeEmail(a.User.Email), u.Email, "email stored normalized")
		require.Equal(t, "$argon2id$x", u.PasswordHash)

		m, err := s.Accounts().Membership(ctx, a.Workspace.ID, u.ID)
		require.NoError(t, err)
		require.Equal(t, store.RoleOwner, m.Role)
		dm, err := s.Accounts().DefaultMembership(ctx, u.ID)
		require.NoError(t, err)
		require.Equal(t, a.Workspace.ID, dm.WorkspaceID)
		w, err := s.Accounts().Workspace(ctx, a.Workspace.ID)
		require.NoError(t, err)
		require.Equal(t, "Ada's workspace", w.Name)
		sess, err := s.Sessions().Get(ctx, a.Session.ID)
		require.NoError(t, err)
		require.Equal(t, u.ID, sess.UserID)
	})

	t.Run("DuplicateEmailWritesNothing", func(t *testing.T) {
		clock := NewClock()
		s := newStore(t, clock)
		email := "dup." + uuid.NewString()[:8] + "@example.com"
		first := newAccount(clock, email)
		require.NoError(t, s.Accounts().Signup(ctx, first))
		second := newAccount(clock, "  "+email)
		err := s.Accounts().Signup(ctx, second)
		require.True(t, store.IsConflict(err, "email"), "got %v", err)
		_, err = s.Accounts().User(ctx, second.User.ID)
		require.ErrorIs(t, err, store.ErrNotFound)
		_, err = s.Accounts().Workspace(ctx, second.Workspace.ID)
		require.ErrorIs(t, err, store.ErrNotFound)
		_, err = s.Sessions().Get(ctx, second.Session.ID)
		require.ErrorIs(t, err, store.ErrNotFound)
	})

	t.Run("UnknownLookupsAreNotFound", func(t *testing.T) {
		s := newStore(t, NewClock())
		_, err := s.Accounts().UserByEmail(ctx, "nobody-"+uuid.NewString()+"@example.com")
		require.ErrorIs(t, err, store.ErrNotFound)
		_, err = s.Accounts().Membership(ctx, "ws-x", "u-x")
		require.ErrorIs(t, err, store.ErrNotFound)
		_, err = s.Accounts().DefaultMembership(ctx, "u-"+uuid.NewString())
		require.ErrorIs(t, err, store.ErrNotFound)
	})

	t.Run("AddMemberRoles", func(t *testing.T) {
		clock := NewClock()
		s := newStore(t, clock)
		a := newAccount(clock, "owner."+uuid.NewString()[:8]+"@example.com")
		require.NoError(t, s.Accounts().Signup(ctx, a))
		viewer := "viewer-" + uuid.NewString()
		require.NoError(t, s.Accounts().AddMember(ctx, store.Member{WorkspaceID: a.Workspace.ID, UserID: viewer, Role: store.RoleViewer}))
		m, err := s.Accounts().Membership(ctx, a.Workspace.ID, viewer)
		require.NoError(t, err)
		require.Equal(t, store.RoleViewer, m.Role)
	})

	t.Run("SessionTouchDeleteAndExpiry", func(t *testing.T) {
		clock := NewClock()
		s := newStore(t, clock)
		now := clock.Now()
		sess := store.Session{
			ID: "sess-" + uuid.NewString(), UserID: "u", WorkspaceID: "w", CSRFToken: "c",
			CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(time.Hour),
		}
		require.NoError(t, s.Sessions().Create(ctx, sess))
		require.True(t, store.IsConflict(s.Sessions().Create(ctx, sess), "id"))

		later := now.Add(10 * time.Minute)
		require.NoError(t, s.Sessions().Touch(ctx, sess.ID, later))
		got, err := s.Sessions().Get(ctx, sess.ID)
		require.NoError(t, err)
		require.True(t, got.LastSeenAt.Equal(later))
		require.True(t, got.ExpiresAt.Equal(sess.ExpiresAt), "touch keeps the absolute expiry")
		require.Nil(t, got.StepUpAt)

		// A password re-confirmation is recorded on the session (OC-052).
		stepped := now.Add(11 * time.Minute)
		require.NoError(t, s.Sessions().StepUp(ctx, sess.ID, stepped))
		got, err = s.Sessions().Get(ctx, sess.ID)
		require.NoError(t, err)
		require.NotNil(t, got.StepUpAt)
		require.True(t, got.StepUpAt.Equal(stepped))
		require.True(t, got.ExpiresAt.Equal(sess.ExpiresAt), "step-up keeps the absolute expiry")

		clock.Advance(2 * time.Hour)
		_, err = s.Sessions().Get(ctx, sess.ID)
		require.ErrorIs(t, err, store.ErrNotFound, "expired sessions are gone")

		require.NoError(t, s.Sessions().Delete(ctx, sess.ID))
		require.NoError(t, s.Sessions().Delete(ctx, sess.ID), "delete is idempotent")
	})

	t.Run("AuditIsTenantScoped", func(t *testing.T) {
		wsA, wsB := workspaces()
		s := newStore(t, NewClock())
		require.NoError(t, s.Audit().Record(ctx, store.AuditEvent{
			WorkspaceID: wsA, UserID: "u", Action: "auth.login.success",
			Metadata: json.RawMessage(`{"ip":"1.2.3.4"}`),
		}))
		require.NoError(t, s.Audit().Record(ctx, store.AuditEvent{WorkspaceID: wsB, UserID: "u2", Action: "auth.signup"}))
		list, err := s.Audit().List(ctx, wsA, 10)
		require.NoError(t, err)
		require.Len(t, list, 1)
		require.Equal(t, "auth.login.success", list[0].Action)
		require.JSONEq(t, `{"ip":"1.2.3.4"}`, string(list[0].Metadata))
		require.NotEmpty(t, list[0].ID)
	})

	t.Run("AuditSearchFiltersAndPages", func(t *testing.T) {
		wsA, wsB := workspaces()
		clock := NewClock()
		s := newStore(t, clock)
		for _, e := range []store.AuditEvent{
			{WorkspaceID: wsA, UserID: "u1", Action: "job.create", EntityType: "job", EntityID: "j1"},
			{WorkspaceID: wsA, UserID: "u1", Action: "job.pause", EntityType: "job", EntityID: "j1"},
			{WorkspaceID: wsA, UserID: "u2", Action: "project.update", EntityType: "project", EntityID: "p1"},
			{WorkspaceID: wsA, UserID: "u2", Action: "job.cancel", EntityType: "job", EntityID: "j1"},
			{WorkspaceID: wsB, UserID: "u9", Action: "job.create", EntityType: "job", EntityID: "j9"},
		} {
			clock.Advance(time.Second)
			require.NoError(t, s.Audit().Record(ctx, e))
		}
		actions := func(es []store.AuditEvent) []string {
			out := make([]string, len(es))
			for i, e := range es {
				out[i] = e.Action
			}
			return out
		}
		page1, next, err := s.Audit().Search(ctx, wsA, store.AuditFilter{}, store.Page{Limit: 3})
		require.NoError(t, err)
		require.Equal(t, []string{"job.cancel", "project.update", "job.pause"}, actions(page1), "newest first")
		require.NotEmpty(t, next)
		page2, next, err := s.Audit().Search(ctx, wsA, store.AuditFilter{}, store.Page{Limit: 3, Cursor: next})
		require.NoError(t, err)
		require.Equal(t, []string{"job.create"}, actions(page2))
		require.Empty(t, next)

		jobs, _, err := s.Audit().Search(ctx, wsA, store.AuditFilter{Action: "job."}, store.Page{})
		require.NoError(t, err)
		require.Equal(t, []string{"job.cancel", "job.pause", "job.create"}, actions(jobs), "action prefix")
		exact, _, err := s.Audit().Search(ctx, wsA, store.AuditFilter{Action: "job.pause"}, store.Page{})
		require.NoError(t, err)
		require.Equal(t, []string{"job.pause"}, actions(exact))
		byUser, _, err := s.Audit().Search(ctx, wsA, store.AuditFilter{UserID: "u2", EntityType: "job"}, store.Page{})
		require.NoError(t, err)
		require.Equal(t, []string{"job.cancel"}, actions(byUser))

		_, _, err = s.Audit().Search(ctx, wsA, store.AuditFilter{}, store.Page{Cursor: "%%%"})
		require.ErrorIs(t, err, store.ErrInvalidCursor)
	})
}
