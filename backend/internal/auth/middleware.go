package auth

import (
	"context"
	"crypto/subtle"
	"net/http"
	"net/url"
	"strings"
	"time"

	"omnidatabus/internal/problem"
	"omnidatabus/internal/store"
)

// CookieName is the session cookie.
const CookieName = "oc_session"

type ctxKey int

const (
	principalKey ctxKey = iota
	sessionErrKey
)

// PrincipalFrom returns the authenticated principal, if any.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey).(Principal)
	return p, ok
}

// WithPrincipal stores a principal (used by middleware and tests).
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey, p)
}

// SessionMiddleware loads the principal from the session cookie when present. It never
// rejects: RequireAuth decides. An expired session clears the cookie.
func SessionMiddleware(svc *Service, cookies CookieSettings) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c, err := r.Cookie(CookieName)
			if err != nil || c.Value == "" {
				next.ServeHTTP(w, r)
				return
			}
			p, err := svc.Authenticate(r.Context(), c.Value)
			if err != nil {
				if problem.HasCode(err, problem.CodeSessionExpired) {
					cookies.Clear(w)
				}
				next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), sessionErrKey, err)))
				return
			}
			next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), p)))
		})
	}
}

// RequireAuth rejects anonymous requests with 401 (SESSION_EXPIRED or UNAUTHENTICATED).
func RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := PrincipalFrom(r.Context()); !ok {
			if err, _ := r.Context().Value(sessionErrKey).(error); problem.HasCode(err, problem.CodeSessionExpired) {
				problem.Write(w, r, ErrSessionExpired)
				return
			}
			problem.Write(w, r, ErrUnauthenticated)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireRole allows principals whose role is at least min (owner > editor > viewer).
func RequireRole(minRole store.MemberRole) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p, ok := PrincipalFrom(r.Context())
			if !ok {
				problem.Write(w, r, ErrUnauthenticated)
				return
			}
			if p.Role.Rank() < minRole.Rank() {
				problem.Write(w, r, ErrForbiddenRole)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// CSRF protects mutating requests: a foreign Origin is always rejected, and signed-in requests
// must echo the session's CSRF token in X-CSRF-Token. Safe methods pass through. Sign-in and
// sign-up are Origin-checked only: they start a new session, so a leftover session cookie whose
// token the page never loaded (e.g. the API was down when it opened) must not block them.
func CSRF(publicURL string) func(http.Handler) http.Handler {
	allowed := originOf(publicURL)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet, http.MethodHead, http.MethodOptions:
				next.ServeHTTP(w, r)
				return
			}
			if o := r.Header.Get("Origin"); o != "" && originOf(o) != allowed && originOf(o) != requestOrigin(r) {
				problem.Write(w, r, ErrCSRF)
				return
			}
			if p, ok := PrincipalFrom(r.Context()); ok && !startsSession(r.URL.Path) {
				got := r.Header.Get("X-CSRF-Token")
				if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(p.CSRFToken)) != 1 {
					problem.Write(w, r, ErrCSRF)
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// startsSession reports whether path is an endpoint that replaces the caller's session.
func startsSession(path string) bool {
	return strings.HasSuffix(path, "/auth/login") || strings.HasSuffix(path, "/auth/signup")
}

func originOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	return strings.ToLower(u.Scheme + "://" + u.Host)
}

// requestOrigin is the server's own origin (the SPA served by this binary).
func requestOrigin(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return strings.ToLower(scheme + "://" + r.Host)
}

// CookieSettings controls the session cookie.
type CookieSettings struct {
	Secure bool
	MaxAge time.Duration
}

// Set writes the session cookie.
func (c CookieSettings) Set(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124: Secure comes from config; false only for local plain-http dev (OC_DEV)
		Name: CookieName, Value: token, Path: "/", HttpOnly: true, Secure: c.Secure,
		SameSite: http.SameSiteLaxMode, MaxAge: int(c.MaxAge.Seconds()),
	})
}

// Clear deletes the session cookie.
func (c CookieSettings) Clear(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124: see Set
		Name: CookieName, Value: "", Path: "/", HttpOnly: true, Secure: c.Secure,
		SameSite: http.SameSiteLaxMode, MaxAge: -1,
	})
}
