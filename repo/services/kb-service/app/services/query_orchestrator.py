"""kb-service synchronous Query orchestrator (Plan step 8A, issue-035).

Orchestrates: RetrieveService.retrieve → three no-result gates →
rag-engine.Generate RPC → return QueryResult.

The three no-result gates reproduce the legacy rag-engine QAService
behavior (qa_service.py lines 471 / 522 / 576):

  ① retrieval empty → NO_RESULT_ANSWER, tokens=0 (LLM not called).
  ② max_score < score_threshold → NO_RESULT_ANSWER, tokens=0
    (LLM not called).
  ③ sources empty after dedup → NO_RESULT_ANSWER, tokens = LLM actual
    usage (LLM was called).

Multi-turn history (Plan §0.1 / §2.1):
  The caller (grpc_server.Query) persists the current-turn user message
  to DB + Redis BEFORE invoking this orchestrator, then loads history
  (which therefore INCLUDES the current-turn user). Generate RPC
  appends ``question`` as the final USER message (reproduces the legacy
  {query_str} template), so the current-turn user appears twice — this
  matches the legacy ContextChatEngine behavior and is intentional.
"""
from __future__ import annotations

import logging
from dataclasses import dataclass, field
from typing import Any, AsyncIterator

from app.services.contracts import (
    QueryResult,
    RagEngineClientProtocol,
    RetrieveServiceProtocol,
)

logger = logging.getLogger(__name__)

# Plan step 8A: NO_RESULT_ANSWER matches legacy qa_service.py line 79.
NO_RESULT_ANSWER = "未检索到与问题相关的内容，无法回答。"


# ── Streaming events (issue-038: Retrieve server-streaming RPC) ──────────────


@dataclass
class StreamTokenEvent:
    """One incremental LLM token chunk."""

    content: str


@dataclass
class StreamSourcesEvent:
    """Finalized source chunks after gates + generation."""

    sources: list[dict[str, Any]] = field(default_factory=list)


@dataclass
class StreamDoneEvent:
    """Stream termination with token usage and session id."""

    session_id: str = ""
    input_tokens: int = 0
    output_tokens: int = 0


@dataclass
class StreamNoResultEvent:
    """Emitted when a no-result gate trips (tokens may be 0 or actual).

    Carries the NO_RESULT_ANSWER text so the caller can emit it as a token
    event, plus the actual token counts to report in the done event.
    """

    answer: str = NO_RESULT_ANSWER
    input_tokens: int = 0
    output_tokens: int = 0


class QueryOrchestrator:
    """Synchronous RAG query orchestrator (Plan step 8A).

    Orchestrates: RetrieveService.retrieve → three no-result gates →
    rag-engine.Generate RPC → return QueryResult.

    Implements ``QueryOrchestratorProtocol`` from contracts.py.
    """

    def __init__(
        self,
        *,
        retrieve_service: RetrieveServiceProtocol,
        rag_engine_client: RagEngineClientProtocol,
    ) -> None:
        """Initialize the orchestrator.

        Args:
            retrieve_service:   RetrieveServiceProtocol — hybrid retrieval
                                (vector + keyword + RRF + dedup).
            rag_engine_client:  RagEngineClientProtocol — gRPC client for
                                rag-engine Generate RPC.
        """
        self._retrieve = retrieve_service
        self._rag_engine = rag_engine_client

    async def query(
        self,
        *,
        tenant_id: str,
        kb_id: str,
        question: str,
        session_id: str,
        top_k: int,
        score_threshold: float,
        retrieval_mode: str,
        inference_service_name: str,
        vector_store_id: str,
        history: list[dict[str, str]],
    ) -> QueryResult:
        """Run a synchronous RAG query and return a QueryResult.

        Three no-result gates (reproduces legacy QAService behavior):
          ① retrieval empty → NO_RESULT_ANSWER, tokens=0 (LLM not called).
          ② max_score < score_threshold → NO_RESULT_ANSWER, tokens=0
            (LLM not called).
          ③ sources empty after dedup → NO_RESULT_ANSWER, tokens = LLM
            actual usage (LLM was called).
        """
        # 1. retrieve → (sources, max_score)
        #    sources already deduped + parent-backfilled by RetrieveService.
        #    max_score: hybrid/vector → max(vector cosine); keyword → max
        #    (token coverage). Used by gate ②.
        sources, max_score = await self._retrieve.retrieve(
            tenant_id=tenant_id,
            kb_id=kb_id,
            question=question,
            top_k=top_k,
            score_threshold=score_threshold,
            retrieval_mode=retrieval_mode,
            vector_store_id=vector_store_id,
        )

        # 2. Gate ①: retrieval empty (legacy QAService lines 471-483).
        #    LLM not called → tokens=0.
        if not sources:
            return QueryResult(
                answer=NO_RESULT_ANSWER,
                sources=[],
                session_id=session_id,
                input_tokens=0,
                output_tokens=0,
            )

        # 3. Gate ②: max_score < score_threshold (legacy QAService 522-533).
        #    LLM not called → tokens=0.
        if max_score < score_threshold:
            return QueryResult(
                answer=NO_RESULT_ANSWER,
                sources=[],
                session_id=session_id,
                input_tokens=0,
                output_tokens=0,
            )

        # 4. rag-engine.Generate RPC (LLM call happens here).
        #    history includes the current-turn user message (reproduces
        #    legacy behavior: kb-service appends user to Redis before calling
        #    rag-engine). Generate RPC appends question as the final USER
        #    message (reproduces {query_str} template) → current-turn user
        #    appears twice (legacy behavior, intentional).
        result = await self._rag_engine.generate(
            question=question,
            session_id=session_id,
            context=sources,
            history=history,
            inference_service_name=inference_service_name,
        )

        answer = str(result.get("answer", "") or "")
        input_tokens = int(result.get("input_tokens", 0) or 0)
        output_tokens = int(result.get("output_tokens", 0) or 0)

        # 5. Gate ③: sources empty after Generate (legacy QAService 576-583).
        #    LLM WAS called → return actual LLM tokens (not 0).
        #    In the legacy qa_service, sources could become empty after a
        #    post-LLM dedup step (source_nodes → _return_parent_and_dedup).
        #    In the new architecture, RetrieveService already deduped before
        #    this point, so `sources` is the same non-empty list from gate ①.
        #    This gate is retained as a safety net matching the legacy
        #    control flow — it is effectively unreachable but documents the
        #    expected behavior should a future code path introduce post-LLM
        #    source filtering (Plan §0.1, accepted difference line 1591).
        if not sources:
            return QueryResult(
                answer=NO_RESULT_ANSWER,
                sources=[],
                session_id=session_id,
                input_tokens=input_tokens,
                output_tokens=output_tokens,
            )

        return QueryResult(
            answer=answer,
            sources=sources,
            session_id=session_id,
            input_tokens=input_tokens,
            output_tokens=output_tokens,
        )

    async def query_stream(
        self,
        *,
        tenant_id: str,
        kb_id: str,
        question: str,
        session_id: str,
        top_k: int,
        score_threshold: float,
        retrieval_mode: str,
        inference_service_name: str,
        vector_store_id: str,
        history: list[dict[str, str]],
    ) -> AsyncIterator[Any]:
        """Streaming RAG query — async generator (issue-038: Retrieve RPC).

        Same gate logic as ``query()`` but yields typed stream events:
          - Normal path: StreamTokenEvent* → StreamSourcesEvent → StreamDoneEvent
          - Gate ①②:     StreamNoResultEvent(tokens=0) → StreamSourcesEvent([]) → StreamDoneEvent(tokens=0)
          - Gate ③:      StreamNoResultEvent(actual tokens) → StreamSourcesEvent([]) → StreamDoneEvent(actual tokens)

        The caller (grpc_server.Retrieve) maps these to ``RetrieveEvent`` proto
        messages. This keeps the three-gate logic in one place, shared by both
        the sync ``query()`` and the streaming ``query_stream()`` paths.
        """
        # 1. retrieve → (sources, max_score)
        sources, max_score = await self._retrieve.retrieve(
            tenant_id=tenant_id,
            kb_id=kb_id,
            question=question,
            top_k=top_k,
            score_threshold=score_threshold,
            retrieval_mode=retrieval_mode,
            vector_store_id=vector_store_id,
        )

        # 2. Gate ①: retrieval empty → NO_RESULT, tokens=0 (LLM not called).
        if not sources:
            yield StreamNoResultEvent(input_tokens=0, output_tokens=0)
            yield StreamSourcesEvent(sources=[])
            yield StreamDoneEvent(
                session_id=session_id, input_tokens=0, output_tokens=0,
            )
            return

        # 3. Gate ②: max_score < score_threshold → NO_RESULT, tokens=0.
        if max_score < score_threshold:
            yield StreamNoResultEvent(input_tokens=0, output_tokens=0)
            yield StreamSourcesEvent(sources=[])
            yield StreamDoneEvent(
                session_id=session_id, input_tokens=0, output_tokens=0,
            )
            return

        # 4. GenerateStream → token events (LLM call happens here).
        input_tokens = 0
        output_tokens = 0
        answer_parts: list[str] = []
        async for tok in self._rag_engine.generate_stream(
            question=question,
            session_id=session_id,
            context=sources,
            history=history,
            inference_service_name=inference_service_name,
        ):
            content = str(tok.get("content", "") or "")
            if tok.get("done"):
                input_tokens = int(tok.get("input_tokens", 0) or 0)
                output_tokens = int(tok.get("output_tokens", 0) or 0)
                if content:
                    answer_parts.append(content)
                    yield StreamTokenEvent(content=content)
            elif content:
                answer_parts.append(content)
                yield StreamTokenEvent(content=content)

        answer = "".join(answer_parts)

        # 5. Gate ③: sources empty after Generate (safety net, tokens=actual).
        if not sources:
            yield StreamNoResultEvent(
                input_tokens=input_tokens, output_tokens=output_tokens,
            )
            yield StreamSourcesEvent(sources=[])
            yield StreamDoneEvent(
                session_id=session_id,
                input_tokens=input_tokens,
                output_tokens=output_tokens,
            )
            return

        # 6. Normal path: sources → done.
        yield StreamSourcesEvent(sources=sources)
        yield StreamDoneEvent(
            session_id=session_id,
            input_tokens=input_tokens,
            output_tokens=output_tokens,
        )
