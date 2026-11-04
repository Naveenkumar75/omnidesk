package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Naveenkumar75/omnidesk/internal/ticket"
)

type Ticket struct {
	ID             int             `json:"id"`
	TenantID       int             `json:"tenant_id"`
	Subject        string          `json:"subject"`
	Description    string          `json:"description"`
	Status         ticket.Status   `json:"status"`
	Priority       ticket.Priority `json:"priority"`
	RequesterEmail string          `json:"requester_email"`
	CreatedBy      int             `json:"created_by"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

type TicketEvent struct {
	FromStatus ticket.Status `json:"from_status"`
	ToStatus   ticket.Status `json:"to_status"`
	ActorID    int           `json:"actor_id"`
	CreatedAt  time.Time     `json:"created_at"`
}

const ticketCols = `id, tenant_id, subject, description, status, priority, requester_email, created_by, created_at, updated_at`

func scanTicket(row interface{ Scan(...any) error }) (Ticket, error) {
	var t Ticket
	err := row.Scan(&t.ID, &t.TenantID, &t.Subject, &t.Description, &t.Status, &t.Priority,
		&t.RequesterEmail, &t.CreatedBy, &t.CreatedAt, &t.UpdatedAt)
	return t, err
}

// Every method below takes tenantID first and includes it in the WHERE clause.
// A ticket that belongs to another tenant is indistinguishable from a missing one.

func (s *Store) CreateTicket(ctx context.Context, tenantID, createdBy int, subject, description, requesterEmail string, p ticket.Priority) (Ticket, error) {
	return scanTicket(s.db.QueryRowContext(ctx,
		`INSERT INTO tickets (tenant_id, subject, description, priority, requester_email, created_by)
		 VALUES ($1, $2, $3, $4, $5, $6) RETURNING `+ticketCols,
		tenantID, subject, description, p, requesterEmail, createdBy))
}

func (s *Store) GetTicket(ctx context.Context, tenantID, id int) (Ticket, error) {
	t, err := scanTicket(s.db.QueryRowContext(ctx,
		`SELECT `+ticketCols+` FROM tickets WHERE tenant_id = $1 AND id = $2`, tenantID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Ticket{}, ErrNotFound
	}
	return t, err
}

// ListTickets returns the tenant's tickets, newest first. An empty status means all.
func (s *Store) ListTickets(ctx context.Context, tenantID int, status ticket.Status) ([]Ticket, error) {
	q := `SELECT ` + ticketCols + ` FROM tickets WHERE tenant_id = $1`
	args := []any{tenantID}
	if status != "" {
		q += ` AND status = $2`
		args = append(args, status)
	}
	q += ` ORDER BY id DESC LIMIT 100`

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Ticket{}
	for rows.Next() {
		t, err := scanTicket(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
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
		`INSERT INTO ticket_events (ticket_id, tenant_id, actor_id, from_status, to_status)
		 VALUES ($1, $2, $3, $4, $5)`, id, tenantID, actorID, from, to); err != nil {
		return Ticket{}, err
	}
	if err := tx.Commit(); err != nil {
		return Ticket{}, err
	}
	return t, nil
}

func (s *Store) TicketEvents(ctx context.Context, tenantID, id int) ([]TicketEvent, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT from_status, to_status, actor_id, created_at FROM ticket_events
		 WHERE tenant_id = $1 AND ticket_id = $2 ORDER BY id`, tenantID, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TicketEvent{}
	for rows.Next() {
		var e TicketEvent
		if err := rows.Scan(&e.FromStatus, &e.ToStatus, &e.ActorID, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
