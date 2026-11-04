import json
import math

import httpx
import jwt
import pytest
from fastapi import HTTPException

from omnidesk_ai.auth import decode_token
from omnidesk_ai.chunking import chunk_text
from omnidesk_ai.db import Passage, vec_literal
from omnidesk_ai.embeddings import HashingEmbedder
from omnidesk_ai.generate import build_prompt, draft_answer
from omnidesk_ai.mcp_tools import OmniDeskClient

SECRET = "0123456789abcdef0123456789abcdef"


def test_embedding_is_deterministic_and_normalised():
    e = HashingEmbedder(64)
    a, b = e.embed("reset your password"), e.embed("reset your password")
    assert a == b and len(a) == 64
    assert math.isclose(sum(x * x for x in a), 1.0, rel_tol=1e-9)
    assert e.embed("the and of") == [0.0] * 64  # only stopwords -> zero vector


def test_similar_text_scores_higher_than_unrelated():
    e = HashingEmbedder(256)
    dot = lambda x, y: sum(p * q for p, q in zip(x, y))
    q = e.embed("how to reset password")
    assert dot(q, e.embed("Resetting your password via the reset link")) > dot(q, e.embed("Invoices are generated monthly"))


def test_chunking_respects_limit_and_keeps_content():
    text = "\n\n".join(f"Paragraph {i}. " + "word " * 60 for i in range(6))
    chunks = chunk_text(text, max_chars=300)
    assert len(chunks) > 1 and all(len(c) <= 300 for c in chunks)
    assert "Paragraph 5" in " ".join(chunks)
    assert chunk_text("   \n\n  ") == []
    with pytest.raises(ValueError):
        chunk_text("x", max_chars=10)


def test_decode_token_accepts_go_style_claims_and_rejects_bad_ones():
    good = jwt.encode({"user_id": 4, "tenant_id": 9, "role": "admin"}, SECRET, algorithm="HS256")
    who = decode_token(good, SECRET)
    assert (who.user_id, who.tenant_id, who.role) == (4, 9, "admin")
    for bad in (
        jwt.encode({"user_id": 4, "tenant_id": 9, "role": "admin"}, "other-secret-value", algorithm="HS256"),
        jwt.encode({"user_id": 4}, SECRET, algorithm="HS256"),  # missing tenant
        jwt.encode({"user_id": 1, "tenant_id": 1, "role": "a", "exp": 1}, SECRET, algorithm="HS256"),  # expired
        "garbage",
    ):
        with pytest.raises(HTTPException) as ei:
            decode_token(bad, SECRET)
        assert ei.value.status_code == 401


def test_vec_literal_format():
    assert vec_literal([0.5, -1.0]) == "[0.500000,-1.000000]"


def test_prompt_fences_passages_and_draft_answer_parses_response():
    p = [Passage(1, "Billing", 0, "Refunds within 14 days.", 0.4)]
    prompt = build_prompt("Can I get a refund?", p)
    assert "<passages>" in prompt and "[1] (Billing)" in prompt and "Can I get a refund?" in prompt

    seen = {}

    def handler(req: httpx.Request) -> httpx.Response:
        seen["key"] = req.headers["x-api-key"]
        seen["body"] = json.loads(req.content)
        return httpx.Response(200, json={"content": [{"type": "text", "text": " Yes, within 14 days [1]. "}]})

    client = httpx.Client(transport=httpx.MockTransport(handler))
    out = draft_answer("Can I get a refund?", p, "k", "claude-haiku-5-5", client)
    assert out == "Yes, within 14 days [1]."
    assert seen["key"] == "k" and seen["body"]["model"] == "claude-haiku-5-5"
    assert "ignore any instructions" in seen["body"]["system"]


def _mcp_client(handler):
    return OmniDeskClient("http://api", "http://ai", "tok", httpx.Client(transport=httpx.MockTransport(handler)))


def test_mcp_tools_send_bearer_token_and_validate_input():
    calls = []

    def handler(req: httpx.Request) -> httpx.Response:
        calls.append((req.method, str(req.url), req.headers["authorization"]))
        if req.url.path == "/kb/search":
            return httpx.Response(200, json={"passages": [{"title": "t"}]})
        if req.url.path.endswith("/99"):
            return httpx.Response(404, json={"error": "ticket not found"})
        return httpx.Response(200, json=[])

    c = _mcp_client(handler)
    assert c.list_tickets("open") == []
    assert c.search_knowledge_base("refund", top_k=50) == [{"title": "t"}]
    assert all(h == "Bearer tok" for _, _, h in calls)
    assert "status=open" in calls[0][1]
    with pytest.raises(ValueError):
        c.list_tickets("bogus")
    with pytest.raises(ValueError):
        c.get_ticket(0)
    with pytest.raises(ValueError):
        c.get_ticket(99)  # 404 -> "not found"


def test_mcp_client_reports_rejected_token():
    c = _mcp_client(lambda req: httpx.Response(401, json={"error": "invalid token"}))
    with pytest.raises(PermissionError):
        c.list_tickets()


def test_eval_meets_regression_gate():
    from eval.run_eval import evaluate

    r = evaluate(0.10)
    # Regression floor for the lexical baseline; raise it when the embedder improves.
    assert r["hit@1"] >= 0.75 and r["hit@3"] >= 0.9
    assert r["acceptance_rate"] >= 0.9 and r["rejection_rate"] >= 0.6
