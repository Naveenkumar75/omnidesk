import time
from dataclasses import dataclass

from psycopg_pool import ConnectionPool


def vec_literal(vec: list[float]) -> str:
    """pgvector text input format, e.g. '[0.1,0.2]'."""
    return "[" + ",".join(f"{v:.6f}" for v in vec) + "]"


SCHEMA = """
CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE IF NOT EXISTS kb_documents (
    id         SERIAL PRIMARY KEY,
    tenant_id  INT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    title      VARCHAR(255) NOT NULL,
    created_by INT NOT NULL REFERENCES users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS kb_chunks (
    id          SERIAL PRIMARY KEY,
    document_id INT NOT NULL REFERENCES kb_documents(id) ON DELETE CASCADE,
    tenant_id   INT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    chunk_index INT NOT NULL,
    content     TEXT NOT NULL,
    embedding   vector({dim}) NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_kb_chunks_tenant ON kb_chunks(tenant_id);
"""


@dataclass(frozen=True)
class Passage:
    document_id: int
    title: str
    chunk_index: int
    content: str
    score: float


class KnowledgeBase:
    """Tenant-scoped storage and similarity search over document chunks.

    Every query includes tenant_id, so one tenant can never retrieve another's text.
    """

    def __init__(self, pool: ConnectionPool, dim: int):
        self.pool = pool
        self.dim = dim

    def _wait_for_core_tables(self, timeout: float) -> None:
        """The Go API owns tenants/users. Wait for its migrations instead of crashing on a startup race."""
        deadline = time.monotonic() + timeout
        while True:
            with self.pool.connection() as conn:
                ready = conn.execute("SELECT to_regclass('public.tenants') IS NOT NULL AND to_regclass('public.users') IS NOT NULL").fetchone()[0]
            if ready:
                return
            if time.monotonic() >= deadline:
                raise RuntimeError("tenants/users tables not found: start the Go API (it runs the migrations) first")
            time.sleep(1)

    def init_schema(self, wait_seconds: float = 60) -> None:
        self._wait_for_core_tables(wait_seconds)
        with self.pool.connection() as conn:
            conn.execute(SCHEMA.format(dim=self.dim))

    def add_document(
        self, tenant_id: int, user_id: int, title: str, chunks: list[str], vectors: list[list[float]]
    ) -> int:
        with self.pool.connection() as conn:  # one transaction: all chunks or none
            doc_id = conn.execute(
                "INSERT INTO kb_documents (tenant_id, title, created_by) VALUES (%s, %s, %s) RETURNING id",
                (tenant_id, title, user_id),
            ).fetchone()[0]
            with conn.cursor() as cur:
                cur.executemany(
                    "INSERT INTO kb_chunks (document_id, tenant_id, chunk_index, content, embedding) "
                    "VALUES (%s, %s, %s, %s, %s::vector)",
                    [
                        (doc_id, tenant_id, i, text, vec_literal(vec))
                        for i, (text, vec) in enumerate(zip(chunks, vectors))
                    ],
                )
            return doc_id

    def search(self, tenant_id: int, query_vec: list[float], top_k: int) -> list[Passage]:
        with self.pool.connection() as conn:
            rows = conn.execute(
                "SELECT c.document_id, d.title, c.chunk_index, c.content, "
                "       1 - (c.embedding <=> %s::vector) AS score "
                "FROM kb_chunks c JOIN kb_documents d ON d.id = c.document_id "
                "WHERE c.tenant_id = %s "
                "ORDER BY c.embedding <=> %s::vector LIMIT %s",
                (vec_literal(query_vec), tenant_id, vec_literal(query_vec), top_k),
            ).fetchall()
        return [Passage(r[0], r[1], r[2], r[3], float(r[4])) for r in rows]
