"""Integration tests against PostgreSQL + pgvector. Skipped unless TEST_DATABASE_URL is set."""

import os
import time

import httpx
import jwt
import psycopg
import pytest
from fastapi.testclient import TestClient

from omnidesk_ai.app import create_app
from omnidesk_ai.config import Settings

URL = os.environ.get("TEST_DATABASE_URL")
SECRET = "0123456789abcdef0123456789abcdef"
pytestmark = pytest.mark.skipif(not URL, reason="TEST_DATABASE_URL not set")

# Minimal copy of the Go API's tables so this suite is self-contained.
PREREQ = """
CREATE TABLE IF NOT EXISTS tenants (id SERIAL PRIMARY KEY, name VARCHAR(255) NOT NULL,
    domain VARCHAR(255) UNIQUE NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now());
CREATE TABLE IF NOT EXISTS users (id SERIAL PRIMARY KEY, tenant_id INT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    email VARCHAR(255) UNIQUE NOT NULL, role VARCHAR(50) NOT NULL, password_hash VARCHAR(255) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now());
"""


def make_tenant(conn, label):
    n = time.time_ns()
    t = conn.execute("INSERT INTO tenants (name, domain) VALUES (%s, %s) RETURNING id", (label, f"{label}{n}.test")).fetchone()[0]
    u = conn.execute(
        "INSERT INTO users (tenant_id, email, role, password_hash) VALUES (%s, %s, 'admin', 'x') RETURNING id",
        (t, f"{label}{n}@x.test"),
    ).fetchone()[0]
    token = jwt.encode({"user_id": u, "tenant_id": t, "role": "admin"}, SECRET, algorithm="HS256")
    return {"Authorization": f"Bearer {token}"}


def make_client(**overrides):
    s = Settings(database_url=URL, jwt_secret=SECRET, **overrides)
    return create_app(s)


@pytest.fixture(autouse=True, scope="module")
def prereq_tables():
    with psycopg.connect(URL, autocommit=True) as conn:
        conn.execute(PREREQ)


@pytest.fixture()
def tenants():
    with psycopg.connect(URL, autocommit=True) as conn:
        return make_tenant(conn, "alpha"), make_tenant(conn, "bravo")


REFUND = "Refunds are available within 14 days of a charge for annual plans. Monthly plans are not refunded."


def test_auth_required():
    with TestClient(make_client()) as c:
        assert c.post("/kb/search", json={"query": "x"}).status_code == 401
        assert c.post("/kb/search", json={"query": "x"}, headers={"Authorization": "Bearer nope"}).status_code == 401


def test_ingest_search_suggest_and_tenant_isolation(tenants):
    a, b = tenants
    with TestClient(make_client()) as c:
        r = c.post("/kb/documents", json={"title": "Refund policy", "content": REFUND}, headers=a)
        assert r.status_code == 201 and r.json()["chunks"] >= 1

        hits = c.post("/kb/search", json={"query": "refund annual plan"}, headers=a).json()["passages"]
        assert hits and hits[0]["title"] == "Refund policy" and hits[0]["score"] > 0.10

        # Tenant B must not see tenant A's content, by search or by suggestion.
        assert c.post("/kb/search", json={"query": "refund annual plan"}, headers=b).json()["passages"] == []
        s = c.post("/suggest", json={"question": "Can I get a refund on an annual plan?"}, headers=b).json()
        assert s["answer"] is None and s["passages"] == []

        # Tenant A: passages returned; no LLM key configured, so no generated answer.
        s = c.post("/suggest", json={"question": "Can I get a refund on an annual plan?"}, headers=a).json()
        assert s["answer"] is None and s["passages"][0]["title"] == "Refund policy"

        # Unrelated question is rejected instead of returning weak matches.
        s = c.post("/suggest", json={"question": "What is the weather in Chennai?"}, headers=a).json()
        assert s["answer"] is None and "No relevant" in s["reason"]


def test_suggest_uses_llm_when_configured_and_falls_back_on_error(tenants):
    a, _ = tenants

    def ok(req: httpx.Request) -> httpx.Response:
        return httpx.Response(200, json={"content": [{"type": "text", "text": "Within 14 days [1]."}]})

    app = create_app(
        Settings(database_url=URL, jwt_secret=SECRET, anthropic_api_key="k"),
        llm_client=httpx.Client(transport=httpx.MockTransport(ok)),
    )
    with TestClient(app) as c:
        c.post("/kb/documents", json={"title": "Refund policy", "content": REFUND}, headers=a)
        s = c.post("/suggest", json={"question": "refund on annual plan?"}, headers=a).json()
        assert s["answer"] == "Within 14 days [1]."

    def boom(req: httpx.Request) -> httpx.Response:
        return httpx.Response(500)

    app = create_app(
        Settings(database_url=URL, jwt_secret=SECRET, anthropic_api_key="k"),
        llm_client=httpx.Client(transport=httpx.MockTransport(boom)),
    )
    with TestClient(app) as c:
        s = c.post("/suggest", json={"question": "refund on annual plan?"}, headers=a).json()
        assert s["answer"] is None and s["passages"]  # degrades to passages only
