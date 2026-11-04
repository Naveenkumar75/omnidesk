# OmniDesk AI service

Retrieval-augmented reply suggestions and an MCP server for OmniDesk.

## How it works

```
document ──chunk──▶ embed ──▶ pgvector (kb_chunks, tenant_id)
question ──embed──▶ cosine search WHERE tenant_id = <from JWT> ──▶ passages
                                          └─ score ≥ MIN_SCORE? ─ no ─▶ "no relevant content"
                                                              └─ yes ─▶ (optional) LLM draft with [n] citations
```

- **Auth:** verifies the Go API's HS256 JWT. `tenant_id` is read from the token, never from the request.
- **Isolation:** every query filters on `tenant_id`; an integration test proves tenant B cannot retrieve tenant A's text.
- **Generation (optional):** set `ANTHROPIC_API_KEY` to draft an answer from the retrieved passages. The prompt restricts the model to the passages, asks for citations, says to admit when they are insufficient, and treats passage text as untrusted data. If the LLM call fails, the endpoint degrades to returning the passages.
- **Startup:** waits for the Go API's migrations (it owns `tenants`/`users`) instead of crashing.

## Endpoints

| Method | Path | Purpose |
|---|---|---|
| POST | `/kb/documents` | Ingest `{title, content}` |
| POST | `/kb/search` | `{query, top_k?}` → passages with scores |
| POST | `/suggest` | `{question}` → passages and optional drafted answer |
| GET | `/healthz` | Liveness |

## MCP server

Read-only tools: `list_tickets`, `get_ticket`, `search_knowledge_base`, `suggest_reply`. It calls the APIs with your JWT, so it has exactly your tenant's permissions.

```json
{
  "mcpServers": {
    "omnidesk": {
      "command": "ai/.venv/bin/python",
      "args": ["ai/mcp_server.py"],
      "env": { "OMNIDESK_TOKEN": "<jwt from /api/v1/auth/login>" }
    }
  }
}
```

## Run

```bash
python3.12 -m venv .venv && .venv/bin/pip install -r requirements.txt
export DATABASE_URL=... JWT_SECRET=...      # same values as the Go API
.venv/bin/uvicorn omnidesk_ai.app:create_app --factory --port 8000
```

## Tests and evaluation

```bash
TEST_DATABASE_URL=... .venv/bin/python -m pytest -q   # unit + pgvector integration + eval gate
.venv/bin/python eval/run_eval.py                     # print retrieval metrics
```

### Current results (lexical baseline, 25 questions over 5 sample help-center docs)

| Metric | Value |
|---|---|
| hit@1 (right document ranked first) | 0.79 |
| hit@3 | 0.95 |
| Answerable questions that pass the threshold | 0.95 |
| Unanswerable questions correctly rejected | 0.67 |

### Known limits (read before trusting it)

- The default embedder is a **deterministic hashing embedder with light stemming**. It matches words, not meaning, so paraphrases without shared vocabulary can miss.
- It **cannot reliably tell "no answer exists" from "weak match"**: 2 of 6 unanswerable eval questions still pass the threshold (for example a weather question can match a billing page on a shared word). The LLM prompt is the second line of defense, not a guarantee.
- The eval set is **small and hand-written**; treat the numbers as a regression floor, not a benchmark. The threshold (`MIN_SCORE=0.10`) was chosen from it and may not transfer to real data.
- Next step: implement the `Embedder` interface with a semantic embedding model, re-run the same eval, and compare.
