CREATE TABLE IF NOT EXISTS tickets (
    id              SERIAL PRIMARY KEY,
    tenant_id       INT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    subject         VARCHAR(255) NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    status          VARCHAR(20) NOT NULL DEFAULT 'open'
                    CHECK (status IN ('open', 'pending', 'resolved', 'closed')),
    priority        VARCHAR(20) NOT NULL DEFAULT 'normal'
                    CHECK (priority IN ('low', 'normal', 'high', 'urgent')),
    requester_email VARCHAR(255) NOT NULL,
    created_by      INT NOT NULL REFERENCES users(id),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_tickets_tenant_status ON tickets(tenant_id, status);

-- Append-only audit trail of status changes.
CREATE TABLE IF NOT EXISTS ticket_events (
    id          SERIAL PRIMARY KEY,
    ticket_id   INT NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
    tenant_id   INT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    actor_id    INT NOT NULL REFERENCES users(id),
    from_status VARCHAR(20) NOT NULL,
    to_status   VARCHAR(20) NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_ticket_events_ticket ON ticket_events(ticket_id);
