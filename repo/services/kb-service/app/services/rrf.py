"""Pure-Python Reciprocal Rank Fusion (Plan §4.1, STEP-4 feature).

Equivalent to ``LlamaIndex QueryFusionRetriever(mode='reciprocal_rerank',
num_queries=1)`` (llama-index-core 0.14.23 ``_reciprocal_rerank_fusion``).

The RRF paper (Cormack et al. 2009) uses k=60 to dampen outlier rankings.
LlamaIndex hardcodes ``k = 60.0`` and sums ``1.0 / (rank + k)`` per
retriever list (0-based ``enumerate`` after sorting each list by score
descending). With ``num_queries=1`` only the original query is used (no
LLM query generation), so each retriever contributes one ranked list.
"""
from __future__ import annotations

from collections import defaultdict
from typing import Iterable

# LlamaIndex 0.14.23 `_reciprocal_rerank_fusion` hardcodes k=60.0
# (the RRF paper's recommended value for best results).
RRF_K = 60.0


def reciprocal_rank_fusion(
    rank_lists: Iterable[list[tuple[str, float]]],
    k: float = RRF_K,
    top_n: int = 10,
) -> list[tuple[str, float]]:
    """Fuse multiple ranked lists into one via Reciprocal Rank Fusion.

    Equivalent to LlamaIndex ``QueryFusionRetriever(mode='reciprocal_rerank',
    num_queries=1)`` (llama-index-core 0.14.23). Each input list is sorted by
    score descending (matching LlamaIndex's ``sorted(..., reverse=True)``),
    then each item contributes ``1.0 / (rank + k)`` (0-based rank) to its
    fused score. Results are sorted by fused score descending and truncated
    to ``top_n``.

    Args:
        rank_lists: Iterable of ranked lists. Each list is a list of
            ``(chunk_id, score)`` tuples. The score is used only for
            intra-list ordering (matching LlamaIndex's sort step); the
            fused score is rank-based, not score-based.
        k: RRF damping constant (default 60.0, matching LlamaIndex and the
            RRF paper). LlamaIndex 0.14.23 does NOT apply retriever weights
            in reciprocal rank mode — each list contributes weight 1.0.
        top_n: Maximum number of fused results to return (matching
            LlamaIndex's ``similarity_top_k`` slice).

    Returns:
        List of ``(chunk_id, fused_score)`` tuples sorted by fused score
        descending, truncated to ``top_n``.
    """
    scores: dict[str, float] = defaultdict(float)
    for rank_list in rank_lists:
        # LlamaIndex sorts each list by score descending before ranking.
        sorted_list = sorted(
            rank_list, key=lambda x: x[1] or 0.0, reverse=True
        )
        for rank, (chunk_id, _) in enumerate(sorted_list):
            # LlamaIndex: fused_scores[hash] += 1.0 / (rank + k)
            # (0-based enumerate, no +1, no retriever_weight in v0.14.23)
            scores[chunk_id] += 1.0 / (rank + k)
    # Sort by fused score descending, truncate to top_n.
    return sorted(scores.items(), key=lambda x: -x[1])[:top_n]
