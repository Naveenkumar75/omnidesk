package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/mail"
	"strings"

	"github.com/Naveenkumar75/omnidesk/internal/auth"
	"github.com/Naveenkumar75/omnidesk/internal/store"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

type API struct {
	Store  *store.Store
	Issuer *auth.Issuer
}

func (a *API) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.Recoverer)

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	r.Route("/api/v1", func(r chi.Router) {
		r.Post("/auth/register", a.register)
		r.Post("/auth/login", a.login)

		r.Group(func(r chi.Router) {
			r.Use(a.Issuer.Middleware)
			r.Get("/me", a.me)

			r.Route("/tickets", func(r chi.Router) {
				r.Post("/", a.createTicket)
				r.Get("/", a.listTickets)
				r.Get("/{id}", a.getTicket)
				r.Post("/{id}/transition", a.transitionTicket)
			})
		})
	})
	return r
}

type registerReq struct {
	TenantName string `json:"tenant_name"`
	Domain     string `json:"domain"`
	Email      string `json:"email"`
	Password   string `json:"password"`
}

func (a *API) register(w http.ResponseWriter, r *http.Request) {
	var req registerReq
	if !decode(w, r, &req) {
		return
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	req.Domain = strings.ToLower(strings.TrimSpace(req.Domain))
	if _, err := mail.ParseAddress(req.Email); err != nil || req.TenantName == "" || req.Domain == "" {
		writeErr(w, http.StatusBadRequest, "tenant_name, domain and a valid email are required")
		return
	}
	if len(req.Password) < 8 {
		writeErr(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	u, err := a.Store.CreateTenantWithAdmin(r.Context(), req.TenantName, req.Domain, req.Email, hash)
	if errors.Is(err, store.ErrConflict) {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"tenant_id": u.TenantID, "user_id": u.ID, "role": u.Role})
}

type loginReq struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (a *API) login(w http.ResponseWriter, r *http.Request) {
	var req loginReq
	if !decode(w, r, &req) {
		return
	}
	u, err := a.Store.UserByEmail(r.Context(), strings.ToLower(strings.TrimSpace(req.Email)))
	// Same response for unknown user and wrong password: no account enumeration.
	if err != nil || !auth.CheckPassword(u.PasswordHash, req.Password) {
		writeErr(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	token, err := a.Issuer.Issue(u.ID, u.TenantID, u.Role)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"token": token})
}

func (a *API) me(w http.ResponseWriter, r *http.Request) {
	c, _ := auth.FromContext(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"user_id": c.UserID, "tenant_id": c.TenantID, "role": c.Role})
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
