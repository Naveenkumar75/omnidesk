# OmniDesk

[![CI](https://github.com/Naveenkumar75/omnidesk/actions/workflows/ci.yml/badge.svg)](https://github.com/Naveenkumar75/omnidesk/actions/workflows/ci.yml)

Multi-tenant customer-support backend written in Go, backed by PostgreSQL.

**Status:** Milestone 1 (foundation) and Milestone 2 (ticketing) complete. SLA workers and AI features are planned.

## What works today

- Tenant signup: creating a tenant and its first admin user happens in **one transaction**, so a failure never leaves an orphaned tenant.
- Password hashing with **bcrypt**.
- Stateless **JWT** login. The token carries `user_id`, `tenant_id` and `role`; the secret comes from the environment, never from source.
- Auth middleware that protects routes (`GET /api/v1/me`).
- Tickets with an explicit **state machine** (`open → pending → resolved → closed`, reopen allowed before close). Invalid moves return `409`.
- Status changes run in a transaction with a row lock (`SELECT … FOR UPDATE`) and write an **audit event**, so concurrent requests can't both act on the same state.
- **Tenant isolation:** every ticket query is scoped by the `tenant_id` in the JWT. Another tenant's ticket returns `404`, and this is covered by an integration test.
- PostgreSQL in Docker; schema applied automatically on startup.

## Stack

Go · chi · PostgreSQL 15 · Docker Compose · bcrypt · JWT (HS256)

## Run locally

```bash
cp .env.example .env            # then set JWT_SECRET: openssl rand -hex 32
docker compose up -d --wait     # PostgreSQL on localhost:5433
set -a; source .env; set +a
go run ./cmd/api
```

```bash
curl -XPOST localhost:8080/api/v1/auth/register \
  -d '{"tenant_name":"Acme","domain":"acme.test","email":"a@acme.test","password":"password123"}'
curl -XPOST localhost:8080/api/v1/auth/login \
  -d '{"email":"a@acme.test","password":"password123"}'
```

## API

| Method | Path | Auth | Purpose |
|---|---|---|---|
| GET | `/healthz` | no | Liveness |
| POST | `/api/v1/auth/register` | no | Create tenant + admin |
| POST | `/api/v1/auth/login` | no | Get JWT |
| GET | `/api/v1/me` | Bearer | Identity from token |
| POST | `/api/v1/tickets` | Bearer | Create ticket (`subject`, `requester_email`, optional `priority`) |
| GET | `/api/v1/tickets?status=` | Bearer | List tenant's tickets |
| GET | `/api/v1/tickets/{id}` | Bearer | Ticket plus status history |
| POST | `/api/v1/tickets/{id}/transition` | Bearer | Move to a new status |

## Design rule

Every tenant-owned query must be scoped by the `tenant_id` from the JWT claims, never from request input.

## Roadmap

1. Async SLA workers (goroutines + Redis)
2. Assignment, comments and attachments
3. AI layer: RAG-based answer suggestions (pgvector) and an MCP server
