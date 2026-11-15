package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/Naveenkumar75/omnidesk/internal/auth"
	"github.com/Naveenkumar75/omnidesk/internal/ratelimit"
	"github.com/Naveenkumar75/omnidesk/internal/store"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

type API struct {
	Store  *store.Store
	Issuer *auth.Issuer
	Log    *slog.Logger

	// Optional; defaults are set in Router.
	LoginPerEmail *ratelimit.Limiter // brute force against one account
	AuthPerIP     *ratelimit.Limiter // one client hammering login/register (bcrypt is CPU-heavy)
}

func (a *API) Router() http.Handler {
	if a.Log == nil {
		a.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if a.LoginPerEmail == nil {
		a.LoginPerEmail = ratelimit.New(12*time.Second, 5) // 5 tries, then one per 12s
	}
	if a.AuthPerIP == nil {
		a.AuthPerIP = ratelimit.New(3*time.Second, 20)
	}

	r := chi.NewRouter()
	r.Use(middleware.RequestID, a.logRequests, middleware.Recoverer)

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	r.Get("/readyz", a.readyz)
	r.Get("/openapi.yaml", serveOpenAPI)

	r.Route("/api/v1", func(r chi.Router) {
		r.Post("/auth/register", a.register)
		r.Post("/auth/login", a.login)

		r.Group(func(r chi.Router) {
			r.Use(a.Issuer.Middleware, a.annotateIdentity)
			r.Get("/me", a.me)

			r.Get("/users", a.listUsers)
			r.With(auth.RequireRole(auth.RoleAdmin)).Post("/users", a.createUser)

			r.Route("/tickets", func(r chi.Router) {
				r.Post("/", a.createTicket)
				r.Get("/", a.listTickets)
				r.Get("/{id}", a.getTicket)
				r.Post("/{id}/transition", a.transitionTicket)
				r.Put("/{id}/assignee", a.assignTicket)
				r.Post("/{id}/comments", a.addComment)
			})
		})
	})
	return r
}

func (a *API) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if a.Store == nil || a.Store.Ping(ctx) != nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

// ---- request logging ------------------------------------------------------

type reqInfoKey struct{}

// reqInfo is filled in by later middleware (auth) so the access log, written
// after the handler returns, can include who made the request.
type reqInfo struct{ tenantID, userID int }

func (a *API) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		info := &reqInfo{}
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		start := time.Now()
		next.ServeHTTP(ww, r.WithContext(context.WithValue(r.Context(), reqInfoKey{}, info)))

		route := r.URL.Path
		if rc := chi.RouteContext(r.Context()); rc != nil && rc.RoutePattern() != "" {
			route = rc.RoutePattern() // "/api/v1/tickets/{id}", not one log key per ticket id
		}
		attrs := []slog.Attr{
			slog.String("method", r.Method),
			slog.String("route", route),
			slog.Int("status", ww.Status()),
			slog.Float64("duration_ms", float64(time.Since(start).Microseconds())/1000),
			slog.String("request_id", middleware.GetReqID(r.Context())),
		}
		if info.tenantID != 0 {
			attrs = append(attrs, slog.Int("tenant_id", info.tenantID), slog.Int("user_id", info.userID))
		}
		level := slog.LevelInfo
		if ww.Status() >= 500 {
			level = slog.LevelError
		}
		a.Log.LogAttrs(r.Context(), level, "http request", attrs...)
	})
}

func (a *API) annotateIdentity(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, ok := auth.FromContext(r.Context()); ok {
			if info, ok := r.Context().Value(reqInfoKey{}).(*reqInfo); ok {
				info.tenantID, info.userID = c.TenantID, c.UserID
			}
		}
		next.ServeHTTP(w, r)
	})
}

// internalError logs the real cause (with the request id, so it can be found
// from a client report) and returns a generic message to the client.
func (a *API) internalError(w http.ResponseWriter, r *http.Request, err error) {
	a.Log.ErrorContext(r.Context(), "internal error",
		"err", err, "request_id", middleware.GetReqID(r.Context()), "route", r.URL.Path)
	writeErr(w, http.StatusInternalServerError, "internal error")
}

// ---- rate limiting ---------------------------------------------------------

// clientIP uses the TCP peer address. X-Forwarded-For is not trusted because
// any client can set it; behind a reverse proxy, configure the proxy to limit.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func tooMany(w http.ResponseWriter, wait time.Duration) {
	w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
	writeErr(w, http.StatusTooManyRequests, "too many attempts, try again later")
}

// ---- JSON helpers ----------------------------------------------------------

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields() // a typo like "prority" is an error, not silently ignored
	if err := dec.Decode(v); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeErr(w, http.StatusRequestEntityTooLarge, "request body too large")
			return false
		}
		writeErr(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
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
