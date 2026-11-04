from contextlib import asynccontextmanager

import httpx
from fastapi import FastAPI, HTTPException
from psycopg_pool import ConnectionPool
from pydantic import BaseModel, Field

from .auth import CurrentIdentity, Identity
from .chunking import chunk_text
from .config import Settings, load_settings
from .db import KnowledgeBase
from .embeddings import Embedder, HashingEmbedder
from .generate import draft_answer

MAX_DOC_CHARS = 200_000


class DocumentIn(BaseModel):
    title: str = Field(min_length=1, max_length=255)
    content: str = Field(min_length=1, max_length=MAX_DOC_CHARS)


class SearchIn(BaseModel):
    query: str = Field(min_length=1, max_length=2000)
    top_k: int | None = Field(default=None, ge=1, le=10)


class SuggestIn(BaseModel):
    question: str = Field(min_length=1, max_length=2000)


def create_app(
    settings: Settings | None = None,
    embedder: Embedder | None = None,
    llm_client: httpx.Client | None = None,
) -> FastAPI:
    settings = settings or load_settings()
    embedder = embedder or HashingEmbedder(settings.embedding_dim)

    @asynccontextmanager
    async def lifespan(app: FastAPI):
        pool = ConnectionPool(settings.database_url, min_size=1, max_size=5, open=True)
        kb = KnowledgeBase(pool, embedder.dim)
        kb.init_schema()
        app.state.kb = kb
        yield
        pool.close()

    app = FastAPI(title="OmniDesk AI", lifespan=lifespan)
    app.state.settings = settings

    @app.get("/healthz")
    def healthz():
        return {"status": "ok"}

    @app.post("/kb/documents", status_code=201)
    def add_document(body: DocumentIn, who: Identity = CurrentIdentity):
        chunks = chunk_text(body.content)
        if not chunks:
            raise HTTPException(status_code=400, detail="document has no text")
        vectors = [embedder.embed(c) for c in chunks]
        doc_id = app.state.kb.add_document(who.tenant_id, who.user_id, body.title, chunks, vectors)
        return {"document_id": doc_id, "chunks": len(chunks)}

    @app.post("/kb/search")
    def search(body: SearchIn, who: Identity = CurrentIdentity):
        passages = app.state.kb.search(who.tenant_id, embedder.embed(body.query), body.top_k or settings.top_k)
        return {"passages": [p.__dict__ for p in passages]}

    @app.post("/suggest")
    def suggest(body: SuggestIn, who: Identity = CurrentIdentity):
        found = app.state.kb.search(who.tenant_id, embedder.embed(body.question), settings.top_k)
        relevant = [p for p in found if p.score >= settings.min_score]
        if not relevant:
            return {
                "answer": None,
                "reason": "No relevant knowledge-base content found for this question.",
                "passages": [],
            }
        answer = None
        if settings.anthropic_api_key:
            try:
                answer = draft_answer(
                    body.question, relevant, settings.anthropic_api_key, settings.llm_model, llm_client
                )
            except httpx.HTTPError:
                answer = None  # fall back to returning the passages only
        return {"answer": answer, "reason": None, "passages": [p.__dict__ for p in relevant]}

    return app
