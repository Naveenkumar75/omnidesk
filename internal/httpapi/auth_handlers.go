package httpapi

import (
	"errors"
	"net/http"
	"net/mail"
	"strings"

	"github.com/Naveenkumar75/omnidesk/internal/auth"
	"github.com/Naveenkumar75/omnidesk/internal/store"
)

type registerReq struct {
	TenantName string `json:"tenant_name"`
	Domain     string `json:"domain"`
	Email      string `json:"email"`
	Password   string `json:"password"`
}

func normalizeEmail(s string) (string, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	addr, err := mail.ParseAddress(s)
	// ParseAddress accepts "Name <a@b>"; require the bare address.
	if err != nil || addr.Address != s || len(s) > 255 {
		return "", false
	}
	return s, true
}

func (a *API) register(w http.ResponseWriter, r *http.Request) {
	if ok, wait := a.AuthPerIP.Allow(clientIP(r)); !ok {
		tooMany(w, wait)
		return
	}
	var req registerReq
	if !decode(w, r, &req) {
		return
	}
	email, ok := normalizeEmail(req.Email)
	req.TenantName = strings.TrimSpace(req.TenantName)
	req.Domain = strings.ToLower(strings.TrimSpace(req.Domain))
	if !ok || req.TenantName == "" || req.Domain == "" || len(req.TenantName) > 255 || len(req.Domain) > 255 {
		writeErr(w, http.StatusBadRequest, "tenant_name, domain and a valid email are required")
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if errors.Is(err, auth.ErrPasswordLength) {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	u, err := a.Store.CreateTenantWithAdmin(r.Context(), req.TenantName, req.Domain, email, hash)
	if errors.Is(err, store.ErrConflict) {
		writeErr(w, http.StatusConflict, "tenant domain or user email already exists")
		return
	}
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"tenant_id": u.TenantID, "user_id": u.ID, "role": u.Role})
}

type loginReq struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (a *API) login(w http.ResponseWriter, r *http.Request) {
	if ok, wait := a.AuthPerIP.Allow(clientIP(r)); !ok {
		tooMany(w, wait)
		return
	}
	var req loginReq
	if !decode(w, r, &req) {
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	if ok, wait := a.LoginPerEmail.Allow(email); !ok {
		tooMany(w, wait)
		return
	}
	u, err := a.Store.UserByEmail(r.Context(), email)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		a.internalError(w, r, err)
		return
	}
	// Unknown email and wrong password get the same response and take the same
	// time (a bcrypt comparison either way), so neither the body nor the
	// latency reveals whether an account exists.
	if errors.Is(err, store.ErrNotFound) {
		auth.BurnPasswordCheck(req.Password)
		writeErr(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	if !auth.CheckPassword(u.PasswordHash, req.Password) {
		writeErr(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	token, err := a.Issuer.Issue(u.ID, u.TenantID, u.Role)
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"token": token})
}

func (a *API) me(w http.ResponseWriter, r *http.Request) {
	c, _ := auth.FromContext(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"user_id": c.UserID, "tenant_id": c.TenantID, "role": c.Role})
}

type createUserReq struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Role     string `json:"role"`
}

// createUser lets an admin add an agent or another admin to their own tenant.
// v1 has no email delivery, so the admin sets the initial password.
func (a *API) createUser(w http.ResponseWriter, r *http.Request) {
	c, _ := auth.FromContext(r.Context())
	var req createUserReq
	if !decode(w, r, &req) {
		return
	}
	email, ok := normalizeEmail(req.Email)
	if !ok {
		writeErr(w, http.StatusBadRequest, "a valid email is required")
		return
	}
	if req.Role == "" {
		req.Role = auth.RoleAgent
	}
	if !auth.ValidRole(req.Role) {
		writeErr(w, http.StatusBadRequest, "role must be admin or agent")
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if errors.Is(err, auth.ErrPasswordLength) {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	u, err := a.Store.CreateUser(r.Context(), c.TenantID, email, req.Role, hash)
	if errors.Is(err, store.ErrConflict) {
		writeErr(w, http.StatusConflict, "a user with this email already exists")
		return
	}
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, u)
}

func (a *API) listUsers(w http.ResponseWriter, r *http.Request) {
	c, _ := auth.FromContext(r.Context())
	users, err := a.Store.ListUsers(r.Context(), c.TenantID)
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": users})
}
