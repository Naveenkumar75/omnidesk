-- v1.0: roles, cross-tenant integrity in the schema, comments and SLA tracking.

-- Roles: admins manage users and the knowledge base; agents work tickets.
ALTER TABLE users ADD CONSTRAINT users_role_check CHECK (role IN ('admin', 'agent'));

-- Composite keys let child tables reference (id, tenant_id) pairs, so the
-- database itself rejects a row that points at another tenant's ticket or user.
ALTER TABLE users   ADD CONSTRAINT users_id_tenant_key   UNIQUE (id, tenant_id);
ALTER TABLE tickets ADD CONSTRAINT tickets_id_tenant_key UNIQUE (id, tenant_id);

ALTER TABLE tickets
    ADD CONSTRAINT tickets_created_by_same_tenant
        FOREIGN KEY (created_by, tenant_id) REFERENCES users (id, tenant_id);

-- Assignment.
ALTER TABLE tickets ADD COLUMN assignee_id INT;
ALTER TABLE tickets
    ADD CONSTRAINT tickets_assignee_same_tenant
        FOREIGN KEY (assignee_id, tenant_id) REFERENCES users (id, tenant_id);
CREATE INDEX idx_tickets_tenant_assignee ON tickets (tenant_id, assignee_id);

-- SLA deadlines are fixed when the ticket is created (see internal/ticket/sla.go).
ALTER TABLE tickets
    ADD COLUMN first_response_due_at      TIMESTAMPTZ,
    ADD COLUMN resolution_due_at          TIMESTAMPTZ,
    ADD COLUMN first_response_at          TIMESTAMPTZ,
    ADD COLUMN first_response_breached_at TIMESTAMPTZ,
    ADD COLUMN resolution_breached_at     TIMESTAMPTZ;

-- Backfill tickets created before v1 with the same policy, then require it.
UPDATE tickets SET
    first_response_due_at = created_at + CASE priority
        WHEN 'urgent' THEN interval '1 hour'  WHEN 'high' THEN interval '4 hours'
        WHEN 'normal' THEN interval '8 hours' ELSE interval '24 hours' END,
    resolution_due_at = created_at + CASE priority
        WHEN 'urgent' THEN interval '4 hours'  WHEN 'high' THEN interval '24 hours'
        WHEN 'normal' THEN interval '48 hours' ELSE interval '120 hours' END
WHERE first_response_due_at IS NULL;

ALTER TABLE tickets
    ALTER COLUMN first_response_due_at SET NOT NULL,
    ALTER COLUMN resolution_due_at     SET NOT NULL;

-- Partial indexes keep the SLA sweeper's scans proportional to tickets that
-- can still breach, not to the whole table.
CREATE INDEX idx_tickets_first_response_pending ON tickets (first_response_due_at)
    WHERE first_response_at IS NULL AND first_response_breached_at IS NULL;
CREATE INDEX idx_tickets_resolution_pending ON tickets (resolution_due_at)
    WHERE resolution_breached_at IS NULL;

-- Events gain a kind so the SLA worker can record breaches. System events have
-- no actor; status fields only apply to status changes.
ALTER TABLE ticket_events
    ADD COLUMN kind VARCHAR(40) NOT NULL DEFAULT 'status_change'
        CHECK (kind IN ('status_change', 'first_response_breach', 'resolution_breach')),
    ALTER COLUMN actor_id    DROP NOT NULL,
    ALTER COLUMN from_status DROP NOT NULL,
    ALTER COLUMN to_status   DROP NOT NULL,
    ADD CONSTRAINT ticket_events_status_fields CHECK (
        kind <> 'status_change' OR (from_status IS NOT NULL AND to_status IS NOT NULL AND actor_id IS NOT NULL)
    ),
    ADD CONSTRAINT ticket_events_ticket_same_tenant
        FOREIGN KEY (ticket_id, tenant_id) REFERENCES tickets (id, tenant_id) ON DELETE CASCADE,
    ADD CONSTRAINT ticket_events_actor_same_tenant
        FOREIGN KEY (actor_id, tenant_id) REFERENCES users (id, tenant_id);

-- Comments: public replies to the requester, or internal notes between agents.
CREATE TABLE ticket_comments (
    id         SERIAL PRIMARY KEY,
    ticket_id  INT NOT NULL,
    tenant_id  INT NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    author_id  INT NOT NULL,
    body       TEXT NOT NULL CHECK (length(body) BETWEEN 1 AND 20000),
    internal   BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (ticket_id, tenant_id) REFERENCES tickets (id, tenant_id) ON DELETE CASCADE,
    FOREIGN KEY (author_id, tenant_id) REFERENCES users (id, tenant_id)
);
CREATE INDEX idx_ticket_comments_ticket ON ticket_comments (ticket_id);
