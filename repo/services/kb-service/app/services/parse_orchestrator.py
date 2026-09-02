"""kb-service document parse pipeline orchestrator (Plan step 5, issue-032).

Orchestrates the parse pipeline end-to-end, equivalent to the legacy
rag-engine ``parse_worker`` (Plan §0.1 Parse pipeline equivalence):

    pending → parsing → rag-engine.Parse RPC → image upload (Core API)
            → indexing → best-effort summary (Generate RPC) →
            rag-engine.Embed RPC → Core vector insert →
            PG kb_chunks write → ready | failed

Key equivalence guarantees (Plan §0.1, §2.1):
  - Input same document → kb_chunks row count and content identical
  - kb-service does NOT download document bytes; it obtains a
    ``download_url`` from the Core API and passes it to rag-engine.Parse RPC
    (Plan §2.1 — "文档下载" row)
  - Images are uploaded to the Core API (not MinIO directly); the
    markdown link [图片: 图片](object_id) is embedded in parent content
    (mirrors legacy parse_service._build_image_placeholder, Plan §2.1
    — "MinIO 图片" row)
  - Embed RPC embeds child chunks + summary separately; summary is NOT
    added to ``child_chunks`` to avoid double-write in ``write_chunks``
    (Plan §2.2 — "Embedding 嵌入策略")
  - Core API inserts pre-computed vectors (Plan §2.2); Core does no
    embedding inference (CLAUDE.md §3.1)
  - ``write_chunks`` receives parents + children + summaries as separate
    lists (Plan §2.1 — "PG kb_chunks 写入" row)
  - State machine: pending → parsing → indexing → ready | failed
    (Plan §0.1 — "Parse 状态机" row); on exception → failed with
    sanitized error message (Plan §0.1, rag-engine parse_worker
    ``_sanitize_error``)
  - Summary generation is best-effort: takes the first 3 parent blocks,
    calls Generate RPC with the same prompt template as legacy
    SummaryService (English prompt that instructs the LLM to match the
    content's language); failure returns None and does not block
    (Plan §0.1 — "Parse 摘要" row, rag-engine ``SummaryService``)

This module satisfies ``ParseOrchestratorProtocol`` (contracts.py).
"""
from __future__ import annotations

import logging
import re
import uuid
from typing import Any, Callable, Protocol, runtime_checkable

from app.repositories import chunk as chunk_repo
from app.repositories import document as doc_repo

logger = logging.getLogger(__name__)

# ── Parse status constants (mirror rag-engine parse_worker, SPEC §5.3) ───────
STATUS_PARSING = "parsing"
STATUS_INDEXING = "indexing"
STATUS_READY = "ready"
STATUS_FAILED = "failed"

# Number of leading parent blocks concatenated as summary LLM input
# (rag-engine SummaryService.DEFAULT_SUMMARY_PARENT_COUNT).
SUMMARY_PARENT_COUNT = 3

# Summary length bounds in characters (PRD US-012 / SPEC §5.1: "200-500 字").
# Matches rag-engine SummaryService.SUMMARY_MIN_CHARS / SUMMARY_MAX_CHARS.
SUMMARY_MIN_CHARS = 200
SUMMARY_MAX_CHARS = 500

# Summary prompt template — mirrors rag-engine SummaryService._SUMMARY_PROMPT_TEMPLATE
# (summary_service.py lines 53-56). The prompt is in English and instructs the
# LLM to "Use the same language as the content", so the generated summary
# automatically matches the document's language (Chinese, English, etc.).
# This preserves equivalence with the legacy SummaryService behavior.
_SUMMARY_PROMPT_TEMPLATE = (
    "Summarize the following content in {lo}-{hi} characters. "
    "Use the same language as the content.\n\n{content}"
)

# Image markdown link format — mirrors rag-engine parse_service._build_image_placeholder
# (parse_service.py lines 323-327). Format: [图片: caption](url).
# Caption defaults to "图片" when not provided (matching old behavior).
_IMAGE_LINK_TEMPLATE = "[图片: {caption}]({url})"

# Default bucket name convention for KB document images (Plan §3.3).
# The Core object-store keys buckets by UUID; the orchestrator resolves the
# name to an id via CoreClient.get_bucket_id_by_name before uploading.
KB_DOCS_BUCKET_NAME = "kb-docs"

# Patterns that may leak sensitive info (file paths, tokens, presigned URL
# params) from exceptions into the user-visible ``error_message`` column.
# Mirrors rag-engine parse_worker._SENSITIVE_PATTERN (parse_worker.py line 61)
# with additions for Windows paths and AWS-style presigned URL signatures.
_SENSITIVE_PATTERN = re.compile(
    r"(/[\w/.\-]+"                   # POSIX paths
    r"|[A-Za-z]:\\[\w\\.\-]+"         # Windows paths
    r"|Bearer\s+[\w.\-]+"            # Bearer tokens
    r"|token[=:]\s*\S+"              # token= / token: secrets
    r"|X-Amz-\w+=\S+"                # AWS presigned URL signature params
    r"|signature=\S+"                # generic signature params
    r"|expires=\d+)",                # presigned URL expiry
    re.IGNORECASE,
)


def _sanitize_error(msg: str) -> str:
    """Redact file paths and tokens; truncate to 500 chars.

    Mirrors rag-engine parse_worker._sanitize_error (parse_worker.py line 64)
    so the persisted error_message has the same shape and length bound.
    """
    return _SENSITIVE_PATTERN.sub("[redacted]", msg)[:500]


# ── Dependency-injection protocols ───────────────────────────────────────────


@runtime_checkable
class _CoreClientFactory(Protocol):
    """Callable that builds a tenant-scoped CoreClient."""

    def __call__(self, tenant_id: str) -> Any: ...


@runtime_checkable
class _RagEngineClient(Protocol):
    """Subset of RagEngineGRPCClient used by ParseOrchestrator.

    Matches the concrete ``RagEngineGRPCClient`` signatures (parse/embed/
    generate) so the real client satisfies this protocol without adapters.
    """

    async def parse(
        self,
        *,
        download_url: str,
        file_name: str,
        file_type: str,
        chunk_size: int = 1024,
    ) -> list[dict[str, Any]]: ...

    async def embed(self, *, texts: list[str]) -> tuple[list[list[float]], int]: ...

    async def generate(
        self,
        *,
        question: str,
        session_id: str = "",
        context: list[dict[str, Any]] | None = None,
        history: list[dict[str, str]] | None = None,
        inference_service_name: str = "",
        max_tokens: int = 2048,
    ) -> dict[str, Any]: ...


class ParseOrchestrator:
    """Document parse pipeline orchestrator (Plan step 5, issue-032).

    Orchestrates: get download_url → rag-engine.Parse RPC → image upload →
    rag-engine.Embed RPC → Core vector insert → PG kb_chunks write →
    best-effort summary via Generate RPC → status update.

    Args:
        db_pool: asyncpg pool for kb_chunks writes + parse_status updates.
        core_client_factory: Callable ``f(tenant_id) -> CoreClient``. Each
            parse task gets a tenant-scoped CoreClient (X-Tenant-Id header).
        rag_engine_client: ``RagEngineGRPCClient`` (or compatible fake)
            exposing parse/embed/generate.
        kb_docs_bucket_name: Bucket name for image uploads (default
            "kb-docs"). Resolved to a UUID via CoreClient.get_bucket_id_by_name.
    """

    def __init__(
        self,
        *,
        db_pool: Any,
        core_client_factory: _CoreClientFactory | Callable[[str], Any],
        rag_engine_client: _RagEngineClient,
        kb_docs_bucket_name: str = KB_DOCS_BUCKET_NAME,
    ) -> None:
        self._pool = db_pool
        self._core_client_factory = core_client_factory
        self._rag_engine = rag_engine_client
        self._bucket_name = kb_docs_bucket_name

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
        On exception the document is marked ``failed`` with a sanitized
        error_message (mirrors rag-engine parse_worker, Plan §0.1).
        """
        if not tenant_id:
            raise ValueError("tenant_id must not be empty for RLS-scoped write")

        # Idempotency: skip if already ready (SPEC §5.4 at-least-once).
        async with self._pool.acquire() as conn:
            existing = await doc_repo.get_document(
                conn, tenant_id=tenant_id, kb_id=kb_id, doc_id=doc_id,
            )
        if existing and existing.get("parse_status") == STATUS_READY:
            logger.info("parse_orchestrator: doc %s already ready, skipping", doc_id)
            return

        # pending → parsing
        # Check the return value — if the UPDATE didn't match a row, the
        # document may have been deleted; abort the pipeline (mirrors
        # rag-engine parse_worker #r5).
        async with self._pool.acquire() as conn:
            updated = await doc_repo.update_parse_status(
                conn,
                tenant_id=tenant_id,
                doc_id=doc_id,
                parse_status=STATUS_PARSING,
            )
        if not updated:
            logger.warning(
                "parse_orchestrator: doc %s not found in kb_documents "
                "(parse_status update matched 0 rows), skipping", doc_id,
            )
            return

        core = None
        try:
            core = self._core_client_factory(tenant_id)

            # 1. Get download_url from Core API and pass to rag-engine.Parse
            #    (kb-service does NOT download document bytes; Plan §2.1).
            dl_resp = await core.request_download_url(object_id=object_id)
            download_url = dl_resp.get("download_url") or dl_resp.get("downloadUrl")
            if not download_url:
                raise RuntimeError(
                    "Core API request_download_url returned no download_url"
                )

            # 2. rag-engine.Parse RPC (passes download_url; rag-engine downloads).
            chunks = await self._rag_engine.parse(
                download_url=download_url,
                file_name=file_name,
                file_type=file_type,
                chunk_size=chunk_size,
            )

            # 3. Image upload to Core API, embed markdown links in parent content
            #    (Plan §2.1 — "MinIO 图片" row; Plan §2.1 — "图片 Core API 上传").
            #    Mirrors legacy parse_service._build_image_placeholder (lines 323-327):
            #    the markdown link [图片: 图片](object_id) is embedded in parent
            #    content so kb_chunks and vectors carry resolvable image references.
            bucket_id = await core.get_bucket_id_by_name(name=self._bucket_name)
            if not bucket_id:
                raise RuntimeError(
                    f"Core bucket {self._bucket_name!r} not found for tenant"
                )
            image_chunks = [c for c in chunks if c.get("image_bytes")]
            image_links: list[str] = []
            for chunk in image_chunks:
                chunk_cid = chunk["chunk_id"]
                img_ext = chunk.get("image_format", "png") or "png"
                # OSS key path matches legacy ImageUploader.upload() format:
                # {bucket}/{prefix}/images/{uuid}.{ext}
                # (minio_client.py line 49: key = f"{object_prefix}/images/{uuid.uuid4().hex}.{ext}")
                img_key = f"{kb_id}/{doc_id}/images/{uuid.uuid4().hex}.{img_ext}"
                _ = await core.upload_object(
                    bucket_id=bucket_id,
                    key=img_key,
                    content_bytes=chunk["image_bytes"],
                    content_type=f"image/{img_ext}",
                    idempotency_key=f"parse-image-{doc_id}-{chunk_cid}",
                )
                # Build markdown link in the same format as legacy
                # parse_service._build_image_placeholder (lines 323-327):
                # [图片: caption](url), caption defaults to "图片".
                # The URL is the OSS object path "{bucket}/{key}" (matches
                # legacy ImageUploader.upload() return value, minio_client.py
                # line 57: return f"{self._bucket}/{key}").
                oss_url = f"{self._bucket_name}/{img_key}"
                image_links.append(
                    _IMAGE_LINK_TEMPLATE.format(caption="图片", url=oss_url)
                )

            # Separate content chunks from image chunks.
            # Image chunks are NOT written to kb_chunks or vectors — their
            # markdown links are embedded in parent content instead.
            content_chunks = [c for c in chunks if not c.get("image_bytes")]
            parents = [c for c in content_chunks if c.get("chunk_type") == "parent"]
            child_chunks = [c for c in content_chunks if c.get("chunk_type") == "child"]

            # Embed image markdown links into parent content.
            # Legacy parse_service appended image nodes after text nodes
            # (parse_service.py line 381: nodes.extend(image_nodes)), and
            # chunk_service grouped them into parent blocks. The gRPC path
            # returns images as separate chunks without position info, so
            # we append all links to the last parent's content (or first
            # child if no parents), matching the most common old behavior.
            if image_links:
                if parents:
                    parents[-1]["content"] += "\n" + "\n".join(image_links)
                elif child_chunks:
                    child_chunks[0]["content"] += "\n" + "\n".join(image_links)

            # Reparse idempotency: delete prior chunks before re-writing.
            # If the document was previously failed/partially-ingested, old
            # kb_chunks rows and Core vectors remain. Clear them to avoid
            # duplicates (chunk_repo.write_chunks has no ON CONFLICT; Core
            # insert idempotency_key is scoped per-request, not per-doc).
            # Mirrors rag-engine parse_worker which deleted prior chunks +
            # DeleteDocument gRPC handler which deletes Core vectors.
            async with self._pool.acquire() as conn:
                await chunk_repo.delete_chunks_by_doc(
                    conn,
                    tenant_id=tenant_id,
                    kb_id=kb_id,
                    doc_id=doc_id,
                )
            # Best-effort Core vector cleanup (same as DeleteDocument handler).
            try:
                await core.delete_vector_store_documents(
                    vector_store_id=vector_store_id,
                    filter_expr=f'doc_id == "{doc_id}"',
                )
            except Exception:
                logger.debug(
                    "parse_orchestrator: Core vector delete for doc %s "
                    "failed (best-effort, ignoring)", doc_id,
                )

            # parsing → indexing
            async with self._pool.acquire() as conn:
                await doc_repo.update_parse_status(
                    conn,
                    tenant_id=tenant_id,
                    doc_id=doc_id,
                    parse_status=STATUS_INDEXING,
                )

            # 4. Best-effort summary (Generate RPC). Failure → None, no block.
            summary_chunk = await self._generate_summary(parents)

            # 5. Embed child chunks + summary SEPARATELY.
            #    Summary is NOT appended to child_chunks to avoid double-write
            #    in write_chunks (Plan §2.2 — "Embedding 嵌入策略").
            embed_chunks = list(child_chunks)
            if summary_chunk is not None:
                embed_chunks.append(summary_chunk)

            # 6. rag-engine.Embed RPC (embed child chunks + summary).
            texts = [c["content"] for c in embed_chunks]
            vectors: list[list[float]] = []
            if texts:
                vectors, _dim = await self._rag_engine.embed(texts=texts)
                if len(vectors) != len(embed_chunks):
                    raise RuntimeError(
                        f"embed returned {len(vectors)} vectors for "
                        f"{len(embed_chunks)} chunks"
                    )

            # 7. Core API insert vectors (pre-computed; Core does no embedding,
            #    Plan §2.2). Metadata must include all fields needed at query
            #    time (matches legacy embed_service.py _build_text_node metadata):
            #    chunk_id, chunk_type, parent_content, parent_chunk_id,
            #    page_number, content_type, doc_id, file_name.
            if embed_chunks:
                documents = [
                    {
                        "id": c["chunk_id"],
                        "content": c["content"],
                        "vector": v,
                        "metadata": {
                            "doc_id": doc_id,
                            "file_name": file_name,
                            "chunk_id": c["chunk_id"],
                            "chunk_type": c.get("chunk_type", "child"),
                            "page_number": str(c.get("page_number", 0)),
                            "content_type": c.get("content_type", "text"),
                            "parent_content": c.get("parent_content", "") or "",
                            "parent_chunk_id": c.get("parent_chunk_id", "") or "",
                        },
                    }
                    for c, v in zip(embed_chunks, vectors)
                ]
                await core.insert_vector_documents(
                    vector_store_id=vector_store_id,
                    documents=documents,
                    idempotency_key=f"parse-{doc_id}",
                )

            # 8. Write kb_chunks: parents + children + summaries SEPARATELY
            #    (avoid double-write; Plan §2.1 — "PG kb_chunks 写入" row).
            async with self._pool.acquire() as conn:
                chunk_count = await chunk_repo.write_chunks(
                    conn,
                    tenant_id=tenant_id,
                    kb_id=kb_id,
                    doc_id=doc_id,
                    file_name=file_name,
                    parents=parents,
                    children=child_chunks,
                    summaries=[summary_chunk] if summary_chunk is not None else None,
                )

            # indexing → ready
            # chunk_count = total kb_chunks rows (parents + children + summaries),
            # matching legacy parse_worker (parse_worker.py line 499).
            async with self._pool.acquire() as conn:
                await doc_repo.update_parse_status(
                    conn,
                    tenant_id=tenant_id,
                    doc_id=doc_id,
                    parse_status=STATUS_READY,
                    chunk_count=chunk_count,
                )
            logger.info(
                "parse_orchestrator: doc %s ready (parents=%d children=%d summary=%s)",
                doc_id,
                len(parents),
                len(child_chunks),
                "yes" if summary_chunk is not None else "no",
            )
        except Exception as exc:
            logger.exception("parse_orchestrator: doc %s failed", doc_id)
            error_msg = _sanitize_error(str(exc))
            try:
                async with self._pool.acquire() as conn:
                    await doc_repo.update_parse_status(
                        conn,
                        tenant_id=tenant_id,
                        doc_id=doc_id,
                        parse_status=STATUS_FAILED,
                        error_message=error_msg,
                    )
            except Exception:
                logger.exception(
                    "parse_orchestrator: failed to mark doc %s as failed", doc_id
                )
        finally:
            # Release the per-task CoreClient's HTTP connection pool.
            if core is not None and hasattr(core, "aclose"):
                try:
                    await core.aclose()
                except Exception:
                    pass

    async def _generate_summary(self, parents: list[dict[str, Any]]) -> dict[str, Any] | None:
        """Best-effort document summary via rag-engine.Generate RPC.

        Mirrors rag-engine ``SummaryService.summarize`` (summary_service.py):
          1. Take the first ``SUMMARY_PARENT_COUNT`` parent blocks' content
          2. Build prompt using the same template as legacy SummaryService
             (summary_service.py lines 53-56): the English prompt instructs
             the LLM to "Use the same language as the content", so the
             generated summary automatically matches the document's language.
          3. Call Generate RPC (context=[], history=[], question=prompt)
          4. On any failure → return None (best-effort, do not block ingestion)

        Note (Plan §2.2): the legacy SummaryService used LlamaIndex
        ``OpenAILike.complete()`` (completion mode); the new path uses the
        Generate RPC (chat mode). vLLM single-turn chat and completion are
        typically equivalent for the same model + temperature=0. E2E
        verifies semantic equivalence.
        """
        if not parents:
            return None
        try:
            selected = parents[:SUMMARY_PARENT_COUNT]
            # .strip() mirrors legacy SummaryService._concat_parents (line 75)
            # so the prompt content has the same whitespace normalization.
            combined = "\n".join(
                p.get("content", "") for p in selected if p.get("content")
            ).strip()
            if not combined:
                return None
            # Use the same prompt template as legacy SummaryService
            # (summary_service.py lines 53-56) — English prompt that instructs
            # the LLM to match the content's language, achieving language
            # adaptation without explicit language detection.
            prompt = _SUMMARY_PROMPT_TEMPLATE.format(
                lo=SUMMARY_MIN_CHARS, hi=SUMMARY_MAX_CHARS, content=combined,
            )
            result = await self._rag_engine.generate(
                question=prompt,
                session_id="",
                context=[],
                history=[],
                inference_service_name="",
                max_tokens=500,
            )
            summary = (result.get("answer") or "").strip()
            if not summary:
                return None
            # Build a summary chunk dict matching the shape write_chunks
            # expects for summaries (chunk_id, content, content_type,
            # page_number, parent_chunk_id, parent_content, token_count,
            # metadata). write_chunks hard-codes chunk_type="doc_summary"
            # for kb_chunks; we set chunk_type here so Core vector metadata
            # (built later from c.get("chunk_type", "child")) correctly
            # records "doc_summary" — matching legacy embed_service.py
            # DOC_SUMMARY_TYPE (embed_service.py line 47).
            return {
                "chunk_id": str(uuid.uuid4()),
                "content": summary,
                "content_type": "text",
                "chunk_type": "doc_summary",
                "page_number": 1,
                "parent_chunk_id": None,
                "parent_content": None,
                "token_count": max(1, len(summary) // 2),
                "metadata": {},
            }
        except Exception as exc:  # noqa: BLE001 — best-effort, degrade to None
            logger.warning(
                "parse_orchestrator: summary failed: %s (degrading)", exc
            )
            return None
