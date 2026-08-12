"""KBService gRPC servicer (SPEC §2.4, §4.1, §6.1).

US-009 wires the repositories + Core API client + rag-engine client into the
10 P0 RPCs. CreateKB and DeleteKB call the Core vector-stores API per
SPEC §6.1. US-010 wires NotifyDocumentUploaded's atomic outbox transaction
(kb_documents + async_tasks + outbox_events) and Query's kb_messages
persistence + Redis session cache.
"""
from __future__ import annotations

import asyncio
import json
import logging
import os
import threading
import uuid
from datetime import datetime
from typing import Any

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
# gRPC servicer methods run in a ThreadPoolExecutor worker thread. The
# CoreClient (httpx) and Redis client bind to the event loop they were
# created on, so sharing clients created on the uvicorn loop across gRPC
# threads causes "Future attached to a different loop" errors.
#
# Fix: a single dedicated event loop runs on a background thread. The
# CoreClient, Redis client, and all async work live on that loop. gRPC
# worker threads submit coroutines via run_coroutine_threadsafe and block
# on the result.
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

    The loop is shared across all gRPC worker threads so CoreClient (httpx)
    and Redis clients always run on the same loop they were created on.
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


def _ts(dt: datetime | str | None) -> timestamp_pb2.Timestamp:
    """Convert a datetime or ISO-format string to a protobuf Timestamp.

    The Core data plane returns PostgreSQL timestamp values as ISO 8601
    strings in JSON rows. Handle both ISO strings and datetime objects.
    """
    ts = timestamp_pb2.Timestamp()
    if dt is None:
        return ts
    if isinstance(dt, str):
        # Core data plane JSON: "2026-08-11T07:12:34.567890+00:00" or
        # "2026-08-11 07:12:34.567890+00:00" (PostgreSQL timestamptz).
        dt = dt.replace(" ", "T", 1) if " " in dt and "T" not in dt else dt
        try:
            dt = datetime.fromisoformat(dt)
        except ValueError:
            return ts
    if isinstance(dt, datetime):
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
        pool: Any | None = None,
        core_client_factory: Any | None = None,
        rag_engine_client_factory: Any | None = None,
        session_cache_factory: Any | None = None,
    ) -> None:
        # When pool is None the servicer still serves RPCs that don't need DB
        # (used by the skeleton tests in test_grpc_server.py). DB-backed RPCs
        # abort with FAILED_PRECONDITION when pool is unset. The pool is a
        # configuration sentinel — actual data access goes through the
        # CoreClient data plane (issue-031, SPEC §4.3).
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

        # 2. idempotency replay: return existing result (data plane, SPEC §4.2)
        async with self._core_client_factory(tenant_id) as core:
            existing = await async_task_repo.find_by_idempotency_key(
                core, tenant_id=tenant_id, idempotency_key=idem_key
            )
        if existing and existing.get("result"):
            # result is JSONB; the data plane returns it as a dict or a JSON
            # string depending on the column codec. Normalize.
            result = existing["result"]
            if isinstance(result, str):
                result = json.loads(result)
            return _kb_row_to_pb(result)

        # 3. INSERT knowledge_bases (data plane, SPEC §4.2)
        async with self._core_client_factory(tenant_id) as core:
            kb_row = await kb_repo.create_kb(
                core,
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
            async with self._core_client_factory(tenant_id) as core:
                await kb_repo.soft_delete_kb(core, tenant_id=tenant_id, kb_id=kb_id)
            context.abort(
                grpc.StatusCode.UNAVAILABLE,
                f"Core vector-store creation failed: {e}",
            )
            return  # unreachable

        # 5. write async_tasks(idempotency_key, result=kb) for replay
        #    (data plane, SPEC §4.2)
        async with self._core_client_factory(tenant_id) as core:
            task_row = await async_task_repo.create_task(
                core,
                tenant_id=tenant_id,
                idempotency_key=idem_key,
                task_type="kb.create",
                resource_type="knowledge_base",
                resource_id=kb_id,
                payload={"kb_id": kb_id, "name": request.name},
                status="pending",
            )
            await async_task_repo.complete_task(
                core,
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
        async with self._core_client_factory(request.tenant_id) as core:
            row = await kb_repo.get_kb(
                core, tenant_id=request.tenant_id, kb_id=request.kb_id
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
        async with self._core_client_factory(request.tenant_id) as core:
            rows, total = await kb_repo.list_kbs(
                core, tenant_id=request.tenant_id, limit=limit, cursor=cursor
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
        async with self._core_client_factory(tenant_id) as core:
            deleted = await kb_repo.soft_delete_kb(
                core, tenant_id=tenant_id, kb_id=kb_id
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
        async with self._core_client_factory(request.tenant_id) as core:
            kb_row = await kb_repo.get_kb(
                core, tenant_id=request.tenant_id, kb_id=request.kb_id
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

        # 2. write kb_documents (parse_status=pending) (SPEC §6.1, data plane §4.2)
        async with self._core_client_factory(request.tenant_id) as core:
            await document_repo.create_document(
                core,
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
        in a single data-plane transaction so the parse task is durably enqueued
        only if the document update commits. The Core data plane runs each
        /data/query call as a single transaction (BEGIN/COMMIT), so a single
        CTE-based SQL statement folding all three writes is atomic (SPEC §4.2
        cross-table atomic fold). The outbox dispatcher publishes the event to
        NATS `ani.tasks.kb.parse` asynchronously (outbox/dispatcher.py).
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

        # Fold the idempotency check + kb_documents UPDATE + async_tasks
        # INSERT + outbox_events INSERT into a single CTE-based data_query
        # call so the check and all three writes commit atomically in one
        # data-plane transaction (SPEC §4.2 cross-table atomic fold). This
        # eliminates the TOCTOU race between a separate find_by_idempotency_key
        # call and the fold: two concurrent RPCs for the same doc will both
        # hit the ON CONFLICT (idempotency_key) DO NOTHING on async_tasks,
        # and the final SELECT returns the existing task id to both.
        #
        # CTE chain:
        #   doc_upd — UPDATE kb_documents, RETURNING id+object_id (0 rows if
        #             doc not found → downstream CTEs produce nothing)
        #   kb_cfg  — read chunk_size from knowledge_bases
        #   existing — SELECT any prior async_tasks row with this idempotency_key
        #   task    — INSERT async_tasks with ON CONFLICT (idempotency_key)
        #             DO NOTHING, only if doc_upd returned a row (new doc)
        #   obx     — INSERT outbox_events, only if doc_upd returned a row
        # Final SELECT: return the task id (new from `task` or existing from
        #   `existing`), or empty if the document was not found.
        payload_json = json.dumps(
            {
                "doc_id": doc_id,
                "kb_id": kb_id,
                "object_id": None,  # filled from doc_upd in the CTE
            },
            default=str,
        )
        outbox_payload_json = json.dumps(
            {
                "doc_id": doc_id,
                "kb_id": kb_id,
                "storage_path": request.storage_path,
                "tenant_id": tenant_id,
                "file_name": "",
                "object_id": None,  # filled from doc_upd in the CTE
                "chunk_size": None,  # filled from kb_cfg in the CTE
            },
            default=str,
        )
        fold_sql = """
            WITH doc_upd AS (
                UPDATE kb_documents
                   SET parse_status = 'pending',
                       error_message = NULL
                 WHERE id = $1
             RETURNING id, object_id
            ),
            kb_cfg AS (
                SELECT chunk_size FROM knowledge_bases WHERE id = $2
            ),
            existing AS (
                SELECT id, status FROM async_tasks
                 WHERE tenant_id = $3 AND idempotency_key = $4
            ),
            task AS (
                INSERT INTO async_tasks
                    (tenant_id, idempotency_key, task_type, resource_type,
                     resource_id, status, payload)
                SELECT $3, $4, 'kb.parse', 'kb_document', $1, 'pending',
                       jsonb_set(
                         jsonb_set($5::jsonb, '{object_id}',
                                   COALESCE(to_jsonb(doc_upd.object_id), 'null'::jsonb)),
                         '{doc_id}', to_jsonb($1::text))
                  FROM doc_upd
                ON CONFLICT (tenant_id, idempotency_key) DO NOTHING
                RETURNING id
            ),
            obx AS (
                INSERT INTO outbox_events
                    (aggregate_type, aggregate_id, event_type, tenant_id,
                     payload)
                SELECT 'kb_documents', $1::uuid, 'kb.parse', $3,
                       jsonb_set(
                         jsonb_set(
                           jsonb_set($6::jsonb, '{object_id}',
                                     COALESCE(to_jsonb(doc_upd.object_id), 'null'::jsonb)),
                           '{chunk_size}',
                           COALESCE(to_jsonb(kb_cfg.chunk_size), 'null'::jsonb)),
                         '{storage_path}', to_jsonb($7::text))
                  FROM doc_upd, kb_cfg
                  WHERE NOT EXISTS (SELECT 1 FROM existing)
             RETURNING id
            )
            SELECT t.id AS task_id, 'pending' AS status
              FROM task t
            UNION ALL
            SELECT e.id AS task_id, e.status AS status
              FROM existing e
            WHERE NOT EXISTS (SELECT 1 FROM task)
        """
        async with self._core_client_factory(tenant_id) as core:
            result = await core.data_query(
                sql=fold_sql,
                params=[
                    doc_id, kb_id, tenant_id, idem_key,
                    payload_json, outbox_payload_json, request.storage_path,
                ],
            )
        rows = result.get("rows", [])
        if not rows:
            # document not found → the UPDATE returned no rows → no task/outbox
            # were inserted (SPEC §6.1: abort before enqueuing parse task).
            context.abort(grpc.StatusCode.NOT_FOUND, "document not found")
            return  # unreachable; for type checkers
        task_id = str(rows[0]["task_id"])
        task_status = rows[0].get("status") or "pending"

        return common_pb2.AsyncTaskRef(
            task_id=task_id,
            task_type="kb.parse",
            status=task_status,
            location_url="",
        )

    def GetDocument(self, request, context):
        return _run_async(self._get_document(request, context))

    async def _get_document(self, request, context) -> kb_pb.KBDocument:
        if self._pool is None:
            context.abort(grpc.StatusCode.FAILED_PRECONDITION, "DB pool not configured")
            return
        async with self._core_client_factory(request.tenant_id) as core:
            row = await document_repo.get_document(
                core,
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
        async with self._core_client_factory(request.tenant_id) as core:
            rows, total = await document_repo.list_documents(
                core,
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
        async with self._core_client_factory(request.tenant_id) as core:
            deleted = await document_repo.soft_delete_document(
                core,
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
                async with self._core_client_factory(tenant_id) as core:
                    kb_row = await kb_repo.get_kb(
                        core, tenant_id=tenant_id, kb_id=kb_id
                    )
            except Exception:  # noqa: BLE001 — degrade to defaults below
                logging.getLogger(__name__).warning(
                    "kb-service: failed to load KB config, using defaults",
                    exc_info=True,
                )
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
        # create_session + insert_message(user) are folded into a single
        # CTE-based data_query call so both writes commit atomically in one
        # data-plane transaction (SPEC §4.2 cross-table atomic fold). A
        # partial user-message write can't survive a crash mid-RPC.
        if self._pool is not None:
            from app.repositories import message as message_repo

            async with self._core_client_factory(tenant_id) as core:
                await message_repo.create_session_and_message(
                    core,
                    tenant_id=tenant_id,
                    kb_id=kb_id,
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
            from app.repositories import message as message_repo

            async with self._core_client_factory(tenant_id) as core:
                await message_repo.insert_message(
                    core,
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
    """Build a CoreClient from app settings (production default).

    In dev auth mode (ANI_AUTH_MODE=dev) the gateway identifies service
    callers via the X-Dev-Scope header. kb-service is a Services-layer
    process and must use scope=service so the data-plane handler accepts
    its /data/query calls (SPEC §3.3-7 service-identity-only).
    """
    from app.core.config import settings

    extra_headers: dict[str, str] = {}
    auth_mode = os.environ.get("ANI_AUTH_MODE", "").lower()
    if auth_mode == "dev":
        extra_headers["X-Dev-Scope"] = "service"

    return CoreClient(
        base_url=settings.core_api_base_url,
        tenant_id=tenant_id,
        extra_headers=extra_headers or None,
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
