package httpapi

import (
	"errors"
	"net/http"
	"net/mail"
	"strconv"
	"strings"

	"github.com/Naveenkumar75/omnidesk/internal/auth"
	"github.com/Naveenkumar75/omnidesk/internal/store"
	"github.com/Naveenkumar75/omnidesk/internal/ticket"
	"github.com/go-chi/chi/v5"
)

type createTicketReq struct {
	Subject        string          `json:"subject"`
	Description    string          `json:"description"`
	RequesterEmail string          `json:"requester_email"`
	Priority       ticket.Priority `json:"priority"`
}

func (a *API) createTicket(w http.ResponseWriter, r *http.Request) {
	c, _ := auth.FromContext(r.Context())
	var req createTicketReq
	if !decode(w, r, &req) {
		return
	}
	req.Subject = strings.TrimSpace(req.Subject)
	if req.Priority == "" {
		req.Priority = ticket.Normal
	}
	if req.Subject == "" || len(req.Subject) > 255 {
		writeErr(w, http.StatusBadRequest, "subject is required (max 255 characters)")
		return
	}
	if _, err := mail.ParseAddress(req.RequesterEmail); err != nil {
		writeErr(w, http.StatusBadRequest, "a valid requester_email is required")
		return
	}
	if !req.Priority.Valid() {
		writeErr(w, http.StatusBadRequest, "priority must be low, normal, high or urgent")
		return
	}
	t, err := a.Store.CreateTicket(r.Context(), c.TenantID, c.UserID, req.Subject, req.Description, req.RequesterEmail, req.Priority)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

func (a *API) listTickets(w http.ResponseWriter, r *http.Request) {
	c, _ := auth.FromContext(r.Context())
	status := ticket.Status(r.URL.Query().Get("status"))
	if status != "" && !status.Valid() {
		writeErr(w, http.StatusBadRequest, "unknown status filter")
		return
	}
	ts, err := a.Store.ListTickets(r.Context(), c.TenantID, status)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, ts)
}

func (a *API) getTicket(w http.ResponseWriter, r *http.Request) {
	c, _ := auth.FromContext(r.Context())
	id, ok := ticketID(w, r)
	if !ok {
		return
	}
	t, err := a.Store.GetTicket(r.Context(), c.TenantID, id)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "ticket not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	events, err := a.Store.TicketEvents(r.Context(), c.TenantID, id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ticket": t, "events": events})
}

type transitionReq struct {
	Status ticket.Status `json:"status"`
}

func (a *API) transitionTicket(w http.ResponseWriter, r *http.Request) {
	c, _ := auth.FromContext(r.Context())
	id, ok := ticketID(w, r)
	if !ok {
		return
	}
	var req transitionReq
	if !decode(w, r, &req) {
		return
	}
	if !req.Status.Valid() {
		writeErr(w, http.StatusBadRequest, "unknown status")
		return
	}
	t, err := a.Store.TransitionTicket(r.Context(), c.TenantID, id, c.UserID, req.Status)
	var te *ticket.TransitionError
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, http.StatusNotFound, "ticket not found")
	case errors.As(err, &te):
		writeErr(w, http.StatusConflict, te.Error())
	case err != nil:
		writeErr(w, http.StatusInternalServerError, "internal error")
	default:
		writeJSON(w, http.StatusOK, t)
	}
}

func ticketID(w http.ResponseWriter, r *http.Request) (int, bool) {
	id, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil || id < 1 {
		writeErr(w, http.StatusBadRequest, "invalid ticket id")
		return 0, false
	}
	return id, true
}
