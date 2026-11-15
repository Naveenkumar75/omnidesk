package store

import "context"

// SweepResult counts the breaches recorded by one SweepSLABreaches call.
type SweepResult struct {
	FirstResponse int
	Resolution    int
}

// The two sweeps share one shape: lock a batch of overdue tickets that are not
// yet marked, mark them, and append an audit event, all in one statement.
//
//   - FOR UPDATE SKIP LOCKED lets several API replicas sweep at once: each
//     claims a disjoint batch instead of waiting on, or double-counting, the
//     others' rows.
//   - The "breached_at IS NULL" predicate makes the sweep idempotent: a ticket
//     is marked at most once however often the sweep runs.
//   - The CTE runs as one statement, so the mark and its event commit together.
const sweepFirstResponse = `
WITH due AS (
    SELECT id FROM tickets
    WHERE first_response_at IS NULL
      AND first_response_breached_at IS NULL
      AND status IN ('open', 'pending')
      AND first_response_due_at < now()
    ORDER BY first_response_due_at
    LIMIT $1
    FOR UPDATE SKIP LOCKED
), marked AS (
    UPDATE tickets t SET first_response_breached_at = now()
    FROM due WHERE t.id = due.id
    RETURNING t.id, t.tenant_id
)
INSERT INTO ticket_events (ticket_id, tenant_id, kind)
SELECT id, tenant_id, 'first_response_breach' FROM marked`

const sweepResolution = `
WITH due AS (
    SELECT id FROM tickets
    WHERE resolution_breached_at IS NULL
      AND status IN ('open', 'pending')
      AND resolution_due_at < now()
    ORDER BY resolution_due_at
    LIMIT $1
    FOR UPDATE SKIP LOCKED
), marked AS (
    UPDATE tickets t SET resolution_breached_at = now()
    FROM due WHERE t.id = due.id
    RETURNING t.id, t.tenant_id
)
INSERT INTO ticket_events (ticket_id, tenant_id, kind)
SELECT id, tenant_id, 'resolution_breach' FROM marked`

// SweepSLABreaches marks up to batch overdue tickets per SLA type. Callers
// loop while a full batch comes back.
func (s *Store) SweepSLABreaches(ctx context.Context, batch int) (SweepResult, error) {
	var r SweepResult
	res, err := s.db.ExecContext(ctx, sweepFirstResponse, batch)
	if err != nil {
		return r, err
	}
	n, _ := res.RowsAffected()
	r.FirstResponse = int(n)

	res, err = s.db.ExecContext(ctx, sweepResolution, batch)
	if err != nil {
		return r, err
	}
	n, _ = res.RowsAffected()
	r.Resolution = int(n)
	return r, nil
}
