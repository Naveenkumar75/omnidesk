# Changelog

## v0.2.0 — 2026-11-15

### Added
- `admin` and `agent` roles; `POST /api/v1/users` (admin only) and `GET /api/v1/users`.
- Ticket assignment, public replies and internal notes.
- SLA deadlines per priority and a background worker that records first-response and resolution breaches (safe with multiple replicas: `FOR UPDATE SKIP LOCKED`, idempotent).
- Keyset pagination and filters (`assignee`, `breached`) on `GET /api/v1/tickets`.
- OpenAPI spec served at `/openapi.yaml`, with a test that keeps it in sync with the router.
- `/readyz`, structured JSON logs with request id and tenant, `LOG_LEVEL`, `SLA_SWEEP_INTERVAL`.
- Login and signup rate limiting (`429` + `Retry-After`).

### Changed
- `GET /api/v1/tickets` now returns `{"tickets": [...], "next_cursor": n|null}` instead of a bare array.
- Migrations are versioned in `schema_migrations`, applied once each in a transaction under an advisory lock.
- Composite `(id, tenant_id)` foreign keys enforce tenant isolation in the schema.
- JWTs carry and require issuer, audience and expiry; the AI service checks the same values.
- Knowledge-base ingest is admin-only.
- Request bodies reject unknown fields; lengths are bounded.

### Security
- Failed logins for unknown emails now cost the same bcrypt work as wrong passwords (timing no longer reveals which accounts exist).
- Passwords over 72 bytes are rejected instead of causing a 500.

## v0.1.0 — 2026-11-04
- Multi-tenant signup and JWT auth, tickets with a state machine and audit trail, RAG service with a lexical baseline embedder, MCP server, CI.
