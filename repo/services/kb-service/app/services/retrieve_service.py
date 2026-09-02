"""kb-service hybrid retrieval orchestrator (Plan §4.2, STEP-4 feature).

Orchestrates: rag-engine.Embed (query) → Core API vector search →
PG keyword search → RRF fusion → parent backfill → dedup.
The result is equivalent to the legacy rag-engine RetrieveService output
(sources chunk_id set Jaccard > 90%, Plan §0.1).

Three retrieval modes (Plan §4.2):
  - hybrid  → vector + keyword → RRF fusion
  - vector  → Core API vector search only
  - keyword → PG pg_trgm keyword search only

Hybrid score normalization (Plan §4.2 / §2.1): RRF scores (~0.016) are not
comparable to a cosine ``score_threshold``, so ``max_score`` for the hybrid
no-result gate is taken from the real vector cosine similarities (matching
the legacy QAService hybrid gate, qa_service.py lines 495-512).
"""
from __future__ import annotations

import asyncio
import uuid
from typing import Any, Callable, Protocol, runtime_checkable

from app.repositories import chunk as chunk_repo
from app.repositories.rls import set_tenant_context
from app.services.rrf import reciprocal_rank_fusion


# ── Defaults (match legacy retrieve_service.py lines 44-54) ────────────────


DEFAULT_TOP_K = 5
DEFAULT_SCORE_THRESHOLD = 0.3
DEFAULT_RRF_K = 60.0


# ── CoreClient / RagEngine client factory protocols ─────────────────────────


@runtime_checkable
class _CoreClientFactory(Protocol):
    """Callable that builds a tenant-scoped CoreClient."""

    def __call__(self, tenant_id: str) -> Any: ...


@runtime_checkable
class _RagEngineEmbedder(Protocol):
    """Async embed(texts) → (vectors, dimension).

    Matches ``RagEngineClientProtocol.embed`` in contracts.py (keyword-only
    ``texts``), so callers must pass ``embed(texts=[...])``.
    """

    async def embed(self, *, texts: list[str]) -> tuple[list[list[float]], int]: ...


@runtime_checkable
class _ParentLookup(Protocol):
    """Async parent-block lookup for backfill (Plan §4.2).

    ``lookup_parent(parent_chunk_id)`` returns a single parent chunk dict
    (or None); ``lookup_parents(doc_id)`` returns all parent chunks for a
    document. Both are RLS-scoped by ``tenant_id``.
    """

    async def lookup_parent(
        self, *, tenant_id: str, parent_chunk_id: str
    ) -> dict[str, Any] | None: ...

    async def lookup_parents(
        self, *, tenant_id: str, doc_id: str
    ) -> list[dict[str, Any]]: ...


class _DbParentLookup:
    """Default _ParentLookup backed by kb-service's asyncpg pool.

    Mirrors the legacy ``_query_one`` / ``_query_parents``
    (rag-engine retrieve_service.py lines 380-414) using kb-service's
    chunk_repo + RLS helper.
    """

    def __init__(self, pool: Any) -> None:
        self._pool = pool

    async def lookup_parent(
        self, *, tenant_id: str, parent_chunk_id: str
    ) -> dict[str, Any] | None:
        async with self._pool.acquire() as conn:
            await set_tenant_context(conn, tenant_id)
            row = await conn.fetchrow(
                """
                SELECT id::text AS chunk_id, content
                  FROM kb_chunks
                 WHERE id = $1 AND chunk_type = 'parent'
                """,
                _to_uuid(parent_chunk_id, "parent_chunk_id"),
            )
        return dict(row) if row else None

    async def lookup_parents(
        self, *, tenant_id: str, doc_id: str
    ) -> list[dict[str, Any]]:
        async with self._pool.acquire() as conn:
            await set_tenant_context(conn, tenant_id)
            rows = await conn.fetch(
                """
                SELECT id::text AS chunk_id, content
                  FROM kb_chunks
                 WHERE doc_id = $1 AND chunk_type = 'parent'
                 ORDER BY id
                """,
                _to_uuid(doc_id, "doc_id"),
            )
        return [dict(r) for r in rows]


def _to_uuid(value: str, field: str) -> uuid.UUID:
    try:
        return uuid.UUID(value)
    except (ValueError, AttributeError, TypeError) as exc:
        raise ValueError(f"invalid UUID for {field!r}: {value!r}") from exc


class RetrieveService:
    """kb-service hybrid retrieval orchestrator (Plan §4.2).

    Orchestrates: rag-engine.Embed (query) → Core API vector search →
    PG keyword search → RRF fusion → parent backfill → dedup.
    Result equivalent to legacy rag-engine RetrieveService.
    """

    def __init__(
        self,
        *,
        db_pool: Any,
        core_client_factory: _CoreClientFactory | Callable[[str], Any],
        rag_engine_client: _RagEngineEmbedder,
        parent_lookup: _ParentLookup | None = None,
        rrf_k: float = DEFAULT_RRF_K,
    ) -> None:
        self._pool = db_pool
        self._core_client_factory = core_client_factory
        self._rag_engine = rag_engine_client
        self._parent_lookup = parent_lookup or _DbParentLookup(db_pool)
        self._rrf_k = rrf_k

    async def retrieve(
        self,
        *,
        tenant_id: str,
        kb_id: str,
        question: str,
        top_k: int = DEFAULT_TOP_K,
        score_threshold: float = DEFAULT_SCORE_THRESHOLD,
        retrieval_mode: str = "hybrid",
        vector_store_id: str | None = None,
    ) -> tuple[list[dict[str, Any]], float]:
        """Run hybrid retrieval and return (sources, max_score).

        Args (see RetrieveServiceProtocol in contracts.py for full docs).

        Note: ``score_threshold`` is accepted to satisfy the protocol
        signature but is NOT applied here — QueryOrchestrator gate ②
        compares the returned ``max_score`` against it.

        Returns:
            (sources, max_score). For hybrid mode max_score = max(vector
            cosine); for vector mode = max(cosine); for keyword mode = max
            (token coverage). Used by QueryOrchestrator gate ②.
        """
        if retrieval_mode in ("hybrid", "vector") and not vector_store_id:
            raise ValueError(
                "vector_store_id is required for vector/hybrid retrieval"
            )

        vector_results: list[dict[str, Any]] = []
        kw_results: list[dict[str, Any]] = []
        vector_ranked: list[tuple[str, float]] = []
        kw_ranked: list[tuple[str, float]] = []

        # ── Vector leg (hybrid | vector) ──────────────────────────────────
        if retrieval_mode in ("hybrid", "vector"):
            vectors, _dim = await self._rag_engine.embed(texts=[question])
            query_vector = vectors[0] if vectors else []
            core = self._core_client_factory(tenant_id)
            vector_results = await core.search_vector_store(
                vector_store_id=vector_store_id,
                vector=query_vector,
                top_k=top_k * 2,
                filter_expr=None,
            )
            vector_ranked = [
                (str(r.get("metadata", {}).get("chunk_id", "")), float(r.get("score", 0.0) or 0.0))
                for r in vector_results
            ]

        # ── Keyword leg (hybrid | keyword) ────────────────────────────────
        if retrieval_mode in ("hybrid", "keyword"):
            async with self._pool.acquire() as conn:
                kw_results = await chunk_repo.keyword_search(
                    conn,
                    tenant_id=tenant_id,
                    kb_id=kb_id,
                    query=question,
                    limit=top_k * 2,
                )
            kw_ranked = [
                (str(r.get("chunk_id", "")), float(r.get("score", 0.0) or 0.0))
                for r in kw_results
            ]

        # ── Fuse / assemble sources ───────────────────────────────────────
        if retrieval_mode == "hybrid":
            fused = reciprocal_rank_fusion(
                [vector_ranked, kw_ranked], k=self._rrf_k, top_n=top_k
            )
            results = self._build_sources_from_fusion(
                fused, vector_results, kw_results
            )
        elif retrieval_mode == "vector":
            results = self._process_vector_only(vector_results)
        else:  # keyword
            results = self._process_keyword_only(kw_results)

        # ── Parent backfill (Plan §4.2; rag-engine retrieve_service.py 911-918) ─
        # Common path: parent_content already denormalized at write time
        # (embed_service.py line 94), so backfill is a no-op. Edge case:
        # metadata missing → fall back to kb_chunks lookup.
        await self._backfill_parents(results, tenant_id=tenant_id)

        # ── Parent replacement + dedup (rag-engine _return_parent_and_dedup) ──
        deduped = self._return_parent_and_dedup(results)

        # ── Hybrid max_score normalization (Plan §4.2 / §2.1) ─────────────
        # RRF scores (~0.016) are not comparable to a cosine score_threshold,
        # so for hybrid mode max_score = max(vector cosine), matching the
        # legacy QAService hybrid gate (qa_service.py lines 495-512).
        if retrieval_mode == "keyword":
            max_score = max(
                (float(r.get("score", 0.0) or 0.0) for r in kw_results),
                default=0.0,
            )
        else:  # hybrid | vector — both use vector cosine scores
            max_score = max(
                (float(r.get("score", 0.0) or 0.0) for r in vector_results),
                default=0.0,
            )

        return deduped, max_score

    # ── Source assembly helpers (from rag-engine retrieve_service.py + qa_service.py) ─

    def _build_sources_from_fusion(
        self,
        fused: list[tuple[str, float]],
        vector_results: list[dict[str, Any]],
        kw_results: list[dict[str, Any]],
    ) -> list[dict[str, Any]]:
        """Assemble sources from RRF fusion + raw results (Plan §4.2).

        Hybrid mode: every chunk's score is the RRF fused score min-max
        normalized against the fused peak (legacy qa_service.py 566-567).
        A chunk hit by both legs sums two rank contributions and therefore
        scores higher than a single-leg hit — this is what distinguishes
        hybrid scores from plain vector cosine scores. (Previously
        vector-leg hits kept their raw cosine score, making hybrid output
        look identical to vector-mode output for the same query.)
        """
        vec_map: dict[str, dict[str, Any]] = {}
        for r in vector_results:
            cid = str(r.get("metadata", {}).get("chunk_id", ""))
            if cid:
                vec_map[cid] = r
        kw_map: dict[str, dict[str, Any]] = {
            str(r.get("chunk_id", "")): r for r in kw_results
        }
        rrf_peak = max((score for _, score in fused), default=0.0)

        sources: list[dict[str, Any]] = []
        for chunk_id, rrf_score in fused:
            if chunk_id in vec_map:
                r = vec_map[chunk_id]
                meta = r.get("metadata", {}) or {}
                # RRF fused score normalized to [0, 1] against the peak.
                # Dual-leg hits carry contributions from both ranked
                # lists, so they land near 1.0; single-leg hits lower.
                if rrf_peak > 0:
                    score = max(0.0, min(1.0, rrf_score / rrf_peak))
                else:
                    score = 0.0
                content = r.get("content", "") or ""
                parent_content = str(meta.get("parent_content", "") or "")
                parent_chunk_id = str(meta.get("parent_chunk_id", "") or "")
                doc_id = str(meta.get("doc_id", "") or "")
                file_name = str(meta.get("file_name", "") or "")
                page_number = int(meta.get("page_number", 0) or 0)
                chunk_type = str(meta.get("chunk_type", "child") or "child")
            elif chunk_id in kw_map:
                r = kw_map[chunk_id]
                # keyword-only: same RRF min-max normalization
                if rrf_peak > 0:
                    score = max(0.0, min(1.0, rrf_score / rrf_peak))
                else:
                    score = 0.0
                content = str(r.get("content", "") or "")
                parent_content = str(r.get("parent_content", "") or "")
                parent_chunk_id = str(r.get("parent_chunk_id", "") or "")
                doc_id = str(r.get("doc_id", "") or "")
                file_name = str(r.get("file_name", "") or "")
                page_number = int(r.get("page_number", 0) or 0)
                chunk_type = str(r.get("chunk_type", "child") or "child")
            else:
                # chunk_id not found in either leg — skip (shouldn't happen
                # in normal operation; guards against malformed RRF input).
                continue
            sources.append({
                "chunk_id": chunk_id,
                "doc_id": doc_id,
                "file_name": file_name,
                "page": page_number,
                "content": content,
                "parent_content": parent_content,
                "parent_chunk_id": parent_chunk_id,
                "chunk_type": chunk_type,
                "score": score,
            })
        return sources

    def _process_vector_only(
        self, vector_results: list[dict[str, Any]]
    ) -> list[dict[str, Any]]:
        """Vector mode: assemble sources from Core API results (Plan §4.2).

        Core API search response includes ``content`` (Plan §1.4
        modification) so no second PG round-trip is needed.
        """
        out: list[dict[str, Any]] = []
        for r in vector_results:
            meta = r.get("metadata", {}) or {}
            out.append({
                "chunk_id": str(meta.get("chunk_id", "") or ""),
                "doc_id": str(meta.get("doc_id", "") or ""),
                "file_name": str(meta.get("file_name", "") or ""),
                "page": int(meta.get("page_number", 0) or 0),
                "content": str(r.get("content", "") or ""),
                "parent_content": str(meta.get("parent_content", "") or ""),
                "parent_chunk_id": str(meta.get("parent_chunk_id", "") or ""),
                "chunk_type": str(meta.get("chunk_type", "child") or "child"),
                "score": float(r.get("score", 0.0) or 0.0),
            })
        return out

    def _process_keyword_only(
        self, kw_results: list[dict[str, Any]]
    ) -> list[dict[str, Any]]:
        """Keyword mode: assemble sources from PG results (Plan §4.2)."""
        out: list[dict[str, Any]] = []
        for r in kw_results:
            out.append({
                "chunk_id": str(r.get("chunk_id", "") or ""),
                "doc_id": str(r.get("doc_id", "") or ""),
                "file_name": str(r.get("file_name", "") or ""),
                "page": int(r.get("page_number", 0) or 0),
                "content": str(r.get("content", "") or ""),
                "parent_content": str(r.get("parent_content", "") or ""),
                "parent_chunk_id": str(r.get("parent_chunk_id", "") or ""),
                "chunk_type": str(r.get("chunk_type", "child") or "child"),
                "score": float(r.get("score", 0.0) or 0.0),
            })
        return out

    def _return_parent_and_dedup(
        self, sources: list[dict[str, Any]]
    ) -> list[dict[str, Any]]:
        """Parent replacement + dedup (rag-engine retrieve_service.py 539-562).

        1. child chunk with parent_content → content replaced with
           parent_content.
        2. doc_summary chunk → NOT deduplicated (kept as-is).
        3. Same parent_chunk_id children → dedup keeping the highest score.
        """
        finalized: list[dict[str, Any]] = []
        best: dict[str, dict[str, Any]] = {}
        for src in sources:
            # child + parent_content → replace content with parent_content
            if src.get("chunk_type") == "child" and src.get("parent_content"):
                src["content"] = src["parent_content"]
            key = src.get("parent_chunk_id", "") or ""
            # No key or non-child → keep as-is
            if not key or src.get("chunk_type") != "child":
                finalized.append(src)
                continue
            # Same-parent dedup, keep the highest score
            if key not in best or src["score"] > best[key]["score"]:
                best[key] = src
        finalized.extend(best.values())
        return finalized

    async def _backfill_parents(
        self, sources: list[dict[str, Any]], *, tenant_id: str
    ) -> None:
        """Backfill parent_content for child + doc_summary chunks (Plan §4.2).

        - child with empty parent_content → lookup parent_chunk_id.
        - doc_summary with empty parent_content → concatenate all parent
          blocks of the document.
        Common path: parent_content is denormalized at write time, so this
        is a no-op. Edge case lookups are dispatched concurrently via
        ``asyncio.gather`` (each lookup is independent — no inter-dependency).
        """
        # Collect (index, kind, coroutine) triples for chunks needing backfill.
        tasks: list[tuple[int, str, Any]] = []
        for i, src in enumerate(sources):
            if src.get("parent_content"):
                continue
            chunk_type = src.get("chunk_type", "")
            if chunk_type == "child":
                parent_chunk_id = src.get("parent_chunk_id", "")
                if not parent_chunk_id:
                    continue
                coro = self._parent_lookup.lookup_parent(
                    tenant_id=tenant_id, parent_chunk_id=parent_chunk_id
                )
                tasks.append((i, "child", coro))
            elif chunk_type == "doc_summary":
                doc_id = src.get("doc_id", "")
                if not doc_id:
                    continue
                coro = self._parent_lookup.lookup_parents(
                    tenant_id=tenant_id, doc_id=doc_id
                )
                tasks.append((i, "summary", coro))

        if not tasks:
            return

        # Dispatch all lookups concurrently (edge-case path; common path
        # has zero tasks because parent_content is denormalized at write time).
        results = await asyncio.gather(
            *(t[2] for t in tasks), return_exceptions=True
        )
        for (idx, kind, _coro), result in zip(tasks, results):
            if isinstance(result, Exception):
                continue
            if kind == "child" and result:
                sources[idx]["parent_content"] = str(
                    result.get("content", "") or ""
                )
            elif kind == "summary" and result:
                sources[idx]["parent_content"] = "\n".join(
                    str(p.get("content", "") or "")
                    for p in result
                    if p.get("content")
                )
