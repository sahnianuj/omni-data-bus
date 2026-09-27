package auth

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"omnidatabus/internal/httpapi"
	"omnidatabus/internal/problem"
	"omnidatabus/internal/store"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
)

const publicURL = "http://localhost:5173"

func testRouter(f fixture) http.Handler {
	cookies := CookieSettings{Secure: true, MaxAge: f.svc.settings.AbsoluteTTL}
	protected := httpapi.RegistrarFunc(func(r chi.Router) {
		r.With(RequireAuth).Post("/things", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusCreated) })
		r.With(RequireAuth).Get("/things", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
		r.With(RequireAuth, RequireRole(store.RoleEditor)).Delete("/things", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})
	})
	return httpapi.NewRouter(httpapi.Options{
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Features:   []httpapi.Registrar{Handlers{Svc: f.svc, Cookies: cookies}, protected},
		Middleware: []func(http.Handler) http.Handler{SessionMiddleware(f.svc, cookies), CSRF(publicURL)},
	})
}

type client struct {
	t      *testing.T
	h      http.Handler
	cookie *http.Cookie
	csrf   string
}

func (c *client) do(method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	c.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = "10.0.0.1:5555"
	req.Header.Set("Content-Type", "application/json")
	if c.cookie != nil {
		req.AddCookie(c.cookie)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	c.h.ServeHTTP(rec, req)
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == CookieName {
			if ck.MaxAge < 0 {
				c.cookie = nil
			} else {
				c.cookie = ck
			}
		}
	}
	var sr SessionResponse
	if json.Unmarshal(rec.Body.Bytes(), &sr) == nil && sr.CSRFToken != "" {
		c.csrf = sr.CSRFToken
	}
	return rec
}

func signedIn(t *testing.T) (*client, fixture) {
	t.Helper()
	f := newFixture(t)
	c := &client{t: t, h: testRouter(f)}
	rec := c.do("POST", "/api/v1/auth/signup", `{"email":"ada@example.com","password":"`+goodPassword+`","displayName":"Ada"}`, nil)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	return c, f
}

func TestSignupHandler_AC1_SetsSecureCookieAndReturnsCSRF(t *testing.T) {
	c, _ := signedIn(t)
	require.NotNil(t, c.cookie)
	require.True(t, c.cookie.HttpOnly)
	require.True(t, c.cookie.Secure)
	require.Equal(t, http.SameSiteLaxMode, c.cookie.SameSite)
	require.NotEmpty(t, c.csrf)
	rec := c.do("GET", "/api/v1/auth/me", "", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"role":"owner"`)
	require.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
}

func TestSignupHandler_AC1_ValidationErrors(t *testing.T) {
	f := newFixture(t)
	c := &client{t: t, h: testRouter(f)}
	rec := c.do("POST", "/api/v1/auth/signup", `{"email":"not-an-email","password":"x","displayName":""}`, nil)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	rec = c.do("POST", "/api/v1/auth/signup", `{"email":"a@b.co","unknown":1}`, nil)
	require.Equal(t, http.StatusBadRequest, rec.Code, "unknown fields rejected")
	rec = c.do("POST", "/api/v1/auth/signup", ``, nil)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestLogout_AC4_DeletesAndClearsCookie(t *testing.T) {
	c, _ := signedIn(t)
	rec := c.do("POST", "/api/v1/auth/logout", "", map[string]string{"X-CSRF-Token": c.csrf})
	require.Equal(t, http.StatusNoContent, rec.Code)
	var cleared bool
	for _, ck := range rec.Result().Cookies() {
		cleared = cleared || (ck.Name == CookieName && ck.MaxAge < 0)
	}
	require.True(t, cleared, "cookie cleared with Max-Age=0")
	require.Equal(t, http.StatusUnauthorized, c.do("GET", "/api/v1/auth/me", "", nil).Code)
}

func TestSessionMiddleware_AC4_ExpiredReturnsSessionExpired(t *testing.T) {
	c, f := signedIn(t)
	f.clock.Advance(13 * 3600 * 1e9)
	rec := c.do("GET", "/api/v1/things", "", nil)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Contains(t, rec.Body.String(), problem.CodeSessionExpired)
}

func TestRequireAuth_AC8_AnonymousIs401(t *testing.T) {
	f := newFixture(t)
	c := &client{t: t, h: testRouter(f)}
	rec := c.do("GET", "/api/v1/things", "", nil)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Contains(t, rec.Body.String(), problem.CodeUnauthenticated)
}

func TestCSRF_AC5_MissingToken(t *testing.T) {
	c, _ := signedIn(t)
	rec := c.do("POST", "/api/v1/things", "{}", nil)
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Contains(t, rec.Body.String(), problem.CodeCSRFFailed)
}

func TestCSRF_AC5_WrongToken(t *testing.T) {
	c, _ := signedIn(t)
	rec := c.do("POST", "/api/v1/things", "{}", map[string]string{"X-CSRF-Token": "nope"})
	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestCSRF_AC5_ForeignOrigin(t *testing.T) {
	c, _ := signedIn(t)
	rec := c.do("POST", "/api/v1/things", "{}", map[string]string{"X-CSRF-Token": c.csrf, "Origin": "https://evil.example"})
	require.Equal(t, http.StatusForbidden, rec.Code)
	f := newFixture(t)
	anon := &client{t: t, h: testRouter(f)}
	rec = anon.do("POST", "/api/v1/auth/login", `{"email":"a@b.co","password":"x"}`, map[string]string{"Origin": "https://evil.example"})
	require.Equal(t, http.StatusForbidden, rec.Code, "login is origin-checked too")
}

func TestCSRF_AC5_LoginWithLeftoverSessionNeedsNoToken(t *testing.T) {
	c, _ := signedIn(t)
	rec := c.do("POST", "/api/v1/auth/login", `{"email":"ada@example.com","password":"`+goodPassword+`"}`, map[string]string{"Origin": publicURL})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	rec = c.do("POST", "/api/v1/auth/logout", "", nil)
	require.Equal(t, http.StatusForbidden, rec.Code, "other mutations still need the token")
}

func TestCSRF_AC5_ValidTokenAndSafeMethodsPass(t *testing.T) {
	c, _ := signedIn(t)
	require.Equal(t, http.StatusCreated, c.do("POST", "/api/v1/things", "{}", map[string]string{"X-CSRF-Token": c.csrf, "Origin": publicURL}).Code)
	require.Equal(t, http.StatusOK, c.do("GET", "/api/v1/things", "", nil).Code)
}

func TestRequireRole_AC6_ViewerBlockedFromEditor(t *testing.T) {
	c, f := signedIn(t)
	me := c.do("GET", "/api/v1/auth/me", "", nil)
	var sr SessionResponse
	require.NoError(t, json.Unmarshal(me.Body.Bytes(), &sr))
	require.NoError(t, f.st.Accounts().AddMember(ctx, store.Member{WorkspaceID: sr.Workspace.ID, UserID: sr.User.ID, Role: store.RoleViewer}))
	rec := c.do("DELETE", "/api/v1/things", "", map[string]string{"X-CSRF-Token": c.csrf})
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Contains(t, rec.Body.String(), problem.CodeForbiddenRole)
	require.Equal(t, http.StatusOK, c.do("GET", "/api/v1/things", "", nil).Code, "viewers can read")
}

func TestRequireRole_AC6_OwnerAllowed(t *testing.T) {
	c, _ := signedIn(t)
	require.Equal(t, http.StatusNoContent, c.do("DELETE", "/api/v1/things", "", map[string]string{"X-CSRF-Token": c.csrf}).Code)
}

func TestLoginHandler_AC7_RetryAfterHeader(t *testing.T) {
	c, _ := signedIn(t)
	c.cookie = nil
	for i := 0; i < 5; i++ {
		c.do("POST", "/api/v1/auth/login", `{"email":"ada@example.com","password":"wrong-password-xx"}`, nil)
	}
	rec := c.do("POST", "/api/v1/auth/login", `{"email":"ada@example.com","password":"`+goodPassword+`"}`, nil)
	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	require.NotEmpty(t, rec.Header().Get("Retry-After"))
}

func TestLoginHandler_AC3_SuccessAndInvalid(t *testing.T) {
	c, _ := signedIn(t)
	c.cookie = nil
	rec := c.do("POST", "/api/v1/auth/login", `{"email":"ada@example.com","password":"wrong-password-xx"}`, nil)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Contains(t, rec.Body.String(), problem.CodeInvalidCreds)
	rec = c.do("POST", "/api/v1/auth/login", `{"email":"ada@example.com","password":"`+goodPassword+`"}`, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, c.cookie)
}
