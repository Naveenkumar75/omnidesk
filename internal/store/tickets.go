package store

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"time"

	"github.com/Naveenkumar75/omnidesk/internal/ticket"
)

type Ticket struct {
	ID                      int             `json:"id"`
	TenantID                int             `json:"tenant_id"`
	Subject                 string          `json:"subject"`
	Description             string          `json:"description"`
	Status                  ticket.Status   `json:"status"`
	Priority                ticket.Priority `json:"priority"`
	RequesterEmail          string          `json:"requester_email"`
	CreatedBy               int             `json:"created_by"`
	AssigneeID              *int            `json:"assignee_id"`
	FirstResponseDueAt      time.Time       `json:"first_response_due_at"`
	ResolutionDueAt         time.Time       `json:"resolution_due_at"`
	FirstResponseAt         *time.Time      `json:"first_response_at"`
	FirstResponseBreachedAt *time.Time      `json:"first_response_breached_at"`
	ResolutionBreachedAt    *time.Time      `json:"resolution_breached_at"`
	CreatedAt               time.Time       `json:"created_at"`
	UpdatedAt               time.Time       `json:"updated_at"`
}

// TicketEvent is one entry in a ticket's audit trail. Status fields and the
// actor are only set for status changes; SLA breaches are system events.
type TicketEvent struct {
	Kind       string         `json:"kind"`
	FromStatus *ticket.Status `json:"from_status"`
	ToStatus   *ticket.Status `json:"to_status"`
	ActorID    *int           `json:"actor_id"`
	CreatedAt  time.Time      `json:"created_at"`
}

type Comment struct {
	ID        int       `json:"id"`
	AuthorID  int       `json:"author_id"`
	Body      string    `json:"body"`
	Internal  bool      `json:"internal"`
	CreatedAt time.Time `json:"created_at"`
}

const ticketCols = `id, tenant_id, subject, description, status, priority, requester_email, created_by,
	assignee_id, first_response_due_at, resolution_due_at, first_response_at,
	first_response_breached_at, resolution_breached_at, created_at, updated_at`

func scanTicket(row interface{ Scan(...any) error }) (Ticket, error) {
	var t Ticket
	err := row.Scan(&t.ID, &t.TenantID, &t.Subject, &t.Description, &t.Status, &t.Priority,
		&t.RequesterEmail, &t.CreatedBy, &t.AssigneeID, &t.FirstResponseDueAt, &t.ResolutionDueAt,
		&t.FirstResponseAt, &t.FirstResponseBreachedAt, &t.ResolutionBreachedAt, &t.CreatedAt, &t.UpdatedAt)
	return t, err
}

// Every method below takes tenantID first and includes it in the WHERE clause.
// A ticket that belongs to another tenant is indistinguishable from a missing one.

type NewTicket struct {
	Subject        string
	Description    string
	RequesterEmail string
	Priority       ticket.Priority
}

func (s *Store) CreateTicket(ctx context.Context, tenantID, createdBy int, in NewTicket) (Ticket, error) {
	// Deadlines are computed from the database clock so they line up with the
	// now() the SLA sweeper compares against.
	fr, res := ticket.SLAFor(in.Priority).FirstResponse, ticket.SLAFor(in.Priority).Resolution
	t, err := scanTicket(s.db.QueryRowContext(ctx,
		`INSERT INTO tickets (tenant_id, subject, description, priority, requester_email, created_by,
		                      first_response_due_at, resolution_due_at)
		 VALUES ($1, $2, $3, $4, $5, $6, now() + $7 * interval '1 second', now() + $8 * interval '1 second')
		 RETURNING `+ticketCols,
		tenantID, in.Subject, in.Description, in.Priority, in.RequesterEmail, createdBy,
		int64(fr.Seconds()), int64(res.Seconds())))
	return t, mapErr(err)
}

func (s *Store) GetTicket(ctx context.Context, tenantID, id int) (Ticket, error) {
	t, err := scanTicket(s.db.QueryRowContext(ctx,
		`SELECT `+ticketCols+` FROM tickets WHERE tenant_id = $1 AND id = $2`, tenantID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Ticket{}, ErrNotFound
	}
	return t, err
}

// TicketFilter narrows ListTickets. Zero values mean "no filter".
type TicketFilter struct {
	Status     ticket.Status
	AssigneeID *int // 0 means unassigned
	Breached   bool // only tickets that missed an SLA
	BeforeID   int  // keyset cursor: return tickets with id < BeforeID
	Limit      int
}

// ListTickets returns one page of the tenant's tickets, newest first, using
// keyset pagination on id. It returns the cursor for the next page, or 0 when
// this is the last page.
func (s *Store) ListTickets(ctx context.Context, tenantID int, f TicketFilter) ([]Ticket, int, error) {
	q := `SELECT ` + ticketCols + ` FROM tickets WHERE tenant_id = $1`
	args := []any{tenantID}
	add := func(cond string, v any) {
		args = append(args, v)
		q += " AND " + cond + " $" + strconv.Itoa(len(args))
	}
	if f.Status != "" {
		add("status =", f.Status)
	}
	if f.AssigneeID != nil {
		if *f.AssigneeID == 0 {
			q += " AND assignee_id IS NULL"
		} else {
			add("assignee_id =", *f.AssigneeID)
		}
	}
	if f.Breached {
		q += " AND (first_response_breached_at IS NOT NULL OR resolution_breached_at IS NOT NULL)"
	}
	if f.BeforeID > 0 {
		add("id <", f.BeforeID)
	}
	args = append(args, f.Limit+1) // fetch one extra row to know if another page exists
	q += " ORDER BY id DESC LIMIT $" + strconv.Itoa(len(args))

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Ticket{}
	for rows.Next() {
		t, err := scanTicket(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	next := 0
	if len(out) > f.Limit {
		out = out[:f.Limit]
		next = out[len(out)-1].ID
	}
	return out, next, nil
}

// TransitionTicket moves a ticket to a new status and records an audit event in
// one transaction. The row is locked (FOR UPDATE) so two concurrent requests
// cannot both act on the same starting status.
func (s *Store) TransitionTicket(ctx context.Context, tenantID, id, actorID int, to ticket.Status) (Ticket, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Ticket{}, err
	}
	defer tx.Rollback()

	var from ticket.Status
	err = tx.QueryRowContext(ctx,
		`SELECT status FROM tickets WHERE tenant_id = $1 AND id = $2 FOR UPDATE`, tenantID, id).Scan(&from)
	if errors.Is(err, sql.ErrNoRows) {
		return Ticket{}, ErrNotFound
	}
	if err != nil {
		return Ticket{}, err
	}

	if err := ticket.Check(from, to); err != nil {
		return Ticket{}, err
	}

	t, err := scanTicket(tx.QueryRowContext(ctx,
		`UPDATE tickets SET status = $1, updated_at = now()
		 WHERE tenant_id = $2 AND id = $3 RETURNING `+ticketCols, to, tenantID, id))
	if err != nil {
		return Ticket{}, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO ticket_events (ticket_id, tenant_id, actor_id, kind, from_status, to_status)
		 VALUES ($1, $2, $3, 'status_change', $4, $5)`, id, tenantID, actorID, from, to); err != nil {
		return Ticket{}, err
	}
	if err := tx.Commit(); err != nil {
		return Ticket{}, err
	}
	return t, nil
}

// AssignTicket sets or clears (assigneeID nil) the assignee. The composite
// foreign key rejects a user from another tenant with ErrInvalidReference.
func (s *Store) AssignTicket(ctx context.Context, tenantID, id int, assigneeID *int) (Ticket, error) {
	t, err := scanTicket(s.db.QueryRowContext(ctx,
		`UPDATE tickets SET assignee_id = $1, updated_at = now()
		 WHERE tenant_id = $2 AND id = $3 RETURNING `+ticketCols, assigneeID, tenantID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Ticket{}, ErrNotFound
	}
	return t, mapErr(err)
}

// ErrTicketClosed is returned when commenting on a closed ticket.
var ErrTicketClosed = errors.New("ticket is closed")

// AddComment stores a comment. The first public (non-internal) comment counts
// as the first response for the SLA; it is recorded in the same transaction.
func (s *Store) AddComment(ctx context.Context, tenantID, ticketID, authorID int, body string, internal bool) (Comment, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Comment{}, err
	}
	defer tx.Rollback()

	var status ticket.Status
	err = tx.QueryRowContext(ctx,
		`SELECT status FROM tickets WHERE tenant_id = $1 AND id = $2 FOR UPDATE`, tenantID, ticketID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return Comment{}, ErrNotFound
	}
	if err != nil {
		return Comment{}, err
	}
	if status == ticket.Closed {
		return Comment{}, ErrTicketClosed
	}

	c := Comment{AuthorID: authorID, Body: body, Internal: internal}
	if err := tx.QueryRowContext(ctx,
		`INSERT INTO ticket_comments (ticket_id, tenant_id, author_id, body, internal)
		 VALUES ($1, $2, $3, $4, $5) RETURNING id, created_at`,
		ticketID, tenantID, authorID, body, internal).Scan(&c.ID, &c.CreatedAt); err != nil {
		return Comment{}, mapErr(err)
	}
	if !internal {
		if _, err := tx.ExecContext(ctx,
			`UPDATE tickets SET first_response_at = $1, updated_at = now()
			 WHERE tenant_id = $2 AND id = $3 AND first_response_at IS NULL`,
			c.CreatedAt, tenantID, ticketID); err != nil {
			return Comment{}, err
		}
	}
	return c, tx.Commit()
}

func (s *Store) TicketComments(ctx context.Context, tenantID, ticketID int) ([]Comment, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, author_id, body, internal, created_at FROM ticket_comments
		 WHERE tenant_id = $1 AND ticket_id = $2 ORDER BY id`, tenantID, ticketID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Comment{}
	for rows.Next() {
		var c Comment
		if err := rows.Scan(&c.ID, &c.AuthorID, &c.Body, &c.Internal, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) TicketEvents(ctx context.Context, tenantID, id int) ([]TicketEvent, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT kind, from_status, to_status, actor_id, created_at FROM ticket_events
		 WHERE tenant_id = $1 AND ticket_id = $2 ORDER BY id`, tenantID, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TicketEvent{}
	for rows.Next() {
		var e TicketEvent
		if err := rows.Scan(&e.Kind, &e.FromStatus, &e.ToStatus, &e.ActorID, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
