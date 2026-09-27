package auth

import (
	"context"
	"testing"
	"time"

	"omnidatabus/internal/problem"
	"omnidatabus/internal/store"

	"github.com/stretchr/testify/require"
)

var ctx = context.Background()

func signup(t *testing.T, f fixture, email string) Result {
	t.Helper()
	res, err := f.svc.Signup(ctx, SignupInput{Email: email, Password: goodPassword, DisplayName: "Ada"}, RequestMeta{IP: "1.2.3.4", UserAgent: "test"})
	require.NoError(t, err)
	return res
}

func TestSignup_AC1_CreatesUserWorkspaceMembership(t *testing.T) {
	f := newFixture(t)
	res := signup(t, f, " Ada@Example.com ")
	require.Equal(t, "ada@example.com", res.User.Email)
	require.Equal(t, "Ada's workspace", res.Workspace.Name)
	require.Equal(t, store.RoleOwner, res.Role)
	require.NotEmpty(t, res.Token)

	m, err := f.st.Accounts().Membership(ctx, res.Workspace.ID, res.User.ID)
	require.NoError(t, err)
	require.Equal(t, store.RoleOwner, m.Role)
	u, err := f.st.Accounts().User(ctx, res.User.ID)
	require.NoError(t, err)
	require.NotEqual(t, goodPassword, u.PasswordHash, "only the hash is stored")
	events, err := f.st.Audit().List(ctx, store.WorkspaceID(res.Workspace.ID), 10)
	require.NoError(t, err)
	require.Equal(t, "auth.signup", events[0].Action)
}

func TestSignup_AC1_DuplicateEmail(t *testing.T) {
	f := newFixture(t)
	signup(t, f, "ada@example.com")
	_, err := f.svc.Signup(ctx, SignupInput{Email: "ADA@example.com", Password: goodPassword, DisplayName: "Other"}, RequestMeta{})
	p := problem.From(err)
	require.Equal(t, 409, p.Status)
	require.Equal(t, problem.CodeEmailTaken, p.Code)
}

func TestSignup_AC1_WeakPasswordAndDisabled(t *testing.T) {
	f := newFixture(t)
	_, err := f.svc.Signup(ctx, SignupInput{Email: "a@b.co", Password: "unbelievable", DisplayName: "A"}, RequestMeta{})
	p := problem.From(err)
	require.Equal(t, 422, p.Status)
	require.Equal(t, "password", p.FieldErrors[0].Field)

	f.svc.settings.SignupEnabled = false
	_, err = f.svc.Signup(ctx, SignupInput{Email: "a@b.co", Password: goodPassword, DisplayName: "A"}, RequestMeta{})
	require.Equal(t, problem.CodeSignupDisabled, problem.From(err).Code)
}

func TestLogin_AC3_UnknownEmailSameErrorAndHashes(t *testing.T) {
	f := newFixture(t)
	f.spy.verifies = 0
	_, err := f.svc.Login(ctx, "nobody@example.com", goodPassword, "", RequestMeta{IP: "9.9.9.9"})
	require.Equal(t, ErrInvalidCredentials, err)
	require.Equal(t, 1, f.spy.verifies, "a dummy hash is verified to equalize timing")
}

func TestLogin_AC3_WrongPassword(t *testing.T) {
	f := newFixture(t)
	signup(t, f, "ada@example.com")
	_, err := f.svc.Login(ctx, "ada@example.com", "wrong password here", "", RequestMeta{IP: "9.9.9.9"})
	require.Equal(t, ErrInvalidCredentials, err, "same error as unknown email")
}

func TestLogin_AC3_RotatesSession(t *testing.T) {
	f := newFixture(t)
	first := signup(t, f, "ada@example.com")
	second, err := f.svc.Login(ctx, "ada@example.com", goodPassword, first.Token, RequestMeta{IP: "1.2.3.4"})
	require.NoError(t, err)
	require.NotEqual(t, first.Token, second.Token)
	_, err = f.st.Sessions().Get(ctx, first.Session.ID)
	require.ErrorIs(t, err, store.ErrNotFound, "previous session revoked")
	_, err = f.svc.Authenticate(ctx, second.Token)
	require.NoError(t, err)
}

func TestSessionStore_AC4_StoresOnlyHash(t *testing.T) {
	f := newFixture(t)
	res := signup(t, f, "ada@example.com")
	_, err := f.st.Sessions().Get(ctx, res.Token)
	require.ErrorIs(t, err, store.ErrNotFound, "the raw token is not a key")
	_, err = f.st.Sessions().Get(ctx, HashToken(res.Token))
	require.NoError(t, err)
}

func TestAuthenticate_AC4_ValidIdleAbsolute(t *testing.T) {
	f := newFixture(t)
	res := signup(t, f, "ada@example.com")

	p, err := f.svc.Authenticate(ctx, res.Token)
	require.NoError(t, err)
	require.Equal(t, res.User.ID, p.UserID)
	require.Equal(t, store.RoleOwner, p.Role)

	f.clock.Advance(13 * time.Hour) // idle > 12h
	_, err = f.svc.Authenticate(ctx, res.Token)
	require.Equal(t, ErrSessionExpired, err)

	res2, err := f.svc.Login(ctx, "ada@example.com", goodPassword, "", RequestMeta{})
	require.NoError(t, err)
	for i := 0; i < 20; i++ { // stay active every 11h, but pass the 7-day absolute limit
		f.clock.Advance(11 * time.Hour)
		if _, err = f.svc.Authenticate(ctx, res2.Token); err != nil {
			break
		}
	}
	require.Equal(t, ErrSessionExpired, err)
	require.Equal(t, 401, problem.From(err).Status)
}

func TestAuthenticate_AC4_TouchThrottled(t *testing.T) {
	f := newFixture(t)
	res := signup(t, f, "ada@example.com")
	f.clock.Advance(30 * time.Second)
	_, err := f.svc.Authenticate(ctx, res.Token)
	require.NoError(t, err)
	sess, _ := f.st.Sessions().Get(ctx, res.Session.ID)
	require.True(t, sess.LastSeenAt.Equal(res.Session.LastSeenAt), "no touch within a minute")

	f.clock.Advance(40 * time.Second)
	_, err = f.svc.Authenticate(ctx, res.Token)
	require.NoError(t, err)
	sess, _ = f.st.Sessions().Get(ctx, res.Session.ID)
	require.True(t, sess.LastSeenAt.Equal(f.clock.Now()), "touched after a minute")
}

func TestAuthenticate_AC4_UnknownToken(t *testing.T) {
	f := newFixture(t)
	_, err := f.svc.Authenticate(ctx, "garbage")
	require.Equal(t, ErrSessionExpired, err)
}

func TestLogout_AC4_RevokesSession(t *testing.T) {
	f := newFixture(t)
	res := signup(t, f, "ada@example.com")
	p, err := f.svc.Authenticate(ctx, res.Token)
	require.NoError(t, err)
	require.NoError(t, f.svc.Logout(ctx, p))
	_, err = f.svc.Authenticate(ctx, res.Token)
	require.Equal(t, ErrSessionExpired, err)
}

func TestMe_ReturnsUserAndWorkspace(t *testing.T) {
	f := newFixture(t)
	res := signup(t, f, "ada@example.com")
	p, err := f.svc.Authenticate(ctx, res.Token)
	require.NoError(t, err)
	me, err := f.svc.Me(ctx, p)
	require.NoError(t, err)
	require.Equal(t, "ada@example.com", me.User.Email)
	require.Equal(t, res.Session.CSRFToken, me.CSRFToken)
}
