# OmniDesk

[![CI](https://github.com/Naveenkumar75/omnidesk/actions/workflows/ci.yml/badge.svg)](https://github.com/Naveenkumar75/omnidesk/actions/workflows/ci.yml)

A multi-tenant customer-support backend: a Go API for tenants, users and tickets with SLA tracking, plus a Python service that suggests replies from each tenant's knowledge base (RAG) and exposes the helpdesk to AI assistants through MCP.

The focus is on backend correctness rather than feature count: tenant isolation enforced in both queries and the schema, transactional state changes, an idempotent background worker that is safe to run on several replicas, and tests that run against a real PostgreSQL.

## Architecture

```mermaid
flowchart LR
    client([HTTP client]) -->|JWT| api
    assistant([AI assistant]) -->|stdio| mcp[MCP server]
    mcp -->|caller's JWT| api
    mcp -->|caller's JWT| ai

    subgraph api[Go API · cmd/api]
        http[chi router<br/>auth · roles · rate limits] --> store[store]
        worker[SLA worker] --> store
    end

    subgraph ai[AI service · FastAPI]
        kb[ingest · search · /suggest] --> llm[(Claude API<br/>optional)]
    end

    store --> pg[(PostgreSQL + pgvector)]
    kb --> pg
```

- **One database, two services.** The Go API owns the schema and runs migrations; the AI service verifies the same JWT (HS256, shared secret, same issuer and audience) and scopes every query by the token's `tenant_id`.
- **The MCP server has no privileges of its own.** It forwards the user's token, so an assistant can only see what that user could.

## What it does

**Tenants and access**
- Signup creates a tenant and its first admin in one transaction, so a failure can't leave an orphaned tenant.
- `admin` and `agent` roles. Admins add users; agents work tickets.
- bcrypt passwords (8–72 bytes; bcrypt ignores anything past 72, so longer input is rejected rather than silently truncated).
- Failed logins do the same bcrypt work whether or not the email exists, so neither the response nor its timing reveals which accounts exist.
- Login is rate limited per email and per client IP (`429` with `Retry-After`).

**Tickets**
- State machine `open → pending ↔ open → resolved → closed`; a resolved ticket can reopen, closed is final. Invalid moves return `409`.
- Status changes lock the row (`SELECT … FOR UPDATE`) and write an audit event in the same transaction.
- Assignment, public replies and internal notes.
- Keyset pagination (`?limit=&cursor=`) with filters for status, assignee (`me`, `none`, id) and SLA breaches.

**SLA tracking**
- First-response and resolution deadlines are set from priority when a ticket is created (urgent 1h/4h, high 4h/24h, normal 8h/48h, low 24h/120h).
- The first public reply stops the first-response clock; internal notes don't.
- A background worker marks overdue tickets and records a breach event. Each sweep is a single statement that claims a batch with `FOR UPDATE SKIP LOCKED`, so several API replicas can sweep at once without blocking each other or recording a breach twice. A test runs four concurrent sweepers over 60 overdue tickets and checks for exactly 60 events.

**Tenant isolation** is enforced twice:
1. Every tenant-owned query filters on the `tenant_id` from the JWT, never from request input. Another tenant's ticket is a `404`, not a `403`.
2. The schema uses composite foreign keys on `(id, tenant_id)`, so the database itself rejects a ticket assigned to another tenant's user, or a comment or event that points across tenants, even if application code got it wrong.

**AI service** ([`ai/`](ai/README.md))
- Documents are chunked, embedded and stored in pgvector; search is cosine similarity filtered by tenant. Ingest is admin-only.
- `/suggest` returns relevant passages, plus an LLM-drafted answer with citations when an API key is set. If nothing passes the relevance threshold it says so instead of guessing, and if the LLM call fails it degrades to passages only.
- A retrieval evaluation runs in CI as a regression gate.
- **Current limitation:** the default embedder is a lexical hashing baseline, not a semantic model, so paraphrases with no shared words can miss. The [AI README](ai/README.md) has the measured numbers and limits.

**Operations**
- Versioned migrations tracked in `schema_migrations`. Each file runs once in its own transaction, under an advisory lock so replicas starting together don't race.
- JSON logs (`log/slog`) with request id, route pattern, tenant, status and latency. Internal errors are logged with the request id and returned as a generic `500`.
- `/healthz` (process up) and `/readyz` (database reachable); full HTTP server timeouts; a bounded connection pool; graceful shutdown that drains requests and stops the worker.

## Run locally

Requires Go 1.26+ and Docker.

```bash
cp .env.example .env                 # then set JWT_SECRET: openssl rand -hex 32
docker compose up -d --wait          # PostgreSQL + pgvector on localhost:5433
set -a; source .env; set +a
go run ./cmd/api                     # applies migrations, listens on :8080
```

Try it:

```bash
curl -s -XPOST localhost:8080/api/v1/auth/register \
  -d '{"tenant_name":"Acme","domain":"acme.test","email":"admin@acme.test","password":"password123"}'

TOKEN=$(curl -s -XPOST localhost:8080/api/v1/auth/login \
  -d '{"email":"admin@acme.test","password":"password123"}' | jq -r .token)

curl -s -XPOST localhost:8080/api/v1/tickets -H "Authorization: Bearer $TOKEN" \
  -d '{"subject":"Cannot log in","requester_email":"customer@example.com","priority":"urgent"}'

curl -s "localhost:8080/api/v1/tickets?status=open" -H "Authorization: Bearer $TOKEN"
```

To run the AI service and MCP server, see [ai/README.md](ai/README.md).

## Configuration

| Variable | Required | Default | Purpose |
|---|---|---|---|
| `DATABASE_URL` | yes | | PostgreSQL connection string |
| `JWT_SECRET` | yes | | HS256 signing key, at least 32 characters. Shared with the AI service |
| `HTTP_ADDR` | no | `:8080` | Listen address |
| `LOG_LEVEL` | no | `info` | `debug`, `info`, `warn` or `error` |
| `SLA_SWEEP_INTERVAL` | no | `30s` | How often the SLA worker runs (minimum `1s`) |

## API

The full contract is in [`internal/httpapi/openapi.yaml`](internal/httpapi/openapi.yaml), also served at `GET /openapi.yaml`. A test fails if a route is added to the router without being documented, or documented without existing.

| Method | Path | Who | Purpose |
|---|---|---|---|
| POST | `/api/v1/auth/register` | public | Create tenant + admin |
| POST | `/api/v1/auth/login` | public | Get a JWT (24h) |
| GET | `/api/v1/me` | any | Identity from the token |
| GET | `/api/v1/users` | any | Users in your tenant |
| POST | `/api/v1/users` | admin | Add an agent or admin |
| POST | `/api/v1/tickets` | any | Create a ticket |
| GET | `/api/v1/tickets` | any | List (filters, pagination) |
| GET | `/api/v1/tickets/{id}` | any | Ticket, audit trail and comments |
| POST | `/api/v1/tickets/{id}/transition` | any | Change status |
| PUT | `/api/v1/tickets/{id}/assignee` | any | Assign or unassign |
| POST | `/api/v1/tickets/{id}/comments` | any | Reply or internal note |
| GET | `/healthz`, `/readyz` | public | Liveness and readiness |

## Tests

```bash
go vet ./... && go test -race ./...                       # unit tests only
TEST_DATABASE_URL=postgres://omnidesk:omnidesk_dev@localhost:5433/omnidesk?sslmode=disable \
  go test -race ./...                                      # plus integration tests
```

Integration tests run against a real PostgreSQL. They cover the signup transaction, tenant isolation for every ticket route, role checks, the cross-tenant foreign keys, comment and SLA behaviour, pagination, rate limiting, concurrent migrations, upgrading a database created before v1, and concurrent SLA sweeps. CI runs the Go and Python suites on every push.

## Project layout

```
cmd/api/            entry point: config, migrations, HTTP server, SLA worker
internal/auth/      JWT, bcrypt, roles middleware
internal/ticket/    domain rules: state machine, SLA policy (no I/O)
internal/store/     PostgreSQL access and embedded SQL migrations
internal/httpapi/   handlers, request logging, OpenAPI spec
internal/worker/    background SLA sweep loop
internal/ratelimit/ per-key token bucket
ai/                 FastAPI RAG service, MCP server, retrieval eval
```

## Design decisions and known limits

- **Shared-schema multi-tenancy** (a `tenant_id` column) rather than a schema or database per tenant: simplest to operate at this size. Postgres row-level security would add a third layer of isolation; it isn't used yet because every request would need a transaction to set the tenant.
- **Emails are unique across tenants**, because login is by email alone. A person can't belong to two tenants.
- **Process-local rate limiting.** With several API replicas each has its own budget; a shared store (Redis or Postgres) would fix that.
- **SLA clocks run in calendar time**, without business hours or pausing while a ticket is pending.
- **Tokens can't be revoked** before they expire (24h). A role change takes effect at the next login.
- **No email delivery**: admins set a new user's initial password.

## Roadmap

1. Semantic embeddings and hybrid (vector + full-text) retrieval, measured against the current baseline with a larger evaluation set
2. A Dockerfile for each service and a single `docker compose up` for the whole stack
3. Attachments and email ingestion
