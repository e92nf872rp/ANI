"""Tests for the pure-Python RRF implementation (issue-031 / Plan §4.1).

Verifies that ``app.services.rrf.reciprocal_rank_fusion`` is numerically
equivalent to LlamaIndex 0.14.23 ``QueryFusionRetriever._reciprocal_rerank_fusion``
(``mode='reciprocal_rerank'``, ``num_queries=1``).

The reference algorithm is reproduced inline here (from the pinned
llama-index-core 0.14.23 source) so the test does NOT require llama_index to
be installed — it compares our implementation against the verbatim
algorithm.
"""
import os
import sys

import pytest

_SERVICE_ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
sys.path.insert(0, _SERVICE_ROOT)
sys.path.insert(0, os.path.join(_SERVICE_ROOT, "app", "generated"))

from app.services.rrf import RRF_K, reciprocal_rank_fusion


# ── Verbatim LlamaIndex 0.14.23 _reciprocal_rerank_fusion reference ──────────


def _llamaindex_rrf_reference(rank_lists):
    """Reproduce llama-index-core 0.14.23 `_reciprocal_rerank_fusion`.

    Source (pinned v0.14.23):
        k = 60.0
        for nodes_with_scores in results.values():
            for rank, nws in enumerate(
                sorted(nodes_with_scores, key=lambda x: x.score or 0.0, reverse=True)
            ):
                fused_scores[hash] += 1.0 / (rank + k)
        return sorted(fused_scores.items(), key=lambda x: x[1], reverse=True)

    Here we operate on (chunk_id, score) tuples (the score field plays the
    role of NodeWithScore.score for intra-list sorting only). The fused
    score is returned for comparison.
    """
    k = 60.0
    fused_scores: dict[str, float] = {}
    for rank_list in rank_lists:
        for rank, (chunk_id, _score) in enumerate(
            sorted(rank_list, key=lambda x: x[1] or 0.0, reverse=True)
        ):
            if chunk_id not in fused_scores:
                fused_scores[chunk_id] = 0.0
            fused_scores[chunk_id] += 1.0 / (rank + k)
    return sorted(fused_scores.items(), key=lambda x: x[1], reverse=True)


# ── Tests ────────────────────────────────────────────────────────────────────


def test_rrf_constant_matches_llamaindex():
    # LlamaIndex 0.14.23 hardcodes k=60.0
    assert RRF_K == 60.0


def test_empty_rank_lists_returns_empty():
    assert reciprocal_rank_fusion([], top_n=5) == []


def test_single_list_truncates_to_top_n():
    rank_list = [(f"c{i}", float(i)) for i in range(5)]
    out = reciprocal_rank_fusion([rank_list], top_n=3)
    assert len(out) == 3
    # Highest score ranks first (sorted desc within the list).
    assert out[0][0] == "c4"
    assert out[1][0] == "c3"
    assert out[2][0] == "c2"


def test_single_list_score_formula():
    """Single list: fused_score[c0] = 1.0 / (0 + 60) at rank 0."""
    rank_list = [("c0", 0.9), ("c1", 0.1)]
    out = dict(reciprocal_rank_fusion([rank_list], top_n=10))
    # c0 (score 0.9) sorts first → rank 0 → 1/60
    # c1 (score 0.1) sorts second → rank 1 → 1/61
    assert out["c0"] == pytest.approx(1.0 / 60.0)
    assert out["c1"] == pytest.approx(1.0 / 61.0)


def test_overlapping_hits_sum_scores():
    """A hit present in both lists sums contributions from each."""
    vec = [("a", 0.9), ("b", 0.8)]
    kw = [("b", 0.5), ("c", 0.4)]
    out = dict(reciprocal_rank_fusion([vec, kw], top_n=10))
    # 'b' is rank 1 in vec (1/61) + rank 0 in kw (1/60)
    assert out["b"] == pytest.approx(1.0 / 61.0 + 1.0 / 60.0)
    # 'a' is rank 0 in vec only → 1/60
    assert out["a"] == pytest.approx(1.0 / 60.0)
    # 'c' is rank 1 in kw only → 1/61
    assert out["c"] == pytest.approx(1.0 / 61.0)
    # 'b' (appears in both) ranks above 'a' and 'c'
    order = [cid for cid, _ in reciprocal_rank_fusion([vec, kw], top_n=10)]
    assert order[0] == "b"


def test_equivalence_with_llamaindex_algorithm_disjoint():
    """Disjoint lists: our output matches LlamaIndex reference exactly."""
    vec = [("v1", 0.8), ("v2", 0.7), ("v3", 0.6)]
    kw = [("k1", 0.5), ("k2", 0.4)]
    ours = reciprocal_rank_fusion([vec, kw], top_n=10)
    ref = _llamaindex_rrf_reference([vec, kw])
    assert ours == ref


def test_equivalence_with_llamaindex_algorithm_overlapping():
    """Overlapping chunk_ids: our output matches LlamaIndex reference exactly."""
    vec = [("c1", 0.9), ("c2", 0.5), ("c3", 0.4)]
    kw = [("c2", 0.8), ("c4", 0.3), ("c1", 0.2)]
    ours = reciprocal_rank_fusion([vec, kw], top_n=10)
    ref = _llamaindex_rrf_reference([vec, kw])
    assert ours == ref


def test_equivalence_with_llamaindex_algorithm_unsorted_input():
    """LlamaIndex sorts each list by score desc; unsorted input must match."""
    vec = [("c3", 0.1), ("c1", 0.9), ("c2", 0.5)]
    kw = [("c2", 0.2), ("c1", 0.8)]
    ours = reciprocal_rank_fusion([vec, kw], top_n=10)
    ref = _llamaindex_rrf_reference([vec, kw])
    assert ours == ref


def test_equivalence_with_llamaindex_three_lists():
    """Three retriever lists (matching num_queries=1, 3 retrievers)."""
    a = [("x", 0.9), ("y", 0.1)]
    b = [("y", 0.8), ("z", 0.2)]
    c = [("x", 0.7), ("z", 0.3)]
    ours = reciprocal_rank_fusion([a, b, c], top_n=10)
    ref = _llamaindex_rrf_reference([a, b, c])
    assert ours == ref


def test_top_n_truncation_after_fusion():
    """top_n truncates the fused result (LlamaIndex similarity_top_k slice)."""
    vec = [(f"v{i}", 1.0 - i * 0.01) for i in range(10)]
    kw = [(f"k{i}", 1.0 - i * 0.01) for i in range(10)]
    out = reciprocal_rank_fusion([vec, kw], top_n=5)
    assert len(out) == 5


def test_duplicate_chunk_id_within_single_list():
    """A chunk_id appearing twice in the same list: LlamaIndex sums both
    (it keys by node.hash, but for our purposes duplicate chunk_ids in one
    list are unusual; we match LlamaIndex's sum behavior)."""
    # LlamaIndex iterates every node in the list (no dedup within a list),
    # so a duplicate chunk_id contributes twice.
    rank_list = [("a", 0.9), ("a", 0.5)]
    out = dict(reciprocal_rank_fusion([rank_list], top_n=10))
    # rank 0 (score 0.9) + rank 1 (score 0.5) → 1/60 + 1/61
    assert out["a"] == pytest.approx(1.0 / 60.0 + 1.0 / 61.0)


def test_zero_score_treated_as_lowest():
    """score=0.0 sorts last (matches LlamaIndex `x.score or 0.0`)."""
    rank_list = [("a", 0.0), ("b", 0.5)]
    out = dict(reciprocal_rank_fusion([rank_list], top_n=10))
    # b (0.5) sorts first → rank 0 → 1/60; a (0.0) → rank 1 → 1/61
    assert out["b"] == pytest.approx(1.0 / 60.0)
    assert out["a"] == pytest.approx(1.0 / 61.0)


def test_none_score_treated_as_zero():
    """None score → 0.0 (matches LlamaIndex `x.score or 0.0`)."""
    rank_list = [("a", None), ("b", 0.5)]
    out = dict(reciprocal_rank_fusion([rank_list], top_n=10))
    assert out["b"] == pytest.approx(1.0 / 60.0)
    assert out["a"] == pytest.approx(1.0 / 61.0)
