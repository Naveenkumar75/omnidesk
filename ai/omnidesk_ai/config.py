import os
from dataclasses import dataclass


@dataclass(frozen=True)
class Settings:
    database_url: str
    jwt_secret: str
    embedding_dim: int = 256
    # Passages scoring below this cosine similarity are treated as "not relevant".
    min_score: float = 0.10
    top_k: int = 4
    anthropic_api_key: str | None = None
    llm_model: str = "claude-haiku-5-5"


def load_settings() -> Settings:
    database_url = os.environ.get("DATABASE_URL", "")
    jwt_secret = os.environ.get("JWT_SECRET", "")
    if not database_url:
        raise RuntimeError("DATABASE_URL is required")
    if len(jwt_secret) < 32:
        raise RuntimeError("JWT_SECRET is required (at least 32 characters)")
    return Settings(
        database_url=database_url,
        jwt_secret=jwt_secret,
        embedding_dim=int(os.environ.get("EMBEDDING_DIM", "256")),
        min_score=float(os.environ.get("MIN_SCORE", "0.10")),
        top_k=int(os.environ.get("TOP_K", "4")),
        anthropic_api_key=os.environ.get("ANTHROPIC_API_KEY") or None,
        llm_model=os.environ.get("LLM_MODEL", "claude-haiku-5-5"),
    )
