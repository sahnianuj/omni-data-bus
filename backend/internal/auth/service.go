package auth

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"omnidatabus/internal/store"

	"github.com/google/uuid"
)

// Principal is the authenticated caller, stored in the request context.
type Principal struct {
	UserID      string
	WorkspaceID store.WorkspaceID
	Role        store.MemberRole
	SessionID   string
	CSRFToken   string
	// StepUpAt is the session's last password re-confirmation (nil = none).
	StepUpAt *time.Time
}

// Settings configure sessions and sign-up.
type Settings struct {
	IdleTTL       time.Duration
	AbsoluteTTL   time.Duration
	SignupEnabled bool
}

// RequestMeta describes the HTTP client for sessions and rate limiting.
type RequestMeta struct {
	UserAgent string
	IP        string
}

// Result is returned by sign-up and sign-in.
type Result struct {
	Token     string // raw session token for the cookie; never stored
	Session   store.Session
	User      store.User
	Workspace store.Workspace
	Role      store.MemberRole
}

// Service implements authentication use cases.
type Service struct {
	store    store.Store
	hasher   Hasher
	clock    store.Clock
	settings Settings
	limiter  *LoginLimiter
	// dummyHash is verified for unknown emails so response time doesn't reveal which emails exist.
	dummyHash string
}

// NewService builds the auth service.
func NewService(st store.Store, hasher Hasher, clock store.Clock, settings Settings) (*Service, error) {
	dummy, err := hasher.Hash("dummy-password-for-timing-equalization")
	if err != nil {
		return nil, err
	}
	return &Service{store: st, hasher: hasher, clock: clock, settings: settings, limiter: NewLoginLimiter(clock), dummyHash: dummy}, nil
}

// SignupInput is the sign-up request.
type SignupInput struct {
	Email       string
	Password    string
	DisplayName string
}

// Signup creates a user, a personal workspace (owner) and a session.
func (s *Service) Signup(ctx context.Context, in SignupInput, meta RequestMeta) (Result, error) {
	if !s.settings.SignupEnabled {
		return Result{}, ErrSignupDisabled
	}
	email := store.NormalizeEmail(in.Email)
	if msg := ValidatePassword(in.Password, email); msg != "" {
		return Result{}, validationProblem("password", msg)
	}
	hash, err := s.hasher.Hash(in.Password)
	if err != nil {
		return Result{}, err
	}
	now := s.clock.Now()
	name := strings.TrimSpace(in.DisplayName)
	user := store.User{ID: uuid.Must(uuid.NewV7()).String(), Email: email, PasswordHash: hash, DisplayName: name, CreatedAt: now}
	ws := store.Workspace{ID: uuid.Must(uuid.NewV7()).String(), Name: name + "'s workspace", CreatedAt: now}
	token, sess := s.newSession(user.ID, ws.ID, meta)
	if err := s.store.Accounts().Signup(ctx, store.NewAccount{User: user, Workspace: ws, Session: sess}); err != nil {
		if store.IsConflict(err, "email") {
			return Result{}, ErrEmailTaken
		}
		return Result{}, err
	}
	s.audit(ctx, ws.ID, user.ID, "auth.signup", nil)
	return Result{Token: token, Session: sess, User: user, Workspace: ws, Role: store.RoleOwner}, nil
}

// Login verifies credentials and starts a new session. previousToken, if any, is revoked
// (session rotation on login).
func (s *Service) Login(ctx context.Context, email, password, previousToken string, meta RequestMeta) (Result, error) {
	email = store.NormalizeEmail(email)
	limitKey := meta.IP + "|" + email
	if ok, wait := s.limiter.Allow(limitKey); !ok {
		return Result{}, &RateLimitedError{RetryAfter: wait}
	}
	user, err := s.store.Accounts().UserByEmail(ctx, email)
	if errors.Is(err, store.ErrNotFound) {
		_, _ = s.hasher.Verify(s.dummyHash, password) // equalize timing with the known-user path
		s.limiter.Fail(limitKey)
		return Result{}, ErrInvalidCredentials
	}
	if err != nil {
		return Result{}, err
	}
	ok, err := s.hasher.Verify(user.PasswordHash, password)
	if err != nil || !ok {
		s.limiter.Fail(limitKey)
		if m, mErr := s.store.Accounts().DefaultMembership(ctx, user.ID); mErr == nil {
			s.audit(ctx, m.WorkspaceID, user.ID, "auth.login.failure", map[string]string{"ip": meta.IP})
		}
		return Result{}, ErrInvalidCredentials
	}
	s.limiter.Reset(limitKey)

	member, err := s.store.Accounts().DefaultMembership(ctx, user.ID)
	if err != nil {
		return Result{}, err
	}
	ws, err := s.store.Accounts().Workspace(ctx, member.WorkspaceID)
	if err != nil {
		return Result{}, err
	}
	if previousToken != "" {
		_ = s.store.Sessions().Delete(ctx, HashToken(previousToken))
	}
	token, sess := s.newSession(user.ID, ws.ID, meta)
	if err := s.store.Sessions().Create(ctx, sess); err != nil {
		return Result{}, err
	}
	s.audit(ctx, ws.ID, user.ID, "auth.login.success", map[string]string{"ip": meta.IP})
	return Result{Token: token, Session: sess, User: user, Workspace: ws, Role: member.Role}, nil
}

// Logout revokes the session.
func (s *Service) Logout(ctx context.Context, p Principal) error {
	s.audit(ctx, string(p.WorkspaceID), p.UserID, "auth.logout", nil)
	return s.store.Sessions().Delete(ctx, p.SessionID)
}

// Authenticate resolves a cookie token to a principal, enforcing idle and absolute expiry and
// refreshing lastSeenAt at most once per minute.
func (s *Service) Authenticate(ctx context.Context, token string) (Principal, error) {
	id := HashToken(token)
	sess, err := s.store.Sessions().Get(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return Principal{}, ErrSessionExpired
	}
	if err != nil {
		return Principal{}, err
	}
	now := s.clock.Now()
	if now.Sub(sess.LastSeenAt) > s.settings.IdleTTL || now.Sub(sess.CreatedAt) >= s.settings.AbsoluteTTL || !now.Before(sess.ExpiresAt) {
		_ = s.store.Sessions().Delete(ctx, id)
		return Principal{}, ErrSessionExpired
	}
	member, err := s.store.Accounts().Membership(ctx, sess.WorkspaceID, sess.UserID)
	if err != nil {
		return Principal{}, ErrSessionExpired // membership revoked
	}
	if now.Sub(sess.LastSeenAt) >= time.Minute {
		if err := s.store.Sessions().Touch(ctx, id, now); err != nil {
			slog.WarnContext(ctx, "session touch failed", "error", err.Error())
		}
	}
	return Principal{
		UserID: sess.UserID, WorkspaceID: store.WorkspaceID(sess.WorkspaceID), Role: member.Role,
		SessionID: id, CSRFToken: sess.CSRFToken, StepUpAt: sess.StepUpAt,
	}, nil
}

// StepUpWindow is how long a password re-confirmation authorizes sensitive actions (OC-052).
const StepUpWindow = 5 * time.Minute

// SteppedUp reports whether p re-confirmed their password within StepUpWindow.
func (p Principal) SteppedUp(now time.Time) bool {
	return p.StepUpAt != nil && now.Sub(*p.StepUpAt) <= StepUpWindow
}

// StepUp re-confirms the signed-in user's password and records it in the session. Failures
// count toward the sign-in rate limit, like a failed login.
func (s *Service) StepUp(ctx context.Context, p Principal, password string, meta RequestMeta) error {
	limitKey := meta.IP + "|stepup|" + p.UserID
	if ok, wait := s.limiter.Allow(limitKey); !ok {
		return &RateLimitedError{RetryAfter: wait}
	}
	user, err := s.store.Accounts().User(ctx, p.UserID)
	if err != nil {
		return err
	}
	ok, err := s.hasher.Verify(user.PasswordHash, password)
	if err != nil || !ok {
		s.limiter.Fail(limitKey)
		s.audit(ctx, string(p.WorkspaceID), p.UserID, "auth.stepup.failure", map[string]string{"ip": meta.IP})
		return ErrInvalidCredentials
	}
	s.limiter.Reset(limitKey)
	if err := s.store.Sessions().StepUp(ctx, p.SessionID, s.clock.Now()); err != nil {
		return err
	}
	s.audit(ctx, string(p.WorkspaceID), p.UserID, "auth.stepup", nil)
	return nil
}

// Me describes the signed-in user.
type Me struct {
	User      store.User
	Workspace store.Workspace
	Role      store.MemberRole
	CSRFToken string
}

// Me loads the current user and workspace.
func (s *Service) Me(ctx context.Context, p Principal) (Me, error) {
	u, err := s.store.Accounts().User(ctx, p.UserID)
	if err != nil {
		return Me{}, err
	}
	ws, err := s.store.Accounts().Workspace(ctx, string(p.WorkspaceID))
	if err != nil {
		return Me{}, err
	}
	return Me{User: u, Workspace: ws, Role: p.Role, CSRFToken: p.CSRFToken}, nil
}

func (s *Service) newSession(userID, wsID string, meta RequestMeta) (string, store.Session) {
	token, id := NewToken()
	now := s.clock.Now()
	return token, store.Session{
		ID: id, UserID: userID, WorkspaceID: wsID, CSRFToken: NewCSRFToken(), CreatedAt: now, LastSeenAt: now,
		ExpiresAt: now.Add(s.settings.AbsoluteTTL), UserAgent: truncate(meta.UserAgent, 256), IP: meta.IP,
	}
}

func (s *Service) audit(ctx context.Context, ws, user, action string, meta map[string]string) {
	var raw json.RawMessage
	if meta != nil {
		raw, _ = json.Marshal(meta)
	}
	if err := s.store.Audit().Record(ctx, store.AuditEvent{
		WorkspaceID: store.WorkspaceID(ws), UserID: user, Action: action,
		EntityType: "user", EntityID: user, Metadata: raw,
	}); err != nil {
		slog.WarnContext(ctx, "audit record failed", "action", action, "error", err.Error())
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
