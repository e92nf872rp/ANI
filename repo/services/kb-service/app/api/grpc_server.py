"""KBService gRPC servicer (SPEC §2.4, §4.1, §6.1).

US-009 wires the repositories + Core API client + rag-engine client into the
10 P0 RPCs. CreateKB and DeleteKB call the Core vector-stores API per
SPEC §6.1. US-010 wires NotifyDocumentUploaded's atomic outbox transaction
(kb_documents + async_tasks + outbox_events) and Query's kb_messages
persistence + Redis session cache.
"""
from __future__ import annotations

import asyncio
import logging
import os
import queue
import threading
import uuid
from datetime import datetime
from typing import Any

import asyncpg
import grpc
from google.protobuf import empty_pb2
from google.protobuf import timestamp_pb2

from app.api import p1_rpcs
import httpx
from app.core_api.client import CoreAPIError, CoreClient
from app.generated.common.v1 import common_pb2
from app.generated.kb.v1 import kb_service_pb2 as kb_pb
from app.generated.kb.v1 import kb_service_pb2_grpc as pb_grpc
from app.rag_engine.client import RagEngineError
from app.repositories import async_task as async_task_repo
from app.repositories import chunk as chunk_repo
from app.repositories import document as document_repo
from app.repositories import knowledge_base as kb_repo
from app.repositories import message as message_repo
from app.core.config import settings
from app.services.contracts import QueryResult

logger = logging.getLogger(__name__)


# ── async bridge for gRPC ThreadPoolExecutor worker threads ──────────────────
# gRPC servicer methods run in a ThreadPoolExecutor worker thread. asyncpg
# connections are bound to the event loop they were created on, so sharing
# a pool created on the uvicorn loop across gRPC threads causes
# "Future attached to a different loop" errors.
#
# Fix: a single dedicated event loop runs on a background thread. The pool,
# Redis client, and all async work live on that loop. gRPC worker threads
# submit coroutines via run_coroutine_threadsafe and block on the result.
_grpc_loop: asyncio.AbstractEventLoop | None = None
_grpc_loop_thread: threading.Thread | None = None


def _start_grpc_loop():
    """Start the dedicated gRPC event loop on a background thread."""
    global _grpc_loop, _grpc_loop_thread
    if _grpc_loop is not None:
        return
    ready = threading.Event()

    def _run_loop():
        global _grpc_loop
        _grpc_loop = asyncio.new_event_loop()
        asyncio.set_event_loop(_grpc_loop)
        ready.set()
        _grpc_loop.run_forever()

    _grpc_loop_thread = threading.Thread(target=_run_loop, daemon=True, name="grpc-async-loop")
    _grpc_loop_thread.start()
    ready.wait(timeout=5)


def _run_async(coro):
    """Submit a coroutine to the dedicated gRPC event loop and block on it.

    The loop is shared across all gRPC worker threads so asyncpg connections
    from the pool always run on the same loop they were created on.
    """
    if _grpc_loop is None:
        _start_grpc_loop()
    future = asyncio.run_coroutine_threadsafe(coro, _grpc_loop)
    return future.result()


def _run_async_bg(coro):
    """Submit a coroutine to the gRPC loop without blocking (fire-and-forget).

    Used by _default_session_cache and _default_core_client which create async
    resources that need to live on the gRPC loop.
    """
    if _grpc_loop is None:
        _start_grpc_loop()
    return asyncio.run_coroutine_threadsafe(coro, _grpc_loop)


def _ts(dt: datetime | None) -> timestamp_pb2.Timestamp:
    """Convert a datetime to a protobuf Timestamp."""
    ts = timestamp_pb2.Timestamp()
    if dt is not None:
        ts.FromDatetime(dt)
    return ts


def _vector_store_name(kb_id: str) -> str:
    """Derive the Core vector-store collection name from a kb_id (SPEC §6.1)."""
    return f"kb_{kb_id.replace('-', '')}"


class KBServiceServicer(pb_grpc.KBServiceServicer):
    """KBService servicer: 10 P0 RPCs wired (US-009) + 3 P1 UNIMPLEMENTED."""

    def __init__(
        self,
        *,
        pool: asyncpg.Pool | None = None,
        core_client_factory: Any | None = None,
        session_cache_factory: Any | None = None,
        retrieve_service_factory: Any | None = None,
        rag_engine_grpc_client_factory: Any | None = None,
    ) -> None:
        # When pool is None the servicer still serves RPCs that don't need DB
        # (used by the skeleton tests in test_grpc_server.py). DB-backed RPCs
        # abort with FAILED_PRECONDITION when pool is unset.
        self._pool = pool
        # core_client_factory(tenant_id) -> CoreClient; injected for testing.
        # In production, constructed from settings in main.py.
        self._core_client_factory = core_client_factory or _default_core_client
        # session_cache_factory() -> SessionCache | None; injected for testing.
        # Returns None when Redis is unavailable (Query degrades to DB-only).
        self._session_cache_factory = session_cache_factory or _default_session_cache
        # retrieve_service_factory(tenant_id) -> RetrieveService; injected for
        # testing. In production, constructed from settings in main.py.
        self._retrieve_service_factory = retrieve_service_factory
        # rag_engine_grpc_client_factory() -> RagEngineGRPCClient; injected for
        # testing. In production, constructed from settings in main.py.
        self._rag_engine_grpc_client_factory = rag_engine_grpc_client_factory
        # Per-tenant orchestrator cache. Each tenant gets its own
        # QueryOrchestrator instance backed by a tenant-scoped
        # RetrieveService (which holds a tenant-scoped CoreClient). This
        # preserves multi-tenant isolation — a single global orchestrator
        # would cross tenant boundaries.
        self._orchestrators: dict[str, Any] = {}

    # ── 10 P0 RPCs ───────────────────────────────────────────────────────────

    def CreateKB(self, request, context):
        """CreateKB: idempotent KB + Core vector-store (SPEC §6.1)."""
        return _run_async(self._create_kb(request, context))

    async def _create_kb(self, request, context) -> kb_pb.KnowledgeBase:
        # 1. validate idempotency_key
        idem = getattr(request, "idempotency_key", "") or ""
        # Note: proto3 CreateKBRequest has no idempotency_key field; it lives
        # on the async_tasks side. We generate a deterministic one from the
        # tenant+name if absent so retries are safe.
        tenant_id = request.tenant_id or ""
        if not tenant_id:
            context.abort(grpc.StatusCode.INVALID_ARGUMENT, "tenant_id is required")
        if not request.name:
            context.abort(grpc.StatusCode.INVALID_ARGUMENT, "name is required")

        # 0. validate KB config ranges (SPEC §6.1 / openapi v1.yaml)
        cs = request.chunk_size or 1024
        if cs < 1 or cs > 8192:
            context.abort(
                grpc.StatusCode.INVALID_ARGUMENT,
                f"chunk_size must be in [1, 8192], got {request.chunk_size}",
            )
        tk = request.top_k or 5
        if tk < 1 or tk > 20:
            context.abort(
                grpc.StatusCode.INVALID_ARGUMENT,
                f"top_k must be in [1, 20], got {request.top_k}",
            )
        st = request.score_threshold
        if st < 0 or st > 1:
            context.abort(
                grpc.StatusCode.INVALID_ARGUMENT,
                f"score_threshold must be in [0, 1], got {request.score_threshold}",
            )

        if self._pool is None:
            context.abort(
                grpc.StatusCode.FAILED_PRECONDITION,
                "DB pool not configured (skeleton mode)",
            )
            return  # unreachable; for type checkers

        idem_key = idem or f"create_kb:{tenant_id}:{request.name}"

        # 2. idempotency replay: return existing result
        async with self._pool.acquire() as conn:
            existing = await async_task_repo.find_by_idempotency_key(
                conn, tenant_id=tenant_id, idempotency_key=idem_key
            )
            if existing and existing.get("result"):
                # result is JSONB; asyncpg may return it as a str or already-
                # parsed dict depending on the codec config. Normalize.
                result = existing["result"]
                if isinstance(result, str):
                    import json
                    result = json.loads(result)
                return _kb_row_to_pb(result)

            # 3. INSERT knowledge_bases
            kb_row = await kb_repo.create_kb(
                conn,
                tenant_id=tenant_id,
                name=request.name,
                description=request.description,
                embedding_model=request.embedding_model or "bge-m3",
                chunk_size=request.chunk_size or 1024,
                top_k=request.top_k or 5,
                # 未显式传入时落库存 0（表示未设置；运行时由 rag-engine 的
                # DEFAULT_SCORE_THRESHOLD 兜底），而不是硬编码 0.3。
                score_threshold=request.score_threshold or 0.0,
                retrieval_mode=request.retrieval_mode or "hybrid",
            )
            kb_id = str(kb_row["id"])

        # 4. Core POST /vector-stores (SPEC §6.1)
        vector_store_id = ""
        try:
            async with self._core_client_factory(tenant_id) as core:
                # dimension: bge-m3 = 1024; fallback to 1024 when unknown.
                dim = 1024
                vs_resp = await core.create_vector_store(
                    name=_vector_store_name(kb_id),
                    dimension=dim,
                    metric="cosine",
                    embedding_model=request.embedding_model or "bge-m3",
                    idempotency_key=idem_key,
                )
                # Persist the Core-returned vector store id (Plan §3.1).
                vector_store_id = str(vs_resp.get("id") or "")
                if vector_store_id:
                    async with self._pool.acquire() as conn:
                        await kb_repo.set_vector_store_id(
                            conn,
                            tenant_id=tenant_id,
                            kb_id=kb_id,
                            vector_store_id=vector_store_id,
                        )
        except CoreAPIError as e:
            # Best-effort cleanup: soft-delete the KB row so retries can
            # re-create. We don't abort here on cleanup failure.
            async with self._pool.acquire() as conn:
                await kb_repo.soft_delete_kb(conn, tenant_id=tenant_id, kb_id=kb_id)
            context.abort(
                grpc.StatusCode.UNAVAILABLE,
                f"Core vector-store creation failed: {e}",
            )
            return  # unreachable

        # 5. write async_tasks(idempotency_key, result=kb) for replay
        async with self._pool.acquire() as conn:
            task_row = await async_task_repo.create_task(
                conn,
                tenant_id=tenant_id,
                idempotency_key=idem_key,
                task_type="kb.create",
                resource_type="knowledge_base",
                resource_id=kb_id,
                payload={"kb_id": kb_id, "name": request.name},
                status="pending",
            )
            await async_task_repo.complete_task(
                conn,
                tenant_id=tenant_id,
                task_id=str(task_row["id"]),
                result=kb_row,
            )

        return _kb_row_to_pb(kb_row)

    def GetKB(self, request, context):
        return _run_async(self._get_kb(request, context))

    async def _get_kb(self, request, context) -> kb_pb.KnowledgeBase:
        if self._pool is None:
            context.abort(grpc.StatusCode.FAILED_PRECONDITION, "DB pool not configured")
            return
        async with self._pool.acquire() as conn:
            row = await kb_repo.get_kb(
                conn, tenant_id=request.tenant_id, kb_id=request.kb_id
            )
        if not row:
            context.abort(grpc.StatusCode.NOT_FOUND, "knowledge base not found")
            return
        return _kb_row_to_pb(row)

    def ListKBs(self, request, context):
        return _run_async(self._list_kbs(request, context))

    async def _list_kbs(self, request, context) -> kb_pb.ListKBsResponse:
        if self._pool is None:
            context.abort(grpc.StatusCode.FAILED_PRECONDITION, "DB pool not configured")
            return
        limit = request.page.limit or 20
        cursor = request.page.cursor or None
        async with self._pool.acquire() as conn:
            rows, total = await kb_repo.list_kbs(
                conn, tenant_id=request.tenant_id, limit=limit, cursor=cursor
            )
        kbs = [_kb_row_to_pb(r) for r in rows]
        next_cursor = str(rows[-1]["id"]) if rows and len(rows) >= limit else ""
        return kb_pb.ListKBsResponse(
            kbs=kbs,
            meta=common_pb2.CursorPageMeta(total=total, next_cursor=next_cursor),
        )

    def DeleteKB(self, request, context):
        return _run_async(self._delete_kb(request, context))

    async def _delete_kb(self, request, context) -> empty_pb2.Empty:
        if self._pool is None:
            context.abort(grpc.StatusCode.FAILED_PRECONDITION, "DB pool not configured")
            return
        tenant_id = request.tenant_id
        kb_id = request.kb_id

        # 1. soft-delete KB + fetch persisted vector_store_id for Core cleanup
        async with self._pool.acquire() as conn:
            kb_row = await kb_repo.get_kb(
                conn, tenant_id=tenant_id, kb_id=kb_id
            )
            if not kb_row:
                context.abort(grpc.StatusCode.NOT_FOUND, "knowledge base not found")
                return
            deleted = await kb_repo.soft_delete_kb(
                conn, tenant_id=tenant_id, kb_id=kb_id
            )
        if not deleted:
            context.abort(grpc.StatusCode.NOT_FOUND, "knowledge base not found")
            return

        # 2. Core DELETE /vector-stores/{id} (SPEC §6.1) — best-effort.
        # Use the persisted vector_store_id (Core UUID), not the derived name.
        vector_store_id = str(kb_row.get("vector_store_id") or "")
        if not vector_store_id:
            logger.warning(
                "kb-service: DeleteKB kb_id=%s has no persisted vector_store_id; "
                "skipping Core vector cleanup", kb_id,
            )
        else:
            try:
                async with self._core_client_factory(tenant_id) as core:
                    await core.delete_vector_store(
                        vector_store_id=vector_store_id
                    )
            except (CoreAPIError, httpx.RequestError) as e:
                # best-effort: KB is already soft-deleted; vector cleanup can be
                # retried by a reconciler.
                logger.warning(
                    "kb-service: DeleteKB kb_id=%s Core vector cleanup failed "
                    "(best-effort): %s", kb_id, e,
                )

        return empty_pb2.Empty()

    def GetDocumentUploadURL(self, request, context):
        return _run_async(self._get_document_upload_url(request, context))

    async def _get_document_upload_url(
        self, request, context
    ) -> kb_pb.GetDocumentUploadURLResponse:
        if not request.idempotency_key:
            context.abort(
                grpc.StatusCode.INVALID_ARGUMENT, "idempotency_key is required"
            )
            return
        # validate file_type (SPEC §6.2)
        allowed = {"pdf", "docx", "xlsx", "pptx", "md", "txt"}
        if request.file_type not in allowed:
            context.abort(
                grpc.StatusCode.INVALID_ARGUMENT,
                f"file_type must be one of {sorted(allowed)}",
            )
            return
        if self._pool is None:
            context.abort(grpc.StatusCode.FAILED_PRECONDITION, "DB pool not configured")
            return

        # 0. verify the KB exists BEFORE resolving an upload URL / reserving a
        # kb_documents row. Otherwise a non-existent kb_id trips the
        # kb_documents.kb_id FK constraint (→ 500) instead of a clean 404.
        async with self._pool.acquire() as conn:
            kb_row = await kb_repo.get_kb(
                conn, tenant_id=request.tenant_id, kb_id=request.kb_id
            )
        if kb_row is None:
            context.abort(grpc.StatusCode.NOT_FOUND, "knowledge base not found")
            return

        doc_id = str(uuid.uuid4())
        storage_path = f"kb-docs/{request.kb_id}/{doc_id}/{request.file_name}"

        # 1. Core POST /objects/upload — get presigned PUT URL
        try:
            async with self._core_client_factory(request.tenant_id) as core:
                # The Core object-store keys buckets by UUID, but kb-service
                # uses the bucket name "kb-docs" as a convention. Look up the
                # UUID by name first.
                bucket_id = await core.get_bucket_id_by_name(name="kb-docs")
                if bucket_id is None:
                    context.abort(
                        grpc.StatusCode.FAILED_PRECONDITION,
                        "kb-docs bucket not found — create it via POST /buckets first",
                    )
                    return
                upload = await core.request_upload_url(
                    bucket_id=bucket_id,
                    key=storage_path,
                    content_type=None,
                    idempotency_key=request.idempotency_key,
                )
        except CoreAPIError as e:
            context.abort(grpc.StatusCode.UNAVAILABLE, f"Core upload URL failed: {e}")
            return

        upload_url = upload.get("upload_url", "")
        object_id = upload.get("object_id", doc_id)

        # 2. write kb_documents (parse_status=pending) (SPEC §6.1)
        async with self._pool.acquire() as conn:
            await document_repo.create_document(
                conn,
                tenant_id=request.tenant_id,
                kb_id=request.kb_id,
                file_name=request.file_name,
                file_type=request.file_type,
                file_size_bytes=request.file_size_bytes,
                storage_path=storage_path,
                checksum_sha256=request.checksum_sha256,
                custom_metadata=_parse_metadata(request.custom_metadata),
                doc_id=doc_id,
                object_id=object_id,
            )

        return kb_pb.GetDocumentUploadURLResponse(
            doc_id=doc_id,
            upload_url=upload_url,
            storage_path=storage_path,
        )

    def NotifyDocumentUploaded(self, request, context):
        return _run_async(self._notify_document_uploaded(request, context))

    async def _notify_document_uploaded(
        self, request, context
    ) -> common_pb2.AsyncTaskRef:
        """NotifyDocumentUploaded — atomic outbox write (SPEC §6.1, US-010).

        Writes kb_documents (parse_status=pending) + async_tasks + outbox_events
        in a single transaction so the parse task is durably enqueued only if
        the document update commits. The outbox dispatcher publishes the event
        to NATS `ani.tasks.kb.parse` asynchronously (outbox/dispatcher.py).
        """
        if self._pool is None:
            context.abort(grpc.StatusCode.FAILED_PRECONDITION, "DB pool not configured")
            return
        tenant_id = request.tenant_id or ""
        kb_id = request.kb_id or ""
        doc_id = request.doc_id or ""
        if not tenant_id or not kb_id or not doc_id:
            context.abort(
                grpc.StatusCode.INVALID_ARGUMENT,
                "tenant_id, kb_id and doc_id are required",
            )
            return
        # NotifyDocumentUploadedRequest (proto) carries no idempotency_key and
        # no checksum field; the proto is the source of truth, so we synthesize
        # a deterministic idempotency_key from (tenant, kb, doc) so retries are
        # safe and return the same AsyncTaskRef.
        idem_key = f"kb.parse:{tenant_id}:{kb_id}:{doc_id}"

        async with self._pool.acquire() as conn:
            # 1. idempotency replay: if a prior notify for this doc completed,
            #    return the same AsyncTaskRef (SPEC §6.4 idempotent replay).
            existing = await async_task_repo.find_by_idempotency_key(
                conn, tenant_id=tenant_id, idempotency_key=idem_key
            )
            if existing and existing.get("status") in ("pending", "completed"):
                # Return the recorded task id + status.
                return common_pb2.AsyncTaskRef(
                    task_id=str(existing["id"]),
                    task_type=existing.get("task_type") or "kb.parse",
                    status=existing.get("status") or "pending",
                    location_url="",
                )

            # 2. atomic write: kb_documents + async_tasks + outbox_events.
            #    outbox.insert_event and create_task_in_tx / update_parse_status_in_tx
            #    do NOT open their own transactions; they run inside this one.
            async with conn.transaction():
                # a. mark the document parse_status=pending (idempotent update).
                updated = await document_repo.update_parse_status_in_tx(
                    conn,
                    tenant_id=tenant_id,
                    doc_id=doc_id,
                    parse_status="pending",
                    error_message=None,
                )
                if not updated:
                    # document not found → abort before writing outbox/async_task
                    context.abort(grpc.StatusCode.NOT_FOUND, "document not found")
                    return  # unreachable; for type checkers

                # b. insert async_tasks row for idempotent replay + status tracking.
                doc_row = await document_repo.get_document(
                    conn, tenant_id=tenant_id, kb_id=kb_id, doc_id=doc_id
                )
                object_id = (doc_row or {}).get("object_id") or ""

                task_row = await async_task_repo.create_task_in_tx(
                    conn,
                    tenant_id=tenant_id,
                    idempotency_key=idem_key,
                    task_type="kb.parse",
                    resource_type="kb_document",
                    resource_id=doc_id,
                    payload={
                        "doc_id": doc_id,
                        "kb_id": kb_id,
                        "object_id": object_id,
                    },
                    status="pending",
                )
                task_id = str(task_row["id"])

                # c. insert outbox_events row; dispatcher publishes to NATS.
                from app.repositories import outbox as outbox_repo
                from app.repositories import knowledge_base as kb_repo

                # Carry the KB's chunk_size through to the parse_worker so each
                # task chunks with the KB's configured size (default 1024 when
                # the KB row is missing or has no chunk_size set).
                kb_row = await kb_repo.get_kb(conn, tenant_id=tenant_id, kb_id=kb_id)
                kb_chunk_size = (kb_row or {}).get("chunk_size") or 1024

                await outbox_repo.insert_event(
                    conn,
                    tenant_id=tenant_id,
                    aggregate_type="kb_documents",
                    aggregate_id=doc_id,
                    event_type="kb.parse",
                    payload={
                        "doc_id": doc_id,
                        "kb_id": kb_id,
                        "storage_path": request.storage_path,
                        "tenant_id": tenant_id,
                        "file_name": "",
                        "object_id": object_id,
                        "chunk_size": kb_chunk_size,
                    },
                )

        return common_pb2.AsyncTaskRef(
            task_id=task_id,
            task_type="kb.parse",
            status="pending",
            location_url="",
        )

    def GetDocument(self, request, context):
        return _run_async(self._get_document(request, context))

    async def _get_document(self, request, context) -> kb_pb.KBDocument:
        if self._pool is None:
            context.abort(grpc.StatusCode.FAILED_PRECONDITION, "DB pool not configured")
            return
        async with self._pool.acquire() as conn:
            row = await document_repo.get_document(
                conn,
                tenant_id=request.tenant_id,
                kb_id=request.kb_id,
                doc_id=request.doc_id,
            )
        if not row:
            context.abort(grpc.StatusCode.NOT_FOUND, "document not found")
            return
        return _doc_row_to_pb(row)

    def ListDocuments(self, request, context):
        return _run_async(self._list_documents(request, context))

    async def _list_documents(self, request, context) -> kb_pb.ListDocumentsResponse:
        if self._pool is None:
            context.abort(grpc.StatusCode.FAILED_PRECONDITION, "DB pool not configured")
            return
        limit = request.page.limit or 20
        cursor = request.page.cursor or None
        async with self._pool.acquire() as conn:
            # KB existence gate: a deleted/unknown KB must yield NOT_FOUND, not
            # an empty list (SPEC §6.1 ListDocuments 404 semantics).
            kb_row = await kb_repo.get_kb(
                conn, tenant_id=request.tenant_id, kb_id=request.kb_id
            )
            if not kb_row:
                context.abort(grpc.StatusCode.NOT_FOUND, "knowledge base not found")
                return
            rows, total = await document_repo.list_documents(
                conn,
                tenant_id=request.tenant_id,
                kb_id=request.kb_id,
                parse_status=request.parse_status or None,
                limit=limit,
                cursor=cursor,
            )
        docs = [_doc_row_to_pb(r) for r in rows]
        next_cursor = str(rows[-1]["id"]) if rows and len(rows) >= limit else ""
        return kb_pb.ListDocumentsResponse(
            documents=docs,
            meta=common_pb2.CursorPageMeta(total=total, next_cursor=next_cursor),
        )

    def DeleteDocument(self, request, context):
        return _run_async(self._delete_document(request, context))

    async def _delete_document(self, request, context) -> empty_pb2.Empty:
        if self._pool is None:
            context.abort(grpc.StatusCode.FAILED_PRECONDITION, "DB pool not configured")
            return
        # 1. soft-delete document + delete chunks + fetch persisted vector_store_id
        async with self._pool.acquire() as conn:
            kb_row = await kb_repo.get_kb(
                conn, tenant_id=request.tenant_id, kb_id=request.kb_id
            )
            if not kb_row:
                context.abort(grpc.StatusCode.NOT_FOUND, "knowledge base not found")
                return
            deleted = await document_repo.soft_delete_document(
                conn,
                tenant_id=request.tenant_id,
                kb_id=request.kb_id,
                doc_id=request.doc_id,
            )
            if not deleted:
                context.abort(grpc.StatusCode.NOT_FOUND, "document not found")
                return
            await chunk_repo.delete_chunks_by_doc(
                conn,
                tenant_id=request.tenant_id,
                kb_id=request.kb_id,
                doc_id=request.doc_id,
            )
        # 2. Core DELETE /vector-stores/{id}/documents?filter=doc_id=="..." — best-effort.
        # Use the persisted vector_store_id (Core UUID), not the derived name.
        vector_store_id = str(kb_row.get("vector_store_id") or "")
        if not vector_store_id:
            logger.warning(
                "kb-service: DeleteDocument kb_id=%s has no persisted vector_store_id; "
                "skipping Core vector cleanup", request.kb_id,
            )
        else:
            try:
                async with self._core_client_factory(request.tenant_id) as core:
                    await core.delete_vector_store_documents(
                        vector_store_id=vector_store_id,
                        filter_expr=f'doc_id == "{request.doc_id}"',
                    )
            except (CoreAPIError, httpx.RequestError) as e:
                # best-effort vector cleanup; document already soft-deleted.
                logger.warning(
                    "kb-service: DeleteDocument kb_id=%s doc_id=%s Core vector "
                    "cleanup failed (best-effort): %s",
                    request.kb_id, request.doc_id, e,
                )
        return empty_pb2.Empty()

    def Query(self, request, context):
        return _run_async(self._query(request, context))

    async def _query(self, request, context) -> kb_pb.QueryResponse:
        """Query — kb_messages persistence + Redis session cache (SPEC §6.1, US-010).

        1. validate idempotency_key
        2. resolve session_id (empty → new UUID)
        3. INSERT kb_messages(role='user', content=question)
        4. Redis: RPUSH user_msg; EXPIRE 24h; LTRIM 20
        5. call rag-engine Query (gRPC-intent client)
        6. INSERT kb_messages(role='assistant', content=answer, sources)
        7. Redis: RPUSH assistant_msg; LTRIM 20
        8. return QueryResponse
        """
        if not request.idempotency_key:
            context.abort(
                grpc.StatusCode.INVALID_ARGUMENT, "idempotency_key is required"
            )
            return
        if not request.question:
            context.abort(
                grpc.StatusCode.INVALID_ARGUMENT, "question is required"
            )
            return

        tenant_id = request.tenant_id or ""
        kb_id = request.kb_id or ""
        if not tenant_id or not kb_id:
            context.abort(
                grpc.StatusCode.INVALID_ARGUMENT,
                "tenant_id and kb_id are required",
            )
            return

        # 2. resolve / create session id (SPEC §6.1 step 2)
        session_id = request.session_id or str(uuid.uuid4())

        # 2.5. Load the KB row BEFORE persisting session/message so a
        # query against a non-existent kb_id returns NOT_FOUND instead of
        # tripping the kb_sessions.kb_id FK constraint (→ 500). This also
        # yields the KB's top_k / score_threshold / retrieval_mode once.
        kb_row = None
        if self._pool is not None:
            try:
                async with self._pool.acquire() as conn:
                    kb_row = await kb_repo.get_kb(
                        conn, tenant_id=tenant_id, kb_id=kb_id
                    )
            except Exception:  # noqa: BLE001 — degrade to defaults below
                logger.warning("kb-service: failed to load KB config, using defaults", exc_info=True)
                kb_row = None
        if kb_row is None:
            context.abort(
                grpc.StatusCode.NOT_FOUND, "knowledge base not found"
            )
            return

        kb_cfg = {
            "top_k": kb_row.get("top_k") or 5,
            # 未设置(0)时透传给 rag-engine，由 DEFAULT_SCORE_THRESHOLD 兜底。
            "score_threshold": kb_row.get("score_threshold") or 0.0,
            "retrieval_mode": kb_row.get("retrieval_mode") or "hybrid",
        }

        # 3-4. persist user message + Redis cache (best-effort).
        # create_session + insert_message(user) run in a single transaction so
        # a partial user-message write can't survive a crash mid-RPC (SPEC §6.1).
        if self._pool is not None:
            async with self._pool.acquire() as conn:
                async with conn.transaction():
                    await message_repo.create_session_in_tx(
                        conn,
                        tenant_id=tenant_id,
                        kb_id=kb_id,
                        session_id=session_id,
                    )
                    await message_repo.insert_message_in_tx(
                        conn,
                        tenant_id=tenant_id,
                        session_id=session_id,
                        role="user",
                        content=request.question,
                    )

        cache = self._session_cache_factory()
        if cache is not None:
            await cache.append_message(
                session_id=session_id, role="user", content=request.question
            )

        # 5. Resolve retrieval configuration from the KB row (loaded at
        #    step 2.5 above). Client request values override the KB config
        #    when explicitly provided.
        top_k = request.top_k if request.top_k else kb_cfg["top_k"]
        score_threshold = (
            request.score_threshold if request.score_threshold != 0
            else kb_cfg["score_threshold"]
        )
        retrieval_mode = (request.retrieval_mode or kb_cfg["retrieval_mode"] or "hybrid")

        # 6. QueryOrchestrator: retrieve → gates → Generate RPC.
        result = await self._query_new_path(
            tenant_id=tenant_id,
            kb_id=kb_id,
            question=request.question,
            session_id=session_id,
            top_k=top_k,
            score_threshold=score_threshold,
            retrieval_mode=retrieval_mode,
            inference_service_name=request.inference_service_name or "default",
            vector_store_id=str(kb_row.get("vector_store_id") or ""),
            cache=cache,
        )
        answer = result.answer
        sources = result.sources
        input_tokens = result.input_tokens
        output_tokens = result.output_tokens

        # 6-7. persist assistant message + Redis cache (best-effort).
        await self._persist_assistant(
            tenant_id=tenant_id, session_id=session_id,
            answer=answer, sources=sources,
            input_tokens=input_tokens, output_tokens=output_tokens,
            cache=cache,
        )

        # 8. build response (session_id may have been newly created).
        source_chunks = [
            kb_pb.SourceChunk(
                doc_id=s.get("doc_id", ""),
                file_name=s.get("file_name", ""),
                page=s.get("page", 0),
                content=s.get("content", ""),
                score=s.get("score", 0.0),
            )
            for s in sources
        ]
        return kb_pb.QueryResponse(
            answer=answer,
            sources=source_chunks,
            session_id=session_id,
            input_tokens=input_tokens,
            output_tokens=output_tokens,
        )

    # ── Plan step 10 (issue-038): Retrieve server-streaming RPC ───────────

    def Retrieve(self, request, context):
        """Retrieve — server-streaming RAG: retrieve → sources → tokens → done.

        Plan §10.2: Gateway SSE switches to this gRPC stream when
        ``KB_SSE_USE_NEW_PATH=True``. The handler orchestrates retrieve →
        three no-result gates → GenerateStream, yielding RetrieveEvent
        messages (token* → sources → done), matching the legacy SSE event
        sequence.

        Session management and persistence mirror Query RPC: persist user
        message first, load history (includes current-turn user), stream
        GenerateStream, then persist assistant message.

        gRPC server-streaming RPC methods must be sync generators (yield
        RetrieveEvent). The async orchestrator runs on the dedicated
        gRPC event loop; events are bridged via a thread-safe queue so the
        sync generator can yield them to the gRPC transport.
        """
        yield from self._retrieve_stream_sync(request, context)

    def _retrieve_stream_sync(self, request, context):
        """Sync generator wrapper: bridge async _retrieve_stream to gRPC.

        Runs the async generator on the dedicated gRPC event loop and
        bridges events through a thread-safe queue. A sentinel (None)
        signals completion; exceptions are re-raised in the sync thread
        so gRPC can map them to status codes.

        Cancellation safety (S1/S2): if the gRPC client disconnects mid-stream
        the sync generator stops iterating (GeneratorExit), which cancels the
        pending ``run_coroutine_threadsafe`` future — the ``_runner`` coroutine
        receives ``CancelledError`` and stops the LLM generation + DB work.
        ``ev_q.get`` uses a timeout so the sync thread cannot hang forever if
        the event loop dies without producing the sentinel.
        """
        if _grpc_loop is None:
            _start_grpc_loop()

        ev_q: queue.Queue = queue.Queue()
        loop = _grpc_loop
        # Max wait for a single event; the gRPC query timeout is 120s on the
        # gateway side, so 130s gives a grace margin before we give up.
        _EVENT_TIMEOUT = 130.0
        future: asyncio.Future | None = None

        async def _runner():
            try:
                async for event in self._retrieve_stream(request, context):
                    ev_q.put(event)
            except Exception as exc:  # noqa: BLE001 — propagate to sync side
                ev_q.put(exc)
            finally:
                ev_q.put(None)  # sentinel: stream complete

        try:
            future = asyncio.run_coroutine_threadsafe(_runner(), loop)

            while True:
                try:
                    item = ev_q.get(timeout=_EVENT_TIMEOUT)
                except queue.Empty:
                    # S2: event loop died or runner hung — cancel and abort.
                    if future is not None and not future.done():
                        future.cancel()
                    context.abort(
                        grpc.StatusCode.DEADLINE_EXCEEDED,
                        "retrieve stream timed out waiting for event",
                    )
                    return
                if item is None:
                    break
                if isinstance(item, BaseException):
                    if isinstance(item, grpc.RpcError):
                        raise item
                    # Map common exceptions to gRPC status codes.
                    if isinstance(item, (CoreAPIError, RagEngineError)):
                        context.abort(
                            grpc.StatusCode.UNAVAILABLE,
                            f"backend unavailable: {item}",
                        )
                        return
                    raise item
                yield item
        except GeneratorExit:
            # S1: client disconnected — cancel the runner coroutine so the
            # LLM generation / DB work stops instead of leaking.
            if future is not None and not future.done():
                future.cancel()
            raise

    async def _retrieve_stream(self, request, context):
        """Async generator: orchestrate retrieve → gates → GenerateStream.

        Yields RetrieveEvent messages (token* → sources → done). Session
        management mirrors Query RPC (issue-038 AC 2).
        """
        # 1. validate request (mirror Query validation).
        if not request.idempotency_key:
            context.abort(
                grpc.StatusCode.INVALID_ARGUMENT, "idempotency_key is required"
            )
            return
        if not request.question:
            context.abort(grpc.StatusCode.INVALID_ARGUMENT, "question is required")
            return
        tenant_id = request.tenant_id or ""
        kb_id = request.kb_id or ""
        if not tenant_id or not kb_id:
            context.abort(
                grpc.StatusCode.INVALID_ARGUMENT,
                "tenant_id and kb_id are required",
            )
            return

        # 2. resolve / create session id.
        session_id = request.session_id or str(uuid.uuid4())

        # 2.5. Load KB row (NOT_FOUND gate, mirror Query).
        kb_row = None
        if self._pool is not None:
            try:
                async with self._pool.acquire() as conn:
                    kb_row = await kb_repo.get_kb(
                        conn, tenant_id=tenant_id, kb_id=kb_id
                    )
            except Exception:  # noqa: BLE001
                logger.warning("kb-service: failed to load KB config, using defaults", exc_info=True)
                kb_row = None
        if kb_row is None:
            context.abort(grpc.StatusCode.NOT_FOUND, "knowledge base not found")
            return

        kb_cfg = {
            "top_k": kb_row.get("top_k") or 5,
            "score_threshold": kb_row.get("score_threshold") or 0.0,
            "retrieval_mode": kb_row.get("retrieval_mode") or "hybrid",
        }

        # 3-4. persist user message + Redis cache (mirror Query).
        if self._pool is not None:
            async with self._pool.acquire() as conn:
                async with conn.transaction():
                    await message_repo.create_session_in_tx(
                        conn,
                        tenant_id=tenant_id,
                        kb_id=kb_id,
                        session_id=session_id,
                    )
                    await message_repo.insert_message_in_tx(
                        conn,
                        tenant_id=tenant_id,
                        session_id=session_id,
                        role="user",
                        content=request.question,
                    )

        cache = self._session_cache_factory()
        if cache is not None:
            await cache.append_message(
                session_id=session_id, role="user", content=request.question
            )

        # 5. Resolve retrieval config (client overrides KB defaults).
        top_k = request.top_k if request.top_k else kb_cfg["top_k"]
        score_threshold = (
            request.score_threshold if request.score_threshold != 0
            else kb_cfg["score_threshold"]
        )
        retrieval_mode = request.retrieval_mode or kb_cfg["retrieval_mode"] or "hybrid"
        inference_service_name = request.inference_service_name or "default"
        vector_store_id = str(kb_row.get("vector_store_id") or "")

        # 6. Load chat history (includes current-turn user, already persisted).
        history = await self._load_history(
            tenant_id=tenant_id,
            session_id=session_id,
            cache=cache,
        )

        # 7. Build or reuse per-tenant orchestrator (mirror _query_new_path).
        from app.services.query_orchestrator import (
            QueryOrchestrator,
            StreamDoneEvent,
            StreamNoResultEvent,
            StreamSourcesEvent,
            StreamTokenEvent,
        )

        orch = self._orchestrators.get(tenant_id)
        if orch is None:
            if self._retrieve_service_factory is not None:
                retrieve_service = self._retrieve_service_factory(tenant_id)
            else:
                retrieve_service = _default_retrieve_service(tenant_id, self._pool)

            if self._rag_engine_grpc_client_factory is not None:
                rag_engine_grpc = self._rag_engine_grpc_client_factory()
            else:
                rag_engine_grpc = _default_rag_engine_grpc_client()

            orch = QueryOrchestrator(
                retrieve_service=retrieve_service,
                rag_engine_client=rag_engine_grpc,
            )
            self._orchestrators[tenant_id] = orch

        # 8. Delegate to orchestrator.query_stream (single source of gate logic).
        answer = ""
        final_sources: list[dict[str, Any]] = []
        final_input_tokens = 0
        final_output_tokens = 0
        async for ev in orch.query_stream(
            tenant_id=tenant_id,
            kb_id=kb_id,
            question=request.question,
            session_id=session_id,
            top_k=top_k,
            score_threshold=score_threshold,
            retrieval_mode=retrieval_mode,
            inference_service_name=inference_service_name,
            vector_store_id=vector_store_id,
            history=history,
        ):
            if isinstance(ev, StreamTokenEvent):
                answer += ev.content
                yield kb_pb.RetrieveEvent(
                    token=kb_pb.RetrieveTokenEvent(content=ev.content)
                )
            elif isinstance(ev, StreamNoResultEvent):
                answer = ev.answer
                final_input_tokens = ev.input_tokens
                final_output_tokens = ev.output_tokens
                yield kb_pb.RetrieveEvent(
                    token=kb_pb.RetrieveTokenEvent(content=ev.answer)
                )
            elif isinstance(ev, StreamSourcesEvent):
                final_sources = ev.sources
                source_chunks = [
                    kb_pb.SourceChunk(
                        doc_id=str(s.get("doc_id", "")),
                        file_name=str(s.get("file_name", "")),
                        page=int(s.get("page", 0) or 0),
                        content=str(s.get("content", "")),
                        score=float(s.get("score", 0.0) or 0.0),
                    )
                    for s in ev.sources
                ]
                yield kb_pb.RetrieveEvent(
                    sources=kb_pb.RetrieveSourcesEvent(sources=source_chunks)
                )
            elif isinstance(ev, StreamDoneEvent):
                final_input_tokens = ev.input_tokens
                final_output_tokens = ev.output_tokens
                yield kb_pb.RetrieveEvent(
                    done=kb_pb.RetrieveDoneEvent(
                        input_tokens=ev.input_tokens,
                        output_tokens=ev.output_tokens,
                        session_id=ev.session_id,
                    )
                )

        # 9. Persist assistant message + Redis cache (mirror Query).
        await self._persist_assistant(
            tenant_id=tenant_id, session_id=session_id,
            answer=answer, sources=final_sources,
            input_tokens=final_input_tokens, output_tokens=final_output_tokens,
            cache=cache,
        )

    async def _persist_assistant(
        self, *, tenant_id: str, session_id: str,
        answer: str, sources: list[dict[str, Any]],
        input_tokens: int, output_tokens: int, cache: Any,
    ) -> None:
        """Persist assistant message to DB + Redis (shared by Query and Retrieve)."""
        if self._pool is not None:
            async with self._pool.acquire() as conn:
                await message_repo.insert_message(
                    conn,
                    tenant_id=tenant_id,
                    session_id=session_id,
                    role="assistant",
                    content=answer,
                    source_chunks=sources,
                    input_tokens=input_tokens,
                    output_tokens=output_tokens,
                )
        if cache is not None:
            await cache.append_message(
                session_id=session_id,
                role="assistant",
                content=answer,
                sources=sources,
                input_tokens=input_tokens,
                output_tokens=output_tokens,
            )

    # ── Plan step 8A: new path helpers (flag=true) ───────────────────────────

    async def _load_history(
        self,
        *,
        tenant_id: str,
        session_id: str,
        cache: Any,
        limit: int = 20,
    ) -> list[dict[str, str]]:
        """Load chat history for the current session (Plan step 8A).

        Priority: Redis cache (``LRANGE key -limit -1`` → most recent N in
        chronological order, matching legacy ChatMemoryBuffer token_limit
        behavior) → fallback to DB ``list_session_messages`` (oldest N).

        The current-turn user message was already persisted to Redis (step
        3-4 of ``_query``) BEFORE this call, so the Redis history INCLUDES
        the current-turn user. The Generate RPC appends ``question`` as the
        final USER message, so the current-turn user appears twice — this
        matches the legacy ContextChatEngine behavior and is intentional.

        Returns a list of ``{role, content}`` dicts in chronological order
        (oldest-first), ready to pass as ``history`` to Generate RPC.
        """
        # 1. Try Redis first (most recent N, chronological order).
        if cache is not None:
            msgs = await cache.list_recent_messages(
                session_id=session_id, limit=limit
            )
            if msgs:
                return [
                    {"role": str(m.get("role", "")), "content": str(m.get("content", ""))}
                    for m in msgs
                ]

        # 2. Fallback to DB (oldest N by created_at ASC).
        if self._pool is not None:
            async with self._pool.acquire() as conn:
                rows = await message_repo.list_session_messages(
                    conn,
                    tenant_id=tenant_id,
                    session_id=session_id,
                    limit=limit,
                )
            return [
                {"role": r.get("role", ""), "content": r.get("content", "")}
                for r in rows
            ]

        return []

    async def _query_new_path(
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
        cache: Any,
    ) -> QueryResult:
        """QueryOrchestrator: retrieve → gates → Generate RPC.

        Returns a ``QueryResult`` dataclass (answer, sources, session_id,
        input_tokens, output_tokens).
        """
        from app.services.query_orchestrator import QueryOrchestrator

        # 1. Load chat history (includes current-turn user, already persisted).
        history = await self._load_history(
            tenant_id=tenant_id,
            session_id=session_id,
            cache=cache,
        )

        # 2. Build or reuse the per-tenant orchestrator.
        #    Each tenant gets its own QueryOrchestrator backed by a
        #    tenant-scoped RetrieveService (which holds a tenant-scoped
        #    CoreClient). This preserves multi-tenant isolation.
        orch = self._orchestrators.get(tenant_id)
        if orch is None:
            if self._retrieve_service_factory is not None:
                retrieve_service = self._retrieve_service_factory(tenant_id)
            else:
                retrieve_service = _default_retrieve_service(tenant_id, self._pool)

            if self._rag_engine_grpc_client_factory is not None:
                rag_engine_grpc = self._rag_engine_grpc_client_factory()
            else:
                rag_engine_grpc = _default_rag_engine_grpc_client()

            orch = QueryOrchestrator(
                retrieve_service=retrieve_service,
                rag_engine_client=rag_engine_grpc,
            )
            self._orchestrators[tenant_id] = orch

        # 3. Run the orchestrator.
        result = await orch.query(
            tenant_id=tenant_id,
            kb_id=kb_id,
            question=question,
            session_id=session_id,
            top_k=top_k,
            score_threshold=score_threshold,
            retrieval_mode=retrieval_mode,
            inference_service_name=inference_service_name,
            vector_store_id=vector_store_id,
            history=history,
        )

        return result

    # ── 3 P1 RPC declarations (always UNIMPLEMENTED in P0) ────────────────────

    def ListKBCitations(self, request, context):
        return p1_rpcs.list_kb_citations(request, context)

    def ListKBSessions(self, request, context):
        return p1_rpcs.list_kb_sessions(request, context)

    def UpdateKBPermissions(self, request, context):
        return p1_rpcs.update_kb_permissions(request, context)


# ── helpers ───────────────────────────────────────────────────────────────────


def _default_core_client(tenant_id: str) -> CoreClient:
    """Build a CoreClient from app settings (production default).

    Reads the service-account token from CORE_SERVICE_TOKEN env var and
    forwards it as the Authorization header so the gateway auth middleware
    accepts the request.
    """
    from app.core.config import settings

    auth_token = os.environ.get("CORE_SERVICE_TOKEN", "")
    return CoreClient(
        base_url=settings.core_api_base_url,
        tenant_id=tenant_id,
        auth_token=auth_token or None,
    )


def _default_session_cache() -> Any:
    """Build a SessionCache from app settings, or None if Redis is unavailable.

    In production main.py builds the cache once at startup and injects it via
    session_cache_factory, so this default is only used by tests / skeleton
    mode / direct servicer construction without main.py. We construct a fresh
    instance per call (no module-global singleton) to preserve test isolation
    — a module-level cache would leak state across tests and block them from
    mocking the factory. Query degrades to DB-only when Redis is down
    (SPEC §7.3).
    """
    from app.core.config import settings
    from app.session.cache import SessionCache

    try:
        import redis.asyncio as aioredis

        client = aioredis.from_url(settings.redis_url, decode_responses=False)
        return SessionCache(redis=client)
    except Exception as e:  # noqa: BLE001 — best-effort cache wiring
        logger.warning("Redis session cache unavailable (Query will be DB-only): %s", e)
        return None


def _default_rag_engine_grpc_client() -> Any:
    """Build a RagEngineGRPCClient from app settings (production default).

    Used when no factory was injected. In production, main.py constructs
    the client once at startup and injects it via
    ``rag_engine_grpc_client_factory``.

    This fallback creates a module-level singleton so the gRPC channel is
    not re-created on every request (avoids channel leak / connection storm).
    """
    global _default_grpc_client_instance
    if _default_grpc_client_instance is None:
        from app.rag_engine.client import RagEngineGRPCClient

        _default_grpc_client_instance = RagEngineGRPCClient(
            addr=settings.rag_engine_grpc_addr
        )
    return _default_grpc_client_instance


def _default_retrieve_service(tenant_id: str, pool: Any) -> Any:
    """Build a RetrieveService from app settings (production default).

    Used when no factory was injected. In production, main.py constructs
    the service once at startup and injects it via
    ``retrieve_service_factory``.

    Reuses the module-level ``_default_rag_engine_grpc_client`` singleton so
    the gRPC channel is shared, not re-created per request.
    """
    from app.services.retrieve_service import RetrieveService

    rag_engine = _default_rag_engine_grpc_client()
    return RetrieveService(
        db_pool=pool,
        core_client_factory=_default_core_client,
        rag_engine_client=rag_engine,
    )


# Module-level singleton for the fallback gRPC client (avoids channel leak).
_default_grpc_client_instance: Any = None


def _kb_bucket_id(kb_id: str) -> str:
    """Derive the MinIO bucket id for a KB.

    Convention: a single shared kb-docs bucket per deployment. The Core
    object-store manages the bucket; kb-service just uses it.
    """
    return "kb-docs"


def _parse_metadata(raw: str) -> dict[str, Any] | None:
    """Parse the custom_metadata JSON string from the proto request."""
    if not raw:
        return None
    import json

    try:
        return json.loads(raw)
    except (json.JSONDecodeError, TypeError):
        return None


def _kb_row_to_pb(row: dict[str, Any]) -> kb_pb.KnowledgeBase:
    """Convert a knowledge_bases repository row to a proto KnowledgeBase."""
    return kb_pb.KnowledgeBase(
        tenant_id=str(row.get("tenant_id", "")),
        id=str(row.get("id", "")),
        name=row.get("name") or "",
        description=row.get("description") or "",
        embedding_model=row.get("embedding_model") or "",
        chunk_size=row.get("chunk_size") or 0,
        top_k=row.get("top_k") or 0,
        score_threshold=row.get("score_threshold") or 0.0,
        retrieval_mode=row.get("retrieval_mode") or "",
        status=row.get("status") or "",
        doc_count=row.get("doc_count") or 0,
        created_at=_ts(row.get("created_at")),
        updated_at=_ts(row.get("updated_at")),
    )


def _doc_row_to_pb(row: dict[str, Any]) -> kb_pb.KBDocument:
    """Convert a kb_documents repository row to a proto KBDocument."""
    import json

    metadata = row.get("custom_metadata")
    if isinstance(metadata, (dict, list)):
        metadata_str = json.dumps(metadata, default=str)
    else:
        metadata_str = str(metadata) if metadata else ""
    return kb_pb.KBDocument(
        tenant_id=str(row.get("tenant_id", "")),
        kb_id=str(row.get("kb_id", "")),
        id=str(row.get("id", "")),
        file_name=row.get("file_name") or "",
        file_type=row.get("file_type") or "",
        file_size_bytes=row.get("file_size_bytes") or 0,
        parse_status=row.get("parse_status") or "",
        chunk_count=row.get("chunk_count") or 0,
        error_message=row.get("error_message") or "",
        custom_metadata=metadata_str,
        created_at=_ts(row.get("created_at")),
        parsed_at=_ts(row.get("parsed_at")),
    )
