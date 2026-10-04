package auth

import (
	"errors"
	"net"
	"net/http"
	"strconv"

	"omnidatabus/internal/httpapi"
	"omnidatabus/internal/problem"
	"omnidatabus/internal/store"

	"github.com/go-chi/chi/v5"
)

// Handlers exposes /auth routes.
type Handlers struct {
	Svc     *Service
	Cookies CookieSettings
}

// Routes implements httpapi.Registrar.
func (h Handlers) Routes(r chi.Router) {
	r.Post("/auth/signup", h.signup)
	r.Post("/auth/login", h.login)
	r.With(RequireAuth).Post("/auth/logout", h.logout)
	r.With(RequireAuth).Get("/auth/me", h.me)
	r.With(RequireAuth).Post("/auth/step-up", h.stepUp)
}

type stepUpRequest struct {
	Password string `json:"password" validate:"required"`
}

// stepUp re-confirms the password before a sensitive action, such as exporting credentials.
func (h Handlers) stepUp(w http.ResponseWriter, r *http.Request) {
	var req stepUpRequest
	if err := httpapi.DecodeJSON(w, r, &req); err != nil {
		problem.Write(w, r, err)
		return
	}
	p, _ := PrincipalFrom(r.Context())
	if err := h.Svc.StepUp(r.Context(), p, req.Password, meta(r)); err != nil {
		var rl *RateLimitedError
		if errors.As(err, &rl) {
			w.Header().Set("Retry-After", strconv.Itoa(rl.Seconds()))
		}
		problem.Write(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type signupRequest struct {
	Email       string `json:"email" validate:"required,email,max=254"`
	Password    string `json:"password" validate:"required"`
	DisplayName string `json:"displayName" validate:"required,max=80"`
}

type loginRequest struct {
	Email    string `json:"email" validate:"required,max=254"`
	Password string `json:"password" validate:"required"`
}

// UserDTO is a user in auth responses.
type UserDTO struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	DisplayName string `json:"displayName"`
}

// WorkspaceDTO is a workspace in auth responses.
type WorkspaceDTO struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// SessionResponse is returned by sign-up, sign-in and /auth/me.
type SessionResponse struct {
	User      UserDTO      `json:"user"`
	Workspace WorkspaceDTO `json:"workspace"`
	Role      string       `json:"role"`
	CSRFToken string       `json:"csrfToken"`
}

func meta(r *http.Request) RequestMeta {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	return RequestMeta{UserAgent: r.UserAgent(), IP: ip}
}

func response(u store.User, ws store.Workspace, role store.MemberRole, csrf string) SessionResponse {
	return SessionResponse{
		User: UserDTO{ID: u.ID, Email: u.Email, DisplayName: u.DisplayName}, Workspace: WorkspaceDTO{ID: ws.ID, Name: ws.Name},
		Role: string(role), CSRFToken: csrf,
	}
}

func (h Handlers) signup(w http.ResponseWriter, r *http.Request) {
	var req signupRequest
	if err := httpapi.DecodeJSON(w, r, &req); err != nil {
		problem.Write(w, r, err)
		return
	}
	res, err := h.Svc.Signup(r.Context(), SignupInput(req), meta(r))
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	h.Cookies.Set(w, res.Token)
	httpapi.WriteJSON(w, http.StatusCreated, response(res.User, res.Workspace, res.Role, res.Session.CSRFToken))
}

func (h Handlers) login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := httpapi.DecodeJSON(w, r, &req); err != nil {
		problem.Write(w, r, err)
		return
	}
	prev := ""
	if c, err := r.Cookie(CookieName); err == nil {
		prev = c.Value
	}
	res, err := h.Svc.Login(r.Context(), req.Email, req.Password, prev, meta(r))
	if err != nil {
		var rl *RateLimitedError
		if errors.As(err, &rl) {
			w.Header().Set("Retry-After", strconv.Itoa(rl.Seconds()))
		}
		problem.Write(w, r, err)
		return
	}
	h.Cookies.Set(w, res.Token)
	httpapi.WriteJSON(w, http.StatusOK, response(res.User, res.Workspace, res.Role, res.Session.CSRFToken))
}

func (h Handlers) logout(w http.ResponseWriter, r *http.Request) {
	p, _ := PrincipalFrom(r.Context())
	if err := h.Svc.Logout(r.Context(), p); err != nil {
		problem.Write(w, r, err)
		return
	}
	h.Cookies.Clear(w)
	w.WriteHeader(http.StatusNoContent)
}

func (h Handlers) me(w http.ResponseWriter, r *http.Request) {
	p, _ := PrincipalFrom(r.Context())
	me, err := h.Svc.Me(r.Context(), p)
	if err != nil {
		problem.Write(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.WriteJSON(w, http.StatusOK, response(me.User, me.Workspace, me.Role, me.CSRFToken))
}
