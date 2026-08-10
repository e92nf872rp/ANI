"""RAG question-answering service (US-014 / SPEC §2.2, §5.1).

``qa_service`` implements the synchronous RAG QA path mandated by
SPEC §5.1 / PRD US-014:

1. Build the fusion retriever from :class:`RetrieveService`
   (vector + pg_trgm + RRF, ``num_queries=1``).
2. Construct a :class:`ContextChatEngine.from_defaults` with:
   * ``retriever=fusion_retriever`` (the QueryFusionRetriever from step 1),
   * ``memory=ChatMemoryBuffer(chat_store=RedisChatStore(...),
     session_id=session_id)`` — multi-turn context backed by Redis
     (SPEC §5.1 qa_service),
   * ``llm=OpenAILike(model=..., api_base=vllm_url, api_key="...",
     is_chat_model=True, context_window=...)`` — vLLM via the OpenAI-compatible
     interface (SPEC §5.1 qa_service / §1.3).
3. Call ``engine.chat(question)`` and return
   ``answer + sources + session_id + tokens`` synchronously (SPEC §5.1 /
   PRD US-014 AC5).

The ``tokens`` accounting (input + output) is derived from the LLM response's
``raw`` attribute when available (LlamaIndex's ``AgentChatResponse.raw``
carries the OpenAI usage object), falling back to 0 when the usage is not
reported (e.g. some vLLM deployments). This keeps the return shape stable
without forcing a specific tokeniser.

Heavy/optional deps (LlamaIndex, RedisChatStore) are imported lazily so the
module loads in unit-test environments where only stubs are present.
"""
from __future__ import annotations

import logging
import uuid
from dataclasses import dataclass, field
from typing import Any, Callable, Protocol

from app.core.config import settings
from app.services.retrieve_service import (
    DEFAULT_SCORE_THRESHOLD,
    DEFAULT_TOP_K,
    RetrievedSource,
    RetrieveService,
    _node_to_source,
    _backfill_parent_for_child,
    _backfill_parents_for_summary,
    _return_parent_and_dedup,
)

logger = logging.getLogger(__name__)


# ── Response shape (mirrors proto QueryResponse) ─────────────────────────────


@dataclass
class QAResult:
    """Synchronous RAG QA response (SPEC §5.1 / proto QueryResponse).

    Fields:
        answer: The LLM-generated answer (or a no-result message when the
            retrieve path returns no sources above ``score_threshold``).
        sources: The retrieved chunks (with parent backfill) used to ground
            the answer. Empty when no sources pass the threshold.
        session_id: The chat session id. A new UUID is generated when the
            caller does not supply one (multi-turn memory is keyed by it).
        input_tokens: Best-effort input token count from the LLM usage.
        output_tokens: Best-effort output token count from the LLM usage.
    """

    answer: str
    sources: list[RetrievedSource] = field(default_factory=list)
    session_id: str = ""
    input_tokens: int = 0
    output_tokens: int = 0


# ── No-result answer (SPEC §5.4: do not hallucinate) ──────────────────────────

NO_RESULT_ANSWER = "未检索到与问题相关的内容，无法回答。"


# ── LLM factory ───────────────────────────────────────────────────────────────


class _LLM(Protocol):
    """Minimal LLM surface used by :class:`QAService` (LlamaIndex-compatible)."""

    def chat(self, messages: Any, **kwargs: Any) -> Any: ...


def _default_llm_factory() -> _LLM:
    """Build the default LlamaIndex :class:`OpenAILike` LLM from settings.

    Per SPEC §5.1 qa_service the LLM is constructed as::

        OpenAILike(model=..., api_base=vllm_url, api_key="...",
                   is_chat_model=True, context_window=...)

    Reads ``vllm_model`` / ``vllm_api_base`` / ``vllm_api_key`` /
    ``vllm_context_window`` from :mod:`app.core.config`. When the vLLM settings
    are absent the factory raises ``RuntimeError`` — callers (gRPC server)
    must inject an ``llm`` in environments without a live vLLM endpoint.
    """
    model = settings.vllm_model
    api_base = settings.vllm_api_base
    api_key = settings.vllm_api_key or "EMPTY"
    if not model or not api_base:
        raise RuntimeError(
            "vLLM model/api_base not configured; set settings.vllm_model and "
            "settings.vllm_api_base (or inject an llm into QAService)"
        )
    from llama_index.llms.openai_like import OpenAILike

    return OpenAILike(
        model=model,
        api_base=api_base,
        api_key=api_key,
        is_chat_model=True,
        context_window=settings.vllm_context_window,
        max_tokens=2048,
        timeout=120.0,
    )


# ── Chat store factory ───────────────────────────────────────────────────────


class _ChatStore(Protocol):
    """Minimal chat-store surface (LlamaIndex RedisChatStore-compatible)."""


def _default_chat_store_factory() -> _ChatStore:
    """Build the default :class:`RedisChatStore` from settings.

    Uses ``settings.redis_url`` (SPEC §5.1 qa_service
    ``ChatMemoryBuffer(chat_store=RedisChatStore)``). The RedisChatStore
    constructor accepts ``redis_url`` (or ``host``/``port``/...) in
    ``llama-index-storage-chat-store-redis``.
    """
    from llama_index.storage.chat_store.redis import RedisChatStore

    return RedisChatStore(redis_url=settings.redis_url)


# ── Token usage extraction ───────────────────────────────────────────────────


def _extract_tokens(response: Any) -> tuple[int, int]:
    """Best-effort input/output token extraction from an LLM chat response.

    LlamaIndex's ``AgentChatResponse`` carries ``raw`` (the underlying
    ``ChatResponse``), whose ``.raw`` is the OpenAI-style usage dict in
    ``OpenAILike``. The shape varies by backend, so we probe a few common
    keys defensively and fall back to (0, 0).
    """
    raw = getattr(response, "raw", None)
    if raw is None:
        return 0, 0
    inner = getattr(raw, "raw", None)
    usage = None
    if isinstance(inner, dict) and "usage" in inner:
        usage = inner["usage"]
    elif isinstance(raw, dict) and "usage" in raw:
        usage = raw["usage"]
    elif hasattr(raw, "usage") and raw.usage is not None:
        usage = raw.usage
    if usage is None:
        return 0, 0
    if isinstance(usage, dict):
        return int(usage.get("prompt_tokens", 0) or 0), int(
            usage.get("completion_tokens", 0) or 0
        )
    return int(getattr(usage, "prompt_tokens", 0) or 0), int(
        getattr(usage, "completion_tokens", 0) or 0
    )


def _usage_from_chat_response(resp: Any) -> tuple[int, int] | None:
    """Return (input, output) tokens from a LlamaIndex ``ChatResponse``.

    ``ChatResponse.raw`` for ``OpenAILike``/vLLM is the OpenAI-style dict that
    includes ``usage`` (``prompt_tokens`` / ``completion_tokens``), carrying
    the real usage that ``AgentChatResponse`` (returned by
    ``ContextChatEngine.chat``) discards. Returns None when not present.
    """
    raw = getattr(resp, "raw", None)
    if raw is None:
        return None
    usage = None
    if isinstance(raw, dict) and "usage" in raw:
        usage = raw["usage"]
    elif hasattr(raw, "usage") and getattr(raw, "usage", None) is not None:
        usage = raw.usage
    if usage is None:
        return None
    if isinstance(usage, dict):
        return int(usage.get("prompt_tokens", 0) or 0), int(
            usage.get("completion_tokens", 0) or 0
        )
    return int(getattr(usage, "prompt_tokens", 0) or 0), int(
        getattr(usage, "completion_tokens", 0) or 0
    )


class _UsageCapturingHandler:
    """Captures the last LLM ``ChatResponse.raw.usage`` via a callback.

    ``ContextChatEngine.chat()`` collapses the LLM response into an
    ``AgentChatResponse`` that carries no usage, so ``_extract_tokens`` on its
    return value is always (0, 0). Instead we attach a callback that observes
    the underlying LLM ``chat`` completion event (``CBEventType.LLM`` with
    ``EventPayload.RESPONSE`` = the ``ChatResponse``) and stores its usage.

    ``CallbackManager`` calls the full handler lifecycle on every registered
    handler (``start_trace`` → ``on_event_start`` → ``on_event_end`` →
    ``end_trace``), so we must implement the whole no-op surface even though
    we only care about ``on_event_end``. We deliberately keep it a plain
    object (not ``BaseCallbackHandler``) so the module still imports in
    unit-test environments without LlamaIndex — llama symbols are imported
    lazily inside ``on_event_end``.
    """

    def __init__(self, event_starts_to_ignore=None, event_ends_to_ignore=None):
        # CallbackManager reads these attributes directly (not via methods) to
        # decide whether to skip events, so they must exist on the handler.
        self.event_starts_to_ignore = set(event_starts_to_ignore or [])
        self.event_ends_to_ignore = set(event_ends_to_ignore or [])
        self._usage: tuple[int, int] | None = None

    # ── No-op lifecycle hooks required by CallbackManager ────────────────────
    def start_trace(self, trace_id=None) -> None:
        pass

    def end_trace(self, trace_id=None, trace_map=None) -> None:
        pass

    def on_event_start(self, event_type, payload=None, event_id="", parent_id="", **kwargs) -> str:
        return event_id

    def on_event_end(self, event_type, payload=None, event_id="", **kwargs):
        try:
            if self._usage is not None:
                return
            from llama_index.core.callbacks.base import CBEventType
            from llama_index.core.callbacks.schema import EventPayload

            if event_type == CBEventType.LLM and EventPayload.RESPONSE in (payload or {}):
                self._usage = _usage_from_chat_response(
                    payload[EventPayload.RESPONSE]
                )
        except Exception:  # noqa: BLE001 — best-effort token capture
            pass

    @property
    def usage(self) -> tuple[int, int] | None:
        return self._usage


def _extract_answer(response: Any) -> str:
    """Extract the answer text from a LlamaIndex chat response.

    ``AgentChatResponse.response`` holds the generated string; some adapters
    expose ``.message.content`` instead. We probe both, then stringify.
    """
    for attr in ("response", "answer"):
        val = getattr(response, attr, None)
        if isinstance(val, str) and val:
            return val
    msg = getattr(response, "message", None)
    if msg is not None:
        content = getattr(msg, "content", None)
        if isinstance(content, str) and content:
            return content
    return str(response) if response is not None else ""


# ── QAService ────────────────────────────────────────────────────────────────


class QAService:
    """Synchronous RAG QA orchestrator (SPEC §5.1 / PRD US-014).

    Wires :class:`RetrieveService` (hybrid retrieval + RRF + parent backfill)
    with a :class:`ContextChatEngine` (Redis-backed multi-turn memory + vLLM
    via :class:`OpenAILike`).

    Args:
        retrieve_service: The hybrid retrieval service. When ``None`` a
            default is constructed (vector + optional keyword retrievers).
        llm: LLM client (LlamaIndex ``OpenAILike``-compatible). When ``None``
            the default factory is invoked lazily on first ``chat`` call so
            the service can be constructed without a live vLLM endpoint (tests
            inject a mock ``llm``).
        chat_store: Redis chat store for multi-turn memory. When ``None`` the
            default factory is invoked lazily.
        llm_factory: Override the default LLM factory (used by tests).
        chat_store_factory: Override the default chat-store factory (tests).
    """

    def __init__(
        self,
        *,
        retrieve_service: RetrieveService | None = None,
        llm: _LLM | None = None,
        chat_store: _ChatStore | None = None,
        llm_factory: Callable[[], _LLM] = _default_llm_factory,
        chat_store_factory: Callable[[], _ChatStore] = _default_chat_store_factory,
    ) -> None:
        self._retrieve_service = retrieve_service or RetrieveService()
        self._llm = llm
        self._chat_store = chat_store
        self._llm_factory = llm_factory
        self._chat_store_factory = chat_store_factory
        self._usage_handler: _UsageCapturingHandler | None = None

    # ── ContextChatEngine factory ─────────────────────────────────────────────

    def _build_engine(self, *, fusion_retriever: Any, session_id: str):
        """Construct the :class:`ContextChatEngine` (SPEC §5.1 qa_service).

        ``ContextChatEngine.from_defaults(
            retriever=fusion_retriever,
            memory=ChatMemoryBuffer(chat_store=RedisChatStore, session_id=...),
            llm=OpenAILike(model=..., api_base=vllm_url, api_key="...",
                           is_chat_model=True, context_window=...))``.
        """
        from llama_index.core.chat_engine import ContextChatEngine
        from llama_index.core.memory import ChatMemoryBuffer
        from llama_index.core.settings import Settings

        llm = self._llm
        if llm is None:
            llm = self._llm = self._llm_factory()

        # Ensure Settings.llm matches our configured LLM so PromptHelper picks
        # up the correct context_window and num_output. Without this,
        # CompactAndRefine falls back to Settings.prompt_helper which uses
        # PromptHelper() defaults (context_window=3900, num_output=256),
        # causing "Chunk size -X is not positive" when retrieved sources are
        # large enough to fill the 3900-token window.
        #
        # NB: assigning Settings.llm alone does NOT rebuild Settings.prompt_helper
        # (it stays the 3900-window default), so we must explicitly derive one
        # from the LLM's real metadata. PromptHelper.from_llm_metadata uses
        # llm.metadata.context_window (= 32768) + num_output, so compact_and_refine
        # repack can size chunks correctly instead of going negative and raising
        # "Chunk size -X is not positive".
        Settings.llm = llm
        from llama_index.core.indices.prompt_helper import PromptHelper

        Settings.prompt_helper = PromptHelper.from_llm_metadata(llm.metadata)

        chat_store = self._chat_store
        if chat_store is None:
            chat_store = self._chat_store = self._chat_store_factory()

        memory = ChatMemoryBuffer(
            chat_store=chat_store,
            chat_store_key=session_id,  # per-session Redis key (not the shared default)
            token_limit=settings.vllm_context_window,  # LlamaIndex 0.14.x requires token_limit > 0
        )

        # Attach a usage-capturing callback so we can report real input/output
        # tokens. NB: ContextChatEngine.from_defaults IGNORES a locally-created
        # callback_manager kwarg and hardcodes ``callback_manager =
        # Settings.callback_manager`` — so we must install the handler on the
        # global Settings (same pattern as ``Settings.llm`` above) for it to
        # ever observe the LLM completion event. ContextChatEngine.chat() drops
        # the LLM usage, so the CBEventType.LLM end → ChatResponse.raw.usage
        # callback is the only reliable hook.
        if self._usage_handler is None:
            self._usage_handler = _UsageCapturingHandler()
        from llama_index.core.callbacks.base import CallbackManager

        current_handlers = list(getattr(Settings.callback_manager, "handlers", None) or [])
        if self._usage_handler not in current_handlers:
            Settings.callback_manager = CallbackManager(
                handlers=current_handlers + [self._usage_handler]
            )

        engine = ContextChatEngine.from_defaults(
            retriever=fusion_retriever,
            memory=memory,
            llm=llm,
        )
        return engine

    # ── chat ──────────────────────────────────────────────────────────────────

    def chat(
        self,
        *,
        kb_id: str,
        question: str,
        session_id: str | None = None,
        top_k: int = DEFAULT_TOP_K,
        score_threshold: float = DEFAULT_SCORE_THRESHOLD,
        retrieval_mode: str = "hybrid",
        dim: int | None = None,
        tenant_id: str = "",
        inference_service_name: str = "default",
    ) -> QAResult:
        """Run synchronous RAG QA (SPEC §5.1 / PRD US-014 AC5).

        1. Retrieve (hybrid + RRF + parent backfill) via ``RetrieveService``.
        2. When no sources pass ``score_threshold`` return a no-result answer
           with empty sources (SPEC §5.4 — do not hallucinate).
        3. Otherwise build the :class:`ContextChatEngine` with the fusion
           retriever + Redis-backed memory + vLLM LLM and call ``engine.chat``.
        4. Return ``answer + sources + session_id + tokens``.

        A new ``session_id`` is generated (UUID4) when the caller does not
        supply one, so multi-turn memory is always keyed consistently.

        Args:
            tenant_id: RLS tenant context for pg_trgm keyword retrieval and
                parent backfill queries (proto QueryRequest.tenant_id). When
                non-empty, passed to ``RetrieveService.build_fusion_retriever``
                so the keyword_search_fn uses the correct RLS context. When
                empty, falls back to the tenant_id bound at
                ``make_pg_trgm_search_fn`` construction time.
            inference_service_name: Which vLLM instance to use (proto
                QueryRequest.inference_service_name). ``"default"`` uses the
                global vLLM settings. Reserved for future per-request LLM
                routing (currently all requests use the same LLM singleton).
        """
        sid = session_id or str(uuid.uuid4())

        # Ensure the LLM is built so we can pass it to the retrieve_service
        # (QueryFusionRetriever pre-resolves a default LLM even with
        # num_queries=1; injecting ours avoids a default-OpenAI lookup).
        llm = self._llm
        if llm is None:
            llm = self._llm = self._llm_factory()
        # Inject the LLM into the retrieve_service if it doesn't have one.
        if not self._retrieve_service.has_llm:
            self._retrieve_service.set_llm(llm)

        # Build the retriever for the KB's retrieval_mode (SPEC §5.1):
        #   hybrid  → QueryFusionRetriever (vector + pg_trgm + RRF)
        #   vector  → Milvus cosine vector-only retriever
        #   keyword → pg_trgm keyword-only retriever
        # The ContextChatEngine will run it internally during engine.chat().
        # We run a pre-check here to apply score_threshold BEFORE the LLM call
        # (avoids wasting LLM compute on low-quality context, SPEC §5.4).
        # The retriever's sync .retrieve() uses _run_async with nest_asyncio,
        # which is safe to call from the main loop; engine.chat() will call
        # .retrieve() again internally, but nest_asyncio handles the nested
        # loop.
        retrieval_mode = retrieval_mode or "hybrid"
        retriever = None
        fusion_retriever = None
        if retrieval_mode == "keyword":
            retriever = self._retrieve_service.build_keyword_retriever(
                kb_id=kb_id, top_k=top_k, tenant_id=tenant_id
            )
        elif retrieval_mode == "vector":
            retriever = self._retrieve_service.build_vector_retriever(
                kb_id=kb_id, top_k=top_k, dim=dim
            )
        else:  # hybrid (default)
            fusion_retriever = self._retrieve_service.build_fusion_retriever(
                kb_id=kb_id, top_k=top_k, dim=dim, tenant_id=tenant_id
            )
            retriever = fusion_retriever

        # Pre-check: run retrieval to check scores before calling the LLM.
        pre_nodes = retriever.retrieve(question)
        if not pre_nodes:
            # No chunks retrieved at all — skip LLM call, return no-result.
            logger.debug(
                "qa_service: no chunks retrieved → no-result (LLM skipped)"
            )
            return QAResult(
                answer=NO_RESULT_ANSWER,
                sources=[],
                session_id=sid,
                input_tokens=0,
                output_tokens=0,
            )

        # Evaluate the no-hallucination gate on a score comparable to
        # ``score_threshold`` (SPEC §5.4). RRF fusion scores are rank-based
        # (~0.016) and are NOT comparable to a cosine-similarity threshold, so
        # the hybrid path uses the best single-vector similarity instead. For
        # the vector / keyword paths the per-node scores are already
        # comparable similarities (cosine / pg_trgm), so we use them directly.
        # For hybrid we also fetch each fused hit's real vector similarity and
        # use it as the returned 0~1 score (norm_fallback below), so the
        # reported score is comparable to vector/keyword modes instead of the
        # tiny RRF rank value (~0.016).
        sim_map: dict[str, float] = {}
        rrf_peak = 0.0
        if retrieval_mode == "hybrid":
            sim_map = self._retrieve_service.vector_similarity_map(
                kb_id=kb_id, question=question, top_k=top_k, dim=dim
            )
            max_score = max(sim_map.values(), default=0.0)
            # Peak RRF score among fused hits, for min-max normalizing chunks
            # that matched only via keyword (absent from the vector map).
            rrf_peak = max(
                (float(getattr(nws, "score", 0.0) or 0.0) for nws in pre_nodes),
                default=0.0,
            )
        else:
            max_score = max(
                (float(getattr(nws, "score", 0.0) or 0.0) for nws in pre_nodes),
                default=0.0,
            )

        # Threshold is applied uniformly across retrieval modes. All modes now
        # report a 0~1 similarity-comparable score:
        #   - vector uses Milvus cosine similarity (0~1).
        #   - keyword uses token coverage normalization (hits/total tokens,
        #     0~1) so its score is comparable to the configured
        #     score_threshold.
        #   - hybrid uses the best single-vector cosine similarity.
        # So the KB's score_threshold applies to every mode (SPEC §5.4).
        if max_score < score_threshold:
            logger.debug(
                "qa_service: pre-check max_score %.3f < threshold %.3f → no-result (LLM skipped)",
                max_score, score_threshold,
            )
            return QAResult(
                answer=NO_RESULT_ANSWER,
                sources=[],
                session_id=sid,
                input_tokens=0,
                output_tokens=0,
            )

        engine = self._build_engine(
            fusion_retriever=retriever,
            session_id=sid,
        )
        response = engine.chat(question)
        answer = _extract_answer(response)
        if self._usage_handler is not None:
            used = self._usage_handler.usage
            input_tokens, output_tokens = used if used is not None else (0, 0)
        else:
            input_tokens, output_tokens = _extract_tokens(response)

        # Extract sources from the engine's response (source_nodes).
        source_nodes = getattr(response, "source_nodes", None) or pre_nodes or []

        sources: list[RetrievedSource] = []
        parent_lookup = self._retrieve_service.parent_lookup
        for nws in source_nodes:
            try:
                src = _node_to_source(nws)
                # Apply parent backfill (same as retrieve_service.retrieve).
                if src.chunk_type == "child":
                    _backfill_parent_for_child(src, nws.node, parent_lookup)
                elif src.chunk_type == "doc_summary":
                    _backfill_parents_for_summary(src, parent_lookup)
                # Hybrid: replace tiny RRF rank score (~0.016) with a readable
                # 0~1 similarity. Chunks found by vector search get their real
                # cosine similarity; keyword-only hits get their RRF score
                # min-max normalized against the fused peak that round.
                if retrieval_mode == "hybrid" and src.chunk_id in sim_map:
                    src.score = sim_map[src.chunk_id]
                elif retrieval_mode == "hybrid" and rrf_peak > 0 and src.score > 0:
                    src.score = max(0.0, min(1.0, src.score / rrf_peak))
                sources.append(src)
            except Exception:
                logger.exception("Failed to process source node, skipping")

        # Return parent blocks and collapse children of the same parent
        # (same behaviour as retrieve_service, SPEC §5.1).
        sources = _return_parent_and_dedup(sources)

        if not sources:
            # No sources retrieved — return no-result answer (SPEC §5.4).
            return QAResult(
                answer=NO_RESULT_ANSWER,
                sources=[],
                session_id=sid,
                input_tokens=input_tokens,
                output_tokens=output_tokens,
            )

        return QAResult(
            answer=answer,
            sources=sources,
            session_id=sid,
            input_tokens=input_tokens,
            output_tokens=output_tokens,
        )


def build_production_qa_service(llm: _LLM | None = None) -> QAService:
    """Construct the production :class:`QAService` with a wired pg_trgm
    keyword search.

    The production service is a long-lived singleton shared across KBs, so it
    cannot bind a single ``kb_id``/``tenant_id`` at construction time. Instead
    it injects a DSN-based ``keyword_search_fn`` whose per-request ``kb_id`` /
    ``tenant_id`` come from the retriever built for each query (see
    ``RetrieveService.build_keyword_retriever`` / ``build_fusion_retriever``).
    This makes ``retrieval_mode=keyword`` and the keyword leg of ``hybrid``
    work in production (SPEC §5.1 全文检索).
    """
    from app.services.retrieve_service import make_pg_trgm_search_fn, RetrieveService

    keyword_search_fn = make_pg_trgm_search_fn(
        settings.pg_dsn,  # DSN (not pool) — binds to the current loop safely
        kb_id="",         # per-request KB passed by each retriever
        tenant_id="",     # per-request tenant passed by each retriever
    )
    retrieve_service = RetrieveService(keyword_search_fn=keyword_search_fn)
    return QAService(retrieve_service=retrieve_service, llm=llm)


if __name__ == "__main__":  # pragma: no cover
    raise SystemExit("qa_service is a library module; import from grpc_server")
