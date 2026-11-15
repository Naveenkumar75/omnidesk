package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Naveenkumar75/omnidesk/internal/auth"
	"github.com/Naveenkumar75/omnidesk/internal/store"
	"github.com/Naveenkumar75/omnidesk/internal/ticket"
	"github.com/go-chi/chi/v5"
)

const (
	maxSubjectLen     = 255
	maxDescriptionLen = 20_000
	maxCommentLen     = 20_000
	defaultPageSize   = 50
	maxPageSize       = 100
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
	if req.Subject == "" || utf8.RuneCountInString(req.Subject) > maxSubjectLen {
		writeErr(w, http.StatusBadRequest, "subject is required (max 255 characters)")
		return
	}
	if utf8.RuneCountInString(req.Description) > maxDescriptionLen {
		writeErr(w, http.StatusBadRequest, "description is too long (max 20000 characters)")
		return
	}
	email, ok := normalizeEmail(req.RequesterEmail)
	if !ok {
		writeErr(w, http.StatusBadRequest, "a valid requester_email is required")
		return
	}
	if !req.Priority.Valid() {
		writeErr(w, http.StatusBadRequest, "priority must be low, normal, high or urgent")
		return
	}
	t, err := a.Store.CreateTicket(r.Context(), c.TenantID, c.UserID, store.NewTicket{
		Subject: req.Subject, Description: req.Description, RequesterEmail: email, Priority: req.Priority,
	})
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

// listTickets supports ?status=, ?assignee=me|none|<user id>, ?breached=true,
// ?limit= (1-100) and ?cursor= (the next_cursor from the previous page).
func (a *API) listTickets(w http.ResponseWriter, r *http.Request) {
	c, _ := auth.FromContext(r.Context())
	q := r.URL.Query()
	f := store.TicketFilter{Status: ticket.Status(q.Get("status")), Limit: defaultPageSize}

	if f.Status != "" && !f.Status.Valid() {
		writeErr(w, http.StatusBadRequest, "unknown status filter")
		return
	}
	switch v := q.Get("assignee"); v {
	case "":
	case "me":
		f.AssigneeID = &c.UserID
	case "none":
		zero := 0
		f.AssigneeID = &zero
	default:
		id, err := strconv.Atoi(v)
		if err != nil || id < 1 {
			writeErr(w, http.StatusBadRequest, "assignee must be me, none or a user id")
			return
		}
		f.AssigneeID = &id
	}
	switch q.Get("breached") {
	case "", "false":
	case "true":
		f.Breached = true
	default:
		writeErr(w, http.StatusBadRequest, "breached must be true or false")
		return
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxPageSize {
			writeErr(w, http.StatusBadRequest, "limit must be between 1 and 100")
			return
		}
		f.Limit = n
	}
	if v := q.Get("cursor"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			writeErr(w, http.StatusBadRequest, "invalid cursor")
			return
		}
		f.BeforeID = n
	}

	ts, next, err := a.Store.ListTickets(r.Context(), c.TenantID, f)
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	var cursor *int
	if next > 0 {
		cursor = &next
	}
	writeJSON(w, http.StatusOK, map[string]any{"tickets": ts, "next_cursor": cursor})
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
		a.internalError(w, r, err)
		return
	}
	events, err := a.Store.TicketEvents(r.Context(), c.TenantID, id)
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	comments, err := a.Store.TicketComments(r.Context(), c.TenantID, id)
	if err != nil {
		a.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ticket": t, "events": events, "comments": comments})
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
		a.internalError(w, r, err)
	default:
		writeJSON(w, http.StatusOK, t)
	}
}

type assignReq struct {
	AssigneeID *int `json:"assignee_id"` // null unassigns
}

func (a *API) assignTicket(w http.ResponseWriter, r *http.Request) {
	c, _ := auth.FromContext(r.Context())
	id, ok := ticketID(w, r)
	if !ok {
		return
	}
	var req assignReq
	if !decode(w, r, &req) {
		return
	}
	if req.AssigneeID != nil && *req.AssigneeID < 1 {
		writeErr(w, http.StatusBadRequest, "assignee_id must be a user id or null")
		return
	}
	t, err := a.Store.AssignTicket(r.Context(), c.TenantID, id, req.AssigneeID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, http.StatusNotFound, "ticket not found")
	case errors.Is(err, store.ErrInvalidReference):
		// Also the answer for a user id that exists in another tenant.
		writeErr(w, http.StatusUnprocessableEntity, "assignee is not a user in this tenant")
	case err != nil:
		a.internalError(w, r, err)
	default:
		writeJSON(w, http.StatusOK, t)
	}
}

type commentReq struct {
	Body     string `json:"body"`
	Internal bool   `json:"internal"`
}

func (a *API) addComment(w http.ResponseWriter, r *http.Request) {
	c, _ := auth.FromContext(r.Context())
	id, ok := ticketID(w, r)
	if !ok {
		return
	}
	var req commentReq
	if !decode(w, r, &req) {
		return
	}
	req.Body = strings.TrimSpace(req.Body)
	if req.Body == "" || utf8.RuneCountInString(req.Body) > maxCommentLen {
		writeErr(w, http.StatusBadRequest, "body is required (max 20000 characters)")
		return
	}
	cm, err := a.Store.AddComment(r.Context(), c.TenantID, id, c.UserID, req.Body, req.Internal)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, http.StatusNotFound, "ticket not found")
	case errors.Is(err, store.ErrTicketClosed):
		writeErr(w, http.StatusConflict, "ticket is closed")
	case err != nil:
		a.internalError(w, r, err)
	default:
		writeJSON(w, http.StatusCreated, cm)
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
