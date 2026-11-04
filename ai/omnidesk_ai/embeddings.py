"""Embedding backends.

`HashingEmbedder` is a deterministic, dependency-free baseline: it hashes word
unigrams and bigrams into a fixed-size vector (the "hashing trick") with
sublinear term weighting, then L2-normalises. It captures lexical overlap, not
meaning, so it will miss paraphrases. It exists so the whole pipeline runs and
is testable offline. Swap in a semantic model by implementing `Embedder`.
"""

import hashlib
import math
import re
from typing import Protocol

_TOKEN = re.compile(r"[a-z0-9]+")
_STOPWORDS = frozenset(
    "a an and are as at be by for from has have how i in is it of on or our that "
    "the this to was we what when where which who why will with you your can do does "
    "my me get need want".split()
)


class Embedder(Protocol):
    dim: int

    def embed(self, text: str) -> list[float]: ...


def _stem(word: str) -> str:
    """Very light suffix stripping so "refunds"/"refund" and "plans"/"plan" match."""
    if len(word) > 4 and word.endswith("ies"):
        return word[:-3] + "y"
    for suffix in ("ing", "ed", "es", "s"):
        if len(word) > len(suffix) + 3 and word.endswith(suffix):
            return word[: -len(suffix)]
    return word


def _tokens(text: str) -> list[str]:
    return [_stem(t) for t in _TOKEN.findall(text.lower()) if t not in _STOPWORDS]


class HashingEmbedder:
    def __init__(self, dim: int = 256):
        if dim < 8:
            raise ValueError("dim must be at least 8")
        self.dim = dim

    def _bucket(self, feature: str) -> tuple[int, float]:
        digest = hashlib.blake2b(feature.encode(), digest_size=8).digest()
        n = int.from_bytes(digest, "big")
        return n % self.dim, 1.0 if (n >> 63) & 1 else -1.0

    def embed(self, text: str) -> list[float]:
        toks = _tokens(text)
        features = toks + [f"{a}_{b}" for a, b in zip(toks, toks[1:])]
        counts: dict[str, int] = {}
        for f in features:
            counts[f] = counts.get(f, 0) + 1

        vec = [0.0] * self.dim
        for feature, count in counts.items():
            idx, sign = self._bucket(feature)
            vec[idx] += sign * (1.0 + math.log(count))

        norm = math.sqrt(sum(v * v for v in vec))
        if norm == 0.0:
            return vec
        return [v / norm for v in vec]
