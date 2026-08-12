"""KBService gRPC servicer (SPEC §2.4, §4.1, §6.1).

US-009 wires the repositories + Core API client + rag-engine client into the
10 P0 RPCs. CreateKB and DeleteKB call the Core vector-stores API per
SPEC §6.1. US-010 wires NotifyDocumentUploaded's atomic outbox transaction
(kb_documents + async_tasks + outbox_events) and Query's kb_messages
persistence + Redis session cache.
"""
from __future__ import annotations

import asyncio
import threading
import uuid
from datetime import datetime
from typing import Any

import asyncpg
import grpc
from google.protobuf import empty_pb2
from google.protobuf import timestamp_pb2

from app.api import p1_rpcs
from app.core_api.client import CoreAPIError, CoreClient
from app.generated.common.v1 import common_pb2
from app.generated.kb.v1 import kb_service_pb2 as kb_pb
from app.generated.kb.v1 import kb_service_pb2_grpc as pb_grpc
from app.rag_engine.client import RagEngineClient, RagEngineError
from app.repositories import async_task as async_task_repo
from app.repositories import document as document_repo
from app.repositories import knowledge_base as kb_repo


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
        rag_engine_client_factory: Any | None = None,
        session_cache_factory: Any | None = None,
    ) -> None:
        # When pool is None the servicer still serves RPCs that don't need DB
        # (used by the skeleton tests in test_grpc_server.py). DB-backed RPCs
        # abort with FAILED_PRECONDITION when pool is unset.
        self._pool = pool
        # core_client_factory(tenant_id) -> CoreClient; injected for testing.
        # In production, constructed from settings in main.py.
        self._core_client_factory = core_client_factory or _default_core_client
        # rag_engine_client_factory() -> RagEngineClient; injected for testing.
        # In production, constructed from settings in main.py.
        self._rag_engine_client_factory = rag_engine_client_factory or _default_rag_engine_client
        # session_cache_factory() -> SessionCache | None; injected for testing.
        # Returns None when Redis is unavailable (Query degrades to DB-only).
        self._session_cache_factory = session_cache_factory or _default_session_cache

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
        try:
            async with self._core_client_factory(tenant_id) as core:
                # dimension: bge-m3 = 1024; fallback to 1024 when unknown.
                dim = 1024
                await core.create_vector_store(
                    name=_vector_store_name(kb_id),
                    dimension=dim,
                    metric="cosine",
                    embedding_model=request.embedding_model or "bge-m3",
                    idempotency_key=idem_key,
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

        # 1. soft-delete KB
        async with self._pool.acquire() as conn:
            deleted = await kb_repo.soft_delete_kb(
                conn, tenant_id=tenant_id, kb_id=kb_id
            )
        if not deleted:
            context.abort(grpc.StatusCode.NOT_FOUND, "knowledge base not found")
            return

        # 2. Core DELETE /vector-stores/{id} (SPEC §6.1) — best-effort
        try:
            async with self._core_client_factory(tenant_id) as core:
                await core.delete_vector_store(
                    vector_store_id=_vector_store_name(kb_id)
                )
        except CoreAPIError:
            # best-effort: KB is already soft-deleted; vector cleanup can be
            # retried by a reconciler. Log and continue.
            pass

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
        # 1. soft-delete document
        async with self._pool.acquire() as conn:
            deleted = await document_repo.soft_delete_document(
                conn,
                tenant_id=request.tenant_id,
                kb_id=request.kb_id,
                doc_id=request.doc_id,
            )
        if not deleted:
            context.abort(grpc.StatusCode.NOT_FOUND, "document not found")
            return
        # 2. Core DELETE /vector-stores/{id}/documents?filter=doc_id=="..." — best-effort
        try:
            async with self._core_client_factory(request.tenant_id) as core:
                await core.delete_vector_store_documents(
                    vector_store_id=_vector_store_name(request.kb_id),
                    filter_expr=f'doc_id == "{request.doc_id}"',
                )
        except CoreAPIError:
            # best-effort vector cleanup; document already soft-deleted.
            pass
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
                from app.repositories import message as message_repo

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

        # 6. call rag-engine Query (injected factory for testability).
        try:
            async with self._rag_engine_client_factory() as rag:
                resp = await rag.query(
                    kb_id=kb_id,
                    tenant_id=tenant_id,
                    question=request.question,
                    session_id=session_id,
                    top_k=top_k,
                    score_threshold=score_threshold,
                    retrieval_mode=retrieval_mode,
                    inference_service_name=request.inference_service_name or "default",
                )
        except RagEngineError as e:
            context.abort(grpc.StatusCode.UNAVAILABLE, f"rag-engine unavailable: {e}")
            return

        answer = resp.get("answer", "")
        sources = resp.get("sources", [])
        input_tokens = int(resp.get("input_tokens", 0) or 0)
        output_tokens = int(resp.get("output_tokens", 0) or 0)

        # 6-7. persist assistant message + Redis cache (best-effort).
        if self._pool is not None:
            async with self._pool.acquire() as conn:
                from app.repositories import message as message_repo

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

    # ── 3 P1 RPC declarations (always UNIMPLEMENTED in P0) ────────────────────

    def ListKBCitations(self, request, context):
        return p1_rpcs.list_kb_citations(request, context)

    def ListKBSessions(self, request, context):
        return p1_rpcs.list_kb_sessions(request, context)

    def UpdateKBPermissions(self, request, context):
        return p1_rpcs.update_kb_permissions(request, context)


# ── helpers ───────────────────────────────────────────────────────────────────


def _default_core_client(tenant_id: str) -> CoreClient:
    """Build a CoreClient from app settings (production default)."""
    from app.core.config import settings

    return CoreClient(
        base_url=settings.core_api_base_url,
        tenant_id=tenant_id,
    )


def _default_rag_engine_client() -> RagEngineClient:
    """Build a RagEngineClient from app settings (production default)."""
    from app.core.config import settings

    return RagEngineClient(base_url=f"http://{settings.rag_engine_addr}")


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
    import logging

    from app.core.config import settings
    from app.session.cache import SessionCache

    logger = logging.getLogger(__name__)
    try:
        import redis.asyncio as aioredis

        client = aioredis.from_url(settings.redis_url, decode_responses=False)
        return SessionCache(redis=client)
    except Exception as e:  # noqa: BLE001 — best-effort cache wiring
        logger.warning("Redis session cache unavailable (Query will be DB-only): %s", e)
        return None


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
