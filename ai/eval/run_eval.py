"""Retrieval evaluation. Needs no database or network.

Indexes eval/corpus in memory with the same chunker and embedder the service
uses, then reports for each answerable question whether the right document is
ranked first / in the top 3, plus how often unanswerable questions are
(correctly) rejected by the similarity threshold.

Usage: python eval/run_eval.py [--min-score 0.10] [--min-hit1 0.0] [--min-reject 0.0]
Exits non-zero if a minimum is not met (used as a regression gate in CI).
"""

import argparse
import json
import pathlib
import sys

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent.parent))

from omnidesk_ai.chunking import chunk_text  # noqa: E402
from omnidesk_ai.embeddings import HashingEmbedder  # noqa: E402

HERE = pathlib.Path(__file__).resolve().parent


def dot(a, b):
    return sum(x * y for x, y in zip(a, b))


def build_index(embedder):
    index = []
    for path in sorted((HERE / "corpus").glob("*.md")):
        for chunk in chunk_text(path.read_text()):
            index.append((path.name, embedder.embed(chunk)))
    return index


def rank_docs(index, qvec):
    """Best score per document, highest first."""
    best = {}
    for doc, vec in index:
        best[doc] = max(best.get(doc, -1.0), dot(qvec, vec))
    return sorted(best.items(), key=lambda kv: kv[1], reverse=True)


def evaluate(min_score: float) -> dict:
    embedder = HashingEmbedder(256)
    index = build_index(embedder)
    questions = json.loads((HERE / "questions.json").read_text())

    hit1 = hit3 = rr = answerable = accepted = 0
    rejected = unanswerable = 0
    misses = []
    for item in questions:
        ranked = rank_docs(index, embedder.embed(item["q"]))
        top_doc, top_score = ranked[0]
        if item["doc"] is None:
            unanswerable += 1
            if top_score < min_score:
                rejected += 1
            else:
                misses.append(f"FALSE ACCEPT ({top_score:.2f} -> {top_doc}): {item['q']}")
            continue
        answerable += 1
        accepted += top_score >= min_score
        docs = [d for d, _ in ranked]
        pos = docs.index(item["doc"]) + 1
        hit1 += pos == 1
        hit3 += pos <= 3
        rr += 1.0 / pos
        if pos != 1:
            misses.append(f"rank {pos} (expected {item['doc']}, got {top_doc}): {item['q']}")
    return {
        "answerable": answerable,
        "hit@1": hit1 / answerable,
        "hit@3": hit3 / answerable,
        "mrr": rr / answerable,
        "acceptance_rate": accepted / answerable,
        "unanswerable": unanswerable,
        "rejection_rate": rejected / unanswerable if unanswerable else 1.0,
        "misses": misses,
    }


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--min-score", type=float, default=0.10)
    ap.add_argument("--min-hit1", type=float, default=0.0)
    ap.add_argument("--min-accept", type=float, default=0.0)
    ap.add_argument("--min-reject", type=float, default=0.0)
    args = ap.parse_args()

    r = evaluate(args.min_score)
    print(f"answerable questions: {r['answerable']}")
    print(f"  hit@1: {r['hit@1']:.2f}   hit@3: {r['hit@3']:.2f}   MRR: {r['mrr']:.2f}")
    print(f"  answerable questions that pass the threshold: {r['acceptance_rate']:.2f}")
    print(f"unanswerable questions: {r['unanswerable']}  correctly rejected: {r['rejection_rate']:.2f} (min_score={args.min_score})")
    for m in r["misses"]:
        print("  -", m)
    ok = (
        r["hit@1"] >= args.min_hit1
        and r["rejection_rate"] >= args.min_reject
        and r["acceptance_rate"] >= args.min_accept
    )
    if not ok:
        print("FAILED: below configured minimums")
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())
