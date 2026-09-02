"""kb-service orchestration-layer abstract interface contracts (issue-027).

This module declares the product-capability Protocols / ABCs that the
orchestration layer (app/services/) and its collaborators (CoreClient,
RagEngineGRPCClient) must satisfy. Only signatures are defined here —
concrete implementations live in the feature issues
(issue-030 infra, issue-031 retrieve, issue-032 parse, issue-035 query).

References:
- Plan §3.5 (app/services directory)
- Plan step 4 (RetrieveService)
- Plan step 5 (ParseOrchestrator)
- Plan step 8A (QueryOrchestrator)
- Plan §3.3 (CoreClient)
- Plan §3.4 (RagEngineGRPCClient)
- CLAUDE.md §4.1 (API contract first, then implementation)
"""
from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, AsyncIterator, Protocol, runtime_checkable


# ── Shared dataclass ────────────────────────────────────────────────────────


@dataclass
class QueryResult:
    """Result of a synchronous Query orchestration (Plan §8A).

    Mirrors the kb_service.proto QueryResponse shape so the gRPC servicer
    can map 1:1 without extra transformation.

    Attributes:
        answer:       LLM-generated answer, or NO_RESULT_ANSWER when any of
                      the three no-result gates trips.
        sources:      Finalized source chunks after dedup + parent backfill.
        session_id:   Session id used for this turn (created or reused).
        input_tokens: Prompt tokens reported by Generate RPC (0 when LLM
                      was not called — gates ① and ②).
        output_tokens: Completion tokens reported by Generate RPC (0 when
                      LLM was not called — gates ① and ②).
    """

    answer: str
    sources: list[dict[str, Any]] = field(default_factory=list)
    session_id: str = ""
    input_tokens: int = 0
    output_tokens: int = 0


# ── RagEngine gRPC client Protocol (Plan §3.4) ──────────────────────────────


@runtime_checkable
class RagEngineClientProtocol(Protocol):
    """Abstract gRPC client surface for rag-engine Parse/Embed/Generate(Stream).

    The concrete RagEngineGRPCClient is implemented in app/rag_engine/client.py.
    """

    async def parse(
        self,
        *,
        download_url: str,
        file_name: str,
        file_type: str,
        chunk_size: int = 1024,
    ) -> list[dict[str, Any]]:
        """Call rag-engine Parse RPC.

        Args:
            download_url: Core API presigned download URL (avoids gRPC 4MB
                          limit; rag-engine downloads the file itself).
            file_name:    Original file name (used for type inference fallback).
            file_type:    pdf | docx | xlsx | pptx | md | txt.
            chunk_size:   Child chunk size override (default 1024).

        Returns:
            List of parsed chunk dicts. Each chunk has keys: chunk_id,
            content, content_type, page_number, parent_content,
            parent_chunk_id, chunk_type, metadata_json, image_bytes
            (optional), image_format (optional).
        """
        ...

    async def embed(
        self, *, texts: list[str]
    ) -> tuple[list[list[float]], int]:
        """Call rag-engine Embed RPC.

        Args:
            texts: Texts to embed.

        Returns:
            (vectors, dimension) tuple. vectors[i] is the embedding of
            texts[i]; dimension is the embedding dimension (e.g. 1024 for
            bge-m3). The RPC returns a flattened 1-D array; the concrete
            client reshapes it to (count, dimension) before returning.
        """
        ...

    async def generate(
        self,
        *,
        question: str,
        session_id: str,
        context: list[dict[str, Any]],
        history: list[dict[str, str]],
        inference_service_name: str = "",
        max_tokens: int = 2048,
    ) -> dict[str, Any]:
        """Call rag-engine Generate RPC.

        Args:
            question:               User question; appended as the final USER
                                    message after history (reproduces legacy
                                    {query_str} template behavior — the current
                                    turn user appears twice).
            session_id:             Session id (pass-through for token accounting).
            context:                Retrieval sources used to build the RAG
                                    system prompt (DEFAULT_CONTEXT_TEMPLATE).
            history:                Chat history INCLUDING the current-turn
                                    user message (reproduces legacy behavior
                                    where kb-service appends user to Redis
                                    before calling rag-engine).
            inference_service_name: vLLM service name; "default" / "" uses the
                                    default model.
            max_tokens:             Max completion tokens.

        Returns:
            Dict with keys: answer (str), input_tokens (int),
            output_tokens (int), session_id (str).
        """
        ...

    async def generate_stream(
        self,
        *,
        question: str,
        session_id: str,
        context: list[dict[str, Any]],
        history: list[dict[str, str]],
        inference_service_name: str = "",
        max_tokens: int = 2048,
    ) -> AsyncIterator[dict[str, Any]]:
        """Call rag-engine GenerateStream RPC; async-iterate tokens.

        Declared `async def` to match the concrete implementation pattern
        (async generator: `async def` + `yield`), Plan §3.4, and the sibling
        parse/embed/generate methods. An async generator function is NOT a
        coroutine function, so callers still iterate with
        `async for tok in client.generate_stream(...)` — no preceding
        `await` is needed.

        Yields dict items with keys: content (str), done (bool),
        input_tokens (int), output_tokens (int). The final yielded item has
        done=True and carries the total token usage (vLLM returns usage in
        the last chunk when stream_options.include_usage=True).
        """
        ...


# ── Core API client Protocol (Plan §3.3) ────────────────────────────────────


@runtime_checkable
class CoreClientProtocol(Protocol):
    """Abstract Core OpenAPI REST client surface used by orchestrators.

    The concrete CoreClient (app/core_api/client.py) already implements
    `request_download_url` and `delete_vector_store_documents`; the
    additional methods (`insert_vector_documents`, `search_vector_store`,
    `upload_object`) are added in issue-030 (STEP-3 feature). This protocol
    captures the full surface that the orchestrators depend on.
    """

    async def insert_vector_documents(
        self,
        *,
        vector_store_id: str,
        documents: list[dict[str, Any]],
        idempotency_key: str,
    ) -> dict[str, Any]:
        """POST /vector-stores/{id}/documents — insert pre-computed vectors.

        Args:
            vector_store_id: Target vector store id.
            documents:       List of dicts with keys: id (str), content (str),
                             vector (list[float] — pre-computed by rag-engine
                             Embed RPC), metadata (dict[str,str] — must include
                             chunk_id, chunk_type, parent_content,
                             parent_chunk_id, doc_id, file_name, page_number,
                             content_type; matches legacy embed_service.py
                             _build_text_node metadata).
            idempotency_key: Idempotency key for safe retry.

        Returns:
            Core API response dict (insert result / accepted task).
        """
        ...

    async def search_vector_store(
        self,
        *,
        vector_store_id: str,
        vector: list[float],
        top_k: int,
        filter: dict[str, str] | None = None,
    ) -> list[dict[str, Any]]:
        """POST /vector-stores/{id}/search — vector search with pre-computed query vector.

        Args:
            vector_store_id: Target vector store id.
            vector:          Pre-computed query embedding (from rag-engine Embed).
            top_k:           Number of hits to return.
            filter:          Optional metadata filter (key-value pairs), matching
                             Core API VectorStoreSearchRequest.filter (object,
                             additionalProperties: string). Example: {"doc_id": "xxx"}.

        Returns:
            List of hit dicts. Each hit has keys: id (str), score (float,
            cosine similarity), content (str — chunk text, added by Core API
            §1.4 modification), metadata (dict[str,str] — includes chunk_id,
            chunk_type, parent_content, parent_chunk_id, doc_id, file_name,
            page_number, content_type).
        """
        ...

    async def upload_object(
        self,
        *,
        bucket_id: str,
        key: str,
        content_bytes: bytes,
        content_type: str,
        idempotency_key: str,
    ) -> str:
        """Two-step upload to Core object storage.

        Step 1: POST /objects/upload → get presigned PUT URL + object_id.
        Step 2: PUT {upload_url} body=content_bytes → upload to object store.

        Args:
            bucket_id:      Target bucket UUID (resolve the "kb-docs" name to
                            an id via get_bucket_id_by_name first; do NOT pass
                            the name directly — Core API keys buckets by UUID).
            key:            Object key (e.g. "{kb_id}/{doc_id}/{chunk_id}").
                            Named `key` to match CoreClient.request_upload_url.
            content_bytes:  Raw bytes to upload (image bytes extracted from
                            documents during Parse orchestration).
            content_type:   MIME type (e.g. "image/png").
            idempotency_key: Idempotency key for safe retry. Required because
                            the underlying request_upload_url mandates it;
                            callers should scope it (e.g.
                            f"parse-image-{doc_id}-{chunk_id}") so retries of
                            the same image upload reuse the same key.

        Returns:
            object_id of the uploaded object (used to replace the placeholder
            in chunk content).
        """
        ...

    async def request_download_url(
        self, *, object_id: str, expires_seconds: int = 3600
    ) -> dict[str, Any]:
        """GET /objects/{id}/download — request a presigned download URL.

        Already implemented on CoreClient; reused by ParseOrchestrator to
        obtain the download_url passed to rag-engine Parse RPC.

        Returns:
            Dict with at least a `download_url` key.
        """
        ...

    async def delete_vector_store_documents(
        self, *, vector_store_id: str, filter_expr: str
    ) -> dict[str, Any]:
        """DELETE /vector-stores/{id}/documents?filter=... — delete vectors by filter.

        Already implemented on CoreClient; reused to clean up vectors when a
        document is deleted (filter_expr e.g. 'doc_id == "{doc_id}"').

        Returns:
            Core API response dict (delete result summary).
        """
        ...


# ── RetrieveService Protocol (Plan step 4) ──────────────────────────────────


@runtime_checkable
class RetrieveServiceProtocol(Protocol):
    """Abstract hybrid retrieval orchestrator (Plan step 4).

    Orchestrates: rag-engine.Embed (query) → Core API vector search →
    PG keyword search → RRF fusion → parent backfill → dedup.
    Result is equivalent to the legacy rag-engine RetrieveService output.
    """

    async def retrieve(
        self,
        *,
        tenant_id: str,
        kb_id: str,
        question: str,
        top_k: int = 5,
        score_threshold: float = 0.3,
        retrieval_mode: str = "hybrid",
        vector_store_id: str | None = None,
    ) -> tuple[list[dict[str, Any]], float]:
        """Run hybrid retrieval and return (sources, max_score).

        Args:
            tenant_id:       Tenant id (RLS context).
            kb_id:           Knowledge base id.
            question:        User question (embedded for vector search).
            top_k:           Number of sources to return after fusion+dedup.
            score_threshold: Minimum score; gate ② in QueryOrchestrator
                             compares max_score against this value.
            retrieval_mode:  hybrid | vector | keyword.
            vector_store_id: Core vector store id; None falls back to
                             kb_metadata.vector_store_id.

        Returns:
            (sources, max_score) tuple.
            sources: finalized list of dicts (post dedup + parent backfill),
                     each with keys: chunk_id, doc_id, file_name, page,
                     content, parent_content, parent_chunk_id, chunk_type,
                     score.
            max_score: for hybrid mode = max(vector cosine scores); for
                       vector mode = max(cosine); for keyword mode = max
                       (token coverage). Used by QueryOrchestrator gate ②.
        """
        ...


# ── ParseOrchestrator Protocol (Plan step 5) ────────────────────────────────


@runtime_checkable
class ParseOrchestratorProtocol(Protocol):
    """Abstract document parse pipeline orchestrator (Plan step 5).

    Orchestrates: get download_url → rag-engine.Parse RPC → image upload →
    rag-engine.Embed RPC → Core vector insert → PG kb_chunks write →
    best-effort summary via Generate RPC → status update.
    """

    async def process_document(
        self,
        *,
        tenant_id: str,
        kb_id: str,
        doc_id: str,
        object_id: str,
        file_name: str,
        file_type: str,
        chunk_size: int,
        vector_store_id: str,
    ) -> None:
        """Process a single document end-to-end through the parse pipeline.

        State machine: pending → parsing → indexing → ready | failed.

        Args:
            tenant_id:       Tenant id (RLS context).
            kb_id:           Knowledge base id.
            doc_id:          Document id (pre-reserved UUID).
            object_id:       Core object-store object id of the uploaded file.
            file_name:       Original file name.
            file_type:       pdf | docx | xlsx | pptx | md | txt.
            chunk_size:      Child chunk size.
            vector_store_id: Target Core vector store id for this KB.

        Returns:
            None. The document's parse_status is updated to `ready` on
            success or `failed` (with sanitized error message) on exception.
        """
        ...


# ── QueryOrchestrator Protocol (Plan step 8A) ──────────────────────────────


@runtime_checkable
class QueryOrchestratorProtocol(Protocol):
    """Abstract synchronous Query orchestrator (Plan step 8A).

    Orchestrates: RetrieveService.retrieve → three no-result gates →
    rag-engine.Generate RPC → return QueryResult.
    """

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

        Args:
            tenant_id:              Tenant id (RLS context).
            kb_id:                  Knowledge base id.
            question:               User question.
            session_id:             Session id (empty creates a new one in the
                                    caller; this method receives the resolved
                                    id).
            top_k:                  Override KB default if nonzero.
            score_threshold:        Override KB default if nonzero.
            retrieval_mode:         hybrid | vector | keyword.
            inference_service_name: vLLM service name; "default" if empty.
            vector_store_id:        Core vector store id for this KB.
            history:                Chat history INCLUDING the current-turn
                                    user message (reproduces legacy behavior:
                                    kb-service appends user to Redis before
                                    calling rag-engine). Generate RPC appends
                                    `question` as the final USER message,
                                    so the current-turn user appears twice —
                                    this matches the legacy {query_str} template
                                    behavior and is intentional.

        Returns:
            QueryResult dataclass (answer, sources, session_id, input_tokens,
            output_tokens).
        """
        ...

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

        Same gate logic as ``query()`` but yields typed stream events
        (StreamTokenEvent, StreamSourcesEvent, StreamDoneEvent,
        StreamNoResultEvent) so the Retrieve gRPC handler can map them
        directly to ``RetrieveEvent`` proto messages without duplicating
        the three-gate logic.
        """
        ...
